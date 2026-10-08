package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
)

// A host reboot must not leave an enabled tunnel down.
//
// Before StartHomeSIMTunnelRestore existed, the apply path was reachable only
// from a PUT on the settings endpoint, so a configured and enabled interface
// stayed down until somebody opened the settings page and saved it again. A
// status poll would not revive it either, because the GET path reports state
// without applying -- by design, and correctly. What was missing was anyone
// calling apply at startup.

// The restore runs on its own goroutine, so ApplyDHT is called concurrently
// with the test goroutine polling for the result. mu guards the two recorded
// fields; tests read them through appliedConfigs(), never directly. Reading
// the slice unguarded was a data race in this fake (found by -race in r128),
// not in the code under test.
type fakeDHTService struct {
	mu      sync.Mutex
	applied []agentclient.DHTConfig
	reads   int
	status  agentclient.DHTStatus
	err     error
}

// appliedConfigs returns a copy, so a caller can index it while the restore
// goroutine keeps running.
func (f *fakeDHTService) appliedConfigs() []agentclient.DHTConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agentclient.DHTConfig(nil), f.applied...)
}

func (f *fakeDHTService) ApplyDHT(
	_ context.Context,
	config agentclient.DHTConfig,
) (agentclient.DHTStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, config)
	if f.err != nil {
		return agentclient.DHTStatus{}, f.err
	}
	return f.status, nil
}

// DialDHT is part of the interface but plays no part in a restore: the
// restore applies stored settings, it does not reach out to peers.
func (f *fakeDHTService) DialDHT(_ context.Context, _ []string) (int, error) {
	return 0, nil
}

func (f *fakeDHTService) DHTStatus(_ context.Context, _ bool) (agentclient.DHTStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.err != nil {
		return agentclient.DHTStatus{}, f.err
	}
	return f.status, nil
}

// tunnelAPI builds an API with real on-disk settings storage, because the bug
// was in the path from stored settings to an apply call and a test that stubbed
// the storage would have skipped exactly the part that was broken.
func tunnelAPI(
	t *testing.T,
	wireGuard WireGuardService,
	dht DHTService,
) *API {
	t.Helper()
	api, err := New(&fakeRepository{}, Options{
		disableAuthentication:     true,
		disableLicenseEnforcement: true,
		ContactBackupDirectory:    filepath.Join(t.TempDir(), "backups", "contacts"),
		WireGuard:                 wireGuard,
		DHT:                       dht,
	})
	if err != nil {
		t.Fatal(err)
	}
	return api
}

func writeSettings(t *testing.T, api *API, service string, payload any) {
	t.Helper()
	root, ok := api.homeSIMDataDirectory()
	if !ok {
		t.Fatal("server storage is not configured; the restore cannot be tested")
	}
	directory := filepath.Join(root, service)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "settings.json"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitForApply gives the restore goroutine its startup delay plus slack.
func waitForApply(t *testing.T, count func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(tunnelRestoreDelay + 5*time.Second)
	for time.Now().Before(deadline) {
		if count() >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("applied %d times after waiting; want %d", count(), want)
}

func TestEnabledWireGuardComesBackUpAfterARestart(t *testing.T) {
	wireGuard := &fakeWireGuardService{
		status: agentclient.WireGuardStatus{State: agentclient.WireGuardStateUp},
	}
	api := tunnelAPI(t, wireGuard, nil)
	writeSettings(t, api, "wireguard", map[string]any{
		"revision":    7,
		"enabled":     true,
		"address":     "10.77.0.1/24",
		"listen_port": 51820,
		"peers":       []any{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.StartHomeSIMTunnelRestore(ctx)

	waitForApply(t, func() int { return len(wireGuard.appliedConfigs()) }, 1)
	applied := wireGuard.appliedConfigs()[0]
	if !applied.Enabled {
		t.Fatal("the restore applied a disabled configuration")
	}
	// The stored revision has to survive untouched, or anything watching
	// revisions sees a restart as an edit.
	if applied.Revision != 7 {
		t.Fatalf("revision = %d; the restore rewrote it", applied.Revision)
	}
}

func TestDisabledTunnelsAreLeftAloneOnRestart(t *testing.T) {
	// An operator who turned WireGuard off must not find it back on after a
	// reboot. The stored flag is the instruction, in both directions.
	wireGuard := &fakeWireGuardService{
		status: agentclient.WireGuardStatus{State: agentclient.WireGuardStateDown},
	}
	dht := &fakeDHTService{status: agentclient.DHTStatus{State: "disabled"}}
	api := tunnelAPI(t, wireGuard, dht)
	writeSettings(t, api, "wireguard", map[string]any{
		"revision": 2, "enabled": false, "address": "10.77.0.1/24",
		"listen_port": 51820, "peers": []any{},
	})
	writeSettings(t, api, "dht", map[string]any{
		"revision": 2, "enabled": false, "listen_port": 4001,
		"bootstrap_peers": []any{}, "authorized_peers": []any{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.StartHomeSIMTunnelRestore(ctx)

	time.Sleep(tunnelRestoreDelay + 500*time.Millisecond)
	if len(wireGuard.appliedConfigs()) != 0 {
		t.Fatalf("a disabled tunnel was brought up: %+v", wireGuard.appliedConfigs())
	}
	if len(dht.appliedConfigs()) != 0 {
		t.Fatalf("a disabled DHT was started: %+v", dht.appliedConfigs())
	}
}

func TestEnabledDHTComesBackUpAfterARestart(t *testing.T) {
	// The DHT is meant to be on by default. On by default that does not
	// survive a reboot is not on by default, which is why this has its own
	// test rather than riding on the WireGuard one.
	dht := &fakeDHTService{status: agentclient.DHTStatus{State: "ready", RoutingPeers: 3}}
	api := tunnelAPI(t, nil, dht)
	writeSettings(t, api, "dht", map[string]any{
		"revision": 4, "enabled": true, "listen_port": 4001,
		"bootstrap_peers": []any{}, "authorized_peers": []any{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.StartHomeSIMTunnelRestore(ctx)

	waitForApply(t, func() int { return len(dht.appliedConfigs()) }, 1)
	if !dht.appliedConfigs()[0].Enabled {
		t.Fatal("the restore started the DHT with enabled=false")
	}
}

func TestAnIsolatedDHTIsNotRestartedInALoop(t *testing.T) {
	// "isolated" means the host is up and has found nobody, which is the most
	// a DHT can promise on a quiet network. Treating it as a failure would
	// restart a perfectly healthy node every ten seconds.
	dht := &fakeDHTService{status: agentclient.DHTStatus{State: "isolated"}}
	api := tunnelAPI(t, nil, dht)
	writeSettings(t, api, "dht", map[string]any{
		"revision": 1, "enabled": true, "listen_port": 4001,
		"bootstrap_peers": []any{}, "authorized_peers": []any{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.StartHomeSIMTunnelRestore(ctx)

	waitForApply(t, func() int { return len(dht.appliedConfigs()) }, 1)
	time.Sleep(600 * time.Millisecond)
	if len(dht.appliedConfigs()) != 1 {
		t.Fatalf("an isolated DHT was applied %d times; it should settle after one", len(dht.appliedConfigs()))
	}
}

func TestAMissingAgentCapabilityIsNotRetriedForever(t *testing.T) {
	// A build whose agent does not offer the capability has nothing to retry.
	// Retrying would fill the log with an error the operator cannot act on.
	api := tunnelAPI(t, nil, nil)
	writeSettings(t, api, "wireguard", map[string]any{
		"revision": 1, "enabled": true, "address": "10.77.0.1/24",
		"listen_port": 51820, "peers": []any{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		api.restore(ctx, "wireguard", func(ctx context.Context) bool {
			settings, err := api.loadWireGuardSettings()
			if err != nil || !settings.Enabled {
				return true
			}
			return api.wireGuardService == nil
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the restore retried a capability the agent does not have")
	}
}
