package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/argon2"

	"github.com/human-agent65535/modemdeck/internal/auth"
	"github.com/human-agent65535/modemdeck/internal/diagnostics"
	"github.com/human-agent65535/modemdeck/internal/mobilepairing"
	platformdb "github.com/human-agent65535/modemdeck/internal/platform/database"
	"github.com/human-agent65535/modemdeck/internal/provision"
	"github.com/human-agent65535/modemdeck/internal/store"
)

// Fixture for the r128 adversarial ("反向") authentication tests.
//
// Everything below drives the real http.Handler (API.ServeHTTP) on top of the
// real SQLite store and the real auth.Service. The older tests in this package
// use in-memory fakes for the authenticator, which is right for handler
// mechanics and wrong for these: revocation, role changes and cross-user
// scoping are properties of the store's rows and of the principal the store
// loads on every request, so a fake would only prove the fake.
//
// All identities here are obviously fake. The phone numbers use the reserved
// 555-01xx fictional range.

const (
	advAdminUser     = "adv-admin"
	advAdminPassword = "adv-admin-password-1"
	advAlicePassword = "adv-alice-password-1"
	advBobPassword   = "adv-bob-password-1"

	advLineAlpha = "line_adv_alpha"
	advLineBeta  = "line_adv_beta"

	advPeerAlpha = "+15555550101"
	advPeerBeta  = "+15555550102"
)

type advHarness struct {
	t         *testing.T
	api       *API
	store     *store.Store
	db        *sql.DB
	auth      *auth.Service
	logs      *diagnostics.LogBuffer
	sink      *advLogSink
	backupDir string

	adminID string
	alice   store.User
	bob     store.User
}

// advLogSink stands in for the process's stdout: it is the handler the
// diagnostics buffer forwards to, so what lands here is what `docker logs`
// would show.
type advLogSink struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (sink *advLogSink) Write(value []byte) (int, error) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.buffer.Write(value)
}

func (sink *advLogSink) String() string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.buffer.String()
}

// advCheapPasswordHash is a valid Argon2id PHC string at the lowest cost the
// verifier accepts. The production cost (64 MiB, t=3) makes every login take
// seconds under the race detector; these tests are about what happens around
// the password check, not about its cost, and auth/password_test.go covers the
// production parameters.
func advCheapPasswordHash(t *testing.T, password string) string {
	t.Helper()
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		t.Fatalf("read salt: %v", err)
	}
	key := argon2.IDKey([]byte(password), salt, 1, 8, 1, 32)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=8,t=1,p=1$%s$%s",
		argon2.Version,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

func newAdvHarness(t *testing.T, configure ...func(*Options)) *advHarness {
	t.Helper()
	root := t.TempDir()
	database, err := platformdb.Open(context.Background(), platformdb.Config{
		TargetPath: filepath.Join(root, "modemdeck.db"),
	})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repository, err := store.New(database)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	service, err := auth.NewService(repository)
	if err != nil {
		t.Fatalf("auth.NewService() error = %v", err)
	}
	harness := &advHarness{
		t:         t,
		store:     repository,
		db:        database,
		auth:      service,
		logs:      diagnostics.NewLogBuffer(diagnostics.DefaultLogCapacity),
		sink:      &advLogSink{},
		backupDir: filepath.Join(root, "backups", "contacts"),
		adminID:   auth.InitialAdminUserID,
	}

	// Lines first: the initial administrator is granted the lines that exist
	// when the account is created.
	harness.exec(`INSERT INTO modemdeck_lines (line_id, phone_number, line_label) VALUES (?, ?, ?)`,
		advLineAlpha, "+15555550111", "alpha")
	harness.exec(`INSERT INTO modemdeck_lines (line_id, phone_number, line_label) VALUES (?, ?, ?)`,
		advLineBeta, "+15555550112", "beta")

	created, err := repository.CreateAdminIfAbsent(context.Background(), auth.AdminCredentials{
		Username:     advAdminUser,
		PasswordHash: advCheapPasswordHash(t, advAdminPassword),
	})
	if err != nil || !created {
		t.Fatalf("CreateAdminIfAbsent() = %v, %v", created, err)
	}
	harness.alice, err = repository.CreateMember(context.Background(), store.CreateMemberInput{
		Username:          "adv-alice",
		PasswordHash:      advCheapPasswordHash(t, advAlicePassword),
		IOSPairingEnabled: true,
		LineIDs:           []string{advLineAlpha},
	})
	if err != nil {
		t.Fatalf("CreateMember(alice) error = %v", err)
	}
	harness.bob, err = repository.CreateMember(context.Background(), store.CreateMemberInput{
		Username:          "adv-bob",
		PasswordHash:      advCheapPasswordHash(t, advBobPassword),
		IOSPairingEnabled: true,
		LineIDs:           []string{advLineBeta},
	})
	if err != nil {
		t.Fatalf("CreateMember(bob) error = %v", err)
	}

	options := Options{
		Authenticator:             service,
		ContactBackupDirectory:    harness.backupDir,
		DiagnosticLogs:            harness.logs,
		Logger:                    slog.New(harness.logs.Handler(slog.NewJSONHandler(harness.sink, nil))).With("component", "http"),
		disableLicenseEnforcement: true,
	}
	for _, apply := range configure {
		apply(&options)
	}
	harness.api, err = New(repository, options)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return harness
}

func (harness *advHarness) exec(statement string, arguments ...any) {
	harness.t.Helper()
	if _, err := harness.db.Exec(statement, arguments...); err != nil {
		harness.t.Fatalf("seed %q: %v", statement, err)
	}
}

// seedHistory gives each line one message thread and one finished call, so a
// cross-user read has something real to leak.
func (harness *advHarness) seedHistory() {
	harness.t.Helper()
	for index, entry := range []struct{ line, peer, body, call string }{
		{advLineAlpha, advPeerAlpha, "adv alpha history", "adv-call-alpha"},
		{advLineBeta, advPeerBeta, "adv beta history", "adv-call-beta"},
	} {
		timestamp := fmt.Sprintf("2026-01-0%d 08:00:00", index+1)
		result, err := harness.db.Exec(`
			INSERT INTO sms (
				line_id, peer, sender, recipient, content, type, timestamp, created_at
			) VALUES (?, ?, ?, '', ?, 1, ?, ?)`,
			entry.line, entry.peer, entry.peer, entry.body, timestamp, timestamp)
		if err != nil {
			harness.t.Fatalf("seed message: %v", err)
		}
		messageID, _ := result.LastInsertId()
		harness.exec(`
			INSERT INTO sms_contacts (
				line_id, imsi, peer, last_sms_id, last_timestamp,
				last_content, last_type, unread_count, created_at, updated_at
			) VALUES (?, '', ?, ?, ?, ?, 1, 1, ?, ?)`,
			entry.line, entry.peer, messageID, timestamp, entry.body, timestamp, timestamp)
		harness.exec(`
			INSERT INTO call_history (
				id, line_id, direction, remote_number, phase,
				created_at, updated_at, ended_at
			) VALUES (?, ?, 'incoming', ?, 'ended', ?, ?, ?)`,
			entry.call, entry.line, entry.peer, timestamp, timestamp, timestamp)
		harness.exec(`
			INSERT INTO modemdeck_call_recordings (
				id, call_id, segment_index, status, started_at, ended_at,
				duration_ms, size_bytes, relative_path
			) VALUES (?, ?, 1, 'ready', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 1000, 128, ?)`,
			entry.call+"-rec", entry.call, entry.call+"-rec.ogg")
	}
}

// advClient is one browser: a cookie jar reduced to the two cookies that
// matter, plus the CSRF value the SPA would echo in the header.
type advClient struct {
	harness *advHarness
	session string
	csrf    string
}

type advRequest struct {
	method  string
	path    string
	body    string
	headers map[string]string
	// noCSRFHeader / noCSRFCookie / noSession remove one credential from an
	// otherwise complete request.
	noCSRFHeader bool
	noCSRFCookie bool
	noSession    bool
	csrfHeader   *string
	host         string
	remoteAddr   string
}

func (harness *advHarness) anonymous() *advClient {
	return &advClient{harness: harness}
}

// login performs a real POST /api/v1/session and keeps the cookies.
func (harness *advHarness) login(username, password string) *advClient {
	harness.t.Helper()
	client, response := harness.tryLogin(username, password, nil)
	if response.Code != http.StatusOK {
		harness.t.Fatalf("login %s: status = %d, body = %s", username, response.Code, response.Body)
	}
	return client
}

func (harness *advHarness) tryLogin(
	username, password string,
	headers map[string]string,
) (*advClient, *httptest.ResponseRecorder) {
	harness.t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/session", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	harness.api.ServeHTTP(response, request)
	client := &advClient{harness: harness}
	for _, cookie := range response.Result().Cookies() {
		switch cookie.Name {
		case sessionCookieName:
			client.session = cookie.Value
		case csrfCookieName:
			client.csrf = cookie.Value
		}
	}
	return client, response
}

func (harness *advHarness) admin() *advClient {
	return harness.login(advAdminUser, advAdminPassword)
}

func (harness *advHarness) aliceClient() *advClient {
	return harness.login("adv-alice", advAlicePassword)
}

func (harness *advHarness) bobClient() *advClient {
	return harness.login("adv-bob", advBobPassword)
}

func (client *advClient) do(input advRequest) *httptest.ResponseRecorder {
	client.harness.t.Helper()
	var request *http.Request
	if input.body != "" {
		request = httptest.NewRequest(input.method, input.path, strings.NewReader(input.body))
		request.Header.Set("Content-Type", "application/json")
	} else {
		request = httptest.NewRequest(input.method, input.path, nil)
		if input.method != http.MethodGet && input.method != http.MethodHead {
			// A body-less write still declares JSON, so a refusal can only be
			// the control under test and never the media-type check.
			request.Header.Set("Content-Type", "application/json")
		}
	}
	if client.session != "" && !input.noSession {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: client.session})
	}
	if client.csrf != "" && !input.noCSRFCookie {
		request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: client.csrf})
	}
	switch {
	case input.csrfHeader != nil:
		request.Header.Set(csrfHeaderName, *input.csrfHeader)
	case client.csrf != "" && !input.noCSRFHeader:
		request.Header.Set(csrfHeaderName, client.csrf)
	}
	for name, value := range input.headers {
		request.Header.Set(name, value)
	}
	if input.host != "" {
		request.Host = input.host
	}
	if input.remoteAddr != "" {
		request.RemoteAddr = input.remoteAddr
	}
	response := httptest.NewRecorder()
	client.harness.api.ServeHTTP(response, request)
	return response
}

func (client *advClient) get(path string) *httptest.ResponseRecorder {
	client.harness.t.Helper()
	return client.do(advRequest{method: http.MethodGet, path: path})
}

func (client *advClient) send(method, path, body string) *httptest.ResponseRecorder {
	client.harness.t.Helper()
	return client.do(advRequest{method: method, path: path, body: body})
}

// alive reports whether the session still authenticates, using an endpoint
// every role may read.
func (client *advClient) alive() bool {
	client.harness.t.Helper()
	response := client.get("/api/v1/account/sessions")
	switch response.Code {
	case http.StatusOK:
		return true
	case http.StatusUnauthorized:
		return false
	}
	client.harness.t.Fatalf("session probe: unexpected status %d, body = %s", response.Code, response.Body)
	return false
}

func advErrorCode(response *httptest.ResponseRecorder) string {
	var body errorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		return ""
	}
	return body.Code
}

func advRequireStatus(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status || (code != "" && advErrorCode(response) != code) {
		t.Fatalf("status = %d code = %q, want %d %q; body = %s",
			response.Code, advErrorCode(response), status, code, response.Body)
	}
}

// accountSessionIDs lists the caller's own web sessions as the API reports
// them, returning the id of the current one separately.
func (client *advClient) accountSessionIDs() (current string, others []string) {
	client.harness.t.Helper()
	response := client.get("/api/v1/account/sessions")
	if response.Code != http.StatusOK {
		client.harness.t.Fatalf("list sessions: %d %s", response.Code, response.Body)
	}
	var body accountSessionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		client.harness.t.Fatalf("decode sessions: %v", err)
	}
	for _, session := range body.Sessions {
		if session.Kind != "web" {
			continue
		}
		if session.Current {
			current = session.ID
		} else {
			others = append(others, session.ID)
		}
	}
	return current, others
}

// mobileToken pairs a bearer credential for a user straight through the
// store, the way a completed enrollment exchange would.
func (harness *advHarness) mobileToken(userID string) string {
	harness.t.Helper()
	token, digest, err := mobilepairing.NewToken()
	if err != nil {
		harness.t.Fatalf("mobilepairing.NewToken() error = %v", err)
	}
	if _, err := harness.store.CreateIOSPairingCredential(context.Background(), userID, digest); err != nil {
		harness.t.Fatalf("CreateIOSPairingCredential() error = %v", err)
	}
	return string(token)
}

// markCloned forces the clone verdict the same way clonedAPI does in
// provisioning_test.go: the decision normally comes from hardware this
// container does not have.
func markCloned(api *API) {
	api.provisionOnce.Do(func() {}) // resolved
	api.provisionDecision = provision.Decision{
		Status:           provision.StatusCloned,
		Reason:           "这份数据来自另一台设备",
		StoredMachineID:  "machine-elsewhere",
		CurrentMachineID: "machine-here",
	}
}
