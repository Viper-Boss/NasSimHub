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
	"time"
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
	if !settings.Enabled || settings.Revision != 0 {
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
	// Factory defaults enable the directory, but with no worker running and
	// no subscription the state machine reports an error, not "disabled".
	if state := manager.status().Runtime.State; state != "error" {
		t.Fatalf("state with factory defaults (enabled, no worker): %q", state)
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

func TestNKNSeedRPCListPutsChinaFirstWithOfficialFallback(t *testing.T) {
	list := nknSeedRPCList()
	if len(list) != len(nknCommunitySeedRPC)+len(nknOfficialSeedRPC) {
		t.Fatalf("seed list length: %d", len(list))
	}
	for i, seed := range nknCommunitySeedRPC {
		if list[i] != seed {
			t.Fatalf("community seed %d must stay in front: %s", i, list[i])
		}
	}
	offset := len(nknCommunitySeedRPC)
	for i, seed := range nknOfficialSeedRPC {
		if list[offset+i] != seed {
			t.Fatalf("official fallback %d missing or reordered: %s", i, list[offset+i])
		}
	}
}

// --- statistics windows ----------------------------------------------------

// Every window has to be answerable by the chain. NKN caps a subscription at
// 400000 blocks (config.MaxSubscribeDuration) and a block is 20s
// (config.ConsensusDuration), so a window longer than ~92 days cannot exist --
// and a window configured beyond the cap would have its subscribe transaction
// rejected, leaving a counter that silently never fills.
func TestNKNWindowsFitWithinTheProtocolLimits(t *testing.T) {
	const maxSubscribeDurationBlocks = 400000
	const blockSeconds = 20

	seen := map[string]bool{}
	for _, window := range nknNetworkWindows {
		if window.Blocks <= 0 || window.Blocks > maxSubscribeDurationBlocks {
			t.Fatalf("%s: duration %d blocks is outside 1..%d", window.Topic, window.Blocks, maxSubscribeDurationBlocks)
		}
		// One identity holds one subscription per topic, so two windows sharing
		// a topic would mean the shorter one repeatedly cutting the longer
		// one's expiry back and both counts becoming the shorter window.
		if seen[window.Topic] {
			t.Fatalf("two windows share the topic %q", window.Topic)
		}
		seen[window.Topic] = true

		covered := time.Duration(window.Blocks) * blockSeconds * time.Second

		// The floor, not the duration, is what the count actually guarantees.
		//
		// An instance that goes quiet keeps its subscription until its LAST
		// RENEWAL plus the duration, and that renewal was up to one renewal
		// interval before it went quiet. So a window advertised as 30 days and
		// built from a 30-day subscription renewed weekly guarantees only 23,
		// and under-reports the number it is named after -- which is exactly
		// the bug this assertion exists to catch.
		floor := covered - window.Renew
		if floor < window.Span {
			t.Fatalf(
				"%s is advertised as %s but only guarantees %s "+
					"(%s subscription renewed every %s)",
				window.Topic, window.Span, floor, covered, window.Renew,
			)
		}
		// One renewal has to be missable: the subscription must outlive the
		// gap to the next attempt, or a single failed renewal drops the
		// instance out of a window it was present for.
		if window.Renew*2 > covered {
			t.Fatalf(
				"%s renews every %s against a %s subscription, too close to expiry",
				window.Topic, window.Renew, covered,
			)
		}
	}
}

// The 24-hour topic keeps its original name. Renaming it would silently move
// every upgraded install into an empty directory while the old one still held
// everyone who had not upgraded.
func TestTwentyFourHourTopicNameIsUnchanged(t *testing.T) {
	if nknNetworkTopic != "modemdecknet" {
		t.Fatalf("the 24h topic was renamed to %q", nknNetworkTopic)
	}
	if nknNetworkWindows[0].Topic != nknNetworkTopic {
		t.Fatalf("the first window is %q, not the established topic", nknNetworkWindows[0].Topic)
	}
}

// A seed that does not answer must leave the count nil. Zero is a real answer
// meaning nobody is out there, and showing it for "the query failed" is a lie
// the reader cannot detect.
func TestUnreachableSeedLeavesCountsNilRatherThanZero(t *testing.T) {
	var result nknNetworkPeersResponse
	// Nothing listens on this port, so every window's query fails.
	applyNKNWindowCounts(&result, nknWindowCountsFrom([]string{"http://127.0.0.1:1"}))
	if result.Active24h != nil || result.ActiveMonth != nil || result.Active90d != nil {
		t.Fatalf("an unreachable seed produced counts: %+v", result)
	}
	if result.StatisticsScope != "onchain_topic_subscribers" {
		t.Fatalf("scope should still say where counts would come from, got %q", result.StatisticsScope)
	}
}
