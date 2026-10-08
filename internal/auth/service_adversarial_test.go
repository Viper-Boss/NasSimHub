package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

// The multi-user half of Service (loginUser, changeUserPassword, the web
// session management) is what production runs -- store.Store implements
// MultiUserRepository -- and service_test.go's memoryRepository does not, so
// the tests there exercise only the legacy single-administrator branch. This
// repository is the smallest thing that puts the multi-user branch under test
// in this package; the same paths are driven against the real store and the
// real HTTP handler in internal/httpapi/*_adversarial_test.go.
type advUser struct {
	credentials UserCredentials
	lines       []string
}

type advRepository struct {
	*memoryRepository
	users    map[string]*advUser
	sessions map[SessionTokenDigest]UserSessionRecord
	// refuseSessionCreate simulates the password changing between the
	// credential read and the conditional session insert.
	refuseSessionCreate bool
	// mismatchedPrincipal makes the store answer a principal for a different
	// user than the session row names.
	mismatchedPrincipal bool
	lookups             int
}

func newAdvRepository() *advRepository {
	return &advRepository{
		memoryRepository: newMemoryRepository(),
		users:            map[string]*advUser{},
		sessions:         map[SessionTokenDigest]UserSessionRecord{},
	}
}

// advCheapHash is a valid PHC string at the lowest cost the verifier accepts;
// password_test.go covers the production parameters.
func advCheapHash(password string) string {
	salt := bytes.Repeat([]byte{0x42}, 16)
	key := argon2.IDKey([]byte(password), salt, 1, 8, 1, 32)
	return fmt.Sprintf("$argon2id$v=%d$m=8,t=1,p=1$%s$%s", argon2.Version,
		rawBase64.EncodeToString(salt), rawBase64.EncodeToString(key))
}

func (r *advRepository) addUser(id, username, password string, role Role, enabled bool) {
	r.users[id] = &advUser{credentials: UserCredentials{
		ID: id, Username: username, PasswordHash: advCheapHash(password), Role: role, Enabled: enabled,
	}}
}

func (r *advRepository) UserCredentialsByUsername(_ context.Context, username string) (UserCredentials, bool, error) {
	for _, user := range r.users {
		if strings.EqualFold(user.credentials.Username, username) {
			return user.credentials, true, nil
		}
	}
	return UserCredentials{}, false, nil
}

func (r *advRepository) UserCredentialsByID(_ context.Context, userID string) (UserCredentials, bool, error) {
	user, found := r.users[userID]
	if !found {
		return UserCredentials{}, false, nil
	}
	return user.credentials, true, nil
}

func (r *advRepository) CreateUserSessionIfPasswordHash(
	_ context.Context,
	expectedPasswordHash string,
	session UserSessionRecord,
) (bool, error) {
	user, found := r.users[session.UserID]
	if r.refuseSessionCreate || !found || !user.credentials.Enabled ||
		user.credentials.PasswordHash != expectedPasswordHash {
		return false, nil
	}
	r.sessions[session.SessionTokenDigest] = session
	return true, nil
}

func (r *advRepository) UserSessionByTokenDigest(
	_ context.Context,
	digest SessionTokenDigest,
) (UserSessionRecord, Principal, bool, error) {
	r.lookups++
	session, found := r.sessions[digest]
	if !found {
		return UserSessionRecord{}, Principal{}, false, nil
	}
	user, found := r.users[session.UserID]
	if !found || !user.credentials.Enabled {
		return UserSessionRecord{}, Principal{}, false, nil
	}
	principal := Principal{
		UserID: user.credentials.ID, Username: user.credentials.Username,
		Role: user.credentials.Role, AllowedLineIDs: append([]string(nil), user.lines...),
	}
	if r.mismatchedPrincipal {
		principal.UserID = "somebody-else"
	}
	return session, principal, true, nil
}

func (r *advRepository) ReplaceUserPasswordHashIfCurrentAndRevokeSessions(
	_ context.Context,
	userID, expectedPasswordHash, replacementPasswordHash string,
) (bool, error) {
	user, found := r.users[userID]
	if !found || !user.credentials.Enabled || user.credentials.PasswordHash != expectedPasswordHash {
		return false, nil
	}
	user.credentials.PasswordHash = replacementPasswordHash
	for digest, session := range r.sessions {
		if session.UserID == userID {
			delete(r.sessions, digest)
		}
	}
	return true, nil
}

func (r *advRepository) UserSessions(_ context.Context, userID string) ([]UserSessionRecord, error) {
	var result []UserSessionRecord
	for _, session := range r.sessions {
		if session.UserID == userID {
			result = append(result, session)
		}
	}
	return result, nil
}

func (r *advRepository) DeleteUserSession(_ context.Context, userID string, digest SessionTokenDigest) (bool, error) {
	session, found := r.sessions[digest]
	if !found || session.UserID != userID {
		return false, nil
	}
	delete(r.sessions, digest)
	return true, nil
}

func (r *advRepository) DeleteOtherUserSessions(
	_ context.Context,
	userID string,
	current SessionTokenDigest,
) ([]SessionTokenDigest, error) {
	var removed []SessionTokenDigest
	for digest, session := range r.sessions {
		if session.UserID == userID && digest != current {
			delete(r.sessions, digest)
			removed = append(removed, digest)
		}
	}
	return removed, nil
}

func (r *advRepository) DeleteSessionByTokenDigest(_ context.Context, digest SessionTokenDigest) error {
	delete(r.sessions, digest)
	return nil
}

func advService(t *testing.T) (*Service, *advRepository) {
	t.Helper()
	repository := newAdvRepository()
	repository.addUser("user_admin", "adv-admin", "adv-admin-password", RoleAdmin, true)
	repository.addUser("user_alice", "adv-alice", "adv-alice-password", RoleMember, true)
	repository.addUser("user_bob", "adv-bob", "adv-bob-password", RoleMember, true)
	repository.addUser("user_off", "adv-disabled", "adv-disabled-password", RoleMember, false)
	return mustService(t, repository), repository
}

func advLogin(t *testing.T, service *Service, username, password string) LoginResult {
	t.Helper()
	result, err := service.Login(context.Background(), username, password)
	if err != nil {
		t.Fatalf("Login(%s) error = %v", username, err)
	}
	return result
}

// Every way of not being let in looks the same from outside: one error code,
// no token. A caller cannot tell a wrong password from an unknown name from a
// disabled account from a password that changed mid-login.
func TestAdversarialLoginRefusalsAreIndistinguishable(t *testing.T) {
	service, repository := advService(t)
	cases := map[string][2]string{
		"wrong password":             {"adv-alice", "adv-wrong-password"},
		"unknown account":            {"adv-nobody", "adv-alice-password"},
		"disabled, right password":   {"adv-disabled", "adv-disabled-password"},
		"disabled, wrong password":   {"adv-disabled", "adv-wrong-password"},
		"another account's password": {"adv-alice", "adv-bob-password"},
		"empty password":             {"adv-alice", ""},
		"NUL in the name":            {"adv-alice\x00", "adv-alice-password"},
		"newline in the name":        {"adv-alice\nadv-admin", "adv-alice-password"},
		"over-long name":             {strings.Repeat("a", MaximumUsernameRunes+1), "adv-alice-password"},
		"over-long password":         {"adv-alice", strings.Repeat("p", MaximumPasswordBytes+1)},
	}
	for name, credentials := range cases {
		result, err := service.Login(context.Background(), credentials[0], credentials[1])
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%s: error = %v, want ErrInvalidCredentials", name, err)
		}
		if result.SessionToken != "" || result.CSRFToken != "" || result.Principal != nil {
			t.Errorf("%s: a refused login returned %+v", name, result)
		}
	}
	if len(repository.sessions) != 0 {
		t.Fatalf("%d sessions were created by refused logins", len(repository.sessions))
	}

	// The password is verified, the row is inserted only if the hash is still
	// the one that was verified. If it is not, there is no session.
	repository.refuseSessionCreate = true
	if _, err := service.Login(context.Background(), "adv-alice", "adv-alice-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login racing a password change: error = %v", err)
	}
}

func TestAdversarialMalformedSessionTokensNeverReachTheStore(t *testing.T) {
	service, repository := advService(t)
	valid := advLogin(t, service, "adv-alice", "adv-alice-password")
	token := string(valid.SessionToken)

	// Same 32 bytes, non-canonical spelling: the last base64url character of a
	// 43-character token has four payload bits and two that must be zero.
	nonCanonical := token[:len(token)-1] + string(advNonCanonicalTail(token[len(token)-1]))
	forged := map[string]string{
		"empty":             "",
		"truncated":         token[:len(token)-1],
		"extended":          token + "A",
		"padded":            token + "=",
		"whitespace":        " " + token,
		"standard alphabet": strings.Repeat("+", len(token)),
		"non-canonical":     nonCanonical,
		"percent-encoded":   strings.ReplaceAll(token, "-", "%2D") + "%00",
		"huge":              strings.Repeat("A", 1<<16),
	}
	before := repository.lookups
	for name, value := range forged {
		if value == token {
			continue
		}
		if _, err := service.Authenticate(context.Background(), SessionToken(value)); !errors.Is(err, ErrInvalidSessionToken) {
			t.Errorf("%s: Authenticate error = %v, want ErrInvalidSessionToken", name, err)
		}
		if err := service.Logout(context.Background(), SessionToken(value)); !errors.Is(err, ErrInvalidSessionToken) {
			t.Errorf("%s: Logout error = %v, want ErrInvalidSessionToken", name, err)
		}
	}
	if repository.lookups != before {
		t.Fatalf("a malformed token caused %d store lookups", repository.lookups-before)
	}
	if _, err := service.Authenticate(context.Background(), valid.SessionToken); err != nil {
		t.Fatalf("the genuine session stopped working: %v", err)
	}
}

// advNonCanonicalTail returns a different final character that decodes, in a
// lenient decoder, to the same bytes as the given one.
func advNonCanonicalTail(last byte) byte {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	index := strings.IndexByte(alphabet, last)
	return alphabet[index|0x03]
}

func TestAdversarialCSRFTokenBelongsToExactlyOneSession(t *testing.T) {
	service, _ := advService(t)
	first := advLogin(t, service, "adv-alice", "adv-alice-password")
	second := advLogin(t, service, "adv-alice", "adv-alice-password")
	bob := advLogin(t, service, "adv-bob", "adv-bob-password")
	if first.CSRFToken == second.CSRFToken || first.SessionToken == second.SessionToken {
		t.Fatal("two logins share a token")
	}
	if string(first.CSRFToken) == string(first.SessionToken) {
		t.Fatal("the CSRF token equals the session token")
	}

	authentication, err := service.Authenticate(context.Background(), first.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := authentication.VerifyCSRF(first.CSRFToken); err != nil {
		t.Fatalf("the session's own CSRF token was refused: %v", err)
	}
	for name, token := range map[string]CSRFToken{
		"same user's other session": second.CSRFToken,
		"another user's session":    bob.CSRFToken,
		"the session token":         CSRFToken(first.SessionToken),
		"empty":                     "",
		"malformed":                 "not-a-token",
	} {
		if err := authentication.VerifyCSRF(token); !errors.Is(err, ErrInvalidCSRFToken) {
			t.Errorf("%s: VerifyCSRF error = %v, want ErrInvalidCSRFToken", name, err)
		}
	}
	// The zero value is no authentication at all: it vouches for nobody and
	// accepts no token, including the empty one.
	var none Authentication
	if _, ok := none.Principal(); ok {
		t.Fatal("the zero Authentication carries a principal")
	}
	if err := none.VerifyCSRF(first.CSRFToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("zero Authentication VerifyCSRF error = %v", err)
	}
}

func TestAdversarialPrincipalIsReadFreshOnEveryAuthentication(t *testing.T) {
	service, repository := advService(t)
	login := advLogin(t, service, "adv-admin", "adv-admin-password")

	principal := func() (Principal, error) {
		authentication, err := service.Authenticate(context.Background(), login.SessionToken)
		if err != nil {
			return Principal{}, err
		}
		result, _ := authentication.Principal()
		return result, nil
	}
	if current, err := principal(); err != nil || !current.IsAdmin() {
		t.Fatalf("principal = %+v, %v", current, err)
	}
	// Downgraded in the store: the very next authentication says member.
	repository.users["user_admin"].credentials.Role = RoleMember
	if current, err := principal(); err != nil || current.IsAdmin() {
		t.Fatalf("after a downgrade the session still authenticates as %q (%v)", current.Role, err)
	}
	// The principal handed out is a copy: a handler cannot widen its own
	// line scope by appending to it.
	repository.users["user_admin"].lines = []string{"line-a"}
	current, _ := principal()
	current.AllowedLineIDs[0] = "line-b"
	if again, _ := principal(); again.AllowedLineIDs[0] != "line-a" {
		t.Fatal("mutating a returned principal changed the next one")
	}
	// Disabled: unauthenticated, not an error state.
	repository.users["user_admin"].credentials.Enabled = false
	if _, err := principal(); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("a disabled user's session: error = %v", err)
	}
	// A store that answers a principal for a different user than the session
	// row names is refused outright rather than believed.
	repository.users["user_admin"].credentials.Enabled = true
	repository.mismatchedPrincipal = true
	if _, err := principal(); !errors.Is(err, ErrInvalidSessionRecord) {
		t.Fatalf("mismatched principal: error = %v", err)
	}
}

func TestAdversarialSessionManagementStaysInsideTheCallersAccount(t *testing.T) {
	service, repository := advService(t)
	alice := advLogin(t, service, "adv-alice", "adv-alice-password")
	aliceOther := advLogin(t, service, "adv-alice", "adv-alice-password")
	bob := advLogin(t, service, "adv-bob", "adv-bob-password")
	ctx := context.Background()

	list := func(token SessionToken) map[string]bool {
		sessions, err := service.WebSessions(ctx, token)
		if err != nil {
			t.Fatalf("WebSessions() error = %v", err)
		}
		ids := map[string]bool{}
		for _, session := range sessions {
			ids[session.ID] = session.Current
		}
		return ids
	}
	aliceIDs, bobIDs := list(alice.SessionToken), list(bob.SessionToken)
	if len(aliceIDs) != 2 || len(bobIDs) != 1 {
		t.Fatalf("alice sees %d sessions and bob %d", len(aliceIDs), len(bobIDs))
	}
	var bobID string
	for id := range bobIDs {
		bobID = id
		if _, shared := aliceIDs[id]; shared {
			t.Fatal("alice's session list contains bob's session")
		}
	}
	// The public id is derived from the token digest but is not the digest,
	// and cannot be used as a token.
	if _, err := service.Authenticate(ctx, SessionToken(bobID)); err == nil {
		t.Fatal("a session id authenticates as a session token")
	}

	if _, err := service.RevokeWebSession(ctx, alice.SessionToken, bobID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("revoking another user's session: error = %v, want ErrSessionNotFound", err)
	}
	if _, err := service.RevokeWebSession(ctx, alice.SessionToken, "  "); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("revoking a blank id: error = %v", err)
	}
	for id, current := range aliceIDs {
		if current {
			if _, err := service.RevokeWebSession(ctx, alice.SessionToken, id); !errors.Is(err, ErrCurrentSession) {
				t.Fatalf("revoking the current session: error = %v", err)
			}
		}
	}
	if _, err := service.RevokeOtherWebSessions(ctx, alice.SessionToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, aliceOther.SessionToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("alice's other session survived: %v", err)
	}
	for _, token := range []SessionToken{alice.SessionToken, bob.SessionToken} {
		if _, err := service.Authenticate(ctx, token); err != nil {
			t.Fatalf("a session that should have survived did not: %v", err)
		}
	}
	// A revoked session cannot manage sessions.
	if _, err := service.WebSessions(ctx, aliceOther.SessionToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("a revoked session listed sessions: %v", err)
	}
	if _, err := service.RevokeOtherWebSessions(ctx, aliceOther.SessionToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("a revoked session revoked sessions: %v", err)
	}
	if len(repository.sessions) != 2 {
		t.Fatalf("%d sessions remain, want alice's current one and bob's", len(repository.sessions))
	}
}

func TestAdversarialPasswordChangeIsScopedToTheAuthenticatedUser(t *testing.T) {
	service, repository := advService(t)
	alice := advLogin(t, service, "adv-alice", "adv-alice-password")
	bob := advLogin(t, service, "adv-bob", "adv-bob-password")
	authentication, err := service.Authenticate(context.Background(), alice.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	principal, _ := authentication.Principal()
	ctx := ContextWithPrincipal(context.Background(), principal)

	// Alice cannot change her password by knowing somebody else's, the
	// administrator's included.
	for _, current := range []string{"adv-bob-password", "adv-admin-password", "adv-wrong-password"} {
		if err := service.ChangePassword(ctx, current, "adv-alice-password-2"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("ChangePassword(current=%q) error = %v", current, err)
		}
	}
	bobHash := repository.users["user_bob"].credentials.PasswordHash
	adminHash := repository.users["user_admin"].credentials.PasswordHash
	if err := service.ChangePassword(ctx, "adv-alice-password", "adv-alice-password-2"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if repository.users["user_bob"].credentials.PasswordHash != bobHash ||
		repository.users["user_admin"].credentials.PasswordHash != adminHash {
		t.Fatal("one member's password change rewrote another account's hash")
	}
	if _, err := service.Authenticate(context.Background(), alice.SessionToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("alice's session survived her password change: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), bob.SessionToken); err != nil {
		t.Fatalf("bob's session did not survive alice's password change: %v", err)
	}
	// The principal in hand is now stale; it must not be able to change the
	// password again with the old one.
	if err := service.ChangePassword(ctx, "adv-alice-password", "adv-alice-password-3"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("a second change with the old password: error = %v", err)
	}
}

// What a client says about itself is shown on the "signed-in devices" page
// and stored. It is bounded and stripped of control characters on the way in,
// and it is never an input to authentication.
func TestAdversarialSessionClientMetadataIsSanitisedAndNonAuthoritative(t *testing.T) {
	service, repository := advService(t)
	hostile := SessionClient{
		UserAgent:  "adv-agent\r\nSet-Cookie: x=1\x00\x1b[31m" + strings.Repeat("A", 4096),
		AccessIP:   "198.51.100.9\n" + strings.Repeat("9", 500),
		AccessHost: "nas.example.test\r\nX-Injected: 1" + strings.Repeat("h", 2000),
	}
	ctx := ContextWithSessionClient(context.Background(), hostile)
	login, err := service.Login(ctx, "adv-alice", "adv-alice-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range repository.sessions {
		for field, value := range map[string]string{
			"user agent": session.UserAgent, "access ip": session.AccessIP, "access host": session.AccessHost,
		} {
			if strings.ContainsAny(value, "\r\n\x00\x1b\x7f") {
				t.Errorf("stored %s contains a control character: %q", field, value)
			}
		}
		if len(session.UserAgent) > maximumUserAgentBytes || len(session.AccessIP) > maximumAccessIPBytes ||
			len(session.AccessHost) > maximumAccessHostBytes {
			t.Errorf("stored client metadata exceeds its bounds: %d/%d/%d",
				len(session.UserAgent), len(session.AccessIP), len(session.AccessHost))
		}
	}
	// The same token from a completely different "client" authenticates the
	// same: the metadata describes, it does not bind.
	elsewhere := ContextWithSessionClient(context.Background(), SessionClient{
		UserAgent: "adv-other-agent", AccessIP: "203.0.113.200", AccessHost: "other.example.test",
	})
	if _, err := service.Authenticate(elsewhere, login.SessionToken); err != nil {
		t.Fatalf("the session is bound to client metadata: %v", err)
	}
}

// Stated once more at this layer, next to the existing
// TestAuthenticateHasNoTimeExpiry (single-administrator branch): the
// multi-user branch has no lifetime either. Audit finding A-07.
func TestAdversarialMultiUserSessionHasNoTimeExpiry(t *testing.T) {
	service, _ := advService(t)
	login := advLogin(t, service, "adv-alice", "adv-alice-password")
	service.now = func() time.Time { return time.Now().Add(20 * 365 * 24 * time.Hour) }
	if _, err := service.Authenticate(context.Background(), login.SessionToken); err != nil {
		t.Fatalf("a twenty-year-old session was refused (%v): a lifetime now exists; update audit finding A-07", err)
	}
}
