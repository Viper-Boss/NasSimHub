package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The login limiter, as implemented (login_limiter.go):
//
//   - keyed by ACCOUNT NAME (trimmed, ASCII-lowercased, hashed), plus one
//     GLOBAL counter over every failed login. It is deliberately not keyed by
//     client address: the API has no list of trusted proxies, so any address
//     it read from a header would be attacker-chosen.
//   - five failures in a five-minute window start an exponential back-off
//     (1 s doubling to 60 s); twenty failures across all accounts do the same
//     for everybody.
//   - a successful login clears that account's counter, and nothing else.
//   - at most 1024 account entries are kept.
//
// advClock replaces the limiter's clock so recovery can be tested without
// sleeping. The seam already existed (loginFailureLimiter.now).
type advClock struct {
	mu  sync.Mutex
	now time.Time
}

func newAdvClock() *advClock {
	return &advClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
}

func (clock *advClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *advClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

func advHarnessWithClock(t *testing.T) (*advHarness, *advClock) {
	t.Helper()
	harness := newAdvHarness(t)
	clock := newAdvClock()
	harness.api.loginFailures.now = clock.Now
	return harness, clock
}

func (harness *advHarness) failLogins(username string, count int) {
	harness.t.Helper()
	for attempt := 0; attempt < count; attempt++ {
		_, response := harness.tryLogin(username, "adv-wrong-password", nil)
		if response.Code != http.StatusUnauthorized {
			harness.t.Fatalf("failed login %d for %q: status = %d %s", attempt+1, username, response.Code, response.Body)
		}
	}
}

func TestAdversarialLoginLockoutEngagesPerAccountAndHidesThePasswordVerdict(t *testing.T) {
	harness, _ := advHarnessWithClock(t)

	harness.failLogins("adv-alice", loginAccountFailureThreshold)

	// Locked: the wrong password and the RIGHT password get the same answer,
	// so the lockout cannot be used as an oracle for a correct guess.
	_, wrong := harness.tryLogin("adv-alice", "adv-wrong-password", nil)
	_, right := harness.tryLogin("adv-alice", advAlicePassword, nil)
	for _, response := range []struct {
		name string
		code int
		body string
		wait string
	}{
		{"wrong password", wrong.Code, wrong.Body.String(), wrong.Header().Get("Retry-After")},
		{"right password", right.Code, right.Body.String(), right.Header().Get("Retry-After")},
	} {
		if response.code != http.StatusTooManyRequests {
			t.Fatalf("%s while locked: status = %d %s", response.name, response.code, response.body)
		}
		if seconds, err := strconv.Atoi(response.wait); err != nil || seconds < 1 {
			t.Fatalf("%s while locked: Retry-After = %q", response.name, response.wait)
		}
	}
	if wrong.Body.String() != right.Body.String() {
		t.Fatalf("the lockout answers differ by password:\n%s\n%s", wrong.Body, right.Body)
	}
	if len(right.Result().Cookies()) != 0 {
		t.Fatal("a locked-out login set cookies")
	}
	// Per account: another member is unaffected.
	if _, response := harness.tryLogin("adv-bob", advBobPassword, nil); response.Code != http.StatusOK {
		t.Fatalf("one account's lockout refused another account: %d %s", response.Code, response.Body)
	}
}

func TestAdversarialLoginLockoutCannotBeSidestepped(t *testing.T) {
	harness, _ := advHarnessWithClock(t)
	harness.failLogins("adv-alice", loginAccountFailureThreshold)

	attempts := []struct {
		name     string
		username string
		headers  map[string]string
		remote   string
	}{
		{name: "upper case", username: "ADV-ALICE"},
		{name: "mixed case", username: "Adv-Alice"},
		{name: "trailing space", username: "adv-alice "},
		{name: "leading and trailing whitespace", username: " \t adv-alice \n"},
		{name: "ideographic space", username: "adv-alice　"},
		{name: "X-Forwarded-For", username: "adv-alice", headers: map[string]string{"X-Forwarded-For": "198.51.100.1"}},
		{name: "X-Real-IP", username: "adv-alice", headers: map[string]string{"X-Real-IP": "198.51.100.2"}},
		{name: "X-ModemDeck-Client-IP", username: "adv-alice", headers: map[string]string{"X-ModemDeck-Client-IP": "198.51.100.3"}},
		{name: "CF-Connecting-IP", username: "adv-alice", headers: map[string]string{"CF-Connecting-IP": "198.51.100.4"}},
		{name: "Forwarded", username: "adv-alice", headers: map[string]string{"Forwarded": "for=198.51.100.5"}},
		{name: "another User-Agent", username: "adv-alice", headers: map[string]string{"User-Agent": "adv-other-agent"}},
	}
	for _, attempt := range attempts {
		// The correct password, so a bypass would show as a 200.
		_, response := harness.tryLogin(attempt.username, advAlicePassword, attempt.headers)
		if response.Code != http.StatusTooManyRequests {
			t.Errorf("%s: status = %d, want 429; the lockout was sidestepped: %s",
				attempt.name, response.Code, response.Body)
		}
	}
}

func TestAdversarialAnotherAccountsSuccessDoesNotResetTheCounter(t *testing.T) {
	harness, _ := advHarnessWithClock(t)

	harness.failLogins("adv-alice", loginAccountFailureThreshold-1)
	// The attacker logs in to an account they do own, hoping the success
	// clears the failures they have run up against alice.
	if _, response := harness.tryLogin("adv-bob", advBobPassword, nil); response.Code != http.StatusOK {
		t.Fatalf("bob login: %d", response.Code)
	}
	harness.failLogins("adv-alice", 1)
	if _, response := harness.tryLogin("adv-alice", advAlicePassword, nil); response.Code != http.StatusTooManyRequests {
		t.Fatalf("alice is not locked after five failures interleaved with bob's success: %d", response.Code)
	}
}

func TestAdversarialUnknownAccountsAreThrottledLikeRealOnes(t *testing.T) {
	harness, _ := advHarnessWithClock(t)

	_, unknown := harness.tryLogin("adv-nobody", "adv-wrong-password", nil)
	_, known := harness.tryLogin("adv-alice", "adv-wrong-password", nil)
	if unknown.Code != known.Code || unknown.Body.String() != known.Body.String() {
		t.Fatalf("a wrong password and an unknown account answer differently:\n%d %s\n%d %s",
			unknown.Code, unknown.Body, known.Code, known.Body)
	}
	harness.failLogins("adv-nobody", loginAccountFailureThreshold-1)
	if _, response := harness.tryLogin("adv-nobody", "adv-wrong-password", nil); response.Code != http.StatusTooManyRequests {
		t.Fatalf("an unknown account is not locked after five failures (%d); the limiter would reveal which names exist",
			response.Code)
	}
}

func TestAdversarialLoginLockoutRecovers(t *testing.T) {
	harness, clock := advHarnessWithClock(t)
	harness.failLogins("adv-alice", loginAccountFailureThreshold)

	// The back-off doubles with each further failure and stops at the cap.
	expected := loginFailureInitialBackoff
	for step := 0; step < 9; step++ {
		_, response := harness.tryLogin("adv-alice", "adv-wrong-password", nil)
		advRequireStatus(t, response, http.StatusTooManyRequests, "login_rate_limited")
		if got := response.Header().Get("Retry-After"); got != strconv.Itoa(int(expected/time.Second)) {
			t.Fatalf("step %d: Retry-After = %s, want %v", step, got, expected)
		}
		// One instant before the block ends it still holds...
		clock.Advance(expected - time.Millisecond)
		if _, response := harness.tryLogin("adv-alice", advAlicePassword, nil); response.Code != http.StatusTooManyRequests {
			t.Fatalf("step %d: the block lifted early (%d)", step, response.Code)
		}
		// ...and at the instant it ends one attempt is admitted.
		clock.Advance(time.Millisecond)
		if clock.Now().Sub(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)) >= loginFailureWindow {
			break // the window itself has now expired; covered below
		}
		_, response = harness.tryLogin("adv-alice", "adv-wrong-password", nil)
		advRequireStatus(t, response, http.StatusUnauthorized, "invalid_credentials")
		expected *= 2
		if expected > loginFailureMaximumBackoff {
			expected = loginFailureMaximumBackoff
		}
	}

	// After the block, the correct password works and clears the account.
	clock.Advance(loginFailureMaximumBackoff)
	if _, response := harness.tryLogin("adv-alice", advAlicePassword, nil); response.Code != http.StatusOK {
		t.Fatalf("the correct password is refused after the block ended: %d %s", response.Code, response.Body)
	}
	harness.failLogins("adv-alice", loginAccountFailureThreshold-1)
	if _, response := harness.tryLogin("adv-alice", advAlicePassword, nil); response.Code != http.StatusOK {
		t.Fatalf("a success did not clear the account's earlier failures: %d", response.Code)
	}

	// A quiet window forgets the failures altogether.
	harness.failLogins("adv-bob", loginAccountFailureThreshold-1)
	clock.Advance(loginFailureWindow)
	harness.failLogins("adv-bob", loginAccountFailureThreshold-1)
	if _, response := harness.tryLogin("adv-bob", advBobPassword, nil); response.Code != http.StatusOK {
		t.Fatalf("failures from an expired window still count: %d", response.Code)
	}
}

// FINDING A-05 (medium, design): the global counter is a denial of service
// against login. Twenty failures against any names at all -- from one
// unauthenticated client -- put EVERY account behind the back-off, the
// administrator's correct password included, and the attacker can renew it
// indefinitely with one request a minute.
//
// It exists because the limiter cannot see who is asking. It is the price of
// refusing to trust client-address headers, not an oversight, so it is left as
// it is and reported. This test pins both halves: that the lock is global, and
// that it does lift.
func TestAdversarialGlobalThrottleLocksEveryAccountAndRecovers(t *testing.T) {
	harness, clock := advHarnessWithClock(t)
	// Driven through the limiter, not twenty real logins: an unknown account
	// costs a full-strength Argon2 comparison each, by design.
	for attempt := 0; attempt < loginGlobalFailureThreshold; attempt++ {
		harness.api.loginFailures.recordFailure(fmt.Sprintf("adv-spray-%d", attempt))
	}

	for _, account := range []struct{ username, password string }{
		{advAdminUser, advAdminPassword},
		{"adv-alice", advAlicePassword},
	} {
		_, response := harness.tryLogin(account.username, account.password, nil)
		advRequireStatus(t, response, http.StatusTooManyRequests, "login_rate_limited")
	}
	// Sessions that already exist are not affected: the throttle is on the
	// password check only.
	clock.Advance(loginFailureInitialBackoff)
	admin := harness.admin()
	if !admin.alive() {
		t.Fatal("the administrator could not log in after the global back-off ended")
	}
	for attempt := 0; attempt < 3; attempt++ {
		harness.api.loginFailures.recordFailure("adv-spray-again")
	}
	if !admin.alive() {
		t.Fatal("the login throttle ended an existing session")
	}
	clock.Advance(loginFailureWindow + loginFailureMaximumBackoff)
	if _, response := harness.tryLogin("adv-alice", advAlicePassword, nil); response.Code != http.StatusOK {
		t.Fatalf("the global throttle did not lift after a quiet window: %d %s", response.Code, response.Body)
	}
}

func TestAdversarialLimiterMemoryIsBoundedUnderAFlood(t *testing.T) {
	clock := newAdvClock()
	limiter := newLoginFailureLimiter(defaultLoginFailurePolicy)
	limiter.now = clock.Now

	const flood = 50_000
	for attempt := 0; attempt < flood; attempt++ {
		limiter.recordFailure(fmt.Sprintf("adv-flood-%d", attempt))
		limiter.retryAfter(fmt.Sprintf("adv-probe-%d", attempt))
		if attempt%1000 == 0 {
			clock.Advance(time.Second)
		}
	}
	limiter.mu.Lock()
	accounts := len(limiter.accounts)
	limiter.mu.Unlock()
	if accounts > loginFailureMaximumAccountKeys {
		t.Fatalf("the limiter holds %d account entries after %d distinct names; the cap is %d",
			accounts, flood, loginFailureMaximumAccountKeys)
	}
	// The day's history is a fixed array, whatever is thrown at it.
	if size := len(limiter.history.minutes); size != int(loginHistoryWindow/time.Minute) {
		t.Fatalf("the failure history has %d slots", size)
	}
	if failures, _ := limiter.history.recent(clock.Now()); failures != flood {
		t.Fatalf("the failure history counted %d of %d failures", failures, flood)
	}
	// Probing a name (retryAfter) never allocates an entry: only a failure
	// does, so reading the limiter cannot be used to fill it.
	fresh := newLoginFailureLimiter(defaultLoginFailurePolicy)
	for attempt := 0; attempt < 5000; attempt++ {
		fresh.retryAfter(fmt.Sprintf("adv-probe-%d", attempt))
	}
	if len(fresh.accounts) != 0 {
		t.Fatalf("probing created %d limiter entries", len(fresh.accounts))
	}
}

// FINDING A-06 (low): the cap is enforced by evicting the least recently
// failed account, so a flood of other names erases a victim's counter. It is
// not a practical bypass -- every one of those names is itself a failed login
// that the global throttle paces -- and that second half is what this test
// holds on to: the flood needed costs hours of enforced waiting to win back
// five guesses.
func TestAdversarialEvictingAVictimsCounterCostsMoreThanItBuys(t *testing.T) {
	clock := newAdvClock()
	started := clock.Now()
	limiter := newLoginFailureLimiter(defaultLoginFailurePolicy)
	limiter.now = clock.Now

	for attempt := 0; attempt < loginAccountFailureThreshold; attempt++ {
		limiter.recordFailure("adv-victim")
	}
	tracked := func() bool {
		limiter.mu.Lock()
		defer limiter.mu.Unlock()
		_, found := limiter.accounts[loginAccountKey("adv-victim")]
		return found
	}
	flood := 0
	// Entries that failed at the same instant as the victim tie for "oldest",
	// so the exact count varies by a few; twice the cap is the bound.
	for ; tracked() && flood < 2*loginFailureMaximumAccountKeys; flood++ {
		name := fmt.Sprintf("adv-evict-%d", flood)
		// What the HTTP handler does while the global throttle is engaged:
		// refuse without verifying, and so without recording a failure. The
		// attacker has to wait the block out to make the next one count.
		if wait := limiter.retryAfter(name); wait > 0 {
			clock.Advance(wait)
		}
		limiter.recordFailure(name)
	}
	if tracked() {
		t.Fatalf("the victim's entry survived %d other failures; A-06 no longer describes the code -- update the audit", flood)
	}
	if flood < loginFailureMaximumAccountKeys-1 {
		t.Fatalf("the victim's counter was evicted after only %d other names; the cap is %d",
			flood, loginFailureMaximumAccountKeys)
	}
	elapsed := clock.Now().Sub(started)
	if elapsed < time.Hour {
		t.Fatalf("erasing one account's counter took only %v of enforced waiting", elapsed)
	}
	t.Logf("erasing one account's five-failure counter took %d failed logins and %v of enforced back-off",
		flood, elapsed.Round(time.Minute))
}

// FINDING A-13 (low): a block that outlasts the five-minute window is cut
// short when the window expires, because the whole state -- blockedUntil
// included -- is dropped at that instant. The loss is at most one back-off
// period (60 s) and the caller starts a fresh window either way.
func TestAdversarialWindowExpiryEndsAnActiveBlockEarly(t *testing.T) {
	clock := newAdvClock()
	limiter := newLoginFailureLimiter(defaultLoginFailurePolicy)
	limiter.now = clock.Now

	for attempt := 0; attempt < loginAccountFailureThreshold; attempt++ {
		limiter.recordFailure("adv-victim")
	}
	clock.Advance(loginFailureWindow - time.Second)
	for attempt := 0; attempt < 8; attempt++ {
		limiter.recordFailure("adv-victim") // drives the back-off to its 60 s cap
	}
	if wait := limiter.retryAfter("adv-victim"); wait != loginFailureMaximumBackoff {
		t.Fatalf("back-off = %v, want the %v cap", wait, loginFailureMaximumBackoff)
	}
	clock.Advance(time.Second) // the window expires 59 s before the block would
	if wait := limiter.retryAfter("adv-victim"); wait != 0 {
		t.Fatalf("the block now outlives the window (%v left): A-13 is fixed -- update the audit and invert this test", wait)
	}
}

// FINDING A-14 (low): PUT /api/v1/account/password verifies current_password
// with no failure counter. It needs a live session and its CSRF token, so it
// is not a way in; it does let whoever holds a stolen session guess the
// password at the speed of the two-slot gate.
func TestAdversarialPasswordChangeHasNoFailureLimiter(t *testing.T) {
	harness, _ := advHarnessWithClock(t)
	alice := harness.aliceClient()
	for attempt := 0; attempt < 2*loginAccountFailureThreshold; attempt++ {
		response := alice.send(http.MethodPut, "/api/v1/account/password",
			`{"current_password":"adv-guess-`+strconv.Itoa(attempt)+`","new_password":"adv-alice-password-9"}`)
		if response.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d was rate limited: A-14 is fixed -- update the audit and invert this test", attempt)
		}
		advRequireStatus(t, response, http.StatusBadRequest, "invalid_current_password")
	}
	// Those guesses do not count against the login limiter either, so they
	// cannot be used to lock the account's owner out.
	if _, response := harness.tryLogin("adv-alice", advAlicePassword, nil); response.Code != http.StatusOK {
		t.Fatalf("wrong current_password guesses locked the login: %d", response.Code)
	}
}

func TestAdversarialBusyLoginGateIsNotCountedAsAFailure(t *testing.T) {
	harness, _ := advHarnessWithClock(t)
	// Occupy both verification slots, as two slow logins in flight would.
	harness.api.loginSlots <- struct{}{}
	harness.api.loginSlots <- struct{}{}
	for attempt := 0; attempt < 3*loginAccountFailureThreshold; attempt++ {
		_, response := harness.tryLogin("adv-alice", "adv-wrong-password", nil)
		advRequireStatus(t, response, http.StatusTooManyRequests, "login_busy")
	}
	<-harness.api.loginSlots
	<-harness.api.loginSlots
	if _, response := harness.tryLogin("adv-alice", advAlicePassword, nil); response.Code != http.StatusOK {
		t.Fatalf("requests turned away at the gate were counted as failed passwords: %d %s", response.Code, response.Body)
	}
}
