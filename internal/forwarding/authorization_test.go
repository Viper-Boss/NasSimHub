// SPDX-License-Identifier: AGPL-3.0-only
package forwarding

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthorizationUnavailableDefersWithoutConsumingAttempts(t *testing.T) {
	for _, boundary := range []string{"before_claim", "before_send", "sms_handler"} {
		t.Run(boundary, func(t *testing.T) {
			ctx := context.Background()
			s, path := openTestStore(t)
			c := configFor(t, s, 1, "all")
			if boundary == "sms_handler" {
				c.Channels[0].Type = "sms"
				c.Channels[0].Params = map[string]string{"line_id": "line-A", "to": "+15555550199"}
				c.Channels[0].Secrets = nil
				if _, err := s.Save(ctx, c); err != nil {
					t.Fatal(err)
				}
			}
			clock := time.Now().UTC().Truncate(time.Millisecond)
			insertEvent(t, s, clock, "private synthetic payload")
			var checks, sends atomic.Int32
			unavailable := true
			engine := &Engine{Store: s, Now: func() time.Time { return clock }}
			engine.Authorize = func(context.Context, string, Event, Channel) error {
				n := checks.Add(1)
				if unavailable && (boundary == "before_claim" || boundary == "before_send" && n%2 == 0) {
					return errors.New("private repository failure")
				}
				return nil
			}
			engine.Sender = senderFunc(func(context.Context, Channel, Rendered, string) error {
				if unavailable && boundary == "sms_handler" {
					return ErrUnavailable
				}
				sends.Add(1)
				return nil
			})
			for i := 0; i <= MaxAttempts; i++ {
				if err := engine.Step(ctx, clock); !errors.Is(err, ErrUnavailable) {
					t.Fatalf("temporary fault %d: %v", i, err)
				}
				rows, err := s.History(ctx, 0)
				if err != nil || len(rows) != 1 {
					t.Fatalf("history: %+v %v", rows, err)
				}
				task := rows[0]
				if task.Status != "pending" || task.ErrorClass != "authorization_unavailable" || task.Attempts != 0 || task.LeaseUntil != 0 || task.NextAt != clock.Add(30*time.Second).UnixMilli() || sends.Load() != 0 {
					t.Fatalf("fault consumed or cancelled delivery: %+v sends=%d", task, sends.Load())
				}
				private, err := s.Task(ctx, task.ID)
				if err != nil || private.Event.Body != "private synthetic payload" || private.Token != "" {
					t.Fatalf("retry lost private payload or lease: %v", err)
				}
				before := checks.Load()
				if err = engine.Step(ctx, clock.Add(29*time.Second)); err != nil || checks.Load() != before {
					t.Fatalf("retried before deadline: %v", err)
				}
				clock = clock.Add(30 * time.Second)
			}
			// The deferred task must survive a restart, not just a live worker.
			if err := s.DB.Close(); err != nil {
				t.Fatal(err)
			}
			s = reopenTestStore(t, path)
			engine.Store = s
			unavailable = false
			if err := engine.Step(ctx, clock); err != nil {
				t.Fatal(err)
			}
			rows, err := s.History(ctx, 0)
			if err != nil || len(rows) != 1 || rows[0].Status != "sent" || rows[0].Attempts != 1 || sends.Load() != 1 {
				t.Fatalf("did not recover exactly once: %+v sends=%d err=%v", rows, sends.Load(), err)
			}
		})
	}
}

func TestExplicitAuthorizationDenialStillCancelsAtBothChecks(t *testing.T) {
	for _, denyAt := range []int32{1, 2} {
		t.Run(fmt.Sprint(denyAt), func(t *testing.T) {
			s, _ := openTestStore(t)
			configFor(t, s, 1, "all")
			now := time.Now().UTC()
			insertEvent(t, s, now, "private synthetic payload")
			var checks, sends atomic.Int32
			engine := &Engine{Store: s, Authorize: func(context.Context, string, Event, Channel) error {
				if checks.Add(1) == denyAt {
					return fmt.Errorf("explicit refusal: %w", ErrDenied)
				}
				return nil
			}, Sender: senderFunc(func(context.Context, Channel, Rendered, string) error { sends.Add(1); return nil })}
			if err := engine.Step(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			rows, err := s.History(context.Background(), 0)
			if err != nil || len(rows) != 1 || rows[0].Status != "cancelled" || rows[0].ErrorClass != "authorization_revoked" || sends.Load() != 0 {
				t.Fatalf("denied send escaped: %+v sends=%d err=%v", rows, sends.Load(), err)
			}
			var payload string
			if err = s.DB.QueryRow(`SELECT payload FROM modemdeck_forwarding_tasks`).Scan(&payload); err != nil || strings.Contains(payload, "private") {
				t.Fatal("cancelled payload retained", err)
			}
		})
	}
}

func TestAuthorizationOutageStillExpiresTask(t *testing.T) {
	s, _ := openTestStore(t)
	configFor(t, s, 1, "all")
	clock := time.Now().UTC()
	insertEvent(t, s, clock, "synthetic")
	var sends atomic.Int32
	engine := &Engine{Store: s, Now: func() time.Time { return clock }, Authorize: func(context.Context, string, Event, Channel) error { return ErrUnavailable }, Sender: senderFunc(func(context.Context, Channel, Rendered, string) error { sends.Add(1); return nil })}
	if err := engine.Step(context.Background(), clock); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	clock = clock.Add(MaxAge)
	if err := engine.Step(context.Background(), clock); err != nil {
		t.Fatal(err)
	}
	rows, err := s.History(context.Background(), 0)
	if err != nil || len(rows) != 1 || rows[0].Status != "expired" || sends.Load() != 0 {
		t.Fatalf("outage extended lifetime: %+v %v", rows, err)
	}
}

func TestAuthorizationDeferralPreservesInterruptedAttemptsAndLeaseOwnership(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t)
	configFor(t, s, 1, "all")
	now := time.Now().UTC().Truncate(time.Millisecond)
	insertEvent(t, s, now, "synthetic")
	if err := s.Ingest(ctx, now); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.Due(ctx, now)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("due: %+v %v", tasks, err)
	}
	interrupted, ok, err := s.Claim(ctx, tasks[0], now)
	if err != nil || !ok {
		t.Fatal("initial claim", ok, err)
	}
	now = now.Add(46 * time.Second)
	// The previous worker might have sent before crashing; never refund it.
	if err = s.deferAuthorization(ctx, interrupted, now, false); err != nil {
		t.Fatal(err)
	}
	deferred, err := s.Task(ctx, interrupted.ID)
	if err != nil || deferred.Attempts != 1 || deferred.Status != "pending" {
		t.Fatalf("interrupted send was refunded: %+v %v", deferred, err)
	}
	now = now.Add(30 * time.Second)
	current, ok, err := s.Claim(ctx, deferred, now)
	if err != nil || !ok {
		t.Fatal("new claim", ok, err)
	}
	for _, claimed := range []bool{false, true} {
		if err = s.deferAuthorization(ctx, interrupted, now, claimed); err != nil {
			t.Fatal(err)
		}
		actual, err := s.Task(ctx, current.ID)
		if err != nil || actual.Status != "sending" || actual.Token != current.Token || actual.Attempts != 2 || actual.LeaseUntil != current.LeaseUntil {
			t.Fatalf("stale worker modified new lease: %+v %v", actual, err)
		}
	}
	if err = s.deferAuthorization(ctx, current, now, true); err != nil {
		t.Fatal(err)
	}
	actual, err := s.Task(ctx, current.ID)
	if err != nil || actual.Status != "pending" || actual.Attempts != 1 || actual.Token != "" {
		t.Fatalf("did not refund only unused claim: %+v %v", actual, err)
	}
}

func TestAuthorizationRetryRechecksRevocationAndConfiguration(t *testing.T) {
	for _, change := range []string{"revoked", "configuration_changed"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			s, _ := openTestStore(t)
			c := configFor(t, s, 1, "all")
			clock := time.Now().UTC()
			insertEvent(t, s, clock, "synthetic")
			var sends atomic.Int32
			engine := &Engine{Store: s, Now: func() time.Time { return clock }, Authorize: func(context.Context, string, Event, Channel) error { return ErrUnavailable }, Sender: senderFunc(func(context.Context, Channel, Rendered, string) error { sends.Add(1); return nil })}
			if err := engine.Step(ctx, clock); !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
			wantClass := "authorization_revoked"
			if change == "revoked" {
				engine.Authorize = func(context.Context, string, Event, Channel) error { return ErrDenied }
			} else {
				c.Enabled = false
				if _, err := s.Save(ctx, c); err != nil {
					t.Fatal(err)
				}
				wantClass = "configuration_changed"
			}
			clock = clock.Add(30 * time.Second)
			if err := engine.Step(ctx, clock); err != nil {
				t.Fatal(err)
			}
			rows, err := s.History(ctx, 0)
			if err != nil || len(rows) != 1 || rows[0].Status != "cancelled" || rows[0].ErrorClass != wantClass || sends.Load() != 0 {
				t.Fatalf("retry bypassed current policy: %+v sends=%d err=%v", rows, sends.Load(), err)
			}
		})
	}
}
