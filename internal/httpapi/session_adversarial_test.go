package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/auth"
	"github.com/human-agent65535/modemdeck/internal/store"
)

// Old sessions must stop working the moment the thing that justified them is
// gone. Each test below takes a session that was valid, removes its
// justification through the real API, and then uses the OLD cookie again.

func TestAdversarialSessionDiesOnLogout(t *testing.T) {
	harness := newAdvHarness(t)
	client := harness.aliceClient()
	other := harness.aliceClient()

	response := client.send(http.MethodDelete, "/api/v1/session", "")
	advRequireStatus(t, response, http.StatusNoContent, "")

	if client.alive() {
		t.Fatal("a logged-out session cookie still authenticates")
	}
	// A write with the dead session and its (once valid) CSRF token must be an
	// authentication failure, not a CSRF one and certainly not a success.
	response = client.send(http.MethodPut, "/api/v1/account/contact", `{"contact_id":""}`)
	advRequireStatus(t, response, http.StatusUnauthorized, "authentication_required")
	if !other.alive() {
		t.Fatal("logging out one browser ended the user's other session")
	}
}

func TestAdversarialOwnPasswordChangeEndsEveryOwnSessionAndOnlyThose(t *testing.T) {
	harness := newAdvHarness(t)
	current := harness.aliceClient()
	elsewhere := harness.aliceClient()
	bob := harness.bobClient()
	mobile := harness.mobileToken(harness.alice.ID)

	response := current.send(http.MethodPut, "/api/v1/account/password",
		`{"current_password":"`+advAlicePassword+`","new_password":"adv-alice-password-2"}`)
	advRequireStatus(t, response, http.StatusNoContent, "")

	if current.alive() || elsewhere.alive() {
		t.Fatal("a session opened under the old password survived the password change")
	}
	if !bob.alive() {
		t.Fatal("one member's password change ended another member's session")
	}
	// The paired phone was authorised under the old password too.
	request := httptest.NewRequest(http.MethodGet, "/api/v1/mobile/session", nil)
	request.Header.Set("Authorization", "Bearer "+mobile)
	recorder := httptest.NewRecorder()
	harness.api.ServeHTTP(recorder, request)
	advRequireStatus(t, recorder, http.StatusUnauthorized, "authentication_required")

	if _, response := harness.tryLogin("adv-alice", advAlicePassword, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("the old password still logs in: %d", response.Code)
	}
	if _, response := harness.tryLogin("adv-alice", "adv-alice-password-2", nil); response.Code != http.StatusOK {
		t.Fatalf("the new password does not log in: %d %s", response.Code, response.Body)
	}
}

func TestAdversarialPasswordChangeNeedsTheCurrentPassword(t *testing.T) {
	harness := newAdvHarness(t)
	client := harness.aliceClient()

	// A stolen session alone must not be enough to take the account over.
	response := client.send(http.MethodPut, "/api/v1/account/password",
		`{"current_password":"not-the-password","new_password":"adv-alice-password-2"}`)
	advRequireStatus(t, response, http.StatusBadRequest, "invalid_current_password")
	if !client.alive() {
		t.Fatal("a refused password change ended the session")
	}
	if _, response := harness.tryLogin("adv-alice", "adv-alice-password-2", nil); response.Code == http.StatusOK {
		t.Fatal("the refused password change took effect")
	}
}

func (harness *advHarness) memberRevision(admin *advClient, userID string) store.User {
	harness.t.Helper()
	response := admin.get("/api/v1/users")
	if response.Code != http.StatusOK {
		harness.t.Fatalf("list users: %d %s", response.Code, response.Body)
	}
	var body usersResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		harness.t.Fatalf("decode users: %v", err)
	}
	for _, user := range body.Users {
		if user.ID == userID {
			return user
		}
	}
	harness.t.Fatalf("user %s is not listed", userID)
	return store.User{}
}

func (harness *advHarness) updateMember(
	admin *advClient,
	user store.User,
	mutate func(*updateMemberRequest),
) *httptest.ResponseRecorder {
	harness.t.Helper()
	current := harness.memberRevision(admin, user.ID)
	input := updateMemberRequest{
		Username:          current.Username,
		Enabled:           current.Enabled,
		IOSPairingEnabled: current.IOSPairingEnabled,
		LineIDs:           current.LineIDs,
		Revision:          current.Revision,
	}
	mutate(&input)
	body, _ := json.Marshal(input)
	return admin.send(http.MethodPut, "/api/v1/users/"+user.ID, string(body))
}

func TestAdversarialAdministratorPasswordResetEndsTheMembersSessions(t *testing.T) {
	harness := newAdvHarness(t)
	admin := harness.admin()
	alice := harness.aliceClient()
	aliceElsewhere := harness.aliceClient()
	bob := harness.bobClient()

	response := admin.send(http.MethodPut, "/api/v1/users/"+harness.alice.ID+"/password",
		`{"password":"adv-alice-reset-1"}`)
	advRequireStatus(t, response, http.StatusNoContent, "")

	if alice.alive() || aliceElsewhere.alive() {
		t.Fatal("the member's sessions survived an administrator password reset")
	}
	if !bob.alive() || !admin.alive() {
		t.Fatal("resetting one member's password ended somebody else's session")
	}
}

func TestAdversarialUpdateWithNewPasswordEndsTheMembersSessions(t *testing.T) {
	harness := newAdvHarness(t)
	admin := harness.admin()
	alice := harness.aliceClient()

	response := harness.updateMember(admin, harness.alice, func(input *updateMemberRequest) {
		input.Password = "adv-alice-reset-2"
	})
	advRequireStatus(t, response, http.StatusOK, "")
	if alice.alive() {
		t.Fatal("the member's session survived a password set through the user update")
	}
}

func TestAdversarialDisablingAMemberEndsItsSessionsForGood(t *testing.T) {
	harness := newAdvHarness(t)
	admin := harness.admin()
	alice := harness.aliceClient()
	mobile := harness.mobileToken(harness.alice.ID)

	response := harness.updateMember(admin, harness.alice, func(input *updateMemberRequest) {
		input.Enabled = false
	})
	advRequireStatus(t, response, http.StatusOK, "")
	if alice.alive() {
		t.Fatal("a disabled member's session still authenticates")
	}
	if _, response := harness.tryLogin("adv-alice", advAlicePassword, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("a disabled member can log in: %d", response.Code)
	}

	// Re-enabling the account must not resurrect what was revoked: the old
	// cookie and the old phone credential stay dead.
	response = harness.updateMember(admin, harness.alice, func(input *updateMemberRequest) {
		input.Enabled = true
	})
	advRequireStatus(t, response, http.StatusOK, "")
	if alice.alive() {
		t.Fatal("re-enabling the member revived a revoked session")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/mobile/session", nil)
	request.Header.Set("Authorization", "Bearer "+mobile)
	recorder := httptest.NewRecorder()
	harness.api.ServeHTTP(recorder, request)
	advRequireStatus(t, recorder, http.StatusUnauthorized, "authentication_required")
}

func TestAdversarialRevokingOneSessionEndsExactlyThatOne(t *testing.T) {
	harness := newAdvHarness(t)
	current := harness.aliceClient()
	victim := harness.aliceClient()
	bystander := harness.aliceClient()

	// Identify the victim's id from the victim's own point of view, then
	// revoke it from another browser of the same user.
	victimID, _ := victim.accountSessionIDs()
	currentID, _ := current.accountSessionIDs()

	response := current.send(http.MethodDelete, "/api/v1/account/sessions/"+victimID, "")
	advRequireStatus(t, response, http.StatusNoContent, "")
	if victim.alive() {
		t.Fatal("an explicitly revoked session still authenticates")
	}
	if !current.alive() || !bystander.alive() {
		t.Fatal("revoking one session ended another")
	}
	// Revoking it again is a clean 404, not an error and not a second effect.
	response = current.send(http.MethodDelete, "/api/v1/account/sessions/"+victimID, "")
	advRequireStatus(t, response, http.StatusNotFound, "session_not_found")
	// The current session cannot be revoked through this route.
	response = current.send(http.MethodDelete, "/api/v1/account/sessions/"+currentID, "")
	advRequireStatus(t, response, http.StatusConflict, "current_session")
	if !current.alive() {
		t.Fatal("the current session was revoked through the per-session route")
	}
}

func TestAdversarialRevokingOthersKeepsOnlyTheCaller(t *testing.T) {
	harness := newAdvHarness(t)
	current := harness.aliceClient()
	second := harness.aliceClient()
	third := harness.aliceClient()
	bob := harness.bobClient()
	mobile := harness.mobileToken(harness.alice.ID)

	response := current.send(http.MethodDelete, "/api/v1/account/sessions/others", "")
	advRequireStatus(t, response, http.StatusNoContent, "")

	if second.alive() || third.alive() {
		t.Fatal(`a session survived "log out other devices"`)
	}
	if !current.alive() {
		t.Fatal(`"log out other devices" ended the caller's own session`)
	}
	if !bob.alive() {
		t.Fatal(`"log out other devices" reached into another user's sessions`)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/mobile/session", nil)
	request.Header.Set("Authorization", "Bearer "+mobile)
	recorder := httptest.NewRecorder()
	harness.api.ServeHTTP(recorder, request)
	advRequireStatus(t, recorder, http.StatusUnauthorized, "authentication_required")
}

// The API has no route that changes a role (there is exactly one
// administrator, enforced by a unique index) and none that deletes a user.
// Both can still happen to the rows -- a restore, a migration, an operator
// with sqlite3 -- so the property is tested against the rows directly: the
// principal is re-read from the store on every request, never cached in the
// session.
func TestAdversarialRoleDowngradeTakesEffectOnTheNextRequest(t *testing.T) {
	harness := newAdvHarness(t)
	admin := harness.admin()
	advRequireStatus(t, admin.get("/api/v1/users"), http.StatusOK, "")

	harness.exec(`UPDATE modemdeck_users SET role = 'member' WHERE id = ?`, harness.adminID)

	// Same cookie, no new login.
	advRequireStatus(t, admin.get("/api/v1/users"), http.StatusForbidden, "admin_required")
	advRequireStatus(t, admin.get("/api/v1/diagnostics/logs"), http.StatusForbidden, "admin_required")
	advRequireStatus(t, admin.get("/api/v1/nodes"), http.StatusForbidden, "admin_required")
	response := admin.send(http.MethodPut, "/api/v1/users/"+harness.alice.ID+"/password",
		`{"password":"adv-should-not-apply"}`)
	advRequireStatus(t, response, http.StatusForbidden, "admin_required")
	if _, response := harness.tryLogin("adv-alice", "adv-should-not-apply", nil); response.Code == http.StatusOK {
		t.Fatal("a downgraded administrator's session still reset a password")
	}
}

func TestAdversarialDeletedUserSessionIsDead(t *testing.T) {
	harness := newAdvHarness(t)
	alice := harness.aliceClient()
	mobile := harness.mobileToken(harness.alice.ID)

	harness.exec(`DELETE FROM modemdeck_users WHERE id = ?`, harness.alice.ID)

	// Two independent reasons the cookie is dead, both asserted: the schema
	// cascades the deletion to the session rows, and authentication joins the
	// session to an enabled user, so even an orphaned row would not pass.
	var sessions int
	if err := harness.db.QueryRow(`SELECT COUNT(*) FROM modemdeck_auth_sessions WHERE user_id = ?`,
		harness.alice.ID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Fatalf("%d session rows outlived their user", sessions)
	}
	response := alice.get("/api/v1/bootstrap")
	advRequireStatus(t, response, http.StatusUnauthorized, "authentication_required")
	response = alice.send(http.MethodPost, "/api/v1/contacts", `{"display_name":"adv ghost"}`)
	advRequireStatus(t, response, http.StatusUnauthorized, "authentication_required")

	request := httptest.NewRequest(http.MethodGet, "/api/v1/mobile/session", nil)
	request.Header.Set("Authorization", "Bearer "+mobile)
	recorder := httptest.NewRecorder()
	harness.api.ServeHTTP(recorder, request)
	advRequireStatus(t, recorder, http.StatusUnauthorized, "authentication_required")
}

func TestAdversarialLineAccessRemovalTakesEffectOnTheNextRequest(t *testing.T) {
	harness := newAdvHarness(t)
	harness.seedHistory()
	admin := harness.admin()
	alice := harness.aliceClient()
	path := "/api/v1/messages?line_id=" + advLineAlpha + "&peer=" + strings.ReplaceAll(advPeerAlpha, "+", "%2B")
	advRequireStatus(t, alice.get(path), http.StatusOK, "")

	response := harness.updateMember(admin, harness.alice, func(input *updateMemberRequest) {
		input.LineIDs = []string{}
	})
	advRequireStatus(t, response, http.StatusOK, "")

	advRequireStatus(t, alice.get(path), http.StatusNotFound, "line_not_found")
}

func TestAdversarialTamperedAndMalformedSessionCookies(t *testing.T) {
	harness := newAdvHarness(t)
	client := harness.aliceClient()

	flipped := []byte(client.session)
	if flipped[5] == 'A' {
		flipped[5] = 'B'
	} else {
		flipped[5] = 'A'
	}
	cases := map[string]string{
		"one character changed":     string(flipped),
		"truncated":                 client.session[:len(client.session)-1],
		"extended":                  client.session + "A",
		"padded base64":             client.session + "=",
		"not base64":                strings.Repeat("!", len(client.session)),
		"sql metacharacters":        `' OR '1'='1`,
		"the csrf token as session": client.csrf,
		"never issued":              base64.RawURLEncoding.EncodeToString(make([]byte, auth.TokenBytes)),
		"very long":                 strings.Repeat("A", 8192),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			forged := &advClient{harness: harness, session: value, csrf: client.csrf}
			response := forged.get("/api/v1/bootstrap")
			advRequireStatus(t, response, http.StatusUnauthorized, "authentication_required")
			response = forged.send(http.MethodPut, "/api/v1/account/contact", `{"contact_id":""}`)
			advRequireStatus(t, response, http.StatusUnauthorized, "authentication_required")
		})
	}
	if !client.alive() {
		t.Fatal("presenting forged cookies revoked the genuine session")
	}
}

// FINDING A-07 / review P1-4 (fixed): a session ends at most
// auth.DefaultSessionMaxAge after sign-in and auth.DefaultSessionIdleTimeout
// after its last use. An expired cookie is refused like a revoked one: the
// row is deleted and the cookies are cleared, so it cannot come back.
func TestAdversarialSessionExpiresOnTheServer(t *testing.T) {
	harness := newAdvHarness(t)
	client := harness.aliceClient()
	if !client.alive() {
		t.Fatal("a fresh session was refused")
	}

	tenYearsAgo := time.Now().Add(-10 * 365 * 24 * time.Hour).Unix()
	harness.exec(`UPDATE modemdeck_auth_sessions SET created_at_unix = ?, last_seen_at_unix = ?`,
		tenYearsAgo, tenYearsAgo)
	if client.alive() {
		t.Fatal("a ten-year-old session was accepted")
	}
	var sessions int
	if err := harness.db.QueryRow(`SELECT COUNT(*) FROM modemdeck_auth_sessions WHERE user_id = ?`,
		harness.alice.ID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Fatalf("%d expired session rows were kept after the cookie was refused", sessions)
	}
}

func TestAdversarialIdleSessionExpiresBeforeItsMaximumAge(t *testing.T) {
	harness := newAdvHarness(t)
	client := harness.aliceClient()

	signedIn := time.Now().Add(-40 * 24 * time.Hour).Unix()
	lastUsed := time.Now().Add(-31 * 24 * time.Hour).Unix()
	harness.exec(`UPDATE modemdeck_auth_sessions SET created_at_unix = ?, last_seen_at_unix = ?`,
		signedIn, lastUsed)
	if client.alive() {
		t.Fatal("a session unused for 31 days was accepted")
	}

	other := harness.aliceClient()
	recent := time.Now().Add(-29 * 24 * time.Hour).Unix()
	harness.exec(`UPDATE modemdeck_auth_sessions SET created_at_unix = ?, last_seen_at_unix = ?`,
		signedIn, recent)
	if !other.alive() {
		t.Fatal("a session used 29 days ago, signed in 40 days ago, was refused")
	}
}

// The only bound on accumulated sessions: the ninth login of one user evicts
// that user's oldest session, and never touches another user's.
func TestAdversarialNinthLoginEvictsOnlyTheSameUsersOldestSession(t *testing.T) {
	harness := newAdvHarness(t)
	bob := harness.bobClient()
	clients := make([]*advClient, 0, 9)
	for index := 0; index < 9; index++ {
		client := harness.aliceClient()
		// Sessions are ordered by created_at (seconds) then rowid; give each
		// a distinct second so "oldest" is unambiguous.
		harness.exec(`UPDATE modemdeck_auth_sessions SET created_at_unix = created_at_unix - 100 + ?
			WHERE user_id = ? AND rowid = (SELECT MAX(rowid) FROM modemdeck_auth_sessions WHERE user_id = ?)`,
			index, harness.alice.ID, harness.alice.ID)
		clients = append(clients, client)
	}
	// One more login to make the store apply the cap after the timestamps
	// above were spread out.
	clients = append(clients, harness.aliceClient())

	alive := 0
	for _, client := range clients {
		if client.alive() {
			alive++
		}
	}
	if alive != 8 {
		t.Fatalf("%d of alice's sessions are alive; the cap is 8", alive)
	}
	if clients[0].alive() {
		t.Fatal("the oldest session survived the cap")
	}
	if !clients[len(clients)-1].alive() {
		t.Fatal("the newest session was evicted")
	}
	if !bob.alive() {
		t.Fatal("one user's session cap evicted another user's session")
	}
}

func TestAdversarialConcurrentRevocationAndUse(t *testing.T) {
	harness := newAdvHarness(t)
	current := harness.aliceClient()
	victim := harness.aliceClient()
	victimID, _ := victim.accountSessionIDs()

	// Hammer the victim session while it is revoked. Every answer must be a
	// clean 200 or 401 -- never a 5xx -- and once a 401 has been seen no later
	// request may succeed.
	var (
		wait     sync.WaitGroup
		mu       sync.Mutex
		failures []string
	)
	start := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			dead := false
			for attempt := 0; attempt < 40; attempt++ {
				response := victim.get("/api/v1/account/sessions")
				switch response.Code {
				case http.StatusOK:
					if dead {
						mu.Lock()
						failures = append(failures, "a revoked session authenticated again")
						mu.Unlock()
					}
				case http.StatusUnauthorized:
					dead = true
				default:
					mu.Lock()
					failures = append(failures, fmt.Sprintf("status %d: %s", response.Code, response.Body))
					mu.Unlock()
				}
			}
		}()
	}
	close(start)
	response := current.send(http.MethodDelete, "/api/v1/account/sessions/"+victimID, "")
	wait.Wait()
	advRequireStatus(t, response, http.StatusNoContent, "")
	if len(failures) > 0 {
		t.Fatalf("revocation raced with use: %v", failures)
	}
	if victim.alive() {
		t.Fatal("the session survived its revocation")
	}
}

func TestAdversarialConcurrentPasswordChangeHasOneWinner(t *testing.T) {
	harness := newAdvHarness(t)
	const attempts = 6
	clients := make([]*advClient, attempts)
	for index := range clients {
		clients[index] = harness.aliceClient()
	}

	var wait sync.WaitGroup
	codes := make([]int, attempts)
	start := make(chan struct{})
	for index, client := range clients {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			response := client.send(http.MethodPut, "/api/v1/account/password", fmt.Sprintf(
				`{"current_password":%q,"new_password":"adv-alice-race-%d"}`, advAlicePassword, index))
			codes[index] = response.Code
		}()
	}
	close(start)
	wait.Wait()

	winner := -1
	winners := 0
	for index, code := range codes {
		switch code {
		case http.StatusNoContent:
			winners++
			winner = index
		case http.StatusTooManyRequests, // the two-slot password gate was full
			http.StatusBadRequest,   // lost the compare-and-swap on the hash
			http.StatusUnauthorized: // its session was revoked by the winner
		default:
			t.Fatalf("attempt %d: unexpected status %d", index, code)
		}
	}
	if winners != 1 {
		t.Fatalf("%d concurrent password changes succeeded (%v); exactly one may", winners, codes)
	}
	// Exactly the winner's password is now the account's. (One loser is
	// probed, not all of them: five wrong passwords would trip the login
	// limiter and turn this into a test of that.)
	if _, response := harness.tryLogin("adv-alice", fmt.Sprintf("adv-alice-race-%d", (winner+1)%attempts), nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("a losing password change took effect: %d", response.Code)
	}
	if _, response := harness.tryLogin("adv-alice", advAlicePassword, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("the replaced password still logs in: %d", response.Code)
	}
	if _, response := harness.tryLogin("adv-alice", fmt.Sprintf("adv-alice-race-%d", winner), nil); response.Code != http.StatusOK {
		t.Fatalf("the winning password does not log in: %d %s", response.Code, response.Body)
	}
	for index, client := range clients {
		if client.alive() {
			t.Fatalf("session %d survived the password change", index)
		}
	}
}

func TestAdversarialConcurrentIdenticalLoginsAreIndependentSessions(t *testing.T) {
	harness := newAdvHarness(t)
	// The login gate admits two verifications at a time and answers the rest
	// with 429 login_busy; submit in pairs so every attempt is admitted.
	var tokens sync.Map
	for round := 0; round < 3; round++ {
		var wait sync.WaitGroup
		for worker := 0; worker < 2; worker++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				client, response := harness.tryLogin("adv-alice", advAlicePassword, nil)
				switch response.Code {
				case http.StatusOK:
					if _, duplicate := tokens.LoadOrStore(client.session, client.csrf); duplicate {
						t.Error("two logins were handed the same session token")
					}
				case http.StatusTooManyRequests:
				default:
					t.Errorf("login: unexpected status %d %s", response.Code, response.Body)
				}
			}()
		}
		wait.Wait()
	}
	count := 0
	tokens.Range(func(session, csrf any) bool {
		count++
		client := &advClient{harness: harness, session: session.(string), csrf: csrf.(string)}
		if !client.alive() {
			t.Error("a concurrently created session does not authenticate")
		}
		return true
	})
	if count == 0 {
		t.Fatal("no concurrent login succeeded")
	}
	var rows int
	if err := harness.db.QueryRow(`SELECT COUNT(*) FROM modemdeck_auth_sessions WHERE user_id = ?`,
		harness.alice.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != count {
		t.Fatalf("%d successful logins left %d session rows", count, rows)
	}
}

// A clone of somebody else's data directory carries their session rows and
// their paired-phone credentials as well as their password. The login refusal
// (TestACloneRefusesTheInheritedPassword) is worth nothing if whoever prepared
// the image can simply keep the cookie they already have.
func TestAdversarialCloneRefusesInheritedSessionsAndBearerTokens(t *testing.T) {
	harness := newAdvHarness(t)
	inherited := harness.admin()
	mobile := harness.mobileToken(harness.adminID)
	advRequireStatus(t, inherited.get("/api/v1/users"), http.StatusOK, "")

	markCloned(harness.api)

	for _, path := range []string{
		"/api/v1/users", "/api/v1/bootstrap", "/api/v1/contacts",
		"/api/v1/diagnostics/logs", "/api/v1/messages/events",
	} {
		response := inherited.get(path)
		advRequireStatus(t, response, http.StatusConflict, "provision_required")
	}
	response := inherited.send(http.MethodPut, "/api/v1/users/"+harness.alice.ID+"/password",
		`{"password":"adv-clone-takeover"}`)
	advRequireStatus(t, response, http.StatusConflict, "provision_required")

	request := httptest.NewRequest(http.MethodGet, "/api/v1/mobile/session", nil)
	request.Header.Set("Authorization", "Bearer "+mobile)
	recorder := httptest.NewRecorder()
	harness.api.ServeHTTP(recorder, request)
	advRequireStatus(t, recorder, http.StatusConflict, "provision_required")

	// What a cloned gateway needs in order to be repaired stays reachable.
	advRequireStatus(t, harness.anonymous().get("/api/v1/provision"), http.StatusOK, "")
	advRequireStatus(t, harness.anonymous().get("/api/v1/session"), http.StatusOK, "")
	advRequireStatus(t, harness.anonymous().get("/api/v1/health/live"), http.StatusOK, "")
}

// FINDING A-10 (low) / review P2-10 (fixed): the "access IP" recorded for a
// session honours X-ModemDeck-Client-IP / X-Real-IP only when the request's
// direct peer is loopback or private -- the Compose network the bundled nginx
// connects from, and which overwrites both headers. A client reaching the API
// straight from a public address is recorded under that address. The value is
// display-only either way, which the last part of this test holds on to.
func TestAdversarialSessionAccessIPIsClientSuppliedAndNeverAuthoritative(t *testing.T) {
	harness := newAdvHarness(t)
	client, response := harness.tryLogin("adv-alice", advAlicePassword, map[string]string{
		"X-ModemDeck-Client-IP": "203.0.113.50",
		"X-Forwarded-For":       "198.51.100.77",
	})
	advRequireStatus(t, response, http.StatusOK, "")

	listing := client.do(advRequest{
		method: http.MethodGet, path: "/api/v1/account/sessions",
		headers: map[string]string{"X-ModemDeck-Client-IP": "203.0.113.50"},
	})
	if strings.Contains(listing.Body.String(), "203.0.113.50") {
		t.Fatalf("a header forged by a public peer was recorded: %s", listing.Body)
	}
	// X-Forwarded-For is not consulted at all.
	if strings.Contains(listing.Body.String(), "198.51.100.77") {
		t.Fatalf("X-Forwarded-For reached the session record: %s", listing.Body)
	}

	// Through the proxy on the Compose network the relayed address is used.
	relayed := client.do(advRequest{
		method: http.MethodGet, path: "/api/v1/account/sessions", remoteAddr: "172.18.0.4:40000",
		headers: map[string]string{"X-ModemDeck-Client-IP": "203.0.113.60"},
	})
	if !strings.Contains(relayed.Body.String(), `"access_ip":"203.0.113.60"`) {
		t.Fatalf("the address relayed by the proxy was not recorded: %s", relayed.Body)
	}

	// A session used from a different address, with different claims about
	// itself, is the same session: nothing is bound to the address.
	moved := client.do(advRequest{
		method: http.MethodGet, path: "/api/v1/bootstrap", remoteAddr: "192.0.2.200:40000",
		headers: map[string]string{"X-ModemDeck-Client-IP": "192.0.2.201", "User-Agent": "adv-moved"},
	})
	advRequireStatus(t, moved, http.StatusOK, "")
}
