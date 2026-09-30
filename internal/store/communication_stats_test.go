package store

import (
	"context"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/auth"
)

func TestCommunicationStatsCountsTheLastDayOfRecords(t *testing.T) {
	t.Parallel()

	repository := newHardwareTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	at := func(offset time.Duration) string { return now.Add(offset).Format(time.RFC3339Nano) }

	messages := []struct {
		line, content, delivery string
		kind                    int
		timestamp, created      string
		deleted                 any
	}{
		{"line-main", "【银行】您的验证码是 482913，5 分钟内有效", "", 1, at(-time.Hour), at(-time.Hour), nil},
		// An older row, written in the space-separated form: 02:30 UTC.
		{"line-main", "hello", "", 1, "2026-09-24 02:30:00", "2026-09-24 02:30:00", nil},
		{"line-main", "on my way", "delivered", 2, at(-3 * time.Hour), at(-3 * time.Hour), nil},
		{"line-main", "on my way?", "failed", 2, at(-3 * time.Hour), at(-3 * time.Hour), nil},
		{"line-main", "yesterday", "", 1, at(-30 * time.Hour), at(-30 * time.Hour), nil},
		{"line-main", "deleted", "", 1, at(-time.Hour), at(-time.Hour), at(-time.Minute)},
		{"line-other", "other line", "", 1, at(-time.Hour), at(-time.Hour), nil},
		// No time of its own: placed by when the gateway saw it.
		{"line-main", "no timestamp", "", 1, "0001-01-01T00:00:00Z", at(-2 * time.Hour), nil},
		// Stamped by an SMSC whose clock runs ahead of this one.
		{"line-main", "from the future", "", 1, at(5 * time.Minute), at(-time.Minute), nil},
	}
	for _, message := range messages {
		if _, err := repository.database.ExecContext(ctx,
			`INSERT INTO sms (line_id, peer, content, delivery_status, type, timestamp, created_at, deleted_at)
			 VALUES (?, '+8613800000000', ?, ?, ?, ?, ?, ?)`,
			message.line, message.content, message.delivery, message.kind,
			message.timestamp, message.created, message.deleted,
		); err != nil {
			t.Fatalf("insert message %q: %v", message.content, err)
		}
	}

	calls := []struct {
		id, direction, phase, created, active, ended, reason string
	}{
		{"answered-in", "incoming", "ended", at(-5 * time.Hour), at(-5*time.Hour + 10*time.Second), at(-5*time.Hour + 130*time.Second), "remote"},
		{"missed-in", "incoming", "ended", at(-4 * time.Hour), "", at(-4*time.Hour + 20*time.Second), "remote"},
		{"rejected-in", "incoming", "ended", at(-4 * time.Hour), "", at(-4*time.Hour + 3*time.Second), "rejected"},
		{"answered-out", "outgoing", "ended", at(-2 * time.Hour), at(-2*time.Hour + 5*time.Second), at(-2*time.Hour + 65*time.Second), "local"},
		{"old-out", "outgoing", "ended", at(-40 * time.Hour), at(-40 * time.Hour), at(-39 * time.Hour), "local"},
		{"ringing-in", "incoming", "ringing", at(-10 * time.Minute), "", "", ""},
	}
	for _, call := range calls {
		var active, ended any
		if call.active != "" {
			active = call.active
		}
		if call.ended != "" {
			ended = call.ended
		}
		if _, err := repository.database.ExecContext(ctx,
			`INSERT INTO call_history (id, line_id, direction, phase, created_at, active_at, ended_at, end_reason)
			 VALUES (?, 'line-main', ?, ?, ?, ?, ?, ?)`,
			call.id, call.direction, call.phase, call.created, active, ended, call.reason,
		); err != nil {
			t.Fatalf("insert call %s: %v", call.id, err)
		}
	}

	stats, err := repository.CommunicationStats(ctx, now, 24)
	if err != nil {
		t.Fatal(err)
	}
	if stats.WindowHours != 24 || len(stats.Hourly) != 24 {
		t.Fatalf("window = %d hours, %d buckets", stats.WindowHours, len(stats.Hourly))
	}
	if stats.SMSReceived != 5 || stats.SMSVerification != 1 || stats.SMSSent != 1 || stats.SMSFailed != 1 {
		t.Fatalf("sms = received %d, verification %d, sent %d, failed %d; want 5, 1, 1, 1",
			stats.SMSReceived, stats.SMSVerification, stats.SMSSent, stats.SMSFailed)
	}
	if stats.CallsIncoming != 4 || stats.CallsOutgoing != 1 || stats.CallsMissed != 1 {
		t.Fatalf("calls = in %d, out %d, missed %d; want 4, 1, 1",
			stats.CallsIncoming, stats.CallsOutgoing, stats.CallsMissed)
	}
	if stats.CallDurationSeconds != 180 {
		t.Fatalf("duration = %d, want 180 (120 + 60, ringing and missed calls add nothing)", stats.CallDurationSeconds)
	}

	var messageTotal, callTotal int
	for _, bucket := range stats.Hourly {
		messageTotal += bucket.Messages
		callTotal += bucket.Calls
	}
	if messageTotal != 7 || callTotal != 5 {
		t.Fatalf("hourly totals = %d messages, %d calls; want 7, 5", messageTotal, callTotal)
	}
	// Bucket 0 is the hour that began a day ago; the last is the hour ending
	// now, and it also takes the message stamped a few minutes ahead.
	if got := stats.Hourly[23]; got.Messages != 3 || got.Calls != 1 {
		t.Fatalf("last hour = %+v, want 3 messages and the ringing call", got)
	}
	// The window began at 12:00 the day before, so 02:30 is 14.5 hours in.
	if got := stats.Hourly[14]; got.Messages != 1 {
		t.Fatalf("02:30 UTC fell in %+v, want bucket 14", stats.Hourly)
	}
	if got := stats.Hourly[22]; got.Messages != 1 || got.Calls != 1 {
		t.Fatalf("two hours ago = %+v, want the untimed message and the outgoing call", got)
	}

	// A member sees only the lines they may open, like the lists do.
	member := auth.ContextWithPrincipal(ctx, auth.Principal{
		UserID:         "member-1",
		Role:           auth.RoleMember,
		AllowedLineIDs: []string{"line-main"},
	})
	scoped, err := repository.CommunicationStats(member, now, 24)
	if err != nil {
		t.Fatal(err)
	}
	if scoped.SMSReceived != 4 {
		t.Fatalf("a member of line-main is counted %d received messages, want 4", scoped.SMSReceived)
	}
	nobody := auth.ContextWithPrincipal(ctx, auth.Principal{UserID: "member-2", Role: auth.RoleMember})
	empty, err := repository.CommunicationStats(nobody, now, 24)
	if err != nil {
		t.Fatal(err)
	}
	if empty.SMSReceived != 0 || empty.CallsIncoming != 0 {
		t.Fatalf("a member with no lines is counted %+v", empty)
	}
}
