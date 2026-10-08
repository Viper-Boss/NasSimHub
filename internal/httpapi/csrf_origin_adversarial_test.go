package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// What protects a state-changing request, as implemented (auth.go
// authorizeAPI): the X-ModemDeck-CSRF header must hash to the CSRF digest
// stored on the session row the cookie names. It is a per-session
// synchroniser token, not a bare double-submit: the modemdeck_csrf cookie is
// only how the SPA learns the value, and the server never compares cookie
// with header. Around it: both cookies are SameSite=Strict, and every body is
// refused unless it is application/json.
//
// What is NOT checked: Origin, Referer, Host, Sec-Fetch-Site. The tests below
// state both halves.

func advProtectedPaths() []string {
	paths := make([]string, 0, len(advStaticRoutes)+len(advAdminResourceRoutes)+8)
	for path, audience := range advStaticRoutes {
		if audience != advPublic {
			paths = append(paths, path)
		}
	}
	paths = append(paths, advAdminResourceRoutes...)
	return append(paths,
		"/api/v1/contacts/adv-any",
		"/api/v1/contacts/backup/contacts-20260101T000000Z-adv1.json",
		"/api/v1/account/sessions/adv-any",
		"/api/v1/calls/adv-any",
		"/api/v1/calls/adv-any/hangup",
		"/api/v1/calls/adv-any/lease",
		"/api/v1/calls/adv-any/media",
		"/api/v1/calls/adv-any/recording",
		"/api/v1/calls/adv-any/recordings/adv-seg",
		"/api/v1/settings/telegram/adv-any",
		"/api/v1/proxies/adv-any",
		"/api/v1/devices/"+advLineAlpha+"/configuration",
		"/api/v1/devices/"+advLineAlpha+"/sim/commands",
		"/api/v1/devices/"+advLineAlpha+"/network-scan",
		"/api/v1/lines/"+advLineAlpha+"/label",
		"/api/v1/not-a-route",
	)
}

func TestAdversarialEveryWriteOnEveryRouteNeedsTheSessionsOwnCSRFToken(t *testing.T) {
	harness := newAdvHarness(t)
	admin := harness.admin()
	adminElsewhere := harness.admin()
	alice := harness.aliceClient()

	empty := ""
	malformed := "not-a-token"
	variants := []struct {
		name  string
		apply func(*advRequest)
	}{
		{"no header, cookie present", func(request *advRequest) { request.noCSRFHeader = true }},
		{"no header, no cookie", func(request *advRequest) { request.noCSRFHeader = true; request.noCSRFCookie = true }},
		{"empty header", func(request *advRequest) { request.csrfHeader = &empty }},
		{"malformed header", func(request *advRequest) { request.csrfHeader = &malformed }},
		{"token of the same user's other session", func(request *advRequest) { request.csrfHeader = &adminElsewhere.csrf }},
		{"token of another user's session", func(request *advRequest) { request.csrfHeader = &alice.csrf }},
		{"the session token itself", func(request *advRequest) { request.csrfHeader = &admin.session }},
		{"token in the query string only", func(request *advRequest) {
			request.noCSRFHeader = true
			request.path += "?csrf_token=" + admin.csrf + "&X-ModemDeck-CSRF=" + admin.csrf
		}},
	}
	requests := 0
	for _, path := range advProtectedPaths() {
		for _, method := range advWriteMethods {
			for _, variant := range variants {
				request := advRequest{method: method, path: path, body: "{}"}
				variant.apply(&request)
				response := admin.do(request)
				requests++
				if response.Code != http.StatusForbidden || advErrorCode(response) != "csrf_failed" {
					t.Errorf("%s %s with %s: status = %d code = %q, want 403 csrf_failed",
						method, path, variant.name, response.Code, advErrorCode(response))
				}
			}
		}
	}
	t.Logf("%d forged writes refused", requests)
	// Refusing forged requests must not have cost anybody their session.
	if !admin.alive() || !adminElsewhere.alive() || !alice.alive() {
		t.Fatal("a refused forged write revoked a session")
	}
}

func TestAdversarialForgedWriteHasNoEffect(t *testing.T) {
	harness := newAdvHarness(t)
	alice := harness.aliceClient()
	other := harness.aliceClient()

	response := alice.do(advRequest{method: http.MethodDelete, path: "/api/v1/account/sessions/others", noCSRFHeader: true})
	advRequireStatus(t, response, http.StatusForbidden, "csrf_failed")
	if !other.alive() {
		t.Fatal(`a "log out other devices" without a CSRF token was carried out`)
	}
	response = alice.do(advRequest{
		method: http.MethodPut, path: "/api/v1/account/password", noCSRFHeader: true,
		body: `{"current_password":"` + advAlicePassword + `","new_password":"adv-forged-password"}`,
	})
	advRequireStatus(t, response, http.StatusForbidden, "csrf_failed")
	if _, response := harness.tryLogin("adv-alice", "adv-forged-password", nil); response.Code == http.StatusOK {
		t.Fatal("a password change without a CSRF token was carried out")
	}
	response = alice.do(advRequest{method: http.MethodDelete, path: "/api/v1/session", noCSRFHeader: true})
	advRequireStatus(t, response, http.StatusForbidden, "csrf_failed")
	if !alice.alive() {
		t.Fatal("a logout without a CSRF token was carried out")
	}
}

// The header alone is sufficient and the cookie alone is not. This is the
// property that makes the scheme a synchroniser token: an attacker who can
// plant a cookie (a sibling subdomain, a plain-HTTP response on the same
// host) gains nothing, because the cookie is never an input.
func TestAdversarialCSRFHeaderIsCheckedAgainstTheSessionNotTheCookie(t *testing.T) {
	harness := newAdvHarness(t)
	alice := harness.aliceClient()
	bob := harness.bobClient()

	response := alice.do(advRequest{
		method: http.MethodPut, path: "/api/v1/account/contact", body: `{"contact_id":""}`,
		noCSRFCookie: true,
	})
	advRequireStatus(t, response, http.StatusNoContent, "")

	// A planted cookie that matches a planted header -- the classic defeat of
	// naive double-submit -- is refused: neither is alice's token.
	request := httptest.NewRequest(http.MethodPut, "/api/v1/account/contact", strings.NewReader(`{"contact_id":""}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: alice.session})
	request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: bob.csrf})
	request.Header.Set(csrfHeaderName, bob.csrf)
	recorder := httptest.NewRecorder()
	harness.api.ServeHTTP(recorder, request)
	advRequireStatus(t, recorder, http.StatusForbidden, "csrf_failed")
}

func TestAdversarialReadsAreNotWritesInDisguise(t *testing.T) {
	harness := newAdvHarness(t)
	alice := harness.aliceClient()
	other := harness.aliceClient()

	// Method-override headers and parameters are not honoured, so a GET --
	// which needs no CSRF token -- cannot be turned into a DELETE.
	for _, path := range []string{"/api/v1/session", "/api/v1/account/sessions/others"} {
		alice.do(advRequest{
			method: http.MethodGet, path: path + "?_method=DELETE", noCSRFHeader: true,
			headers: map[string]string{
				"X-HTTP-Method-Override": "DELETE",
				"X-Method-Override":      "DELETE",
				"X-HTTP-Method":          "DELETE",
			},
		})
	}
	if !alice.alive() || !other.alive() {
		t.Fatal("a GET carrying a method override ended a session")
	}
	// HEAD and OPTIONS are exempt from the token as well; neither may act.
	for _, method := range []string{http.MethodHead, http.MethodOptions} {
		response := alice.do(advRequest{method: method, path: "/api/v1/account/sessions/others", noCSRFHeader: true})
		if response.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /account/sessions/others = %d, want 405", method, response.Code)
		}
	}
	if !other.alive() {
		t.Fatal("a HEAD or OPTIONS request ended a session")
	}
}

// Bodies must be JSON. That closes the cross-site "simple request": an HTML
// form can only send urlencoded, multipart or text/plain, and none is read.
func TestAdversarialNonJSONBodiesAreRefusedEvenWithAValidToken(t *testing.T) {
	harness := newAdvHarness(t)
	alice := harness.aliceClient()
	for _, contentType := range []string{
		"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=adv", "",
	} {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/account/password",
			strings.NewReader(`{"current_password":"`+advAlicePassword+`","new_password":"adv-form-password"}`))
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: alice.session})
		request.Header.Set(csrfHeaderName, alice.csrf)
		recorder := httptest.NewRecorder()
		harness.api.ServeHTTP(recorder, request)
		advRequireStatus(t, recorder, http.StatusUnsupportedMediaType, "unsupported_media_type")
	}
	// Login is the one write with no token (there is no session yet); the
	// same media-type rule is what keeps a cross-site form from logging a
	// victim's browser into an attacker's account.
	request := httptest.NewRequest(http.MethodPost, "/api/v1/session",
		strings.NewReader(`{"username":"adv-alice","password":"`+advAlicePassword+`"}`))
	request.Header.Set("Content-Type", "text/plain")
	recorder := httptest.NewRecorder()
	harness.api.ServeHTTP(recorder, request)
	advRequireStatus(t, recorder, http.StatusUnsupportedMediaType, "unsupported_media_type")
	if len(recorder.Result().Cookies()) != 0 {
		t.Fatal("a text/plain login set cookies")
	}
}

// FINDING A-09 (design, documented): the gateway does not look at Origin,
// Referer, Host or the forwarded-host headers when authorising a request. A
// write with a valid session token is accepted whatever those say, and a write
// without one is refused whatever those say. The token (plus SameSite=Strict
// and the JSON-only rule) is the whole control.
//
// No Origin check was added in r128. The gateway is reached under several
// names at once -- directly, through the bundled nginx (which forwards
// X-Forwarded-Host), through Cloudflare, through Tailscale Serve -- and it has
// no list of trusted proxies, so it cannot tell a forwarded host it should
// believe from one a direct client typed. A check that is right for one of
// those paths and wrong for another locks an owner out of their own gateway.
func TestAdversarialOriginRefererAndHostAreNotInputsToAuthorization(t *testing.T) {
	harness := newAdvHarness(t)
	alice := harness.aliceClient()

	shapes := map[string]advRequest{
		"same origin, direct": {
			host: "nas.example.test:8080", headers: map[string]string{"Origin": "http://nas.example.test:8080"},
		},
		"behind the bundled proxy": {
			host: "modemdeck:8080", headers: map[string]string{
				"Origin": "https://nas.example.test", "X-Forwarded-Host": "nas.example.test", "X-Forwarded-Proto": "https",
			},
		},
		"through Cloudflare": {
			host: "modemdeck:8080", headers: map[string]string{
				"Origin": "https://gateway.example.test", "X-Forwarded-Host": "gateway.example.test",
				"CF-Connecting-IP": "203.0.113.7", "CF-Ray": "adv-ray",
			},
		},
		"through Tailscale Serve": {
			host: "nas.tailnet-adv.ts.net", headers: map[string]string{
				"Origin": "https://nas.tailnet-adv.ts.net", "Tailscale-User-Login": "adv@example.test",
			},
		},
		"foreign Origin":           {host: "nas.example.test", headers: map[string]string{"Origin": "https://evil.example.test"}},
		"Origin: null":             {host: "nas.example.test", headers: map[string]string{"Origin": "null"}},
		"foreign Referer":          {host: "nas.example.test", headers: map[string]string{"Referer": "https://evil.example.test/attack.html"}},
		"Sec-Fetch-Site":           {host: "nas.example.test", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example.test"}},
		"Host unlike the Origin":   {host: "evil.example.test", headers: map[string]string{"Origin": "https://nas.example.test"}},
		"forged forwarded host":    {host: "nas.example.test", headers: map[string]string{"X-Forwarded-Host": "evil.example.test"}},
		"no Origin and no Referer": {host: "nas.example.test"},
	}
	for name, shape := range shapes {
		t.Run(name, func(t *testing.T) {
			withToken := shape
			withToken.method, withToken.path, withToken.body = http.MethodPut, "/api/v1/account/contact", `{"contact_id":""}`
			response := alice.do(withToken)
			advRequireStatus(t, response, http.StatusNoContent, "")

			withoutToken := withToken
			withoutToken.noCSRFHeader = true
			response = alice.do(withoutToken)
			advRequireStatus(t, response, http.StatusForbidden, "csrf_failed")
		})
	}
}

// The gateway answers no CORS preflight, so a cross-origin page cannot send
// the custom header or the JSON content type in the first place, and cannot
// read any response.
func TestAdversarialNoCrossOriginResourceSharingIsOffered(t *testing.T) {
	harness := newAdvHarness(t)
	alice := harness.aliceClient()
	for _, path := range []string{"/api/v1/session", "/api/v1/account/contact", "/api/v1/contacts", "/api/v1/health/live"} {
		for _, method := range []string{http.MethodOptions, http.MethodGet} {
			response := alice.do(advRequest{
				method: method, path: path, noCSRFHeader: true,
				headers: map[string]string{
					"Origin":                         "https://evil.example.test",
					"Access-Control-Request-Method":  "PUT",
					"Access-Control-Request-Headers": "content-type, x-modemdeck-csrf",
				},
			})
			for header := range response.Header() {
				if strings.HasPrefix(strings.ToLower(header), "access-control-") {
					t.Errorf("%s %s answered %s: %s", method, path, header, response.Header().Get(header))
				}
			}
			if response.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
				t.Errorf("%s %s: Cross-Origin-Resource-Policy = %q", method, path,
					response.Header().Get("Cross-Origin-Resource-Policy"))
			}
			if response.Header().Get("X-Frame-Options") != "DENY" {
				t.Errorf("%s %s: X-Frame-Options = %q", method, path, response.Header().Get("X-Frame-Options"))
			}
		}
	}
}

func TestAdversarialCookieAttributes(t *testing.T) {
	for _, secure := range []bool{true, false} {
		harness := newAdvHarness(t, func(options *Options) { options.SecureCookies = secure })
		_, response := harness.tryLogin("adv-alice", advAlicePassword, nil)
		advRequireStatus(t, response, http.StatusOK, "")
		seen := map[string]*http.Cookie{}
		for _, cookie := range response.Result().Cookies() {
			seen[cookie.Name] = cookie
		}
		session, csrf := seen[sessionCookieName], seen[csrfCookieName]
		if session == nil || csrf == nil || len(seen) != 2 {
			t.Fatalf("login set cookies %v; want exactly the session and CSRF cookies", seen)
		}
		if !session.HttpOnly {
			t.Error("the session cookie is readable from script")
		}
		if csrf.HttpOnly {
			t.Error("the CSRF cookie is HttpOnly; the SPA could not echo it")
		}
		for _, cookie := range []*http.Cookie{session, csrf} {
			if cookie.SameSite != http.SameSiteStrictMode {
				t.Errorf("%s: SameSite = %v, want Strict", cookie.Name, cookie.SameSite)
			}
			if cookie.Secure != secure {
				t.Errorf("%s: Secure = %v with SecureCookies = %v", cookie.Name, cookie.Secure, secure)
			}
			if cookie.Path != "/" || cookie.Domain != "" {
				t.Errorf("%s: Path = %q Domain = %q; want a host-only cookie on /", cookie.Name, cookie.Path, cookie.Domain)
			}
			if cookie.MaxAge != int(persistentCookieLifetime/time.Second) {
				t.Errorf("%s: Max-Age = %d", cookie.Name, cookie.MaxAge)
			}
		}
		if got := response.Header().Get("Strict-Transport-Security"); (got != "") != secure {
			t.Errorf("Strict-Transport-Security = %q with SecureCookies = %v", got, secure)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Error("the login response, which carries the CSRF token, is cacheable")
		}
		// Neither token is ever stored: the database holds digests only.
		var rows int
		if err := harness.db.QueryRow(
			`SELECT COUNT(*) FROM modemdeck_auth_sessions
			 WHERE CAST(session_token_digest AS TEXT) = ? OR CAST(csrf_token_digest AS TEXT) = ?`,
			session.Value, csrf.Value).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 0 {
			t.Fatal("a plaintext token is stored in the session table")
		}
	}
}

// Session fixation: a token the attacker chose in advance must not become the
// victim's session when the victim logs in.
func TestAdversarialLoginNeverAdoptsAPresentedSessionToken(t *testing.T) {
	harness := newAdvHarness(t)
	planted := opaqueTestToken(0x5a)

	body := `{"username":"adv-alice","password":"` + advAlicePassword + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/session", bytes.NewReader([]byte(body)))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: planted})
	request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: planted})
	recorder := httptest.NewRecorder()
	harness.api.ServeHTTP(recorder, request)
	advRequireStatus(t, recorder, http.StatusOK, "")
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Value == planted {
			t.Fatalf("login kept the presented %s value", cookie.Name)
		}
	}
	fixed := &advClient{harness: harness, session: planted, csrf: planted}
	if fixed.alive() {
		t.Fatal("the planted token authenticates after the victim logged in")
	}

	// Logging in again from an authenticated browser issues a new, separate
	// session rather than re-using or upgrading the one presented.
	alice := harness.aliceClient()
	request = httptest.NewRequest(http.MethodPost, "/api/v1/session",
		strings.NewReader(`{"username":"`+advAdminUser+`","password":"`+advAdminPassword+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: alice.session})
	recorder = httptest.NewRecorder()
	harness.api.ServeHTTP(recorder, request)
	advRequireStatus(t, recorder, http.StatusOK, "")
	advRequireStatus(t, alice.get("/api/v1/users"), http.StatusForbidden, "admin_required")
}

// FINDING A-08 (low, design): GET /api/v1/session revokes a session that is
// presented without its matching CSRF cookie. It fails closed -- the browser
// could not have made a write anyway -- but it means anything able to drop or
// overwrite the modemdeck_csrf cookie can log the user out. Pinned so the
// audit's description stays true.
func TestAdversarialSessionProbeWithoutTheCSRFCookieRevokesTheSession(t *testing.T) {
	harness := newAdvHarness(t)
	alice := harness.aliceClient()
	other := harness.aliceClient()

	response := alice.do(advRequest{method: http.MethodGet, path: "/api/v1/session", noCSRFCookie: true})
	advRequireStatus(t, response, http.StatusOK, "")
	if strings.Contains(response.Body.String(), `"authenticated":true`) || strings.Contains(response.Body.String(), "csrf_token") {
		t.Fatalf("the probe reported a session, or a token, without the CSRF cookie: %s", response.Body)
	}
	if alice.alive() {
		t.Fatal("the session survived; A-08 no longer describes the code -- update the audit")
	}
	if !other.alive() {
		t.Fatal("the probe revoked more than the session it was shown")
	}
	// The probe never hands the CSRF token to a caller who did not already
	// have it: with a WRONG cookie it does not echo the right one.
	bob := harness.bobClient()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: other.session})
	request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: bob.csrf})
	recorder := httptest.NewRecorder()
	harness.api.ServeHTTP(recorder, request)
	if strings.Contains(recorder.Body.String(), other.csrf) || strings.Contains(recorder.Body.String(), bob.csrf) {
		t.Fatalf("the session probe disclosed a CSRF token: %s", recorder.Body)
	}
}

// The write endpoints that are reachable with no session at all, and
// therefore with no CSRF token, each have their own precondition.
func TestAdversarialUnauthenticatedWritesAreInert(t *testing.T) {
	harness := newAdvHarness(t)
	anonymous := harness.anonymous()

	response := anonymous.send(http.MethodPost, "/api/v1/setup", `{"username":"adv-intruder","password":"adv-intruder-password"}`)
	advRequireStatus(t, response, http.StatusConflict, "setup_complete")
	if _, response := harness.tryLogin("adv-intruder", "adv-intruder-password", nil); response.Code == http.StatusOK {
		t.Fatal("setup created an account on a configured gateway")
	}
	response = anonymous.send(http.MethodPost, "/api/v1/provision/reset", `{"confirm_machine_id":"machine-here"}`)
	advRequireStatus(t, response, http.StatusConflict, "provision_not_required")
	response = anonymous.send(http.MethodPost, "/api/v1/mobile/enrollment/exchange",
		`{"invitation":"`+strings.Repeat("A", 43)+`","public_key":"not a key"}`)
	advRequireStatus(t, response, http.StatusBadRequest, "invalid_public_key")
	var credentials int
	if err := harness.db.QueryRow(`SELECT COUNT(*) FROM modemdeck_ios_pairing_credentials`).Scan(&credentials); err != nil {
		t.Fatal(err)
	}
	if credentials != 0 {
		t.Fatal("an unauthenticated exchange minted a phone credential")
	}
}
