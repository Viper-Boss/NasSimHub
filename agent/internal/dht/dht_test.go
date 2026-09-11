package dht

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func enabled() Config {
	// Port 0 lets the OS choose, so tests never collide with a real node.
	//
	// LAN discovery is off: these tests are about the host and what it exposes,
	// and starting a real mDNS responder would put multicast traffic on
	// whatever network the test machine is on. TestLANDiscoveryIsOnByDefault
	// covers the setting itself.
	return Config{Enabled: true, ListenPort: 0, DisableLANDiscovery: true}
}

// The PeerID must survive a restart. A node that regenerates it silently orphans
// itself from every peer that pinned the old one.
func TestPeerIDIsStableAcrossRestarts(t *testing.T) {
	directory := t.TempDir()

	first := New(directory)
	before, err := first.PeerID()
	if err != nil {
		t.Fatal(err)
	}
	if before == "" {
		t.Fatal("no PeerID was produced")
	}

	second := New(directory)
	after, err := second.PeerID()
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("PeerID changed across restarts: %q -> %q", before, after)
	}
}

// The identity is a private key: it must not be world-readable.
func TestIdentityFileIsOwnerOnly(t *testing.T) {
	directory := t.TempDir()
	service := New(directory)
	if _, err := service.PeerID(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(directory, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("identity mode = %o; want 600", mode)
	}
}

// A node with nobody to talk to is "isolated", not "ready". This is the case
// the handoff document is blunt about: a fresh install with no bootstrap source
// genuinely cannot join, and the status has to say so rather than imply a
// network that is not there.
func TestNodeWithNoPeersReportsIsolated(t *testing.T) {
	service := New(t.TempDir())
	t.Cleanup(service.Stop)

	status := service.Apply(context.Background(), enabled())

	if status.State != StateIsolated {
		t.Fatalf("state = %q; want isolated, error = %q", status.State, status.Error)
	}
	if status.PeerID == "" {
		t.Fatal("a running host must report its PeerID")
	}
	if len(status.Addresses) == 0 {
		t.Fatal("a running host must report the addresses it listens on")
	}
	if status.RoutingPeers != 0 || status.ConnectedPeers != 0 {
		t.Fatalf("counts invented from nothing: %+v", status)
	}
	if status.BootstrapConfigured {
		t.Fatal("no bootstrap peers were configured, so this must be false")
	}
}

// Without an entry point the status has to say so: an empty routing table with
// no explanation looks like a bug rather than the physical limit it is.
func TestBootstrapConfiguredReflectsTheSettings(t *testing.T) {
	service := New(t.TempDir())
	t.Cleanup(service.Stop)

	config := enabled()
	config.BootstrapPeers = []string{
		"/ip4/127.0.0.1/tcp/4001/p2p/12D3KooWGRUHnqbfMBTvRqjLzEMqsMfrJfrEsJvNTzBTdNVA1vqk",
	}
	status := service.Apply(context.Background(), config)

	if !status.BootstrapConfigured {
		t.Fatalf("bootstrap peers were configured but the status denies it: %+v", status)
	}
}

// Disabling must actually stop the host, not merely report it as off.
func TestDisablingStopsTheHost(t *testing.T) {
	service := New(t.TempDir())
	t.Cleanup(service.Stop)

	running := service.Apply(context.Background(), enabled())
	if running.PeerID == "" {
		t.Fatal("host did not start")
	}

	stopped := service.Apply(context.Background(), Config{Enabled: false})
	if stopped.State != StateDisabled {
		t.Fatalf("state = %q; want disabled", stopped.State)
	}
	if stopped.PeerID != "" || len(stopped.Addresses) != 0 {
		t.Fatalf("a stopped node still reports a live host: %+v", stopped)
	}

	service.mu.Lock()
	host := service.host
	service.mu.Unlock()
	if host != nil {
		t.Fatal("the libp2p host was left running after being disabled")
	}
}

// Applying twice must not tear the node down and rebuild it: the routing table
// takes time to fill, and dropping it on every settings read would keep the
// node permanently useless.
func TestApplyIsIdempotent(t *testing.T) {
	service := New(t.TempDir())
	t.Cleanup(service.Stop)

	first := service.Apply(context.Background(), enabled())
	second := service.Apply(context.Background(), enabled())

	if first.PeerID != second.PeerID {
		t.Fatal("the node was restarted by a second apply")
	}
	if len(first.Addresses) == 0 || first.Addresses[0] != second.Addresses[0] {
		t.Fatalf("listen addresses changed: %v -> %v", first.Addresses, second.Addresses)
	}
}

// Status must never carry the private key, since it is logged and returned
// from an API.
func TestStatusCarriesNoPrivateKey(t *testing.T) {
	directory := t.TempDir()
	service := New(directory)
	t.Cleanup(service.Stop)
	status := service.Apply(context.Background(), enabled())

	stored, err := os.ReadFile(filepath.Join(directory, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	rendered := status.PeerID + status.Error
	for _, address := range status.Addresses {
		rendered += address
	}
	if len(stored) > 0 && len(rendered) > 0 {
		for index := 0; index+8 <= len(stored); index++ {
			if contains(rendered, string(stored[index:index+8])) {
				t.Fatal("status contains bytes from the identity key")
			}
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		func() bool {
			for index := 0; index+len(needle) <= len(haystack); index++ {
				if haystack[index:index+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}

// --- containment -----------------------------------------------------------
//
// These tests exist because the failure they guard against is silent. A node
// that joins the public DHT, or answers a relay protocol, works perfectly from
// the operator's side while serving strangers in the background. Nothing in the
// UI would show it. So the wire surface is asserted directly, against the
// running host, rather than trusting the option list in startLocked.

// The node must not be on the public IPFS DHT. In server mode the library
// default would have this NAS storing and answering records for the whole
// world.
func TestDHTSpeaksOnlyOurOwnProtocol(t *testing.T) {
	service := New(t.TempDir())
	t.Cleanup(func() { service.Apply(context.Background(), Config{}) })
	if status := service.Apply(context.Background(), enabled()); status.State == StateError {
		t.Fatalf("host did not start: %s", status.Error)
	}

	service.mu.Lock()
	host := service.host
	service.mu.Unlock()
	if host == nil {
		t.Fatal("no host")
	}

	var kademlia []string
	for _, id := range host.Mux().Protocols() {
		name := string(id)
		if strings.Contains(name, "/kad/") {
			kademlia = append(kademlia, name)
		}
		// The public DHT prefix must appear nowhere.
		if strings.HasPrefix(name, "/ipfs/kad/") {
			t.Fatalf("host answers the public IPFS DHT protocol %q", name)
		}
	}
	if len(kademlia) == 0 {
		t.Fatal("host answers no Kademlia protocol at all")
	}
	for _, name := range kademlia {
		if !strings.HasPrefix(name, ProtocolPrefix+"/kad/") {
			t.Fatalf("Kademlia protocol %q is outside our prefix", name)
		}
	}
}

// Nothing beyond discovery may be on the wire. A relay or proxy protocol here
// is how this NAS would end up carrying someone else's traffic.
func TestHostOffersNoProtocolBeyondTheAllowlist(t *testing.T) {
	service := New(t.TempDir())
	t.Cleanup(func() { service.Apply(context.Background(), Config{}) })
	if status := service.Apply(context.Background(), enabled()); status.State == StateError {
		t.Fatalf("host did not start: %s", status.Error)
	}

	service.mu.Lock()
	host := service.host
	service.mu.Unlock()

	allowed := make(map[string]struct{}, len(allowedProtocols))
	for _, name := range allowedProtocols {
		allowed[name] = struct{}{}
	}
	for _, id := range host.Mux().Protocols() {
		if _, ok := allowed[string(id)]; !ok {
			t.Fatalf("host answers unexpected protocol %q", id)
		}
	}

	// Named explicitly so the intent survives a refactor of the list above:
	// these are the ones that would make this node useful to a stranger.
	for _, forbidden := range []string{
		"/libp2p/circuit/relay/0.2.0/hop",
		"/libp2p/circuit/relay/0.2.0/stop",
		"/libp2p/dcutr",
		"/libp2p/autonat/1.0.0",
	} {
		for _, id := range host.Mux().Protocols() {
			if string(id) == forbidden {
				t.Fatalf("host answers %q, which lets others route through it", forbidden)
			}
		}
	}
}

// An empty allowlist has to accept peers, or a fresh install could never be
// joined by its own partner. One entry makes it strict.
func TestPeerGateIsOpenUntilItIsGivenAList(t *testing.T) {
	open := newPeerGate(nil, nil)
	stranger, err := peer.Decode("12D3KooWGRUEWFuHGsH1xjmnJLvS2rXcQfvnYmVpvsjRFJnGfbRs")
	if err != nil {
		t.Fatal(err)
	}
	if !open.permits(stranger) {
		t.Fatal("an unconfigured node refused a peer, so nobody could ever reach it")
	}

	strict := newPeerGate(nil, []string{"12D3KooWH3uVF6wv47WnArKHk5p6cvgCJEb74UTmxztmQDc298L3"})
	if strict.permits(stranger) {
		t.Fatal("a peer outside the allowlist was permitted")
	}
	known, err := peer.Decode("12D3KooWH3uVF6wv47WnArKHk5p6cvgCJEb74UTmxztmQDc298L3")
	if err != nil {
		t.Fatal(err)
	}
	if !strict.permits(known) {
		t.Fatal("a listed peer was refused")
	}
	// Refusal has to happen once the identity is known, which is
	// InterceptSecured -- InterceptAccept runs before there is an identity.
	if strict.InterceptSecured(0, stranger, nil) {
		t.Fatal("an unauthorized peer survived the secured check")
	}
}

// Configuring a bootstrap peer is itself an authorization: dialling a node and
// then refusing its answer would simply never connect.
func TestBootstrapPeersAreAuthorizedByBeingConfigured(t *testing.T) {
	const id = "12D3KooWH3uVF6wv47WnArKHk5p6cvgCJEb74UTmxztmQDc298L3"
	gate := newPeerGate([]string{"/ip4/203.0.113.9/tcp/4001/p2p/" + id}, nil)
	parsed, err := peer.Decode(id)
	if err != nil {
		t.Fatal(err)
	}
	if !gate.permits(parsed) {
		t.Fatal("the configured bootstrap peer was refused by the gate")
	}
	other, err := peer.Decode("12D3KooWGRUEWFuHGsH1xjmnJLvS2rXcQfvnYmVpvsjRFJnGfbRs")
	if err != nil {
		t.Fatal(err)
	}
	if gate.permits(other) {
		t.Fatal("a bootstrap list must not authorize peers it does not name")
	}
}

// LAN discovery is on unless explicitly switched off. It is the only bootstrap
// source that needs no configuration at all, so it is what stops two fresh
// installs on one network from both sitting in "isolated" forever.
func TestLANDiscoveryIsOnByDefault(t *testing.T) {
	var zero Config
	if zero.DisableLANDiscovery {
		t.Fatal("LAN discovery must default to on")
	}
}

// Switching the DHT off must not block, even where mDNS cannot run.
//
// The library's Close waits on its resolver goroutine, and that goroutine does
// not return on a host with no usable multicast interface -- every container
// without IPv6 multicast, which is where this actually runs. Waiting for it
// under the service mutex hung shutdown for as long as the process lived.
func TestStoppingDoesNotBlockOnLANDiscovery(t *testing.T) {
	service := New(t.TempDir())
	// Start with discovery on, which is what production does.
	status := service.Apply(context.Background(), Config{Enabled: true, ListenPort: 0})
	if status.State == StateError {
		t.Skipf("host could not start here: %s", status.Error)
	}

	done := make(chan Status, 1)
	go func() { done <- service.Apply(context.Background(), Config{}) }()
	select {
	case stopped := <-done:
		if stopped.State != StateDisabled {
			t.Fatalf("expected disabled, got %q", stopped.State)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("switching the DHT off blocked; mDNS Close is being waited on")
	}
}
