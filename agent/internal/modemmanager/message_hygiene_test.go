package modemmanager

import (
	"context"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

func TestMessageStateInTransit(t *testing.T) {
	t.Parallel()

	for state, want := range map[string]bool{
		"receiving": true,
		"sending":   true,
		"RECEIVING": true,
		" received": false,
		"received":  false,
		"sent":      false,
		"stored":    false,
		"unknown":   false,
		"":          false,
	} {
		if got := messageStateInTransit(state); got != want {
			t.Errorf("messageStateInTransit(%q) = %t, want %t", state, got, want)
		}
	}
}

// TestStuckTransitMessagesOnlyReportsSecondObservation pins the "stuck" rule:
// a message is only reported once it has been in transit across two
// consecutive passes, so a reception that is legitimately in flight is never
// touched.
func TestStuckTransitMessagesOnlyReportsSecondObservation(t *testing.T) {
	t.Parallel()

	provider := &Provider{}
	first := provider.stuckTransitMessages([]domain.Message{
		{ID: "message_wedged", LineID: "line-1", State: "receiving"},
		{ID: "message_arriving", LineID: "line-1", State: "receiving"},
		{ID: "message_done", LineID: "line-1", State: "received"},
	})
	if len(first) != 0 {
		t.Fatalf("first pass stuck = %+v, want none", first)
	}

	// message_arriving completes, message_wedged never progresses.
	stuck := provider.stuckTransitMessages([]domain.Message{
		{ID: "message_wedged", LineID: "line-1", State: "receiving"},
		{ID: "message_arriving", LineID: "line-1", State: "received"},
		{ID: "message_done", LineID: "line-1", State: "sent"},
	})
	if len(stuck) != 1 || stuck[0].ID != "message_wedged" {
		t.Fatalf("second pass stuck = %+v, want only message_wedged", stuck)
	}

	// A message that disappears from the snapshot is forgotten, so a later
	// reappearance starts a fresh interval instead of inheriting the old one.
	if stuck := provider.stuckTransitMessages(nil); len(stuck) != 0 {
		t.Fatalf("pass after removal stuck = %+v, want none", stuck)
	}
	if stuck := provider.stuckTransitMessages([]domain.Message{
		{ID: "message_wedged", LineID: "line-1", State: "receiving"},
	}); len(stuck) != 0 {
		t.Fatalf("reappearing message stuck = %+v, want none", stuck)
	}
}

// TestReconcileStuckMessagesClearsWedgedReception covers the production failure
// end to end: an SMS whose reception never completes must be cleared from the
// modem on the pass after it is first seen stalled, freeing the SMS slot.
func TestReconcileStuckMessagesClearsWedgedReception(t *testing.T) {
	t.Parallel()

	path := smsCacheTestPath(1)
	caller := newSMSCacheCaller()
	caller.setMessages(
		[]dbus.ObjectPath{path},
		map[dbus.ObjectPath]Properties{
			// State 2 is MM_SMS_STATE_RECEIVING: reception started and never
			// finished.
			path: smsCacheTestProperties(2, "partial reception"),
		},
	)
	provider, err := New(caller)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// First pass only records what is in transit.
	if err := provider.ReconcileStuckMessages(context.Background()); err != nil {
		t.Fatalf("first ReconcileStuckMessages() error = %v", err)
	}
	if deleted := caller.deleted(); len(deleted) != 0 {
		t.Fatalf("first pass deleted = %+v, want none", deleted)
	}

	// Second pass: the message is still in transit, so it is wedged.
	if err := provider.ReconcileStuckMessages(context.Background()); err != nil {
		t.Fatalf("second ReconcileStuckMessages() error = %v", err)
	}
	if deleted := caller.deleted(); len(deleted) != 1 || deleted[0] != path {
		t.Fatalf("deleted paths = %+v, want [%s]", deleted, path)
	}

	// The object is gone, so a further pass is a no-op.
	if err := provider.ReconcileStuckMessages(context.Background()); err != nil {
		t.Fatalf("third ReconcileStuckMessages() error = %v", err)
	}
	if deleted := caller.deleted(); len(deleted) != 1 {
		t.Fatalf("deleted paths after cleanup = %+v, want one", deleted)
	}
}

// TestReconcileStuckMessagesLeavesCompletedReceptionAlone guards the happy path:
// a message that reached a terminal state between passes must not be deleted by
// the reconciler (the API owns persist-then-delete for those).
func TestReconcileStuckMessagesLeavesCompletedReceptionAlone(t *testing.T) {
	t.Parallel()

	path := smsCacheTestPath(1)
	caller := newSMSCacheCaller()
	caller.setMessages(
		[]dbus.ObjectPath{path},
		map[dbus.ObjectPath]Properties{
			// State 4 is MM_SMS_STATE_SENDING, the outgoing in-flight state.
			path: smsCacheTestProperties(4, "in flight"),
		},
	)
	provider, err := New(caller)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := provider.ReconcileStuckMessages(context.Background()); err != nil {
		t.Fatalf("first ReconcileStuckMessages() error = %v", err)
	}
	if deleted := caller.deleted(); len(deleted) != 0 {
		t.Fatalf("in-flight pass deleted = %+v, want none", deleted)
	}

	// Reception completes before the next pass.
	caller.setMessages(
		[]dbus.ObjectPath{path},
		map[dbus.ObjectPath]Properties{
			path: smsCacheTestProperties(5, "sent"),
		},
	)
	if err := provider.ReconcileStuckMessages(context.Background()); err != nil {
		t.Fatalf("second ReconcileStuckMessages() error = %v", err)
	}
	if deleted := caller.deleted(); len(deleted) != 0 {
		t.Fatalf("completed reception deleted = %+v, want none", deleted)
	}
}
