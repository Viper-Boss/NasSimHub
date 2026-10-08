package trustengine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/trustengine"
)

// seeded returns a fixture with one trusted device and a journal of several
// events.
func seeded(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.observe(node, "sim-a", true, 0)
	f.pass(node, "sim-a", 7*24*time.Hour)
	f.observe(node, "sim-b", true, time.Minute)
	f.pass(node, "sim-b", 7*24*time.Hour)
	if f.subject(node).State != trustengine.Trusted {
		t.Fatal("setup: expected trusted")
	}
	report, err := f.engine.VerifyChain(context.Background())
	if err != nil || !report.OK || report.Events < 5 || report.HeadSeq != report.Events {
		t.Fatalf("an untouched journal verifies: %+v %v", report, err)
	}
	return f
}

func (f *fixture) exec(query string, arguments ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(query, arguments...); err != nil {
		f.t.Fatal(err)
	}
}

func TestEveryKindOfJournalDamageIsDetectedAndRestricts(t *testing.T) {
	for name, damage := range map[string]func(*fixture){
		"an event is edited":  func(f *fixture) { f.exec(`UPDATE security_events SET result = 'trusted' WHERE seq = 3`) },
		"an event is removed": func(f *fixture) { f.exec(`DELETE FROM security_events WHERE seq = 2`) },
		"the tail is removed": func(f *fixture) {
			f.exec(`DELETE FROM security_events WHERE seq = (SELECT MAX(seq) FROM security_events)`)
		},
		"the head is removed": func(f *fixture) { f.exec(`DELETE FROM security_meta WHERE key = 'head'`) },
		"the head is rewritten": func(f *fixture) {
			f.exec(`UPDATE security_meta SET value = replace(value, '"seq":', '"seq":1') WHERE key = 'head'`)
		},
		"the head is made garbage": func(f *fixture) { f.exec(`UPDATE security_meta SET value = 'x' WHERE key = 'head'`) },
		"an event is re-hashed":    func(f *fixture) { f.exec(`UPDATE security_events SET cause = 'x', hash = 'ab' WHERE seq = 2`) },
		"the journal is emptied":   func(f *fixture) { f.exec(`DELETE FROM security_events`) },
		"tail and head are removed": func(f *fixture) {
			f.exec(`DELETE FROM security_events WHERE seq > 2`)
			f.exec(`DELETE FROM security_meta WHERE key = 'head'`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := seeded(t)
			ctx := context.Background()
			damage(f)
			f.reopen()
			report := f.engine.Chain()
			if report.OK || report.Problem == "" {
				t.Fatalf("damage went unnoticed: %+v", report)
			}
			view := f.subject(node)
			if view.EffectiveState != trustengine.Restricted || view.Allowed[trustengine.ActionDial] || !view.Allowed[trustengine.ActionReceiveCall] {
				t.Fatalf("a journal that does not verify restricts outgoing use and keeps receiving: %+v", view)
			}
			if err := f.engine.Require(ctx, node, trustengine.ActionDial); !refused(err) {
				t.Fatalf("dial must be refused: %v", err)
			}
			// Recording continues, but it does not repair the journal.
			f.observe(node, "sim-c", true, time.Minute)
			f.reopen()
			if f.engine.Chain().OK {
				t.Fatal("ordinary recording healed a damaged journal")
			}

			report, err := f.engine.Reseal(ctx, "admin", "restored the database from a backup")
			if err != nil || !report.OK || report.SealedFrom == 0 {
				t.Fatalf("reseal: %+v %v", report, err)
			}
			view = f.subject(node)
			if view.State != trustengine.Observation || view.EffectiveState != trustengine.Observation || view.RequiredSeconds != 14*24*3600 {
				t.Fatalf("after a reseal every device is observed again: %+v", view.Record)
			}
			f.reopen()
			if report = f.engine.Chain(); !report.OK || report.SealedFrom == 0 {
				t.Fatalf("a resealed journal verifies after a restart, from the reseal on: %+v", report)
			}
			events, _ := f.engine.Events(ctx, report.SealedFrom-1, 5, "")
			if len(events) == 0 || events[0].Kind != trustengine.EventChainResealed || !strings.Contains(events[0].Source, "admin") {
				t.Fatalf("the authorised recovery is itself in the journal: %+v", events)
			}
			if _, err := f.engine.Reseal(ctx, "admin", "again"); !errors.Is(err, trustengine.ErrChainIntact) {
				t.Fatalf("an intact journal cannot be resealed: %v", err)
			}
		})
	}
}

func TestAnEditedTrustRecordIsNotBelieved(t *testing.T) {
	for name, damage := range map[string]string{
		"state raised":      `UPDATE trust_records SET state = 'TRUSTED'`,
		"progress raised":   `UPDATE trust_records SET stable_seconds = required_seconds`,
		"risk cleared":      `UPDATE trust_records SET risk_score = 0, sim_changes = 0`,
		"signature removed": `UPDATE trust_records SET signature = x''`,
		"generation reset":  `UPDATE trust_records SET generation = 1`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			f.observe(node, "sim-a", true, 0)
			f.pass(node, "sim-a", 24*time.Hour)
			f.observe(node, "sim-b", true, time.Minute) // observation, risk 2
			f.exec(damage)
			f.reopen()
			view := f.subject(node)
			if view.RecordVerified || view.Allowed[trustengine.ActionDial] || trustengineRank(view.EffectiveState) < trustengineRank(trustengine.Restricted) {
				t.Fatalf("an edited record must fail closed: %+v", view)
			}
			// Observation neither credits nor repairs it.
			f.pass(node, "sim-b", 40*24*time.Hour)
			f.reopen()
			if view = f.subject(node); view.RecordVerified || view.Allowed[trustengine.ActionDial] {
				t.Fatalf("time repaired an edited record: %+v", view)
			}
			if err := f.engine.Restore(ctx, node, "admin", "checked the device"); err != nil {
				t.Fatal(err)
			}
			view = f.subject(node)
			if !view.RecordVerified || view.State != trustengine.Observation || view.RequiredSeconds != 30*24*3600 {
				t.Fatalf("an administrator rebuilds the record into the longest observation: %+v", view.Record)
			}
			kinds := strings.Join(f.kinds(node), ",")
			if !strings.Contains(kinds, trustengine.EventRecordDamaged) || !strings.HasSuffix(kinds, trustengine.EventManualRestore) {
				t.Fatalf("the damage and the recovery are journalled: %s", kinds)
			}
		})
	}
}

func trustengineRank(state trustengine.State) int {
	// MostRestrictive is the exported ordering; derive a rank from it.
	order := []trustengine.State{trustengine.Trusted, trustengine.Observation, trustengine.Restricted, trustengine.Quarantine}
	for index, candidate := range order {
		if candidate == state {
			return index
		}
	}
	return len(order)
}

func TestARecordFromAnotherGatewayOrADeletedLastEventIsNotBelieved(t *testing.T) {
	f := newFixture(t)
	f.observe(node, "sim-a", true, 0)
	f.pass(node, "sim-a", 7*24*time.Hour)
	// The record survives but the event it rests on is replaced by an older
	// journal (a rollback of the journal alone, with a head that matches it).
	other := newFixture(t)
	other.observe(node, "sim-a", true, 0)
	rows, err := other.db.Query(`SELECT seq, at, subject_id, kind, cause, source, result, prev_hash, hash FROM security_events`)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`DELETE FROM security_events`)
	for rows.Next() {
		var seq int64
		var at, subject, kind, cause, source, result, prev, hash string
		if err := rows.Scan(&seq, &at, &subject, &kind, &cause, &source, &result, &prev, &hash); err != nil {
			t.Fatal(err)
		}
		f.exec(`INSERT INTO security_events VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, seq, at, subject, kind, cause, source, result, prev, hash)
	}
	rows.Close()
	var head string
	if err := other.db.QueryRow(`SELECT value FROM security_meta WHERE key = 'head'`).Scan(&head); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE security_meta SET value = ? WHERE key = 'head'`, head)
	f.reopen()
	// The transplanted head carries another gateway's signature.
	if report := f.engine.Chain(); report.OK {
		t.Fatalf("a journal signed by another gateway verified: %+v", report)
	}
	if view := f.subject(node); view.Allowed[trustengine.ActionDial] {
		t.Fatalf("dialling stayed allowed: %+v", view)
	}
}
