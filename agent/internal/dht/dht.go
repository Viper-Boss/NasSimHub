// Package dht runs this gateway's libp2p host and its Kademlia participation.
//
// It is the second, independent control path beside NKN: a way for two
// instances to find each other when NKN is unavailable or blocked. It carries
// no user data — no messages, no call audio, no WireGuard keys. Discovery and
// routing only.
//
// Honesty rules, same as the WireGuard manager:
//
//   - The identity key is generated here, stored 0600, and reused across
//     restarts. A new PeerID on every boot would make every peer that pinned
//     this one unable to find it again.
//   - A node with no peers says so. There is no synthetic "connected" state:
//     a fresh install with no bootstrap source and no cached peers genuinely
//     cannot join, and the status has to show that rather than pretend.
package dht

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	kaddht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/control"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	mdns "github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/multiformats/go-multiaddr"
)

// State is what this node is actually doing.
type State string

const (
	// StateDisabled means the feature is switched off in settings.
	StateDisabled State = "disabled"
	// StateStarting means the host is up but the routing table is still empty.
	StateStarting State = "starting"
	// StateIsolated means the host is running but has found nobody. A fresh
	// install with no bootstrap peers and no cache is genuinely alone, and the
	// status must say so instead of implying a network that is not there.
	StateIsolated State = "isolated"
	// StateReady means the routing table has peers and lookups can run.
	StateReady State = "ready"
	// StateError means the host could not start; Error says why.
	StateError State = "error"
)

// Status is safe to log and to return from an API: it carries the public
// PeerID but never the private key.
type Status struct {
	State State `json:"state"`
	// PeerID is this node's stable public identity.
	PeerID string `json:"peer_id,omitempty"`
	// Addresses are the multiaddrs this host listens on.
	Addresses []string `json:"addresses,omitempty"`
	// ConnectedPeers is how many peers this host holds an open connection to.
	ConnectedPeers int `json:"connected_peers"`
	// RoutingPeers is the Kademlia routing table size — the number this node
	// can actually route lookups through.
	RoutingPeers int `json:"routing_peers"`
	// BootstrapConfigured reports whether any entry point was supplied at all.
	// Without one, and with no cached peers, joining is impossible; saying so
	// is more useful than reporting an empty table with no explanation.
	BootstrapConfigured bool `json:"bootstrap_configured"`
	// LANDiscovery reports whether mDNS is running. It is the one bootstrap
	// source that needs no configuration, so whether it is up explains a lot
	// about why a node did or did not find anybody.
	LANDiscovery bool `json:"lan_discovery"`
	// LANPeers counts peers found on the local network and successfully
	// dialled this run.
	LANPeers  int       `json:"lan_peers"`
	Error     string    `json:"error,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Config is what the API sends down.
type Config struct {
	Enabled bool `json:"enabled"`
	// ListenPort 0 lets the OS choose.
	ListenPort int `json:"listen_port"`
	// BootstrapPeers are multiaddrs including the /p2p/<id> component.
	BootstrapPeers []string `json:"bootstrap_peers"`
	// AuthorizedPeers are PeerIDs allowed to connect, on top of whatever the
	// bootstrap list names. Empty means "anyone who speaks our protocol",
	// which is what a freshly installed node needs so its partner can reach it
	// at all; once any entry is present the gate is strict.
	AuthorizedPeers []string `json:"authorized_peers"`
	// DisableLANDiscovery turns off mDNS. It is on by default because it is
	// the only bootstrap source that needs no configuration at all: two
	// instances on one LAN find each other within seconds of both being up.
	DisableLANDiscovery bool `json:"disable_lan_discovery"`
}

// ProtocolPrefix scopes every DHT protocol this node speaks.
//
// This is the single most important control in this package. With the library
// default the node joins the PUBLIC IPFS DHT, and in server mode it then stores
// and answers records for the entire world — a NAS in someone's flat quietly
// becoming public infrastructure. Under a prefix of our own it only ever talks
// to peers running this project, so server mode serves our own network and
// nobody else's.
//
// The value comes from the integration handoff, section 12: /smsbridge/kad/1.0.0.
const ProtocolPrefix = "/smsbridge"

// allowedProtocols is everything this host is permitted to answer.
//
// The handoff is explicit about scope (section 12): the DHT does peer
// discovery, peer routing and FindPeer. It does not store messages and it is
// not a transport for other people's traffic. Anything the libp2p stack
// registers beyond this list is torn down at startup, so a future dependency
// bump cannot quietly add a service this node then offers to strangers.
var allowedProtocols = []string{
	"/ipfs/id/1.0.0",      // identify: required for any libp2p connection
	"/ipfs/id/push/1.0.0", // identify push
	"/ipfs/ping/1.0.0",    // liveness only
	ProtocolPrefix + "/kad/1.0.0",
}

// Service owns the host for the life of the process.
type Service struct {
	mu        sync.Mutex
	stateDir  string
	host      host.Host
	kad       *kaddht.IpfsDHT
	cancel    context.CancelFunc
	lastError string
	// bootstrapConfigured records whether the running host was given any entry
	// point, so the status can explain an empty routing table.
	bootstrapConfigured bool
	// mdns is the LAN discovery service, nil when it is switched off.
	mdns io.Closer
	// lanFound counts peers mDNS has handed us, so the status can say whether
	// LAN discovery is doing anything rather than leaving it a mystery.
	lanFound int
}

func New(stateDir string) *Service {
	return &Service{stateDir: stateDir}
}

func (s *Service) identityPath() string {
	return filepath.Join(s.stateDir, "identity.key")
}

// loadOrCreateIdentity keeps the PeerID stable across restarts. Regenerating it
// would silently orphan this node from everyone who pinned it.
func (s *Service) loadOrCreateIdentity() (crypto.PrivKey, error) {
	if err := os.MkdirAll(s.stateDir, 0o700); err != nil {
		return nil, err
	}
	path := s.identityPath()
	raw, err := os.ReadFile(path)
	if err == nil && len(raw) > 0 {
		key, err := crypto.UnmarshalPrivateKey(raw)
		if err != nil {
			return nil, fmt.Errorf("stored libp2p identity is unreadable: %w", err)
		}
		return key, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	key, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, err
	}
	encoded, err := crypto.MarshalPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// PeerID returns this node's identity without starting anything, generating it
// on first use so the address can be shown (and put in a pairing QR) before the
// node is ever switched on.
func (s *Service) PeerID() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host != nil {
		return s.host.ID().String(), nil
	}
	key, err := s.loadOrCreateIdentity()
	if err != nil {
		return "", err
	}
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// Apply brings the node into line with cfg and reports what happened.
func (s *Service) Apply(ctx context.Context, cfg Config) Status {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !cfg.Enabled {
		s.stopLocked()
		return Status{State: StateDisabled, UpdatedAt: time.Now().UTC()}
	}
	if s.host != nil {
		// Already running. Restarting on every settings read would drop the
		// routing table that took time to build.
		return s.statusLocked()
	}
	if err := s.startLocked(ctx, cfg); err != nil {
		s.lastError = err.Error()
		return Status{State: StateError, Error: err.Error(), UpdatedAt: time.Now().UTC()}
	}
	return s.statusLocked()
}

func (s *Service) startLocked(ctx context.Context, cfg Config) error {
	key, err := s.loadOrCreateIdentity()
	if err != nil {
		return err
	}
	listen := fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", cfg.ListenPort)
	gate := newPeerGate(cfg.BootstrapPeers, cfg.AuthorizedPeers)
	created, err := libp2p.New(
		libp2p.Identity(key),
		libp2p.ListenAddrStrings(listen),
		// Only authorized peers get as far as a usable connection.
		libp2p.ConnectionGater(gate),
		// This node must never carry anyone else's bytes. DisableRelay turns
		// off the circuit-relay transport entirely; EnableRelayService, which
		// is what would make this host a hop for strangers, is never called.
		//
		// Both are deliberate and neither is an oversight: handoff section 20
		// does want AutoRelay and DCUtR for NAT traversal, but that belongs
		// with the candidate exchange in phase 2 and buys nothing today, while
		// section 28 is unambiguous that a relay must never become a general
		// proxy. Strict now, relaxed on purpose later — and only the client
		// side, never the service.
		libp2p.DisableRelay(),
		// Do not advertise addresses other peers observe for us; the endpoint
		// this node is reachable at is exchanged deliberately, not gossiped.
		libp2p.DisableIdentifyAddressDiscovery(),
	)
	if err != nil {
		return fmt.Errorf("start libp2p host: %w", err)
	}
	kad, err := kaddht.New(
		created,
		kaddht.Mode(kaddht.ModeServer),
		// Server mode is only safe because of this line: it scopes every DHT
		// protocol to our own network instead of the public IPFS one.
		kaddht.ProtocolPrefix(ProtocolPrefix),
	)
	if err != nil {
		_ = created.Close()
		return fmt.Errorf("start Kademlia: %w", err)
	}
	// Belt and braces over the option list above: whatever the stack actually
	// registered, only the allowlist survives.
	removed := pruneStreamHandlers(created)
	if len(removed) > 0 {
		slog.Warn(
			"removed libp2p protocols this node does not offer",
			"component", "dht",
			"protocols", strings.Join(removed, ","),
		)
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.host = created
	s.kad = kad
	s.cancel = cancel
	s.lastError = ""
	s.bootstrapConfigured = len(cfg.BootstrapPeers) > 0

	if !cfg.DisableLANDiscovery {
		s.startLANDiscoveryLocked(runCtx)
	}

	if err := kad.Bootstrap(runCtx); err != nil {
		// Not fatal: the table simply stays empty, which the status reports
		// honestly rather than hiding.
		s.lastError = fmt.Sprintf("bootstrap: %v", err)
	}
	go s.dialBootstrap(runCtx, cfg.BootstrapPeers)
	return nil
}

// Dial connects to peers discovered out of band, typically addresses a paired
// instance sent over NKN.
//
// It exists because Apply deliberately does nothing when the host is already
// running -- restarting to pick up one new address would throw away a routing
// table that took time to build. A late-arriving peer therefore needs a way in
// that is not a restart.
//
// Dialling is not authorizing: every connection still passes the gate built at
// startup, so a strict allowlist refuses these exactly as it refuses any other
// peer.
func (s *Service) Dial(ctx context.Context, addresses []string) (int, error) {
	s.mu.Lock()
	current := s.host
	s.mu.Unlock()
	if current == nil {
		return 0, fmt.Errorf("DHT is not running")
	}
	dialled := 0
	var lastErr error
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		maddr, err := multiaddr.NewMultiaddr(address)
		if err != nil {
			lastErr = err
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			lastErr = err
			continue
		}
		if info.ID == current.ID() {
			// Our own address, echoed back by a peer that learned it from us.
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err = s.dial(dialCtx, *info)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		dialled++
	}
	if dialled == 0 && lastErr != nil {
		return 0, lastErr
	}
	return dialled, nil
}

// dialBootstrap connects to the configured entry points. Failures are recorded
// but never fatal: one unreachable bootstrap peer must not stop the node.
func (s *Service) dialBootstrap(ctx context.Context, addresses []string) {
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		maddr, err := multiaddr.NewMultiaddr(address)
		if err != nil {
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		_ = s.dial(dialCtx, *info)
		cancel()
	}
}

func (s *Service) dial(ctx context.Context, info peer.AddrInfo) error {
	s.mu.Lock()
	current := s.host
	s.mu.Unlock()
	if current == nil {
		return fmt.Errorf("host is not running")
	}
	return current.Connect(ctx, info)
}

// Status reports the live node without changing it.
func (s *Service) Status(cfg Config) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !cfg.Enabled && s.host == nil {
		return Status{State: StateDisabled, UpdatedAt: time.Now().UTC()}
	}
	return s.statusLocked()
}

func (s *Service) statusLocked() Status {
	now := time.Now().UTC()
	if s.host == nil {
		state := StateDisabled
		if s.lastError != "" {
			state = StateError
		}
		return Status{State: state, Error: s.lastError, UpdatedAt: now}
	}
	addresses := make([]string, 0, len(s.host.Addrs()))
	for _, addr := range s.host.Addrs() {
		addresses = append(addresses, addr.String())
	}
	sort.Strings(addresses)

	connected := 0
	for _, id := range s.host.Network().Peers() {
		if s.host.Network().Connectedness(id) == network.Connected {
			connected++
		}
	}
	routing := 0
	if s.kad != nil {
		routing = s.kad.RoutingTable().Size()
	}

	// An empty routing table is not "ready" however long the host has been up.
	state := StateReady
	switch {
	case routing == 0 && connected == 0:
		state = StateIsolated
	case routing == 0:
		state = StateStarting
	}

	return Status{
		State:               state,
		PeerID:              s.host.ID().String(),
		Addresses:           addresses,
		ConnectedPeers:      connected,
		RoutingPeers:        routing,
		BootstrapConfigured: s.bootstrapConfigured,
		LANDiscovery:        s.mdns != nil,
		LANPeers:            s.lanFound,
		Error:               s.lastError,
		UpdatedAt:           now,
	}
}

// Stop shuts the node down. Safe to call when it is not running.
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

func (s *Service) stopLocked() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.mdns != nil {
		// Closed off the mutex and without waiting. The library's Close waits
		// on its resolver goroutine, and that goroutine does not return on a
		// host with no usable multicast interface -- which is every container
		// without IPv6 multicast. Waiting for it here would hang whoever
		// switched the DHT off, and hang shutdown with it.
		closer := s.mdns
		go func() { _ = closer.Close() }()
		s.mdns = nil
	}
	s.lanFound = 0
	if s.kad != nil {
		_ = s.kad.Close()
		s.kad = nil
	}
	if s.host != nil {
		_ = s.host.Close()
		s.host = nil
	}
	s.bootstrapConfigured = false
}

// pruneStreamHandlers removes every registered protocol that is not on the
// allowlist and reports what it removed.
//
// The option list in startLocked says what this node should offer. This says
// what it actually offers, which is not the same statement: a dependency bump
// can register a new service without anyone editing this file, and the first
// sign would be a stranger using it. Reading the mux back is the only way to
// know.
func pruneStreamHandlers(h host.Host) []string {
	allowed := make(map[string]struct{}, len(allowedProtocols))
	for _, name := range allowedProtocols {
		allowed[name] = struct{}{}
	}
	var removed []string
	for _, id := range h.Mux().Protocols() {
		name := string(id)
		if _, ok := allowed[name]; ok {
			continue
		}
		h.RemoveStreamHandler(id)
		removed = append(removed, name)
	}
	sort.Strings(removed)
	return removed
}

// peerGate decides who may connect.
//
// An empty allowlist accepts any peer that speaks our protocol prefix. That is
// deliberate: a freshly installed node has nobody on its list yet, and a gate
// that refused everyone would make it impossible for its own partner to reach
// it. Obscurity is not authorization, which is why the list exists at all — as
// soon as it has one entry the gate is strict and everyone else is refused
// before the connection is usable.
type peerGate struct {
	allowed map[peer.ID]struct{}
}

func newPeerGate(bootstrap []string, authorized []string) *peerGate {
	gate := &peerGate{allowed: make(map[peer.ID]struct{})}
	// A bootstrap peer is one we dial on purpose, so it is authorized by the
	// act of being configured. Requiring it to be listed twice would be a
	// footgun with no security benefit.
	for _, address := range bootstrap {
		maddr, err := multiaddr.NewMultiaddr(strings.TrimSpace(address))
		if err != nil {
			continue
		}
		if info, err := peer.AddrInfoFromP2pAddr(maddr); err == nil {
			gate.allowed[info.ID] = struct{}{}
		}
	}
	for _, id := range authorized {
		parsed, err := peer.Decode(strings.TrimSpace(id))
		if err != nil {
			continue
		}
		gate.allowed[parsed] = struct{}{}
	}
	return gate
}

// permits reports whether a peer may talk to this node.
func (g *peerGate) permits(id peer.ID) bool {
	if len(g.allowed) == 0 {
		return true
	}
	_, ok := g.allowed[id]
	return ok
}

func (g *peerGate) InterceptPeerDial(id peer.ID) bool { return g.permits(id) }

func (g *peerGate) InterceptAddrDial(id peer.ID, _ multiaddr.Multiaddr) bool {
	return g.permits(id)
}

// InterceptAccept runs before the remote identity is known, so it cannot
// consult the allowlist. InterceptSecured below is where an unauthorized peer
// is actually refused.
func (g *peerGate) InterceptAccept(_ network.ConnMultiaddrs) bool { return true }

func (g *peerGate) InterceptSecured(_ network.Direction, id peer.ID, _ network.ConnMultiaddrs) bool {
	return g.permits(id)
}

func (g *peerGate) InterceptUpgraded(network.Conn) (bool, control.DisconnectReason) {
	return true, 0
}

// --- LAN discovery ----------------------------------------------------------

// lanServiceName scopes the mDNS announcement to this project.
//
// The library's default service name is shared by every libp2p application, so
// using it would mean announcing ourselves to, and being pestered by, unrelated
// software on the same network. Our own name keeps the broadcast to instances
// that could actually be peers.
const lanServiceName = "nassimhub-smsbridge"

// startLANDiscoveryLocked announces this node on the local network and dials
// what it finds.
//
// This is the one bootstrap source that needs no configuration whatsoever
// (handoff section 14, source D). Everything else requires somebody to have
// obtained an address first: a bootstrap multiaddr typed in by hand, a paired
// QR, a cached peer from a previous run. Two fresh installs on one LAN have
// none of those and would otherwise both sit in "isolated" forever, which is
// exactly the state a pair of NAS boxes in the same flat start in.
//
// What it broadcasts is this node's PeerID and its LAN addresses, on the local
// segment only. It is not a way onto the internet and it does not widen who may
// connect: a peer found here goes through the same connection gate as one dialled
// from the bootstrap list, so a strict allowlist still refuses it.
func (s *Service) startLANDiscoveryLocked(ctx context.Context) {
	service := mdns.NewMdnsService(s.host, lanServiceName, &lanNotifee{service: s, ctx: ctx})
	if err := service.Start(); err != nil {
		// Not fatal. A container without multicast, or a host where port 5353
		// is taken, simply has no LAN discovery; every other bootstrap source
		// still works and the status already reports an empty routing table
		// honestly.
		slog.Warn(
			"LAN discovery unavailable; other bootstrap sources are unaffected",
			"component", "dht",
			"err", err,
		)
		return
	}
	s.mdns = service
}

// lanNotifee dials peers mDNS announces.
type lanNotifee struct {
	service *Service
	ctx     context.Context
}

func (n *lanNotifee) HandlePeerFound(info peer.AddrInfo) {
	if info.ID == "" {
		return
	}
	n.service.mu.Lock()
	current := n.service.host
	if current != nil && info.ID == current.ID() {
		// Our own announcement comes back to us on the same segment.
		n.service.mu.Unlock()
		return
	}
	n.service.mu.Unlock()
	if current == nil {
		return
	}

	go func() {
		dialCtx, cancel := context.WithTimeout(n.ctx, 15*time.Second)
		defer cancel()
		if err := n.service.dial(dialCtx, info); err != nil {
			// An announcement is not a promise: the peer may be on a subnet we
			// cannot reach, or refused by the gate. Either is ordinary.
			return
		}
		n.service.mu.Lock()
		n.service.lanFound++
		n.service.mu.Unlock()
		slog.Info(
			"found a peer on the local network",
			"component", "dht",
			"peer", info.ID.String(),
		)
	}()
}
