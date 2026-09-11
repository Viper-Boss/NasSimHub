package httpapi

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
)

type fakeWireGuardService struct {
	applied  []agentclient.WireGuardConfig
	reads    int
	status   agentclient.WireGuardStatus
	failWith error
}

func (f *fakeWireGuardService) ApplyWireGuard(
	_ context.Context,
	config agentclient.WireGuardConfig,
) (agentclient.WireGuardStatus, error) {
	f.applied = append(f.applied, config)
	if f.failWith != nil {
		return agentclient.WireGuardStatus{}, f.failWith
	}
	return f.status, nil
}

func (f *fakeWireGuardService) WireGuardStatusFor(
	_ context.Context,
	_ string,
	_ bool,
) (agentclient.WireGuardStatus, error) {
	f.reads++
	if f.failWith != nil {
		return agentclient.WireGuardStatus{}, f.failWith
	}
	return f.status, nil
}

func newWireGuardAPI(t *testing.T, service WireGuardService) *API {
	t.Helper()
	api, err := New(&fakeRepository{}, Options{
		disableAuthentication:  true,
		ContactBackupDirectory: filepath.Join(t.TempDir(), "backups", "contacts"),
		WireGuard:              service,
	})
	if err != nil {
		t.Fatal(err)
	}
	return api
}

// Reading status must not reconfigure the interface. Applying on every poll
// would rewrite the tunnel continuously.
func TestWireGuardStatusReadDoesNotApply(t *testing.T) {
	service := &fakeWireGuardService{status: agentclient.WireGuardStatus{State: agentclient.WireGuardStateUp}}
	api := newWireGuardAPI(t, service)

	status := api.wireGuardRuntimeStatus(context.Background(), wireGuardSettings{Enabled: true, Revision: 3}, false)

	if len(service.applied) != 0 {
		t.Fatalf("a status read applied configuration %d times", len(service.applied))
	}
	if service.reads != 1 {
		t.Fatalf("reads = %d; want 1", service.reads)
	}
	if status.State != "up" {
		t.Fatalf("state = %q", status.State)
	}
}

func TestWireGuardSaveAppliesSettingsToTheAgent(t *testing.T) {
	service := &fakeWireGuardService{status: agentclient.WireGuardStatus{State: agentclient.WireGuardStateUp, Revision: 4}}
	api := newWireGuardAPI(t, service)

	settings := wireGuardSettings{
		Enabled:    true,
		Address:    "10.77.0.1/24",
		ListenPort: 51820,
		Revision:   4,
		Peers: []wireGuardPeer{{
			Name:       "second-nas",
			PublicKey:  "peerkeyAAA=",
			AllowedIPs: []string{"10.77.0.2/32"},
		}},
	}
	api.wireGuardRuntimeStatus(context.Background(), settings, true)

	if len(service.applied) != 1 {
		t.Fatalf("applied %d times; want 1", len(service.applied))
	}
	applied := service.applied[0]
	if applied.Address != "10.77.0.1/24" || applied.ListenPort != 51820 || !applied.Enabled {
		t.Fatalf("configuration was not forwarded: %+v", applied)
	}
	if len(applied.Peers) != 1 || applied.Peers[0].PublicKey != "peerkeyAAA=" {
		t.Fatalf("peers were not forwarded: %+v", applied.Peers)
	}
	// A NAS behind NAT must keep its mapping alive or the peer can never reach
	// it again after the first idle period.
	if applied.Peers[0].PersistentKeepalive != wireGuardKeepaliveSeconds {
		t.Fatalf("keepalive = %d; want %d", applied.Peers[0].PersistentKeepalive, wireGuardKeepaliveSeconds)
	}
}

// Without an agent the endpoint must say the runtime is unavailable. Reporting
// a plausible-looking state instead is how this feature stayed fake for so
// long: the UI showed a tunnel page that nothing implemented.
func TestWireGuardWithoutAgentReportsUnavailable(t *testing.T) {
	api := newWireGuardAPI(t, nil)

	status := api.wireGuardRuntimeStatus(context.Background(), wireGuardSettings{Enabled: true}, false)

	if status.State != "unavailable" {
		t.Fatalf("state = %q; want unavailable", status.State)
	}
	if status.Error == "" {
		t.Fatal("an unavailable runtime must say why")
	}
}

func TestWireGuardAgentErrorIsSurfacedNotSwallowed(t *testing.T) {
	service := &fakeWireGuardService{failWith: errors.New("agent socket closed")}
	api := newWireGuardAPI(t, service)

	status := api.wireGuardRuntimeStatus(context.Background(), wireGuardSettings{Enabled: true}, false)

	if status.State != "unavailable" {
		t.Fatalf("state = %q", status.State)
	}
	if status.Error != "agent socket closed" {
		t.Fatalf("error = %q; the agent's reason must reach the operator", status.Error)
	}
}

// Peer statistics may be shown to the operator, so the conversion must carry
// the live figures across — and must not invent a handshake that never
// happened.
func TestWireGuardPeerStatusConversion(t *testing.T) {
	converted := convertWireGuardPeerStatus([]agentclient.WireGuardPeerStatus{{
		Name:            "second-nas",
		PublicKey:       "peerkeyAAA=",
		Endpoint:        "203.0.113.9:51820",
		LastHandshake:   1780000000,
		TransferRxBytes: 2048,
		TransferTxBytes: 4096,
		Current:         true,
	}, {
		PublicKey: "peerkeyBBB=",
	}})

	if len(converted) != 2 {
		t.Fatalf("converted = %+v", converted)
	}
	if converted[0].RxBytes != 2048 || converted[0].TxBytes != 4096 || !converted[0].Current {
		t.Fatalf("live figures lost: %+v", converted[0])
	}
	if converted[1].Current || converted[1].LastHandshake != 0 {
		t.Fatalf("a peer that never handshook must not read as current: %+v", converted[1])
	}
}
