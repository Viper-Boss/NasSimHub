package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/messageevents"
)

// Log redaction, end to end.
//
// Recognisable fake secrets are pushed through the places a secret really
// travels -- a failed login, a request the server cannot complete, a settings
// save, a push channel that cannot be reached, an incoming SMS -- and then
// every place a log line can be read is searched for them: the process log
// (the handler the diagnostics buffer forwards to), the three diagnostics
// endpoints, and every error response collected along the way.
//
// Every value is invented for this test. The gateway has no Wi-Fi setting, so
// there is no request that carries a PSK; the PSK-shaped value is planted at
// the logging layer in internal/diagnostics instead (logs_adversarial_test.go).
const (
	advSecretPassword    = "adv-planted-password-7Qx"
	advSecretNewPassword = "adv-planted-new-password-4Zr"
	advSecretBarkKey     = "advPlantedBarkDeviceKey0123"
	advSecretWebhookPath = "advPlantedWebhookPathSecret0123"
	advSecretAccessToken = "advplantedaccesstoken0123456789abcdef"
	advSecretSigning     = "SECadvPlantedDingTalkSigningSecret"
	advSecretF50Password = "adv-planted-f50-password"
	advSecretBotToken    = "987654321:AAadvPlantedTelegramBotToken0123456"
	advSecretBearer      = "advPlantedBearerCredential0123456789"
	advSecretPhone       = "+15555550188"
	advSecretSMSCode     = "adv-code-482913"
	advSecretSearch      = "adv-planted-query-value"
)

// advSecretBox is a SecretBox that only has to round-trip; sealing is covered
// by internal/secretbox.
type advSecretBox struct{}

func (advSecretBox) Seal(plaintext, _ []byte) ([]byte, []byte, error) {
	sealed := make([]byte, len(plaintext))
	for index, value := range plaintext {
		sealed[index] = value ^ 0x5a
	}
	return []byte("adv-nonce"), sealed, nil
}

func (advSecretBox) Open(_, ciphertext, _ []byte) ([]byte, error) {
	opened := make([]byte, len(ciphertext))
	for index, value := range ciphertext {
		opened[index] = value ^ 0x5a
	}
	return opened, nil
}

func TestAdversarialPlantedSecretsNeverReachALogOrAnErrorResponse(t *testing.T) {
	events := messageevents.NewBuffer(8)
	telegram := &fakeTelegramSettings{
		// What a real Telegram client error looks like: net/http quotes the
		// request URL, and the URL is the bot token.
		createError: &url.Error{
			Op:  "Post",
			URL: "https://api.telegram.example.test/bot" + advSecretBotToken + "/getMe",
			Err: errors.New("dial tcp 192.0.2.1:443: i/o timeout"),
		},
	}
	harness := newAdvHarness(t, func(options *Options) {
		options.MessageEvents = events
		options.SettingsSecrets = advSecretBox{}
		options.TelegramSettings = telegram
	})
	t.Setenv("ADV_PLANTED_BARK_KEY", advSecretBarkKey)
	workerContext, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	harness.api.StartHomeSIMForwarderWorker(workerContext)

	admin := harness.admin()
	alice := harness.aliceClient()

	var errorBodies []string
	record := func(response *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		if response.Code >= 400 {
			errorBodies = append(errorBodies, fmt.Sprintf("%d %s", response.Code, response.Body.String()))
		}
		return response
	}

	// --- login failures -------------------------------------------------
	for _, username := range []string{"adv-alice", advAdminUser, "adv-nobody", advSecretPassword} {
		_, response := harness.tryLogin(username, advSecretPassword, map[string]string{
			"Authorization": "Bearer " + advSecretBearer,
			"Cookie":        sessionCookieName + "=" + alice.session + "; " + csrfCookieName + "=" + alice.csrf,
		})
		record(response)
	}
	record(alice.send(http.MethodPut, "/api/v1/account/password",
		`{"current_password":"`+advSecretPassword+`","new_password":"`+advSecretNewPassword+`"}`))
	// A bearer credential that is not one.
	request := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	request.Header.Set("Authorization", "Bearer "+advSecretBearer)
	recorder := httptest.NewRecorder()
	harness.api.ServeHTTP(recorder, request)
	record(recorder)

	// --- requests the server refuses ------------------------------------
	record(alice.send(http.MethodPost, "/api/v1/contacts", `{"display_name":"`+advSecretPassword+`",`))
	record(alice.send(http.MethodPost, "/api/v1/contacts", `{"unknown_`+advSecretPassword+`":1}`))
	record(alice.send(http.MethodPost, "/api/v1/messages",
		`{"request_id":"adv-r1","line_id":"`+advLineAlpha+`","to":"`+advSecretPhone+`","content":"`+advSecretSMSCode+`"}`))
	record(alice.send(http.MethodPost, "/api/v1/messages",
		`{"request_id":"adv-r2","line_id":"`+advLineBeta+`","to":"`+advSecretPhone+`","content":"`+advSecretSMSCode+`"}`))
	record(alice.do(advRequest{method: http.MethodPut, path: "/api/v1/account/contact",
		body: `{"contact_id":"` + advSecretPassword + `"}`, noCSRFHeader: true}))
	record(alice.get("/api/v1/messages?line_id=" + advLineAlpha + "&peer=" + url.QueryEscape(advSecretPhone) + "&cursor=" + advSecretSearch))
	record(alice.get("/api/v1/users?token=" + advSecretBearer))

	// --- settings updates -----------------------------------------------
	record(admin.send(http.MethodPost, "/api/v1/users",
		`{"username":"adv-carol","password":"`+advSecretNewPassword+`","line_ids":["line_adv_missing"]}`))
	record(admin.send(http.MethodPut, "/api/v1/users/"+harness.bob.ID+"/password", `{"password":"`+advSecretNewPassword+`"}`))
	record(admin.send(http.MethodPut, "/api/v1/users/adv-missing-user/password", `{"password":"`+advSecretNewPassword+`"}`))

	webhook := "https://127.0.0.1:1/hook/" + advSecretWebhookPath + "?access_token=" + advSecretAccessToken
	forwarder := `{"id":"adv-hook","name":"adv","type":"dingtalk","enabled":true,"events":["sms"],` +
		`"config":{"webhook_url":"` + webhook + `","secret":"` + advSecretSigning + `"}}`
	response := record(admin.send(http.MethodPut, "/api/v1/settings/homesim/forwarders",
		`{"revision":1,"forwarders":[`+forwarder+`]}`))
	advRequireStatus(t, response, http.StatusOK, "")
	if strings.Contains(response.Body.String(), advSecretSigning) {
		t.Errorf("saving a channel echoed its signing secret: %s", response.Body)
	}
	response = record(admin.send(http.MethodPost, "/api/v1/settings/homesim/forwarders/test", forwarder))
	advRequireStatus(t, response, http.StatusBadGateway, "forward_failed")

	response = record(admin.send(http.MethodPut, "/api/v1/settings/homesim/bark",
		`{"revision":1,"server_url":"https://127.0.0.1:1","credential_ref":"ADV_PLANTED_BARK_KEY","group":"adv","sound":""}`))
	advRequireStatus(t, response, http.StatusOK, "")
	response = record(admin.send(http.MethodPost, "/api/v1/settings/homesim/bark/test", "{}"))
	advRequireStatus(t, response, http.StatusBadGateway, "push_failed")

	response = record(admin.send(http.MethodPut, "/api/v1/settings/homesim/f50",
		`{"revision":1,"base_url":"http://127.0.0.1:1","username":"admin","password":"`+advSecretF50Password+`"}`))
	advRequireStatus(t, response, http.StatusOK, "")
	if strings.Contains(response.Body.String(), advSecretF50Password) {
		t.Errorf("saving the F50 password echoed it: %s", response.Body)
	}
	record(admin.get("/api/v1/homesim/f50/status"))
	record(admin.get("/api/v1/homesim/f50/sms"))

	response = record(admin.send(http.MethodPost, "/api/v1/settings/telegram",
		`{"display_name":"adv","enabled":true,"bot_token":"`+advSecretBotToken+`","chat_id":"1","admin_id":"1","line_scopes":[],"incoming_sms":true}`))
	advRequireStatus(t, response, http.StatusInternalServerError, "internal_error")

	// --- an incoming SMS, fanned out to the unreachable channel -----------
	events.Publish(messageevents.IncomingSMS{
		MessageID: "adv-sms-1", LineID: advLineAlpha, Peer: advSecretPhone, Content: "your code is " + advSecretSMSCode,
	})
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(harness.sink.String(), "SMS forward failed") {
		if time.Now().After(deadline) {
			t.Fatal("the forwarding failure was never logged; the test would be searching an empty log")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// --- read the logs back every way there is ----------------------------
	sinks := map[string]string{"process log": harness.sink.String()}
	history := admin.get("/api/v1/diagnostics/logs?limit=2000")
	advRequireStatus(t, history, http.StatusOK, "")
	sinks["/diagnostics/logs"] = history.Body.String()
	download := admin.get("/api/v1/diagnostics/logs/download")
	advRequireStatus(t, download, http.StatusOK, "")
	sinks["/diagnostics/logs/download"] = download.Body.String()
	sinks["/diagnostics/logs/stream"] = advReadLogStream(t, harness, admin)
	// Searching the log for a secret must not be a way to confirm it either.
	search := admin.get("/api/v1/diagnostics/logs?search=" + url.QueryEscape(advSecretBarkKey))
	advRequireStatus(t, search, http.StatusOK, "")
	var found diagnosticLogResponse
	if err := json.Unmarshal(search.Body.Bytes(), &found); err != nil {
		t.Fatal(err)
	}
	if len(found.Entries) != 0 {
		t.Errorf("searching the log for a secret matched %d entries", len(found.Entries))
	}

	secrets := map[string]string{
		"login password":          advSecretPassword,
		"new password":            advSecretNewPassword,
		"session token":           alice.session,
		"CSRF token":              alice.csrf,
		"administrator session":   admin.session,
		"administrator CSRF":      admin.csrf,
		"bearer credential":       advSecretBearer,
		"Bark device key":         advSecretBarkKey,
		"webhook path secret":     advSecretWebhookPath,
		"webhook access token":    advSecretAccessToken,
		"DingTalk signing secret": advSecretSigning,
		"F50 password":            advSecretF50Password,
		"Telegram bot token":      advSecretBotToken,
		"phone number":            advSecretPhone,
		"SMS verification code":   advSecretSMSCode,
		"query value":             advSecretSearch,
	}
	for name, text := range sinks {
		for _, marker := range []string{"SMS forward failed", "create Telegram unit"} {
			if !strings.Contains(text, marker) {
				t.Errorf("%s does not contain %q; it is not showing the lines this test planted", name, marker)
			}
		}
		for label, secret := range secrets {
			if strings.Contains(text, secret) {
				t.Errorf("%s contains the %s (%q)", name, label, secret)
			}
		}
	}
	// Error responses. The message endpoint legitimately has no service here
	// and the contact endpoints reject the bodies; none may echo what it was
	// sent, and the three "unreachable" errors may not quote the URL.
	if len(errorBodies) < 15 {
		t.Fatalf("only %d error responses were collected", len(errorBodies))
	}
	for _, body := range errorBodies {
		for label, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Errorf("an error response contains the %s: %s", label, body)
			}
		}
	}
}

// advReadLogStream reads the SSE log stream over a real connection until the
// replayed window has arrived, then hangs up.
func advReadLogStream(t *testing.T, harness *advHarness, admin *advClient) string {
	t.Helper()
	server := httptest.NewServer(harness.api)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/diagnostics/logs/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: admin.session})
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("open log stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("log stream status = %d", response.StatusCode)
	}
	var collected strings.Builder
	reader := bufio.NewReader(response.Body)
	sawForward, sawTelegram := false, false
	for !(sawForward && sawTelegram) {
		line, err := reader.ReadString('\n')
		collected.WriteString(line)
		sawForward = sawForward || strings.Contains(line, "SMS forward failed")
		sawTelegram = sawTelegram || strings.Contains(line, "create Telegram unit")
		if err != nil {
			break
		}
	}
	return collected.String()
}

// The stream is re-authorised while it is open: it must end when the session
// that opened it is revoked, not run on until the browser closes it.
func TestAdversarialLogStreamEndsWhenItsSessionIsRevoked(t *testing.T) {
	harness := newAdvHarness(t)
	harness.api.streamAuthInterval = 30 * time.Millisecond
	admin := harness.admin()
	other := harness.admin()

	server := httptest.NewServer(harness.api)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/diagnostics/logs/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: admin.session})
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("open log stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("log stream status = %d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("the stream did not start: %v", err)
	}

	advRequireStatus(t, other.send(http.MethodDelete, "/api/v1/account/sessions/others", ""), http.StatusNoContent, "")

	closed := make(chan error, 1)
	go func() {
		for {
			if _, err := reader.ReadString('\n'); err != nil {
				closed <- err
				return
			}
		}
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the log stream stayed open after its session was revoked")
	}
}
