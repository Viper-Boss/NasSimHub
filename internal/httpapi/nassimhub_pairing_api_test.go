package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
)

type fakeNodeFleet struct {
	pairRequests []nassimhubnode.PairRequest
	pairErr      error
	unpaired     []string
	unpairErr    error
	released     bool
}

func (*fakeNodeFleet) CoreID() string { return "nas-0123456789ab" }

func (*fakeNodeFleet) Nodes() []nassimhubnode.NodeView {
	return []nassimhubnode.NodeView{{DeviceID: "NSH-410-0A0B0C", Online: true}}
}

func (*fakeNodeFleet) Discover(context.Context) ([]nassimhubnode.Candidate, error) {
	return []nassimhubnode.Candidate{{DeviceID: "NSH-410-0A0B0C", Fingerprint: "AAAA BBBB", Ticket: "t"}}, nil
}

func (f *fakeNodeFleet) Pair(_ context.Context, request nassimhubnode.PairRequest) (nassimhubnode.NodeView, error) {
	f.pairRequests = append(f.pairRequests, request)
	if f.pairErr != nil {
		return nassimhubnode.NodeView{}, f.pairErr
	}
	return nassimhubnode.NodeView{DeviceID: request.DeviceID, Online: true}, nil
}

func (f *fakeNodeFleet) Unpair(_ context.Context, deviceID string) (bool, error) {
	f.unpaired = append(f.unpaired, deviceID)
	return f.released, f.unpairErr
}

func (*fakeNodeFleet) Refresh(_ context.Context, deviceID string) (nassimhubnode.NodeView, error) {
	if deviceID != "NSH-410-0A0B0C" {
		return nassimhubnode.NodeView{}, nassimhubnode.ErrUnknownNode
	}
	return nassimhubnode.NodeView{DeviceID: deviceID}, nil
}

func jsonRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestNodePairingRoutesAreAdminOnly(t *testing.T) {
	for _, path := range []string{"/api/v1/nodes/pair", "/api/v1/nodes/unpair", "/api/v1/nodes/refresh"} {
		if !adminOnlyAPIPath(path, http.MethodPost) {
			t.Errorf("%s must be admin only", path)
		}
	}
}

func TestNodePairPassesConfirmationThrough(t *testing.T) {
	fleet := &fakeNodeFleet{}
	api := &API{nodeFleet: fleet}
	response := httptest.NewRecorder()
	api.nassimhubPair(response, jsonRequest(http.MethodPost, "/api/v1/nodes/pair",
		`{"ticket":"t","device_id":"NSH-410-0A0B0C","fingerprint":"AAAA BBBB","display_name":"客厅棒子"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("pair: %d %s", response.Code, response.Body.String())
	}
	if len(fleet.pairRequests) != 1 || fleet.pairRequests[0].Fingerprint != "AAAA BBBB" || fleet.pairRequests[0].DisplayName != "客厅棒子" {
		t.Fatalf("pair request = %+v", fleet.pairRequests)
	}
}

func TestNodePairRejectsIncompleteConfirmation(t *testing.T) {
	fleet := &fakeNodeFleet{}
	api := &API{nodeFleet: fleet}
	for _, body := range []string{
		`{"device_id":"NSH-410-0A0B0C","fingerprint":"AAAA"}`,
		`{"ticket":"t","fingerprint":"AAAA"}`,
		`{"ticket":"t","device_id":"NSH-410-0A0B0C"}`,
		`{"ticket":"t","device_id":"NSH-410-0A0B0C","fingerprint":"AAAA","display_name":"` + strings.Repeat("长", 65) + `"}`,
		`{"ticket":"t","device_id":"NSH-410-0A0B0C","fingerprint":"AAAA","unexpected":true}`,
	} {
		response := httptest.NewRecorder()
		api.nassimhubPair(response, jsonRequest(http.MethodPost, "/api/v1/nodes/pair", body))
		if response.Code != http.StatusBadRequest {
			t.Errorf("body %s: status %d, want 400", body, response.Code)
		}
	}
	if len(fleet.pairRequests) != 0 {
		t.Fatalf("incomplete confirmations reached the fleet: %+v", fleet.pairRequests)
	}
}

func TestNodeErrorsMapToStableCodes(t *testing.T) {
	for target, code := range map[error]string{
		nassimhubnode.ErrTicketInvalid:       "pairing_expired",
		nassimhubnode.ErrFingerprintMismatch: "fingerprint_mismatch",
		nassimhubnode.ErrAlreadyPaired:       "already_paired",
		nassimhubnode.ErrKeyChanged:          "key_changed",
		nassimhubnode.ErrOwnedElsewhere:      "owned_elsewhere",
		nassimhubnode.ErrIncompatible:        "incompatible",
		nassimhubnode.ErrPairingNotConfirmed: "not_confirmed",
		nassimhubnode.ErrNodeUnavailable:     "node_unreachable",
		nassimhubnode.ErrUnknownNode:         "node_not_found",
	} {
		api := &API{nodeFleet: &fakeNodeFleet{pairErr: fmt.Errorf("wrapped: %w", target)}}
		response := httptest.NewRecorder()
		api.nassimhubPair(response, jsonRequest(http.MethodPost, "/api/v1/nodes/pair",
			`{"ticket":"t","device_id":"NSH-410-0A0B0C","fingerprint":"AAAA"}`))
		if response.Code < 400 || !strings.Contains(response.Body.String(), `"`+code+`"`) {
			t.Errorf("%v: status %d body %s, want code %s", target, response.Code, response.Body.String(), code)
		}
	}
}

func TestNodeUnpairReportsDeviceRelease(t *testing.T) {
	fleet := &fakeNodeFleet{released: false}
	api := &API{nodeFleet: fleet}
	response := httptest.NewRecorder()
	api.nassimhubUnpair(response, jsonRequest(http.MethodPost, "/api/v1/nodes/unpair", `{"device_id":" NSH-410-0A0B0C "}`))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"released_on_device":false`) {
		t.Fatalf("unpair: %d %s", response.Code, response.Body.String())
	}
	fleet.unpairErr = nassimhubnode.ErrUnknownNode
	response = httptest.NewRecorder()
	api.nassimhubUnpair(response, jsonRequest(http.MethodPost, "/api/v1/nodes/unpair", `{"device_id":"other"}`))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown node unpair: %d", response.Code)
	}
}

func TestNodeFleetBacksListAndDiscovery(t *testing.T) {
	api := &API{nodeFleet: &fakeNodeFleet{}}
	response := httptest.NewRecorder()
	api.nassimhubNodes(response, httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"core_id":"nas-0123456789ab"`) {
		t.Fatalf("nodes: %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	api.nassimhubDiscover(response, httptest.NewRequest(http.MethodGet, "/api/v1/nodes/discover", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "AAAA BBBB") {
		t.Fatalf("discover: %d %s", response.Code, response.Body.String())
	}
}

func TestNodePairingUnavailableWithoutFleet(t *testing.T) {
	api := &API{}
	response := httptest.NewRecorder()
	api.nassimhubPair(response, jsonRequest(http.MethodPost, "/api/v1/nodes/pair", `{}`))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("pair without fleet: %d", response.Code)
	}
}
