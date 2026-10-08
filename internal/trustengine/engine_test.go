package trustengine_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/platform/database"
	"github.com/human-agent65535/modemdeck/internal/trustengine"
)

type fixture struct {
	t      *testing.T
	db     *sql.DB
	key    ed25519.PrivateKey
	now    time.Time
	engine *trustengine.Engine
	mode   trustengine.Enforcement
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := database.Open(context.Background(), database.Config{TargetPath: filepath.Join(t.TempDir(), "modemdeck.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, db: db, key: key, now: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC), mode: trustengine.Enforce}
	f.reopen()
	return f
}

// reopen builds a new engine over the same database, as a restart does.
func (f *fixture) reopen() {
	f.t.Helper()
	store, err := trustengine.NewSQLStore(f.db)
	if err != nil {
		f.t.Fatal(err)
	}
	engine, err := trustengine.New(context.Background(), trustengine.Options{
		Store: store, Key: f.key, Enforcement: f.mode, Now: func() time.Time { return f.now }, FlushEvery: time.Minute,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	f.engine = engine
}

func (f *fixture) observe(subject, sim string, healthy bool, elapsed time.Duration) {
	f.t.Helper()
	f.now = f.now.Add(elapsed)
	if err := f.engine.Observe(context.Background(), trustengine.Reading{
		SubjectID: subject, IdentityHash: "identity-of-" + subject, SIMHash: sim, Healthy: healthy, Elapsed: elapsed,
	}); err != nil {
		f.t.Fatal(err)
	}
}

// pass credits healthy time in steps the engine accepts.
func (f *fixture) pass(subject, sim string, total time.Duration) {
	f.t.Helper()
	for total > 0 {
		step := min(total, 10*time.Minute)
		f.observe(subject, sim, true, step)
		total -= step
	}
}

func (f *fixture) subject(id string) trustengine.SubjectView {
	f.t.Helper()
	for _, view := range f.engine.Subjects() {
		if view.SubjectID == id {
			return view
		}
	}
	f.t.Fatalf("no subject %s", id)
	return trustengine.SubjectView{}
}

func (f *fixture) kinds(subject string) []string {
	f.t.Helper()
	events, err := f.engine.Events(context.Background(), 0, 500, subject)
	if err != nil {
		f.t.Fatal(err)
	}
	kinds := make([]string, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

func refused(err error) bool {
	var refusal *trustengine.ErrRefused
	return errors.As(err, &refusal)
}

const node = "NSH-410-TEST01"

func TestANewDeviceReceivesButDoesNotSendUntilSevenStableDaysHavePassed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if ok, state, _ := f.engine.Check(node, trustengine.ActionDial); ok || state != trustengine.Unbound {
		t.Fatalf("a device with no record must not be usable: %v %s", ok, state)
	}
	f.observe(node, "sim-a", true, 0)
	view := f.subject(node)
	if view.State != trustengine.Observation || view.RequiredSeconds != 7*24*3600 || view.RemainingSeconds != 7*24*3600 {
		t.Fatalf("a first binding starts a seven-day observation: %+v", view.Record)
	}
	for action, want := range map[trustengine.Action]bool{
		trustengine.ActionReceiveCall: true, trustengine.ActionReceiveSMS: true, trustengine.ActionForwardOTP: true,
		trustengine.ActionUpdate: true, trustengine.ActionDiagnose: true,
		trustengine.ActionDial: false, trustengine.ActionSendSMS: false,
	} {
		if view.Allowed[action] != want {
			t.Fatalf("during observation %s allowed=%v, want %v", action, view.Allowed[action], want)
		}
		if err := f.engine.Require(ctx, node, action); refused(err) == want {
			t.Fatalf("Require(%s) = %v", action, err)
		}
	}

	// Unhealthy time, and time the gateway was not watching, earn nothing.
	f.observe(node, "sim-a", false, 10*time.Minute)
	if got := f.subject(node).StableSeconds; got != 0 {
		t.Fatalf("an unhealthy interval was credited: %d", got)
	}
	f.observe(node, "sim-a", true, 6*24*time.Hour) // one observation after a long gap
	if got := f.subject(node).StableSeconds; got != 600 {
		t.Fatalf("a single observation may credit at most ten minutes, credited %d s", got)
	}

	f.pass(node, "sim-a", 7*24*time.Hour-20*time.Minute)
	if view := f.subject(node); view.State != trustengine.Observation || view.RemainingSeconds != 600 {
		t.Fatalf("ten minutes short of seven days is still observation: %+v", view.Record)
	}
	f.pass(node, "sim-a", 10*time.Minute)
	view = f.subject(node)
	if view.State != trustengine.Trusted || !view.Allowed[trustengine.ActionDial] || !view.Allowed[trustengine.ActionSendSMS] {
		t.Fatalf("seven stable days earn trust: %+v", view)
	}
	if err := f.engine.Require(ctx, node, trustengine.ActionDial); err != nil {
		t.Fatalf("a trusted device may dial: %v", err)
	}
	if got := strings.Join(f.kinds(node), ","); got != "PAIRING_BOUND,ACTION_REFUSED,ACTION_REFUSED,TRUST_UPGRADE" {
		t.Fatalf("journal: %s", got)
	}
}

func TestProgressSurvivesARestartAndRefusalsAreJournalledOncePerHour(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.observe(node, "sim-a", true, 0)
	f.pass(node, "sim-a", 3*24*time.Hour)
	f.reopen()
	view := f.subject(node)
	if view.State != trustengine.Observation || view.StableSeconds < 3*24*3600-60 || view.StableSeconds > 3*24*3600 {
		t.Fatalf("a restart may lose at most the flush interval of credit: %d s", view.StableSeconds)
	}
	for range 50 {
		if err := f.engine.Require(ctx, node, trustengine.ActionSendSMS); !refused(err) {
			t.Fatalf("send must be refused in observation: %v", err)
		}
	}
	count := func() int {
		n := 0
		for _, kind := range f.kinds(node) {
			if kind == trustengine.EventActionRefused {
				n++
			}
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("fifty refusals within the hour must be one journal entry, have %d", count())
	}
	f.now = f.now.Add(61 * time.Minute)
	_ = f.engine.Require(ctx, node, trustengine.ActionSendSMS)
	if count() != 2 {
		t.Fatalf("a refusal an hour later is journalled again, have %d", count())
	}
}

func TestChangingTheSIMRestartsObservationAndRepeatsLengthenIt(t *testing.T) {
	f := newFixture(t)
	f.observe(node, "", true, 0) // adopted with no SIM readable
	f.observe(node, "sim-a", true, time.Minute)
	if view := f.subject(node); view.SIMChanges != 0 {
		t.Fatalf("the first SIM seen is not a change: %+v", view.Record)
	}
	f.pass(node, "sim-a", 7*24*time.Hour)
	if f.subject(node).State != trustengine.Trusted {
		t.Fatal("setup: expected trusted")
	}
	// A SIM that is briefly unreadable is not a change either.
	f.observe(node, "", true, time.Minute)
	f.observe(node, "sim-a", true, time.Minute)
	if view := f.subject(node); view.State != trustengine.Trusted || view.SIMChanges != 0 {
		t.Fatalf("an unreadable SIM must not count as a change: %+v", view.Record)
	}

	f.observe(node, "sim-b", true, time.Minute)
	view := f.subject(node)
	if view.State != trustengine.Observation || view.SIMChanges != 1 || view.RiskScore != 2 || view.RequiredSeconds != 7*24*3600 || view.StableSeconds != 0 {
		t.Fatalf("first SIM change: %+v", view.Record)
	}
	if view.Allowed[trustengine.ActionDial] || !view.Allowed[trustengine.ActionReceiveSMS] {
		t.Fatalf("after a SIM change the device still receives and no longer sends: %+v", view.Allowed)
	}
	f.observe(node, "sim-c", true, time.Minute)
	f.observe(node, "sim-d", true, time.Minute)
	view = f.subject(node)
	if view.SIMChanges != 3 || view.RiskScore != 6 || view.RequiredSeconds != 14*24*3600 {
		t.Fatalf("repeated SIM changes extend the window to fourteen days: %+v", view.Record)
	}
	f.observe(node, "sim-e", true, time.Minute)
	if view = f.subject(node); view.RequiredSeconds != 30*24*3600 {
		t.Fatalf("frequent changes reach the thirty-day window: %+v", view.Record)
	}
	// It does not grow without bound: more changes keep thirty days until the
	// score reaches quarantine, which needs an administrator.
	for _, sim := range []string{"f", "g", "h", "i", "j"} {
		f.observe(node, "sim-"+sim, true, time.Minute)
		if got := f.subject(node).RequiredSeconds; got != 30*24*3600 {
			t.Fatalf("the window must not exceed thirty days: %d", got)
		}
	}
	f.observe(node, "sim-k", true, time.Minute)
	view = f.subject(node)
	if view.State != trustengine.Quarantine || view.Allowed[trustengine.ActionReceiveSMS] || !view.Allowed[trustengine.ActionUpdate] || !view.Allowed[trustengine.ActionDiagnose] {
		t.Fatalf("at the top of the scale the device is quarantined, keeping only update and diagnosis: %+v", view)
	}
	// Time alone never leaves quarantine.
	f.pass(node, "sim-k", 40*24*time.Hour)
	if f.subject(node).State != trustengine.Quarantine {
		t.Fatal("quarantine ended without an administrator")
	}
	if err := f.engine.Restore(context.Background(), node, "admin", "reviewed with the owner"); err != nil {
		t.Fatal(err)
	}
	view = f.subject(node)
	if view.State != trustengine.Observation || view.RequiredSeconds != 30*24*3600 || view.Allowed[trustengine.ActionDial] {
		t.Fatalf("restoring leads to observation with the window the risk calls for, never straight to trusted: %+v", view.Record)
	}
}

func TestAnAdministratorRestrictionHoldsUntilAnAdministratorLiftsIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.observe(node, "sim-a", true, 0)
	f.pass(node, "sim-a", 7*24*time.Hour)
	if err := f.engine.Restore(ctx, node, "admin", "nothing to do"); !errors.Is(err, trustengine.ErrNotAllowed) {
		t.Fatalf("restoring a trusted device is not an action: %v", err)
	}
	if err := f.engine.Restrict(ctx, node, "admin", "lent to a friend, number 10000000001", false); err != nil {
		t.Fatal(err)
	}
	view := f.subject(node)
	if view.State != trustengine.Restricted || !view.Manual || view.Allowed[trustengine.ActionDial] || !view.Allowed[trustengine.ActionReceiveCall] || view.Allowed[trustengine.ActionForwardOTP] {
		t.Fatalf("restricted: %+v", view)
	}
	if strings.Contains(view.Reason, "10000000001") {
		t.Fatalf("a number given as a reason must not be stored: %q", view.Reason)
	}
	f.pass(node, "sim-a", 60*24*time.Hour)
	f.reopen()
	if f.subject(node).State != trustengine.Restricted {
		t.Fatal("a manual restriction ended on its own")
	}
	if err := f.engine.Restrict(ctx, node, "admin", "revoked", true); err != nil {
		t.Fatal(err)
	}
	if view = f.subject(node); view.State != trustengine.Quarantine || view.Allowed[trustengine.ActionReceiveCall] {
		t.Fatalf("revoked: %+v", view)
	}
	// Restricting again never loosens.
	if err := f.engine.Restrict(ctx, node, "admin", "again", false); err != nil {
		t.Fatal(err)
	}
	if f.subject(node).State != trustengine.Quarantine {
		t.Fatal("a lighter restriction replaced a quarantine")
	}
	if err := f.engine.Restore(ctx, node, "admin", "returned"); err != nil {
		t.Fatal(err)
	}
	if view = f.subject(node); view.State != trustengine.Observation || view.Manual {
		t.Fatalf("restored: %+v", view.Record)
	}
	if err := f.engine.Restrict(ctx, "NSH-410-NOBODY", "admin", "x", false); !errors.Is(err, trustengine.ErrUnknownSubject) {
		t.Fatalf("unknown subject: %v", err)
	}
	events, _ := f.engine.Events(ctx, 0, 500, node)
	for _, event := range events {
		if strings.Contains(event.Cause+event.Result+event.Source, "10000000001") {
			t.Fatalf("the journal holds a phone number: %+v", event)
		}
	}
}

func TestVerificationCodeVolumeIsScoredOnceAndLimitsForwardingThenUse(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.observe(node, "sim-a", true, 0)
	f.pass(node, "sim-a", 7*24*time.Hour)
	for _, count := range []int{0, 3, 5} {
		if err := f.engine.ObserveOTP(ctx, node, count); err != nil {
			t.Fatal(err)
		}
	}
	if view := f.subject(node); view.RiskScore != 0 || view.OTPLimited {
		t.Fatalf("five codes a day is ordinary use: %+v", view.Record)
	}
	for range 5 { // the same reading repeated must not score repeatedly
		_ = f.engine.ObserveOTP(ctx, node, 8)
	}
	view := f.subject(node)
	if view.RiskScore != 3 || view.OTPLimited || view.State != trustengine.Trusted || !view.Allowed[trustengine.ActionForwardOTP] {
		t.Fatalf("six to ten codes: noted and scored once: %+v", view.Record)
	}
	_ = f.engine.ObserveOTP(ctx, node, 11)
	view = f.subject(node)
	if !view.OTPLimited || view.Allowed[trustengine.ActionForwardOTP] || !view.Allowed[trustengine.ActionDial] || !view.Allowed[trustengine.ActionReceiveSMS] {
		t.Fatalf("more than ten: forwarding stops, ordinary use continues: %+v", view)
	}
	_ = f.engine.ObserveOTP(ctx, node, 25)
	view = f.subject(node)
	if view.State != trustengine.Restricted || view.Allowed[trustengine.ActionDial] || !view.Allowed[trustengine.ActionReceiveSMS] {
		t.Fatalf("more than twenty: outgoing use restricted, receiving kept: %+v", view)
	}
	_ = f.engine.ObserveOTP(ctx, node, 2)
	view = f.subject(node)
	if view.OTPLimited || view.State != trustengine.Restricted {
		t.Fatalf("the forwarding limit lifts with the count; the restriction waits for an administrator: %+v", view.Record)
	}
	got := strings.Join(f.kinds(node), ",")
	for _, want := range []string{"OTP_LIMIT_TRIGGERED", "TRUST_DOWNGRADE", "OTP_LIMIT_CLEARED"} {
		if !strings.Contains(got, want) {
			t.Fatalf("journal lacks %s: %s", want, got)
		}
	}
}

func TestIntegrityFailureAndIdentityChangeRestrict(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.observe(node, "sim-a", true, 0)
	f.pass(node, "sim-a", 7*24*time.Hour)
	if err := f.engine.IntegrityFailure(ctx, node, "release r-1 no longer matched its signed manifest at start"); err != nil {
		t.Fatal(err)
	}
	view := f.subject(node)
	if view.State != trustengine.Restricted || view.IntegrityFailures != 1 || view.RiskScore != 5 {
		t.Fatalf("integrity failure: %+v", view.Record)
	}
	f.pass(node, "sim-a", 30*24*time.Hour)
	if f.subject(node).State != trustengine.Restricted {
		t.Fatal("an integrity restriction ended on its own")
	}

	other := "NSH-410-TEST02"
	f.observe(other, "sim-z", true, 0)
	f.pass(other, "sim-z", 7*24*time.Hour)
	if err := f.engine.Observe(ctx, trustengine.Reading{SubjectID: other, IdentityHash: "another-key", SIMHash: "sim-z", Healthy: true, Elapsed: time.Minute}); err != nil {
		t.Fatal(err)
	}
	view = f.subject(other)
	if view.State != trustengine.Observation || view.IdentityChanges != 1 || view.RiskScore != 4 {
		t.Fatalf("identity change: %+v", view.Record)
	}
	// One device's events never change another's record.
	if f.subject(node).IdentityChanges != 0 || f.subject(other).IntegrityFailures != 0 {
		t.Fatal("records are not isolated per device")
	}
}

func TestDevicesPairedBeforeTrustRecordsExistedKeepWorkingAndLaterOnesAreObserved(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.engine.Grandfather(ctx, map[string]string{node: "identity-of-" + node}); err != nil {
		t.Fatal(err)
	}
	view := f.subject(node)
	if view.State != trustengine.Trusted || !view.Allowed[trustengine.ActionDial] {
		t.Fatalf("an already-paired device keeps working after the upgrade: %+v", view)
	}
	f.observe(node, "sim-a", true, time.Minute) // its first reading must not look like a change
	if view = f.subject(node); view.State != trustengine.Trusted || view.SIMChanges != 0 || view.IdentityChanges != 0 {
		t.Fatalf("the first reading of a grandfathered device: %+v", view.Record)
	}
	f.reopen()
	f.observe(node, "sim-b", true, time.Minute)
	if view = f.subject(node); view.State != trustengine.Observation || view.SIMChanges != 1 {
		t.Fatalf("a grandfathered device is still subject to the rules afterwards: %+v", view.Record)
	}
	// It happens once: a device adopted later is not grandfathered.
	later := "NSH-410-LATER1"
	if err := f.engine.Grandfather(ctx, map[string]string{later: "identity-of-" + later}); err != nil {
		t.Fatal(err)
	}
	if ok, state, _ := f.engine.Check(later, trustengine.ActionDial); ok || state != trustengine.Unbound {
		t.Fatalf("grandfathering ran twice: %v %s", ok, state)
	}
	if got := f.kinds(node)[0]; got != trustengine.EventGrandfathered {
		t.Fatalf("grandfathering is journalled: %s", got)
	}
}

func TestMonitorModeRefusesNothingAndSaysSoInTheJournal(t *testing.T) {
	f := newFixture(t)
	f.mode = trustengine.Monitor
	f.reopen()
	ctx := context.Background()
	f.observe(node, "sim-a", true, 0)
	if err := f.engine.Require(ctx, node, trustengine.ActionDial); err != nil {
		t.Fatalf("monitor mode must not refuse: %v", err)
	}
	if ok, _, _ := f.engine.Check(node, trustengine.ActionDial); ok {
		t.Fatal("monitor mode must still report that the action is not allowed")
	}
	events, _ := f.engine.Events(ctx, 0, 10, node)
	last := events[len(events)-1]
	if last.Kind != trustengine.EventActionRefused || !strings.Contains(last.Result, "not enforced") {
		t.Fatalf("the journal must say the refusal was not enforced: %+v", last)
	}
	if _, err := trustengine.New(ctx, trustengine.Options{Store: mustStore(t, f.db), Key: f.key, Enforcement: "off"}); err == nil {
		t.Fatal("an unknown enforcement mode must be an error, not a default")
	}
}

func mustStore(t *testing.T, db *sql.DB) *trustengine.SQLStore {
	t.Helper()
	store, err := trustengine.NewSQLStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestSanitizeRemovesLongDigitRunsAndKeepsShortOnes(t *testing.T) {
	for input, want := range map[string]string{
		"call to +8610000000001 failed": "call to +[redacted] failed",
		"iccid 89860012345678901234":    "iccid [redacted]",
		"more than 10 codes in 24h":     "more than 10 codes in 24h",
		"release r-1.2.0 at 08:00":      "release r-1.2.0 at 08:00",
		"line\nbreak\x1finjected":       "line break injected",
		"1234567":                       "[redacted]",
		"123456":                        "123456",
	} {
		if got := trustengine.Sanitize(input); got != want {
			t.Fatalf("Sanitize(%q) = %q, want %q", input, got, want)
		}
	}
	if got := trustengine.Sanitize(strings.Repeat("a", 1000)); len(got) != 240 {
		t.Fatalf("journal text must be bounded, got %d", len(got))
	}
}
