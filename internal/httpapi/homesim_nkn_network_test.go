package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func newTestNKNNetworkManager(t *testing.T) *nknNetworkManager {
	t.Helper()
	t.Setenv(nknNodeDirEnv, filepath.Join(t.TempDir(), "nkn-node"))
	return newNKNNetworkManager(nil)
}

func TestNKNNetworkSettingsRoundTrip(t *testing.T) {
	manager := newTestNKNNetworkManager(t)
	settings, err := manager.loadSettings()
	if err != nil {
		t.Fatalf("loadSettings on missing file: %v", err)
	}
	if settings.Enabled || settings.Revision != 0 {
		t.Fatalf("unexpected factory defaults: %+v", settings)
	}
	settings = nknNetworkSettings{Enabled: true, Revision: 7}
	if err := manager.saveSettings(settings); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}
	loaded, err := manager.loadSettings()
	if err != nil {
		t.Fatalf("loadSettings after save: %v", err)
	}
	if loaded != settings {
		t.Fatalf("round trip mismatch: got %+v want %+v", loaded, settings)
	}
}

func TestNKNNetworkApplyRevisionConflict(t *testing.T) {
	manager := newTestNKNNetworkManager(t)
	api := &API{}
	if _, err := manager.apply(api, nknNetworkUpdateRequest{Enabled: false, ExpectedRevision: 5}); !errors.Is(err, errNKNNetworkRevisionConflict) {
		t.Fatalf("expected revision conflict on factory settings, got %v", err)
	}
	status, err := manager.apply(api, nknNetworkUpdateRequest{Enabled: false, ExpectedRevision: 0})
	if err != nil {
		t.Fatalf("apply without expected revision: %v", err)
	}
	if status.Settings.Enabled || status.Settings.Revision != 1 {
		t.Fatalf("unexpected settings after first apply: %+v", status.Settings)
	}
	if status.Runtime.State != "disabled" {
		t.Fatalf("state for disabled settings: %q", status.Runtime.State)
	}
	if _, err := manager.apply(api, nknNetworkUpdateRequest{Enabled: false, ExpectedRevision: 99}); !errors.Is(err, errNKNNetworkRevisionConflict) {
		t.Fatalf("expected revision conflict on stale revision, got %v", err)
	}
}

func TestNKNNetworkStatusStates(t *testing.T) {
	manager := newTestNKNNetworkManager(t)
	if state := manager.status().Runtime.State; state != "disabled" {
		t.Fatalf("state with no settings: %q", state)
	}
	// Enabled settings persisted without a running worker surface as an
	// error state rather than a permanent "joining" limbo.
	if err := manager.saveSettings(nknNetworkSettings{Enabled: true, Revision: 1}); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}
	status := manager.status()
	if status.Runtime.State != "error" {
		t.Fatalf("state for enabled settings without worker: %q", status.Runtime.State)
	}
	if status.Topic != nknNetworkTopic {
		t.Fatalf("status topic: %q", status.Topic)
	}
}

func TestNKNNetworkPeersRotationAndParse(t *testing.T) {
	self := "modemdeck.ABCDEF0123"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		var payload struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}
		if payload.Method != "getsubscribers" {
			t.Errorf("request method: %q", payload.Method)
		}
		var params struct {
			Topic string `json:"topic"`
			Meta  bool   `json:"meta"`
		}
		if err := json.Unmarshal(payload.Params, &params); err != nil {
			t.Errorf("decode params: %v", err)
			return
		}
		if params.Topic != nknNetworkTopic {
			t.Errorf("request topic: %q", params.Topic)
		}
		if !params.Meta {
			t.Error("request must ask for metadata")
		}
		// The self address is stored lowercase on chain; matching must be
		// case insensitive and the list must come back sorted.
		fmt.Fprintf(response, `{"jsonrpc":"2.0","id":1,"result":{"subscribers":{"modemdeck.deadbeef":"other","modemdeck.abcdef0123":"self"}}}`)
	}))
	defer server.Close()

	result, err := fetchNKNNetworkPeersFrom(self, []string{"http://127.0.0.1:1", server.URL})
	if err != nil {
		t.Fatalf("fetchNKNNetworkPeersFrom: %v", err)
	}
	if result.Topic != nknNetworkTopic || result.Window != "24h" || result.Count != 2 {
		t.Fatalf("unexpected envelope: %+v", result)
	}
	if len(result.Peers) != 2 {
		t.Fatalf("peer count: %d", len(result.Peers))
	}
	if result.Peers[0].Address != "modemdeck.abcdef0123" || result.Peers[0].Meta != "self" || !result.Peers[0].IsSelf {
		t.Fatalf("first peer (sorted, self): %+v", result.Peers[0])
	}
	if result.Peers[1].Address != "modemdeck.deadbeef" || result.Peers[1].Meta != "other" || result.Peers[1].IsSelf {
		t.Fatalf("second peer: %+v", result.Peers[1])
	}
}

func TestNKNNetworkPeersAllSeedsFail(t *testing.T) {
	if _, err := fetchNKNNetworkPeersFrom("", []string{"http://127.0.0.1:1"}); err == nil {
		t.Fatal("expected an error when no seed answers")
	}
}
