package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/auth"
)

func postureAPI(t *testing.T, settings TLSSettingsService) *API {
	t.Helper()
	api, err := New(&fakeRepository{}, Options{
		TLSSettings:               settings,
		disableAuthentication:     true,
		disableLicenseEnforcement: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return api
}

func readPosture(t *testing.T, api *API, request *http.Request) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("posture answered %d: %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func postureRequest(headers map[string]string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/security/posture", nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	return request
}

func row(t *testing.T, body map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := body[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is missing from %v", key, body)
	}
	return value
}

func TestThePostureDescribesTheViewersOwnConnection(t *testing.T) {
	status := testTLSStatus()
	status.KeyAlgorithm = "ECDSA P-256"
	status.NotAfter = time.Now().Add(80 * 24 * time.Hour).UTC().Format(time.RFC3339)
	api := postureAPI(t, &fakeTLSSettings{status: status})

	// The spellings nginx actually produces, checked against a real nginx:
	// $ssl_protocol "TLSv1.3", $ssl_cipher "TLS_AES_128_GCM_SHA256", and for a
	// hybrid group OpenSSL has no NID for, "0x" and the code point.
	body := readPosture(t, api, postureRequest(map[string]string{
		edgeHeader:      edgeDirect,
		edgeTLSProtocol: "TLSv1.3",
		edgeTLSGroup:    "0x11ec",
		edgeTLSCipher:   "TLS_AES_128_GCM_SHA256",
	}))

	checks := []struct{ key, state, value, detail string }{
		{"transport", "ok", "TLS 1.3", ""},
		{"post_quantum", "ok", "ML-KEM", "X25519MLKEM768"},
		{"packet_auth", "ok", "AES-128-GCM", "TLS_AES_128_GCM_SHA256"},
		{"replay", "ok", "", ""},
		{"identity", "ok", "ECDSA P-256", "AA:BB:CC"},
		{"pinning", "off", "", ""},
		// The fingerprint of the one compiled-in key, as the update card
		// and the issuing console print it.
		{"ota", "ok", "Ed25519", "b813a48a"},
	}
	for _, check := range checks {
		got := row(t, body, check.key)
		if got["state"] != check.state || stringField(got, "value") != check.value ||
			stringField(got, "detail") != check.detail {
			t.Errorf("%s = %v, want state %q value %q detail %q",
				check.key, got, check.state, check.value, check.detail)
		}
	}
	events := row(t, body, "events")
	if events["downgrade_guard"] != true || events["key_state"] != "ok" {
		t.Fatalf("events = %v", events)
	}
	if _, present := events["replay_blocked"]; present {
		t.Fatal("replay_blocked is reported, but nothing counts rejected replays")
	}
}

func stringField(fields map[string]any, key string) string {
	value, _ := fields[key].(string)
	return value
}

func TestPostQuantumIsOnlyClaimedForAHybridGroup(t *testing.T) {
	cases := []struct{ group, state, detail string }{
		{"X25519MLKEM768", "ok", "X25519MLKEM768"},
		{"0x11EC", "ok", "X25519MLKEM768"},
		{"X25519", "off", "X25519"},
		{"prime256v1", "off", "P-256"},
		{"secp384r1", "off", "P-384"},
		// A name nobody taught this about is unknown, not a pass and not a
		// fail.
		{"0x9999", "unknown", "0x9999"},
		{"", "unknown", ""},
	}
	for _, c := range cases {
		got := postQuantumRow(c.group)
		if got.State != c.state || got.Detail != c.detail {
			t.Errorf("postQuantumRow(%q) = %+v, want %s/%s", c.group, got, c.state, c.detail)
		}
		if got.State != "ok" && got.Value != "" {
			t.Errorf("postQuantumRow(%q) carries value %q without a hybrid group", c.group, got.Value)
		}
	}
}

func TestRecordProtectionReadsBothCipherSpellings(t *testing.T) {
	cases := map[string]string{
		"TLS_AES_128_GCM_SHA256":        "AES-128-GCM",
		"TLS_AES_256_GCM_SHA384":        "AES-256-GCM",
		"TLS_CHACHA20_POLY1305_SHA256":  "ChaCha20-Poly1305",
		"ECDHE-ECDSA-AES256-GCM-SHA384": "AES-256-GCM",
		"ECDHE-RSA-CHACHA20-POLY1305":   "ChaCha20-Poly1305",
		"ECDHE-RSA-AES128-SHA256":       "HMAC-SHA256",
		"ECDHE-RSA-AES256-SHA":          "HMAC-SHA1",
		"":                              "",
		"SOMETHING-ELSE":                "",
	}
	for cipher, want := range cases {
		if got := recordProtection(cipher); got != want {
			t.Errorf("recordProtection(%q) = %q, want %q", cipher, got, want)
		}
	}
}

func TestThroughCloudflareTheViewersHandshakeIsUnknown(t *testing.T) {
	api := postureAPI(t, nil)
	// nginx blanks the TLS headers on the Cloudflare listeners, but even if
	// they arrived they describe the tunnel's hop, not the viewer's.
	body := readPosture(t, api, postureRequest(map[string]string{
		edgeHeader:      edgeCloudflare,
		edgeTLSProtocol: "TLSv1.3",
		edgeTLSGroup:    "X25519MLKEM768",
	}))
	if got := row(t, body, "transport"); got["value"] != "Cloudflare" {
		t.Fatalf("transport = %v", got)
	}
	for _, key := range []string{"post_quantum", "packet_auth", "replay"} {
		if got := row(t, body, key); got["state"] != "unknown" {
			t.Errorf("%s = %v through Cloudflare, want unknown", key, got)
		}
	}
	if events, ok := body["events"].(map[string]any); ok {
		if _, present := events["downgrade_guard"]; present {
			t.Fatal("downgrade_guard is claimed for a handshake the gateway never saw")
		}
	}
}

func TestWithoutTheEdgeHeadersNoConnectionRowIsClaimed(t *testing.T) {
	api := postureAPI(t, nil)
	// An older web container, or a request that bypassed nginx.
	body := readPosture(t, api, postureRequest(nil))
	for _, key := range []string{"transport", "post_quantum", "packet_auth", "replay", "identity"} {
		if _, present := body[key]; present {
			t.Errorf("%s = %v with nothing describing the connection", key, body[key])
		}
	}
	// What the gateway knows about itself is still said.
	if row(t, body, "ota")["state"] != "ok" || row(t, body, "pinning")["state"] != "off" {
		t.Fatalf("body = %v", body)
	}
}

func TestAPlainConnectionIsAWarningNotAPass(t *testing.T) {
	api := postureAPI(t, nil)
	body := readPosture(t, api, postureRequest(map[string]string{edgeHeader: edgeDirect}))
	if got := row(t, body, "transport"); got["state"] != "warn" || got["value"] != "HTTP" {
		t.Fatalf("transport = %v", got)
	}
	for _, key := range []string{"post_quantum", "packet_auth", "replay"} {
		if _, present := body[key]; present {
			t.Errorf("%s is claimed on a connection with no TLS", key)
		}
	}
	if events := row(t, body, "events"); events["downgrade_guard"] != false {
		t.Fatalf("events = %v", events)
	}
}

func TestFailedLoginsAreCountedOverTheTimeActuallyWatched(t *testing.T) {
	api := postureAPI(t, nil)
	now := time.Now()
	api.loginFailures.history = newLoginFailureHistory(now.Add(-30 * time.Hour))
	api.loginFailures.history.record(now.Add(-25 * time.Hour)) // outside the day
	api.loginFailures.history.record(now.Add(-2 * time.Hour))
	api.loginFailures.recordFailure("someone")
	api.loginFailures.recordFailure("someone")
	// A success clears the throttle, and must not clear the count.
	api.loginFailures.recordSuccess("someone")

	events := row(t, readPosture(t, api, postureRequest(nil)), "events")
	if events["window_hours"] != float64(24) || events["anomalous_logins"] != float64(3) {
		t.Fatalf("events = %v, want 3 failures over 24 hours", events)
	}

	// Ten hours after a restart the figure covers ten hours, and says so.
	api.loginFailures.history = newLoginFailureHistory(now.Add(-10*time.Hour - time.Minute))
	events = row(t, readPosture(t, api, postureRequest(nil)), "events")
	if events["window_hours"] != float64(10) || events["anomalous_logins"] != float64(0) {
		t.Fatalf("events = %v, want 0 over 10 hours", events)
	}

	// Under an hour there is no honest window, so no figure.
	api.loginFailures.history = newLoginFailureHistory(now.Add(-20 * time.Minute))
	body := readPosture(t, api, postureRequest(nil))
	if events, ok := body["events"].(map[string]any); ok {
		if _, present := events["anomalous_logins"]; present {
			t.Fatalf("events = %v twenty minutes after a restart", events)
		}
	}
}

func TestGatewayWideEventsAreForAdministratorsOnly(t *testing.T) {
	status := testTLSStatus()
	status.KeyAlgorithm = "ECDSA P-256"
	api := postureAPI(t, &fakeTLSSettings{status: status})
	api.loginFailures.history = newLoginFailureHistory(time.Now().Add(-48 * time.Hour))
	api.loginFailures.recordFailure("admin")

	request := postureRequest(map[string]string{edgeHeader: edgeDirect, edgeTLSProtocol: "TLSv1.3"})
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{
		UserID: "member-1",
		Role:   auth.RoleMember,
	}))
	recorder := httptest.NewRecorder()
	api.securityPosture(recorder, request)
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	events := row(t, body, "events")
	for _, key := range []string{"window_hours", "anomalous_logins", "key_state"} {
		if _, present := events[key]; present {
			t.Errorf("a member is shown %s", key)
		}
	}
	// Their own connection is theirs to see.
	if events["downgrade_guard"] != true || row(t, body, "transport")["value"] != "TLS 1.3" {
		t.Fatalf("body = %v", body)
	}
}

func TestTheKeyStateFollowsTheServedCertificate(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		notAfter time.Time
		expired  bool
		want     string
	}{
		{"healthy", now.Add(60 * 24 * time.Hour), false, "ok"},
		{"inside the renewal window", now.Add(10 * 24 * time.Hour), false, "warn"},
		{"expired", now.Add(-time.Hour), true, "expired"},
	}
	for _, c := range cases {
		status := testTLSStatus()
		status.NotAfter = c.notAfter.UTC().Format(time.RFC3339)
		status.Expired = c.expired
		api := postureAPI(t, &fakeTLSSettings{status: status})
		if got := api.certificateKeyState(now); got != c.want {
			t.Errorf("%s: key state %q, want %q", c.name, got, c.want)
		}
	}
	if got := postureAPI(t, nil).certificateKeyState(now); got != "" {
		t.Fatalf("key state %q with no certificate service", got)
	}
}

func TestThePostureIsReadOnly(t *testing.T) {
	api := postureAPI(t, nil)
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/security/posture", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST answered %d", recorder.Code)
	}
}
