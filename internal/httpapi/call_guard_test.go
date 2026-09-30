package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// The dashboard's security card reads the guard through this. It must report
// the real count, never the numbers themselves, and nothing it cannot back.
func TestModuleSecurityReportsTheGuardAndNothingElse(t *testing.T) {
	api, _, _ := licensedAPI(t)
	calls, ok := api.callGuardStore()
	if !ok {
		t.Fatal("the dial guard is unavailable")
	}
	messages, ok := api.messageGuardStore()
	if !ok {
		t.Fatal("the message guard is unavailable")
	}
	calls.Attempt("13800000001")
	calls.Attempt("13800000002")
	calls.Attempt("13800000001")
	messages.Attempt("13900000009")

	read := func() map[string]any {
		t.Helper()
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/security/module", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("module security answered %d: %s", recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), "13800000001") {
			t.Fatal("the response lists a dialled number; it must only count them")
		}
		var body map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	body := read()
	for _, absent := range []string{"risk", "integrity", "trust"} {
		if _, present := body[absent]; present {
			t.Fatalf("%q is in the response, but nothing in this gateway computes it", absent)
		}
	}
	guard, _ := body["guard"].(map[string]any)
	want := map[string]float64{"call_used": 2, "call_limit": 5, "message_used": 1, "message_limit": 3}
	for key, value := range want {
		if guard[key] != value {
			t.Fatalf("%s = %v, want %v (guard %v)", key, guard[key], value, guard)
		}
	}
	if guard["unlocked"] != false || guard["date"] == "" {
		t.Fatalf("guard = %v", guard)
	}
	// The reset is the next LOCAL midnight -- the same boundary the guard's
	// own date comparison uses -- reported as an absolute instant.
	resetsAt, err := time.Parse(time.RFC3339, fmt.Sprint(guard["resets_at"]))
	if err != nil {
		t.Fatalf("resets_at = %v: %v", guard["resets_at"], err)
	}
	if until := time.Until(resetsAt); until <= 0 || until > 24*time.Hour {
		t.Fatalf("resets_at %v is not within the next day", resetsAt)
	}
	if local := resetsAt.In(time.Local); local.Hour() != 0 || local.Minute() != 0 {
		t.Fatalf("resets_at %v is not a local midnight", local)
	}

	calls.Unlock()
	if read()["guard"].(map[string]any)["unlocked"] != true {
		t.Fatal("a lifted guard was not reported as lifted")
	}

	post := httptest.NewRecorder()
	api.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/api/v1/security/module", nil))
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST answered %d", post.Code)
	}
}

func TestTheGuardRollsOverAtTheNextLocalMidnight(t *testing.T) {
	zone := time.FixedZone("CST", 8*3600)
	for _, now := range []time.Time{
		time.Date(2026, 9, 24, 16, 40, 0, 0, zone),
		time.Date(2026, 9, 30, 23, 59, 59, 0, zone),
		time.Date(2026, 12, 31, 0, 0, 0, 0, zone),
	} {
		next := nextGuardRollover(now)
		if next.Hour() != 0 || next.Minute() != 0 || !next.After(now) || next.Sub(now) > 24*time.Hour {
			t.Fatalf("after %v the guard rolls over at %v", now, next)
		}
		if next.Format("2006-01-02") == now.Format("2006-01-02") {
			t.Fatalf("after %v the rollover %v is the same day", now, next)
		}
	}
}
