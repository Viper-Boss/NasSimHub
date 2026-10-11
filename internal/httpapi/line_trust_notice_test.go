package httpapi

import (
	"context"
	"encoding/json"
	"github.com/human-agent65535/modemdeck/internal/communication"
	"github.com/human-agent65535/modemdeck/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLineTrustNoticeOnlyFollowsAuthorizedNodeLines(t *testing.T) {
	api, _, _ := newTrustAPI(t)
	lines := []store.LineSummary{{ID: "visible", EndpointID: store.NasSimHubNodeEndpointPrefix + "NSH-410-AAAAAA:0"}, {ID: "host", EndpointID: "host-line"}}
	got := api.attachLineTrust(lineSummaryResponses(lines), lines)
	if got[0].TrustNotice == nil || got[0].TrustNotice.State != "OBSERVATION" || got[0].TrustNotice.OutgoingAllowed {
		t.Fatal("missing refusal notice", got)
	}
	if got[1].TrustNotice != nil {
		t.Fatal("host inherited node trust")
	}
	raw, _ := json.Marshal(got[0])
	if strings.Contains(string(raw), "NSH-410-") {
		t.Fatal("node identity leaked")
	}
	if len(api.attachLineTrust(nil, nil)) != 0 {
		t.Fatal("unassigned nodes leaked")
	}
}
func TestTrustApproveNeedsIssuerGrantAndAdminRoute(t *testing.T) {
	if !adminOnlyAPIPath("/api/v1/security/trust/approve", http.MethodPost) {
		t.Fatal("approval is not admin-only")
	}
	api, _, _ := licensedAPI(t)
	w := httptest.NewRecorder()
	api.securityTrustApprove(w, httptest.NewRequest("POST", "/api/v1/security/trust/approve", strings.NewReader(`{"device_id":"some","reason":"verified"}`)))
	if w.Code != 403 || !strings.Contains(w.Body.String(), "issuer_authorization_required") {
		t.Fatal("unsigned grant accepted", w.Code, w.Body.String())
	}
	_ = context.Background()
	_ = communication.ErrPermissionDenied
}
