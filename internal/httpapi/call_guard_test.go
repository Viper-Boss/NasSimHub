package httpapi

import (
	"path/filepath"
	"testing"
)

func TestCallGuardDistinctNumberLimit(t *testing.T) {
	guard := newCallGuard(filepath.Join(t.TempDir(), "call_guard.json"))
	if guard.Attempt("13800000001") {
		t.Fatal("first dial must not be blocked")
	}
	if guard.Attempt("13800000001") {
		t.Fatal("repeat dial of the same number must not be blocked")
	}
	if guard.Attempt("138-0000-0001") {
		t.Fatal("formatting variants count as the same number")
	}
	for _, number := range []string{"13800000002", "13800000003", "13800000004", "13800000005"} {
		if guard.Attempt(number) {
			t.Fatalf("dial %s within the daily budget must not be blocked", number)
		}
	}
	if !guard.Attempt("13800000006") {
		t.Fatal("the sixth distinct number must be blocked")
	}
	if guard.Attempt("13800000001") {
		t.Fatal("re-dialing an allowed number stays allowed after the limit hit")
	}
	guard.Unlock()
	if guard.Attempt("13800000006") {
		t.Fatal("after unlock any number is allowed")
	}
	if guard.Attempt("13900000000") {
		t.Fatal("after unlock any number is allowed")
	}
}

func TestCallGuardPersistsUnlockAndRollover(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "call_guard.json")
	guard := newCallGuard(path)
	for _, number := range []string{"1", "2", "3", "4", "5"} {
		guard.Attempt(number)
	}
	if !guard.Attempt("6") {
		t.Fatal("expected the limit to block the sixth distinct number")
	}
	guard.Unlock()

	reloaded := newCallGuard(path)
	if reloaded.Attempt("6") {
		t.Fatal("unlocked state must survive a restart")
	}
	reloaded.mu.Lock()
	reloaded.state.Date = "2000-01-01"
	reloaded.mu.Unlock()
	if reloaded.Attempt("7") {
		t.Fatal("a new day resets the budget regardless of unlock state")
	}
}
