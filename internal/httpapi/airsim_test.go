package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/airsimgw"
)

type airsimTestBox struct{}

func (airsimTestBox) Seal(plaintext, associated []byte) ([]byte, []byte, error) {
	return append([]byte(nil), associated...), append([]byte(nil), plaintext...), nil
}

func (airsimTestBox) Open(nonce, ciphertext, associated []byte) ([]byte, error) {
	if string(nonce) != string(associated) {
		return nil, errors.New("wrong associated data")
	}
	return append([]byte(nil), ciphertext...), nil
}

func airsimHarness(t *testing.T) (*advHarness, *advCommunications, *fakeCallLeases, *airsimgw.Gateway) {
	t.Helper()
	gateway, err := airsimgw.New(airsimgw.Config{
		PairingAddress: "127.0.0.1:0",
		StatePath:      filepath.Join(t.TempDir(), "airsim.json"),
		Secrets:        airsimTestBox{},
	})
	if err != nil {
		t.Fatal(err)
	}
	communications := &advCommunications{}
	leases := &fakeCallLeases{}
	harness := newAdvHarness(t, func(options *Options) {
		options.Communications = communications
		options.CallLeases = leases
		options.AirSIM = gateway
	})
	harness.seedHistory()
	return harness, communications, leases, gateway
}

func TestAirSIMCommandsRunAsTheLineScopedPairingAccount(t *testing.T) {
	harness, communications, leases, _ := airsimHarness(t)
	backend := airsimBackend{api: harness.api}
	ctx := context.Background()
	identity := airsimgw.Identity{PairingID: "pairing-1", UserID: harness.adminID, LineID: advLineAlpha, Peer: "192.168.1.20"}

	if err := backend.Authorize(ctx, identity); err != nil {
		t.Fatalf("administrator pairing refused: %v", err)
	}
	member := identity
	member.UserID = harness.alice.ID
	if err := backend.Authorize(ctx, member); !errors.Is(err, airsimgw.ErrForbidden) {
		t.Fatalf("member pairing error = %v, want ErrForbidden", err)
	}
	unknownLine := identity
	unknownLine.LineID = "line_nobody_has"
	if err := backend.Authorize(ctx, unknownLine); err == nil || errors.Is(err, airsimgw.ErrForbidden) {
		t.Fatalf("unassigned line error = %v, want a refusal that keeps the pairing", err)
	}

	call, err := backend.Dial(ctx, identity, "+15555550199", "airsim-dial-test-1")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if call.ID != "adv-call-started" {
		t.Fatalf("dialled call = %+v", call)
	}
	holder := backend.holder(identity)
	if len(communications.starts) != 1 || communications.starts[0].LineID != advLineAlpha ||
		communications.starts[0].RequestScope != holder || leases.holderID != holder ||
		leases.subjectID != harness.adminID {
		t.Fatalf("start = %+v, lease holder %q subject %q, want holder %q", communications.starts, leases.holderID, leases.subjectID, holder)
	}
	if browser := callLeaseHolderForMobileCredential([32]byte{}); browser == holder {
		t.Fatal("the iPhone shares a call-ownership holder with another client")
	}

	// The pairing principal carries only the gateway's line, so the API's
	// own line check refuses anything else.
	principal, err := backend.airsimPrincipal(ctx, identity)
	if err != nil || principal.IsAdmin() || len(principal.AllowedLineIDs) != 1 || !principal.CanAccessLine(advLineAlpha) {
		t.Fatalf("pairing principal = %+v, %v", principal, err)
	}
	if err := backend.SendMessage(ctx, identity, "+15555550198", "hello", "airsim-sms-test-1"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(communications.sends) != 1 || communications.sends[0].LineID != advLineAlpha {
		t.Fatalf("sends = %+v", communications.sends)
	}

	messages, err := backend.Messages(ctx, identity, time.Time{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "adv alpha history" || messages[0].Sender != advPeerAlpha ||
		!strings.HasPrefix(messages[0].DeliveryID, "nassimhub-sms-") || messages[0].Timestamp.IsZero() {
		t.Fatalf("messages = %+v", messages)
	}
	_, history, err := backend.Calls(ctx, identity, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].ID != "adv-call-alpha" {
		t.Fatalf("history = %+v, want only line alpha's call", history)
	}
	line, err := backend.Line(ctx, identity)
	if err != nil || line.PhoneNumber != "+15555550111" {
		t.Fatalf("line = %+v, %v", line, err)
	}
}

func TestAirSIMInternalIdentityIsNeverTakenFromTheRequest(t *testing.T) {
	harness, communications, _, _ := airsimHarness(t)
	// Headers and cookies can say anything; only the in-process context
	// value counts, and a network request cannot carry one.
	response := harness.anonymous().do(advRequest{
		method: http.MethodPost,
		path:   "/api/v1/calls",
		body:   `{"request_id":"x-1","line_id":"` + advLineAlpha + `","number":"+15555550199"}`,
		headers: map[string]string{
			"User-Agent":                "AirSIM (NasSimHub gateway)",
			"X-AirSIM-VoWLAN-Version":   "1",
			"X-AirSIM-VoWLAN-Signature": "forged",
		},
	})
	advRequireStatus(t, response, http.StatusUnauthorized, "")
	if len(communications.starts) != 0 {
		t.Fatalf("a forged AirSIM request started a call: %+v", communications.starts)
	}
}

func TestAirSIMSettingsAreAdministratorOnly(t *testing.T) {
	harness, _, _, gateway := airsimHarness(t)
	admin := harness.admin()
	response := admin.get("/api/v1/settings/airsim")
	advRequireStatus(t, response, http.StatusOK, "")
	var settings airsimSettingsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &settings); err != nil || !settings.Available || settings.Status.Enabled {
		t.Fatalf("settings = %s", response.Body)
	}
	advRequireStatus(t, admin.send(http.MethodPost, "/api/v1/settings/airsim/pairing", "{}"), http.StatusConflict, "airsim_disabled")
	advRequireStatus(t, admin.send(http.MethodPut, "/api/v1/settings/airsim", `{"enabled":true,"line_id":""}`), http.StatusBadRequest, "line_required")
	advRequireStatus(t, admin.send(http.MethodPut, "/api/v1/settings/airsim", `{"enabled":true,"line_id":"`+advLineAlpha+`"}`), http.StatusOK, "")

	response = admin.send(http.MethodPost, "/api/v1/settings/airsim/pairing", "{}")
	advRequireStatus(t, response, http.StatusOK, "")
	var window airsimgw.WindowStatus
	if err := json.Unmarshal(response.Body.Bytes(), &window); err != nil || len(window.Code) != 6 || window.ExpiresAt.IsZero() {
		t.Fatalf("window = %s", response.Body)
	}
	if status := gateway.Status(); status.Pairing == nil || status.Pairing.Code != window.Code {
		t.Fatalf("gateway status = %+v", status)
	}

	member := harness.aliceClient()
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/settings/airsim", ""},
		{http.MethodPut, "/api/v1/settings/airsim", `{"enabled":false}`},
		{http.MethodPost, "/api/v1/settings/airsim/pairing", "{}"},
		{http.MethodPost, "/api/v1/settings/airsim/devices/revoke", `{"id":"x"}`},
	} {
		advRequireStatus(t, member.send(request.method, request.path, request.body), http.StatusForbidden, "admin_required")
	}
	advRequireStatus(t, admin.send(http.MethodPost, "/api/v1/settings/airsim/devices/revoke", `{"id":"missing"}`), http.StatusNotFound, "not_found")
	advRequireStatus(t, admin.send(http.MethodDelete, "/api/v1/settings/airsim/pairing", ""), http.StatusNoContent, "")
	if gateway.Status().Pairing != nil {
		t.Fatal("cancel left the pairing window open")
	}
}

func TestAirSIMSettingsWithoutAGatewayReportUnavailable(t *testing.T) {
	harness := newAdvHarness(t)
	response := harness.admin().get("/api/v1/settings/airsim")
	advRequireStatus(t, response, http.StatusOK, "")
	if !strings.Contains(response.Body.String(), `"available":false`) {
		t.Fatalf("settings = %s", response.Body)
	}
	advRequireStatus(t, harness.admin().send(http.MethodPost, "/api/v1/settings/airsim/pairing", "{}"),
		http.StatusServiceUnavailable, "airsim_unavailable")
}

func TestAirSIMMultipleLinesStayInsideCurrentAdministratorScope(t *testing.T) {
	h, _, _, gateway := airsimHarness(t)
	admin := h.admin()
	response := admin.send(http.MethodPut, "/api/v1/settings/airsim", `{"enabled":true,"line_ids":["`+advLineAlpha+`","`+advLineBeta+`"]}`)
	advRequireStatus(t, response, 200, "")
	if len(gateway.Status().LineIDs) != 2 || gateway.Status().LineID != advLineAlpha {
		t.Fatal("missing multi-line settings")
	}
	advRequireStatus(t, admin.send(http.MethodPost, "/api/v1/settings/airsim/pairing", `{}`), 200, "")
	advRequireStatus(t, admin.send(http.MethodPut, "/api/v1/settings/airsim", `{"enabled":true,"line_ids":["`+advLineAlpha+`","not-assigned"]}`), 404, "")
	if len(gateway.Status().LineIDs) != 2 {
		t.Fatal("invalid save changed selection")
	}
	advRequireStatus(t, admin.send(http.MethodPut, "/api/v1/settings/airsim", `{"enabled":true,"line_ids":["`+advLineAlpha+`","`+advLineAlpha+`"]}`), 400, "invalid_line_selection")
	if _, err := h.db.Exec(`DELETE FROM modemdeck_user_lines WHERE user_id=? AND line_id=?`, h.adminID, advLineBeta); err != nil {
		t.Fatal(err)
	}
	advRequireStatus(t, admin.send(http.MethodPost, "/api/v1/settings/airsim/pairing", `{}`), 403, "line_not_assigned")
}
