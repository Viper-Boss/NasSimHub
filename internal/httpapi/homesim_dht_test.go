package httpapi

import (
	"strings"
	"testing"
)

// The DHT ships switched on, like NKN beside it: a redundant control path that
// arrives switched off is not redundancy, and handoff section 13 has every
// install join.
//
// This is only defensible because of what the node is confined to, which the
// dht package enforces and its own tests assert. If that confinement is ever
// relaxed, this default has to be revisited in the same change.
func TestDHTIsOnByDefault(t *testing.T) {
	api := &API{}
	// No storage configured, so this exercises the in-code default rather than
	// anything previously written to disk.
	settings, _ := api.loadDHTSettings()
	if !settings.Enabled {
		t.Fatal("the DHT must be enabled by default")
	}
	if settings.ListenPort != dhtDefaultListenPort {
		t.Fatalf("unexpected default port %d", settings.ListenPort)
	}
	// Both lists must be empty rather than nil: a nil slice serializes as null
	// and the settings form would have nothing to bind to.
	if settings.BootstrapPeers == nil || settings.AuthorizedPeers == nil {
		t.Fatal("peer lists must default to empty slices, not nil")
	}
}

// The allowlist is an identity list. A multiaddr here would look like it
// authorized something while authorizing nobody.
func TestAuthorizedPeersMustBePeerIDs(t *testing.T) {
	base := dhtSettings{Revision: 1, Enabled: true, ListenPort: dhtDefaultListenPort}

	withAddress := base
	withAddress.AuthorizedPeers = []string{"/ip4/203.0.113.9/tcp/4001/p2p/12D3KooWabc"}
	err := validDHTSettings(withAddress)
	if err == nil {
		t.Fatal("a multiaddr was accepted as an authorized peer")
	}
	if !strings.Contains(err.Error(), "PeerID") {
		t.Fatalf("the error should say what to put there instead, got %q", err)
	}

	withID := base
	withID.AuthorizedPeers = []string{"12D3KooWH3uVF6wv47WnArKHk5p6cvgCJEb74UTmxztmQDc298L3"}
	if err := validDHTSettings(withID); err != nil {
		t.Fatalf("a PeerID was refused: %v", err)
	}

	tooMany := base
	for range dhtMaxAuthorizedPeers + 1 {
		tooMany.AuthorizedPeers = append(tooMany.AuthorizedPeers, "12D3KooWH3uVF6wv47WnArKHk5p6cvgCJEb74UTmxztmQDc298L3")
	}
	if err := validDHTSettings(tooMany); err == nil {
		t.Fatal("an unbounded allowlist was accepted")
	}
}
