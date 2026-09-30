package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/store"
)

type testNasSimHubNodeRepository struct{ *fakeRepository }

func (*testNasSimHubNodeRepository) NasSimHubNodes(context.Context) ([]store.NasSimHubNodeSummary, error) {
	return []store.NasSimHubNodeSummary{{ID: "NSH-test-1", PairState: "paired"}}, nil
}

func TestNasSimHubUSBDiscoveryIsOnlyAnUnverifiedCandidate(t *testing.T) {
	var probed string
	closeSide, acceptSide := net.Pipe()
	defer acceptSide.Close()
	candidates, err := discoverNasSimHubUSB(context.Background(), func(_ context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" {
			t.Fatalf("unexpected network: %s", network)
		}
		probed = address
		return closeSide, nil
	})
	if err != nil || probed != usbNodeAddress || len(candidates) != 1 || candidates[0].Verified {
		t.Fatalf("unexpected discovery: address=%q candidates=%+v err=%v", probed, candidates, err)
	}
	missing, err := discoverNasSimHubUSB(context.Background(), func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("not connected")
	})
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing USB must be empty, not an error: %v %v", missing, err)
	}
	if !adminOnlyAPIPath("/api/v1/nodes/discover", http.MethodGet) {
		t.Fatal("discovery must be admin only")
	}
}

func TestNasSimHubNodesRouteIsAdminOnlyAndReadOnly(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		if !adminOnlyAPIPath("/api/v1/nodes", method) {
			t.Fatalf("nodes route is not admin-only for %s", method)
		}
	}
	api := &API{repository: &testNasSimHubNodeRepository{&fakeRepository{}}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
	response := httptest.NewRecorder()
	api.nassimhubNodes(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "NSH-test-1") {
		t.Fatalf("unexpected nodes response: %d %s", response.Code, response.Body.String())
	}
}
