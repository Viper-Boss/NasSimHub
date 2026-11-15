// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/auth"
	"github.com/human-agent65535/modemdeck/internal/forwarding"
	"github.com/human-agent65535/modemdeck/internal/secretbox"
	"github.com/human-agent65535/modemdeck/internal/store"
)

func forwardingHarness(t *testing.T) *advHarness {
	t.Helper()
	box, err := secretbox.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	h := newAdvHarness(t, func(options *Options) { options.SettingsSecrets = box })
	h.api.forwardingStore = &forwarding.SQLStore{DB: h.db}
	if err = h.api.forwardingStore.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return h
}
func forwardingBody(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

type forwardingUsersRepository struct {
	Repository
	userRepository
	lookupUsers func(context.Context) ([]store.User, error)
}

func (r forwardingUsersRepository) Users(ctx context.Context) ([]store.User, error) {
	return r.lookupUsers(ctx)
}

func TestForwardingUserLookupFailureIsUnavailableNotRevoked(t *testing.T) {
	h := forwardingHarness(t)
	users := h.api.repository.(userRepository)
	event := forwarding.Event{Type: "incoming_sms", LineID: advLineAlpha}
	for _, fault := range []error{errors.New("private database failure"), context.Canceled, context.DeadlineExceeded} {
		h.api.repository = forwardingUsersRepository{Repository: h.api.repository, userRepository: users, lookupUsers: func(context.Context) ([]store.User, error) { return nil, fault }}
		err := h.api.authorizeForwarding(context.Background(), auth.InitialAdminUserID, event, forwarding.Channel{Type: "webhook"})
		if !errors.Is(err, forwarding.ErrUnavailable) || errors.Is(err, forwarding.ErrDenied) {
			t.Fatal("lookup failure treated as revoked", err)
		}
		response := httptest.NewRecorder()
		h.api.forwardingFailure(response, err)
		advRequireStatus(t, response, http.StatusServiceUnavailable, "forwarding_unavailable")
		if strings.Contains(response.Body.String(), fault.Error()) {
			t.Fatal("repository error exposed")
		}
	}
	service := &advCommunications{}
	h.api.communications = service
	ctx := forwarding.ContextWithSenderOwner(context.Background(), auth.InitialAdminUserID)
	channel := forwarding.Channel{Type: "sms", Params: map[string]string{"line_id": advLineAlpha, "to": "+15555550199"}}
	if err := h.api.forwardingDispatcher().Send(ctx, channel, forwarding.Rendered{Body: "synthetic"}, "forwarding_1001"); !errors.Is(err, forwarding.ErrUnavailable) || errors.Is(err, forwarding.ErrDenied) {
		t.Fatal("SMS boundary hid lookup failure", err)
	}
	service.mu.Lock()
	count := len(service.sends)
	service.mu.Unlock()
	if count != 0 {
		t.Fatal("lookup failure reached SMS service")
	}
	for _, user := range []store.User{
		{ID: "other-owner", Enabled: true, Role: auth.RoleAdmin},
		{ID: auth.InitialAdminUserID, Enabled: false, Role: auth.RoleAdmin},
		{ID: auth.InitialAdminUserID, Enabled: true, Role: auth.RoleMember},
	} {
		h.api.repository = forwardingUsersRepository{Repository: h.api.repository, userRepository: users, lookupUsers: func(context.Context) ([]store.User, error) { return []store.User{user}, nil }}
		if err := h.api.authorizeForwarding(context.Background(), auth.InitialAdminUserID, event, channel); !errors.Is(err, forwarding.ErrDenied) {
			t.Fatalf("revoked owner accepted: %+v err=%v", user, err)
		}
	}
}
func TestForwardingOriginalAdminAndCSRFBoundary(t *testing.T) {
	h := forwardingHarness(t)
	admin, alice := h.admin(), h.aliceClient()
	for _, path := range []string{forwardingAPI, forwardingAPI + "/deliveries"} {
		w := httptest.NewRecorder()
		h.api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 401 {
			t.Fatalf("anonymous %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{forwardingAPI, forwardingAPI + "/deliveries", forwardingAPI + "/preview", forwardingAPI + "/test", forwardingAPI + "/retry", forwardingAPI + "/migrate"} {
		if !adminOnlyAPIPath(path, http.MethodGet) || !adminOnlyAPIPath(path, http.MethodPost) {
			t.Fatal("missing admin gate", path)
		}
		advRequireStatus(t, alice.get(path), 403, "")
	}
	cfg := forwarding.DefaultConfig()
	cfg.OwnerID = "attacker-chosen-owner"
	advRequireStatus(t, admin.do(advRequest{method: http.MethodPut, path: forwardingAPI, body: forwardingBody(t, cfg), noCSRFHeader: true}), 403, "")
	advRequireStatus(t, admin.send(http.MethodPut, forwardingAPI, forwardingBody(t, cfg)), 200, "")
	stored, err := h.api.forwardingStore.Config(context.Background())
	if err != nil || stored.OwnerID != auth.InitialAdminUserID {
		t.Fatal("forged owner persisted", stored.OwnerID, err)
	}
	advRequireStatus(t, admin.send(http.MethodPut, forwardingAPI, forwardingBody(t, cfg)), 409, "revision_conflict")
}
func TestForwardingWriteOnlySecretsRealSyntheticTestAndNoHistoryPayload(t *testing.T) {
	h := forwardingHarness(t)
	admin := h.admin()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	c := forwarding.DefaultConfig()
	c.Channels = []forwarding.Channel{{ID: "hook", Type: "webhook", Name: "synthetic", Enabled: false, Params: map[string]string{}, Secrets: map[string]forwarding.Secret{"url": {Value: server.URL + "/synthetic-secret"}}}}
	response := admin.send(http.MethodPut, forwardingAPI, forwardingBody(t, c))
	advRequireStatus(t, response, 200, "")
	for _, payload := range []string{response.Body.String(), admin.get(forwardingAPI).Body.String()} {
		if strings.Contains(payload, "synthetic-secret") || strings.Contains(payload, "ciphertext") || strings.Contains(payload, "nonce") {
			t.Fatal("secret exposed")
		}
	}
	advRequireStatus(t, admin.send(http.MethodPost, forwardingAPI+"/test", `{"channel_id":"hook"}`), 200, "")
	if calls.Load() != 1 {
		t.Fatal("synthetic send not exercised")
	}
	public, _ := h.api.forwardingStore.Config(context.Background())
	public = public.Public()
	advRequireStatus(t, admin.send(http.MethodPut, forwardingAPI, forwardingBody(t, public)), 200, "")
	stored, _ := h.api.forwardingStore.Config(context.Background())
	if len(stored.Channels[0].Secrets["url"].Ciphertext) == 0 {
		t.Fatal("blank hidden secret erased")
	}
	history := admin.get(forwardingAPI + "/deliveries")
	advRequireStatus(t, history, 200, "")
	if strings.Contains(history.Body.String(), "synthetic-secret") {
		t.Fatal("private payload in history")
	}
}
func TestForwardingCurrentLineAndOTPPolicyAtSendBoundary(t *testing.T) {
	h := forwardingHarness(t)
	event := forwarding.Event{Type: "incoming_sms", LineID: advLineAlpha, Body: "验证码 123456", EndpointID: store.NasSimHubNodeEndpointPrefix + "node-id:0", EndpointMessageID: "nsh-in:source"}
	channel := forwarding.Channel{Type: "webhook"}
	var checked bool
	h.api.forwardingOTP = func(_ context.Context, m store.Message) bool {
		checked = m.EndpointLineID == event.EndpointID && m.EndpointMessageID == event.EndpointMessageID
		return false
	}
	if h.api.authorizeForwarding(context.Background(), auth.InitialAdminUserID, event, channel) == nil || !checked {
		t.Fatal("OTP source policy bypassed")
	}
	h.api.forwardingOTP = func(context.Context, store.Message) bool { return true }
	if err := h.api.authorizeForwarding(context.Background(), auth.InitialAdminUserID, event, channel); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`DELETE FROM modemdeck_user_lines WHERE user_id=? AND line_id=?`, auth.InitialAdminUserID, advLineAlpha); err != nil {
		t.Fatal(err)
	}
	if h.api.authorizeForwarding(context.Background(), auth.InitialAdminUserID, event, channel) == nil {
		t.Fatal("revoked line still sends")
	}
}
func TestForwardingMigrationLeavesOriginalFilesAndMasterOff(t *testing.T) {
	h := forwardingHarness(t)
	admin := h.admin()
	path, ok := h.api.homeSIMForwarderPath()
	if !ok {
		t.Fatal("no legacy path")
	}
	old := homeSIMForwarderSettings{Revision: 4, Forwarders: []homeSIMForwarder{{ID: "old", Name: "old", Type: "dingtalk", Enabled: true, Events: []string{"sms"}, Config: homeSIMForwarderConfig{WebhookURL: "https://example.invalid/secret-hook", Secret: "synthetic-signing-secret"}}, {ID: "nkn", Type: "nkn", Events: []string{"sms"}, Config: homeSIMForwarderConfig{CredentialRef: "TEST_NKN", NKNTopic: "synthetic"}}}}
	if err := writeHomeSIMJSON(path, old); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	response := admin.send(http.MethodPost, forwardingAPI+"/migrate", `{"revision":1}`)
	advRequireStatus(t, response, 200, "")
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("legacy file was changed")
	}
	raw := response.Body.String()
	if strings.Contains(raw, "secret-hook") || strings.Contains(raw, "synthetic-signing-secret") || !strings.Contains(raw, "nkn_sms") {
		t.Fatal("migration secrets or skipped status")
	}
	stored, err := h.api.forwardingStore.Config(context.Background())
	if err != nil || stored.Enabled || len(stored.Channels) != 1 || len(stored.Channels[0].Secrets["url"].Ciphertext) == 0 {
		t.Fatalf("migration %+v %v", stored.Public(), err)
	}
	advRequireStatus(t, admin.send(http.MethodPost, forwardingAPI+"/migrate", `{"revision":2}`), 409, "revision_conflict")
	for _, path := range []string{"/api/v1/settings/homesim/bark", "/api/v1/settings/homesim/forwarders"} {
		advRequireStatus(t, admin.get(path), 410, "forwarding_replaced")
		advRequireStatus(t, admin.send(http.MethodPost, path+"/test", `{}`), 410, "forwarding_replaced")
	}
}
func TestForwardingPreviewHasNoSendAndRejectsTemplateExecution(t *testing.T) {
	h := forwardingHarness(t)
	admin := h.admin()
	c := forwarding.DefaultConfig()
	c.Channels = []forwarding.Channel{{ID: "ch", Type: "webhook", Params: map[string]string{}, Secrets: map[string]forwarding.Secret{}}}
	c.Rules = []forwarding.Rule{{ID: "r", Enabled: true, Events: []string{"incoming_sms"}, ChannelIDs: []string{"ch"}, Logic: "all", TemplateID: "default", Condition: forwarding.Condition{Op: "notis", Field: "peer", Value: "10086||10000"}}}
	payload := map[string]any{"config": c, "rule_id": "r", "event": forwarding.Event{Type: "incoming_sms", Peer: "10086", Body: "synthetic", OccurredAt: time.Now()}}
	response := admin.send(http.MethodPost, forwardingAPI+"/preview", forwardingBody(t, payload))
	advRequireStatus(t, response, 200, "")
	if !strings.Contains(response.Body.String(), `"matched":false`) || !strings.Contains(response.Body.String(), `"sends":false`) {
		t.Fatal(response.Body.String())
	}
	var count int
	h.db.QueryRow(`SELECT COUNT(*) FROM modemdeck_forwarding_tasks`).Scan(&count)
	if count != 0 {
		t.Fatal("preview queued a send")
	}
	c.Templates[0].Body = `{{printf "%999999s" .Body}}`
	payload["config"] = c
	advRequireStatus(t, admin.send(http.MethodPost, forwardingAPI+"/preview", forwardingBody(t, payload)), 400, "invalid_forwarding_configuration")
}

func TestForwardingSMSReusesOriginalAntiFraudAndTargetLineGuard(t *testing.T) {
	h := forwardingHarness(t)
	service := &advCommunications{}
	h.api.communications = service
	h.api.licenseEnforced = true
	guard, ok := h.api.messageGuardStore()
	if !ok {
		t.Fatal("no original message guard")
	}
	for i := 0; i < messageGuardDailyLimit; i++ {
		guard.Attempt("+1555555010" + string(rune('0'+i)))
	}
	ctx := forwarding.ContextWithSenderOwner(context.Background(), auth.InitialAdminUserID)
	dispatcher := h.api.forwardingDispatcher()
	channel := forwarding.Channel{Type: "sms", Params: map[string]string{"line_id": advLineAlpha, "to": "+15555550199"}}
	if err := dispatcher.Send(ctx, channel, forwarding.Rendered{Body: "synthetic"}, "forwarding_999"); err == nil {
		t.Fatal("SMS bypassed original anti-fraud limit")
	}
	service.mu.Lock()
	count := len(service.sends)
	service.mu.Unlock()
	if count != 0 {
		t.Fatal("guard refusal reached communication service")
	}
	channel.Params["line_id"] = "unassigned"
	if err := dispatcher.Send(ctx, channel, forwarding.Rendered{Body: "synthetic"}, "forwarding_1000"); err == nil {
		t.Fatal("unassigned target sent")
	}
}
