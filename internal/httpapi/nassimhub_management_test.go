package httpapi

import (
	"context"
	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type managementFleet struct {
	fakeNodeFleet
	requested string
}

func (f *managementFleet) LocalManagement(_ context.Context, id string) (nassimhubnode.ManagementLink, error) {
	f.requested = id
	return nassimhubnode.ManagementLink{URL: "https://device:7581/#setup=synthetic", Configured: false}, nil
}
func TestManagementRouteIsAdminOnlyNoStoreAndSelectsOnlyDeviceID(t *testing.T) {
	if !adminOnlyAPIPath("/api/v1/nodes/admin", "POST") {
		t.Fatal("management route must be admin-only")
	}
	fleet := &managementFleet{}
	api := &API{nodeFleet: fleet}
	w := httptest.NewRecorder()
	api.nassimhubLocalManagement(w, jsonRequest("POST", "/api/v1/nodes/admin", `{"device_id":"known"}`))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || fleet.requested != "known" {
		t.Fatal("incorrect management handoff")
	}
	w = httptest.NewRecorder()
	api.nassimhubLocalManagement(w, jsonRequest("POST", "/api/v1/nodes/admin", `{"device_id":"known","url":"https://attacker"}`))
	if w.Code < 400 {
		t.Fatal("client-selected endpoint accepted")
	}
	w = httptest.NewRecorder()
	api.nassimhubLocalManagement(w, jsonRequest("POST", "/api/v1/nodes/admin", `{"device_id":""}`))
	if w.Code != 400 {
		t.Fatal("empty id accepted")
	}
	w = httptest.NewRecorder()
	api.nassimhubLocalManagement(w, jsonRequest("POST", "/api/v1/nodes/admin", strings.Repeat("x", 2048)))
	if w.Code < 400 {
		t.Fatal("oversized request accepted")
	}
}
