// SPDX-License-Identifier: AGPL-3.0-only
package forwarding

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/platform/database"
)

func openTestStore(t *testing.T) (*SQLStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "forward.db")
	s := reopenTestStore(t, path)
	return s, path
}
func reopenTestStore(t *testing.T, path string) *SQLStore {
	t.Helper()
	db, err := database.Open(context.Background(), database.Config{TargetPath: path})
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s := &SQLStore{DB: db}
	if err = s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}
func configFor(t *testing.T, s *SQLStore, count int, logic string) Config {
	t.Helper()
	c := DefaultConfig()
	c.Enabled = true
	c.OwnerID = "owner"
	ids := []string{}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("ch_%d", i)
		ids = append(ids, id)
		c.Channels = append(c.Channels, Channel{ID: id, Name: id, Type: "webhook", Enabled: true, Params: map[string]string{}, Secrets: map[string]Secret{"url": {Env: "TEST_URL"}}})
	}
	c.Rules = []Rule{{ID: "rule", Enabled: true, Events: []string{"incoming_sms"}, ChannelIDs: ids, Logic: logic, TemplateID: "default", Condition: Condition{Op: "always"}}}
	next, err := s.Save(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return next
}
func insertEventTx(t *testing.T, tx *sql.Tx, when time.Time, body string) {
	t.Helper()
	var existing int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sms`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	endpointMessageID := "nsh-in:original"
	if existing > 0 {
		endpointMessageID = fmt.Sprintf("nsh-in:original-%d", existing)
	}
	result, err := tx.Exec(`INSERT INTO sms(line_id,endpoint_line_id,endpoint_message_id,peer,content,timestamp) VALUES('line-A','nsh-node:original:0',?,'10086',?,?)`, endpointMessageID, body, when.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	_, err = tx.Exec(`INSERT INTO modemdeck_notification_events(event_key,event_type,resource_id,line_id,peer,body,occurred_at) VALUES(?,'incoming_sms',?,'line-A','10086',?,?)`, fmt.Sprintf("sms:%d", id), fmt.Sprint(id), body, when.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
}
func insertEvent(t *testing.T, s *SQLStore, when time.Time, body string) {
	t.Helper()
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	insertEventTx(t, tx, when, body)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func allowed(context.Context, string, Event, Channel) error { return nil }
func engineFor(s *SQLStore, server *httptest.Server) *Engine {
	return &Engine{Store: s, Authorize: allowed, Sender: &Dispatcher{LookupEnv: func(string) (string, bool) { return server.URL, true }}}
}
func TestNegativeKeywordCombinations(t *testing.T) {
	for _, tt := range []struct {
		op, value, body, peer string
		want                  bool
	}{
		{"notis", "10086||10000", "", "10086", false}, {"notis", "10086||10000", "", "10000", false}, {"notis", "10086||10000", "", "12345", true},
		{"notcontain", "bank&&code||parcel", "bank code", "", false}, {"notcontain", "bank&&code||parcel", "parcel arrived", "", false}, {"notcontain", "bank&&code||parcel", "bank statement", "", true}, {"notcontain", "bank&&code||parcel", "ordinary message", "", true},
	} {
		field := "body"
		if tt.op == "notis" {
			field = "peer"
		}
		c := Condition{Op: tt.op, Field: field, Value: tt.value}
		e := Event{Body: tt.body, Peer: tt.peer}
		if got := c.Matches(e); got != tt.want {
			t.Fatalf("%+v = %v", tt, got)
		}
		if (Condition{Op: "not", Children: []Condition{c}}).Matches(e) == tt.want {
			t.Fatal("explicit NOT failed")
		}
	}
}
func TestAttemptExhaustionAfterLeaseInterruptionsStopsUntilFail(t *testing.T) {
	s, _ := openTestStore(t)
	configFor(t, s, 2, "until_fail")
	now := time.Now().UTC()
	insertEvent(t, s, now, "synthetic")
	if err := s.Ingest(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxAttempts; i++ {
		tasks, err := s.Due(context.Background(), now)
		if err != nil || len(tasks) != 1 {
			t.Fatalf("due %d: %+v %v", i, tasks, err)
		}
		_, ok, err := s.Claim(context.Background(), tasks[0], now)
		if err != nil || !ok {
			t.Fatal("claim", ok, err)
		}
		now = now.Add(46 * time.Second)
	}
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	if err := engineFor(s, server).Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.History(context.Background(), 0)
	if len(rows) != 2 || rows[1].Status != "failed" || rows[1].ErrorClass != "attempt_limit" || rows[0].Status != "cancelled" || sends.Load() != 0 {
		t.Fatalf("tasks %+v sends %d", rows, sends.Load())
	}
}
func TestExpiredSMSNeverSendsOrReplays(t *testing.T) {
	s, _ := openTestStore(t)
	configFor(t, s, 1, "all")
	now := time.Now().UTC()
	insertEvent(t, s, now.Add(-25*time.Hour), "expired private body")
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends.Add(1) }))
	defer server.Close()
	if err := engineFor(s, server).Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.History(context.Background(), 0)
	if len(rows) != 1 || rows[0].Status != "expired" || rows[0].ErrorClass != "expired" || sends.Load() != 0 {
		t.Fatalf("%+v", rows)
	}
	if !errors.Is(s.Retry(context.Background(), rows[0].ID, now), ErrDenied) {
		t.Fatal("expired task was replayed")
	}
}
func TestTwoEventsHundredChannelsDoNotStarve(t *testing.T) {
	s, _ := openTestStore(t)
	configFor(t, s, 100, "until_success")
	now := time.Now().UTC()
	insertEvent(t, s, now, "one")
	insertEvent(t, s, now, "two")
	if err := s.Ingest(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.Due(context.Background(), now)
	if err != nil || len(tasks) != 2 || tasks[0].Position != 0 || tasks[1].Position != 0 {
		t.Fatalf("eligible heads %+v %v", tasks, err)
	}
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	if err := engineFor(s, server).Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var sent, cancelled int
	s.DB.QueryRow(`SELECT COUNT(*) FROM modemdeck_forwarding_tasks WHERE status='sent'`).Scan(&sent)
	s.DB.QueryRow(`SELECT COUNT(*) FROM modemdeck_forwarding_tasks WHERE status='cancelled'`).Scan(&cancelled)
	if sent != 2 || cancelled != 198 || sends.Load() != 2 {
		t.Fatalf("sent=%d cancelled=%d requests=%d", sent, cancelled, sends.Load())
	}
}
func TestJournalAtomicRestartPreservesEndpointPolicyAndPrivateHistory(t *testing.T) {
	s, path := openTestStore(t)
	configFor(t, s, 1, "all")
	now := time.Now().UTC()
	tx, _ := s.DB.Begin()
	insertEventTx(t, tx, now, "rolled back")
	tx.Rollback()
	var count int
	s.DB.QueryRow(`SELECT COUNT(*) FROM modemdeck_forwarding_events`).Scan(&count)
	if count != 0 {
		t.Fatal("journal escaped rollback")
	}
	insertEvent(t, s, now, "synthetic bank code 123456")
	if err := s.Ingest(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s = reopenTestStore(t, path)
	tasks, err := s.Due(context.Background(), now)
	if err != nil || len(tasks) != 1 || tasks[0].Event.EndpointID != "nsh-node:original:0" || tasks[0].Event.EndpointMessageID != "nsh-in:original" {
		t.Fatalf("lost source endpoint: %+v %v", tasks, err)
	}
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends.Add(1) }))
	defer server.Close()
	engine := engineFor(s, server)
	engine.Authorize = func(_ context.Context, _ string, event Event, _ Channel) error {
		if event.EndpointMessageID == "nsh-in:original" {
			return ErrDenied
		}
		return nil
	}
	if err = engine.Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.History(context.Background(), 0)
	raw, _ := json.Marshal(rows)
	if rows[0].ErrorClass != "authorization_revoked" || sends.Load() != 0 || strings.Contains(string(raw), "123456") || strings.Contains(string(raw), "nsh-in") || strings.Contains(string(raw), "token") {
		t.Fatalf("history or gate: %s", raw)
	}
}
func TestConcurrentClaimAndStaleLeaseReceipt(t *testing.T) {
	s, _ := openTestStore(t)
	configFor(t, s, 1, "all")
	now := time.Now().UTC()
	insertEvent(t, s, now, "test")
	s.Ingest(context.Background(), now)
	tasks, _ := s.Due(context.Background(), now)
	var claims atomic.Int32
	var winner Task
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, ok, err := s.Claim(context.Background(), tasks[0], now)
			if err != nil {
				t.Error(err)
			}
			if ok {
				claims.Add(1)
				mu.Lock()
				winner = task
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal("concurrent sends", claims.Load())
	}
	later := now.Add(time.Minute)
	tasks, _ = s.Due(context.Background(), later)
	newer, ok, err := s.Claim(context.Background(), tasks[0], later)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if !errors.Is(s.Finish(context.Background(), winner, "sent", "", later), ErrConflict) {
		t.Fatal("stale receipt committed")
	}
	if err = s.Finish(context.Background(), newer, "sent", "", later); err != nil {
		t.Fatal(err)
	}
}
func TestConfigurationCASAndDisableCancelPending(t *testing.T) {
	s, _ := openTestStore(t)
	c := configFor(t, s, 1, "all")
	now := time.Now().UTC()
	insertEvent(t, s, now, "test")
	s.Ingest(context.Background(), now)
	c.Enabled = false
	if _, err := s.Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(context.Background(), c); !errors.Is(err, ErrConflict) {
		t.Fatal("lost revision guard")
	}
	tasks, _ := s.Due(context.Background(), now)
	if len(tasks) != 0 {
		t.Fatal("disabled pending tasks escaped")
	}
}
func TestQuietHoursMidnightAndTimezone(t *testing.T) {
	q := QuietHours{Enabled: true, Start: "22:00", End: "08:00", Timezone: "Asia/Shanghai", Days: []int{1}}
	now := time.Date(2026, 11, 16, 23, 0, 0, 0, time.FixedZone("CST", 8*3600))
	if q.Next(now).Sub(now) != 9*time.Hour {
		t.Fatal(q.Next(now))
	}
	next := now.Add(3 * time.Hour)
	if q.Next(next).Sub(next) != 6*time.Hour {
		t.Fatal("overnight weekday", q.Next(next))
	}
	if !q.Next(now.Add(24 * time.Hour)).Equal(now.Add(24 * time.Hour)) {
		t.Fatal("weekday leak")
	}
}

func TestDisabledOrOlderConfigurationEventsAreNotReplayedOnSave(t *testing.T) {
	s, _ := openTestStore(t)
	now := time.Now().UTC()
	insertEvent(t, s, now, "received while OFF")
	configFor(t, s, 1, "all")
	if err := s.Ingest(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	tasks, _ := s.Due(context.Background(), now)
	if len(tasks) != 0 {
		t.Fatal("OFF backlog replayed")
	}
	insertEvent(t, s, now, "old recipient config")
	c, _ := s.Config(context.Background())
	c.Channels[0].Name = "new config"
	if _, err := s.Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	tasks, _ = s.Due(context.Background(), now)
	if len(tasks) != 0 {
		t.Fatal("old event adopted new recipient configuration")
	}
}
func TestInvalidTimestampExpiresWithoutBlockingFollowingJournal(t *testing.T) {
	s, _ := openTestStore(t)
	configFor(t, s, 1, "all")
	_, err := s.DB.Exec(`INSERT INTO modemdeck_notification_events(event_key,event_type,resource_id,line_id,peer,body,occurred_at) VALUES('invalid-time','incoming_sms','invalid','line-A','10086','synthetic','bad-time')`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	insertEvent(t, s, now, "valid event")
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sends.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	if err := engineFor(s, server).Step(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.History(context.Background(), 0)
	if len(rows) != 2 || rows[1].Status != "expired" || rows[0].Status != "sent" || sends.Load() != 1 {
		t.Fatalf("timestamp poison journal: %+v", rows)
	}
}

type senderFunc func(context.Context, Channel, Rendered, string) error

func (f senderFunc) Send(ctx context.Context, ch Channel, r Rendered, id string) error {
	return f(ctx, ch, r, id)
}
func TestExpiredDuringBatchWaitCannotBeMadeFreshByBatchClock(t *testing.T) {
	s, _ := openTestStore(t)
	configFor(t, s, 1, "all")
	batch := time.Now().UTC().Add(-25 * time.Hour)
	insertEvent(t, s, batch, "expired while waiting")
	var sends atomic.Int32
	engine := &Engine{Store: s, Authorize: allowed, Sender: senderFunc(func(context.Context, Channel, Rendered, string) error { sends.Add(1); return nil })}
	if err := engine.Step(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.History(context.Background(), 0)
	if len(rows) != 1 || rows[0].Status != "expired" || sends.Load() != 0 {
		t.Fatal("stale batch clock authorized an expired send", rows, sends.Load())
	}
}
func TestLeaseAndRetryDeadlinesUseClaimAndCompletionTimes(t *testing.T) {
	s, _ := openTestStore(t)
	configFor(t, s, 1, "all")
	batch := time.Now().UTC()
	insertEvent(t, s, batch, "synthetic")
	clock := batch.Add(5 * time.Minute)
	var lease int64
	engine := &Engine{Store: s, Authorize: allowed, Now: func() time.Time { return clock }, Sender: senderFunc(func(_ context.Context, _ Channel, _ Rendered, id string) error {
		if err := s.DB.QueryRow(`SELECT lease_until FROM modemdeck_forwarding_tasks WHERE status='sending'`).Scan(&lease); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(20 * time.Second)
		return ErrDelivery
	})}
	if err := engine.Step(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.History(context.Background(), 0)
	if lease != batch.Add(5*time.Minute+45*time.Second).UnixMilli() {
		t.Fatal("lease started at stale batch time", lease)
	}
	if len(rows) != 1 || rows[0].NextAt != clock.Add(30*time.Second).UnixMilli() {
		t.Fatal("retry used pre-send time", rows)
	}
}
func TestQuietHoursAreRecheckedAfterWaitingForDispatch(t *testing.T) {
	s, _ := openTestStore(t)
	c := configFor(t, s, 1, "all")
	c.Rules[0].Quiet = QuietHours{Enabled: true, Start: "22:00", End: "08:00", Timezone: "UTC"}
	if _, err := s.Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	batch := time.Date(2026, 11, 15, 21, 59, 0, 0, time.UTC)
	dispatch := batch.Add(31 * time.Minute)
	insertEvent(t, s, batch, "synthetic")
	var sends atomic.Int32
	engine := &Engine{Store: s, Authorize: allowed, Now: func() time.Time { return dispatch }, Sender: senderFunc(func(context.Context, Channel, Rendered, string) error { sends.Add(1); return nil })}
	if err := engine.Step(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.History(context.Background(), 0)
	want := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC).UnixMilli()
	if len(rows) != 1 || rows[0].Status != "pending" || rows[0].NextAt != want || rows[0].Attempts != 0 || sends.Load() != 0 {
		t.Fatal("quiet hours ignored after batch delay", rows)
	}
}
