package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestCallGuardDistinctNumberLimit(t *testing.T) {
	guard := newCallGuard(filepath.Join(t.TempDir(), "call_guard.json"), callGuardDailyLimit)
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
	guard := newCallGuard(path, callGuardDailyLimit)
	for _, number := range []string{"1", "2", "3", "4", "5"} {
		guard.Attempt(number)
	}
	if !guard.Attempt("6") {
		t.Fatal("expected the limit to block the sixth distinct number")
	}
	guard.Unlock()

	reloaded := newCallGuard(path, callGuardDailyLimit)
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

// Messages get the same guard with a smaller budget, because three messages
// and five calls are different amounts of the same kind of harm.
func TestMessageGuardAllowsThreeNewRecipients(t *testing.T) {
	guard := newCallGuard(filepath.Join(t.TempDir(), "message_guard.json"), messageGuardDailyLimit)

	for index, number := range []string{"13800000001", "13800000002", "13800000003"} {
		if guard.Attempt(number) {
			t.Fatalf("recipient %d was blocked inside the allowance", index+1)
		}
	}
	if !guard.Attempt("13800000004") {
		t.Fatal("a fourth new recipient was allowed")
	}

	// The point of counting DISTINCT numbers: an ordinary conversation never
	// runs into the limit, however many messages it involves.
	for range 20 {
		if guard.Attempt("13800000001") {
			t.Fatal("replying to someone already messaged today was blocked")
		}
	}
}

// The remaining count is what lets the UI warn while there is still something
// to do about it. A guard that only speaks when it blocks has already cost the
// operator the message they were writing.
func TestMessageGuardReportsWhatIsLeft(t *testing.T) {
	guard := newCallGuard(filepath.Join(t.TempDir(), "message_guard.json"), messageGuardDailyLimit)

	if remaining, unlocked := guard.Remaining(); remaining != 3 || unlocked {
		t.Fatalf("a fresh guard should report 3 remaining, got %d (unlocked=%v)", remaining, unlocked)
	}
	guard.Attempt("13800000001")
	if remaining, _ := guard.Remaining(); remaining != 2 {
		t.Fatalf("expected 2 remaining, got %d", remaining)
	}
	// Repeating a number costs nothing.
	guard.Attempt("13800000001")
	if remaining, _ := guard.Remaining(); remaining != 2 {
		t.Fatalf("a repeat recipient consumed the allowance, %d left", remaining)
	}

	guard.Unlock()
	if _, unlocked := guard.Remaining(); !unlocked {
		t.Fatal("an unlocked guard should report as unlocked")
	}
	if guard.Attempt("13900000000") {
		t.Fatal("an unlocked guard still blocked a new recipient")
	}
}

// One "unlock" lifts BOTH channels.
//
// The operator is telling the software who they are, not which feature they
// want, and a partial unlock would leave them re-tripping a guard they thought
// they had lifted. Asserted through the HTTP endpoint rather than the guard
// objects because the shared part lives in the handler: each guard has its own
// file and its own Unlock, and it is the handler that calls both.
func TestTheDialPadUnlockLiftsCallsAndMessagesTogether(t *testing.T) {
	api, _, _ := licensedAPI(t)
	calls, ok := api.callGuardStore()
	if !ok {
		t.Fatal("the dial guard is unavailable")
	}
	messages, ok := api.messageGuardStore()
	if !ok {
		t.Fatal("the message guard is unavailable")
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/calls/guard",
		strings.NewReader(`{"action":"unlock"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unlock answered %d: %s", recorder.Code, recorder.Body.String())
	}

	if _, unlocked := calls.Remaining(); !unlocked {
		t.Fatal("the dial guard was not lifted")
	}
	if _, unlocked := messages.Remaining(); !unlocked {
		t.Fatal(
			"the message guard was not lifted; the operator unlocked on the dial pad " +
				"and would still be blocked from texting a new number",
		)
	}
}
