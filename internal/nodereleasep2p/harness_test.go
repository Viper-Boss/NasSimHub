package nodereleasep2p

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/nassimhub-node/proto"
)

// Everything here is offline. The "network" is a queue of datagrams between
// engines in one process; it stands in for NKN's addressing and delivery and
// for nothing else. Signatures, hashes, the release store and the files on
// disk are the real ones.

const testKeyID = "test-2026"

// publisher signs Node releases with a real Ed25519 key, as the store's own
// tests do: a test that stubbed verification would be testing a gateway that
// believes strangers.
type publisher struct {
	t       *testing.T
	private ed25519.PrivateKey
	keyID   string
}

func newPublisher(t *testing.T, first byte) *publisher {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = first + byte(index)
	}
	return &publisher{t: t, private: ed25519.NewKeyFromSeed(seed), keyID: testKeyID}
}

func (p *publisher) keyring() proto.OTAKeyring {
	return proto.OTAKeyring{p.keyID: p.private.Public().(ed25519.PublicKey)}
}

func (p *publisher) manifest(id, version string, artifact []byte) proto.OTAManifest {
	return proto.OTAManifest{
		SchemaVersion: proto.OTASchemaVersion, ReleaseID: id, Version: version,
		Channel: proto.OTAChannelStable, Platform: proto.PlatformMSM8916,
		ReleasedAt:    time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		ProtocolMajor: proto.ProtocolMajor, ProtocolMinor: proto.ProtocolMinor, MinCoreVersion: "1.0.0",
		Artifacts: []proto.OTAArtifact{{
			Kind: proto.OTAArtifactAgent, Name: "nassimhub-agent",
			SHA256: proto.ArtifactDigest(artifact), Size: int64(len(artifact)),
			URL: "https://invalid.example/agent", Arch: "arm64",
		}},
	}
}

func (p *publisher) sign(manifest proto.OTAManifest) []byte {
	p.t.Helper()
	signed, err := proto.SignManifest(manifest, p.keyID, p.private)
	if err != nil {
		p.t.Fatal(err)
	}
	raw, err := json.Marshal(signed)
	if err != nil {
		p.t.Fatal(err)
	}
	return raw
}

// release signs a manifest for an artifact; edits change the manifest before
// it is signed (so the result is still genuinely the publisher's).
func (p *publisher) release(id, version string, artifact []byte, edits ...func(*proto.OTAManifest)) []byte {
	p.t.Helper()
	manifest := p.manifest(id, version, artifact)
	for _, edit := range edits {
		edit(&manifest)
	}
	return p.sign(manifest)
}

// artifactBytes is a reproducible artifact that is not a whole number of
// chunks long, so the short last chunk is always exercised.
func artifactBytes(seed int64, size int) []byte {
	data := make([]byte, size)
	rand.New(rand.NewSource(seed)).Read(data)
	return data
}

const (
	testChunk    = MinChunkSize
	testArtifact = 12*testChunk + 4321 // 13 chunks
)

// clock is the time every engine in a test shares.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(by time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(by)
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------
// The in-memory peer network
// ---------------------------------------------------------------------------

type endpoint interface {
	Handle(from string, data []byte) bool
}

type endpointFunc func(from string, data []byte) bool

func (f endpointFunc) Handle(from string, data []byte) bool { return f(from, data) }

type packet struct {
	from, to string
	data     []byte
}

func (p packet) message(t *testing.T) Message {
	t.Helper()
	var message Message
	if err := json.Unmarshal(p.data, &message); err != nil {
		t.Fatalf("undecodable packet on the test network: %v", err)
	}
	return message
}

// network delivers datagrams between endpoints by address, in order, one at
// a time, when the test says so. It can lose them (an endpoint that is down,
// or a filter), deliver them twice, or deliver them in another order.
type network struct {
	t         *testing.T
	mu        sync.Mutex
	endpoints map[string]endpoint
	queue     []packet
	delivered []packet
	down      map[string]bool
	// filter decides per packet; false loses it.
	filter func(packet, Message) bool
	// rewrite may change a packet on its way.
	rewrite   func(*packet, Message)
	duplicate bool
	shuffle   *rand.Rand
	// sendFails makes the named endpoint's own transport report an error.
	sendFails map[string]bool
}

func newNetwork(t *testing.T) *network {
	return &network{t: t, endpoints: map[string]endpoint{}, down: map[string]bool{}, sendFails: map[string]bool{}}
}

func (n *network) attach(address string, target endpoint) {
	n.mu.Lock()
	n.endpoints[address] = target
	n.mu.Unlock()
}

func (n *network) senderFor(address string) func(string, []byte) error {
	return func(to string, data []byte) error {
		n.mu.Lock()
		defer n.mu.Unlock()
		if n.sendFails[address] {
			return errors.New("test network: not connected")
		}
		n.queue = append(n.queue, packet{from: address, to: to, data: append([]byte(nil), data...)})
		return nil
	}
}

func (n *network) setDown(address string, down bool) {
	n.mu.Lock()
	n.down[address] = down
	n.mu.Unlock()
}

// pump delivers until nothing is queued and reports how many it delivered.
func (n *network) pump() int {
	delivered := 0
	for step := 0; step < 200000; step++ {
		n.mu.Lock()
		if len(n.queue) == 0 {
			n.mu.Unlock()
			return delivered
		}
		index := 0
		if n.shuffle != nil {
			index = n.shuffle.Intn(len(n.queue))
		}
		next := n.queue[index]
		n.queue = append(n.queue[:index], n.queue[index+1:]...)
		message := next.message(n.t)
		lost := n.down[next.from] || n.down[next.to] || (n.filter != nil && !n.filter(next, message))
		if !lost && n.rewrite != nil {
			n.rewrite(&next, message)
		}
		target := n.endpoints[next.to]
		copies := 1
		if n.duplicate {
			copies = 2
		}
		if !lost {
			n.delivered = append(n.delivered, next)
		}
		n.mu.Unlock()
		if lost || target == nil {
			continue
		}
		for copy := 0; copy < copies; copy++ {
			target.Handle(next.from, next.data)
			delivered++
		}
	}
	n.t.Fatal("the test network did not go quiet")
	return delivered
}

// sentOfType counts delivered packets of one type from one address; an empty
// address or type matches any.
func (n *network) sentOfType(from, messageType string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	count := 0
	for _, entry := range n.delivered {
		if from != "" && entry.from != from {
			continue
		}
		if messageType == "" || entry.message(n.t).Type == messageType {
			count++
		}
	}
	return count
}

// ---------------------------------------------------------------------------
// A gateway: a real release store and an engine on the test network
// ---------------------------------------------------------------------------

type gateway struct {
	t        *testing.T
	address  string
	net      *network
	clock    *clock
	storeDir string
	cacheDir string
	keys     proto.OTAKeyring
	store    *nassimhubnode.NodeReleaseStore
	engine   *Engine
	options  Options
	enabled  atomic.Bool
	sharing  atomic.Bool
	nodesMu  sync.Mutex
	nodes    []PairedNode
}

type gatewayOption func(*gateway)

func withoutKeys() gatewayOption { return func(g *gateway) { g.keys = nil } }

func withOptions(edit func(*Options)) gatewayOption { return func(g *gateway) { edit(&g.options) } }

func withNodes(nodes ...PairedNode) gatewayOption {
	return func(g *gateway) { g.nodes = nodes }
}

func testTuning() Tuning {
	return Tuning{SeedChunkSize: testChunk}
}

func newGateway(t *testing.T, net *network, shared *clock, address string, signer *publisher, options ...gatewayOption) *gateway {
	t.Helper()
	root := t.TempDir()
	g := &gateway{
		t: t, address: address, net: net, clock: shared,
		storeDir: filepath.Join(root, "node-releases"),
		cacheDir: filepath.Join(root, "node-release-downloads"),
		keys:     signer.keyring(),
		nodes:    []PairedNode{{Platform: string(proto.PlatformMSM8916), AgentVersion: "1.0.0"}},
	}
	g.options = Options{CacheCapBytes: -1, Tuning: testTuning()}
	g.enabled.Store(true)
	g.sharing.Store(true)
	for _, option := range options {
		option(g)
	}
	g.open()
	return g
}

// open (re)opens the store and the engine on the same directories, which is
// what a process restart does.
func (g *gateway) open() {
	g.t.Helper()
	store, err := nassimhubnode.OpenNodeReleaseStore(nassimhubnode.NodeReleaseStoreOptions{
		Dir: g.storeDir, Keyring: g.keys, Now: g.clock.Now,
	})
	if err != nil {
		g.t.Fatal(err)
	}
	options := g.options
	options.Dir, options.Store, options.Now = g.cacheDir, store, g.clock.Now
	options.Send = g.net.senderFor(g.address)
	options.PairedNodes = func() []PairedNode {
		g.nodesMu.Lock()
		defer g.nodesMu.Unlock()
		return append([]PairedNode(nil), g.nodes...)
	}
	engine, err := Open(options)
	if err != nil {
		g.t.Fatal(err)
	}
	engine.SetGates(g.enabled.Load, g.sharing.Load)
	g.store, g.engine = store, engine
	g.net.attach(g.address, engine)
	g.t.Cleanup(engine.Close)
}

// restart stands in for the process being killed and started again: the old
// engine is dropped without being given a chance to tidy anything, and a new
// one is opened on whatever is on disk.
func (g *gateway) restart() {
	g.t.Helper()
	g.engine.Close() // closes file handles only; writes nothing
	g.open()
}

// hold imports a complete release into the gateway's store, as an
// administrator's upload would.
func (g *gateway) hold(raw, artifact []byte) {
	g.t.Helper()
	release, err := g.store.ImportManifest(raw)
	if err != nil {
		g.t.Fatal(err)
	}
	if _, err := g.store.ImportArtifact(release.ReleaseID, bytes.NewReader(artifact)); err != nil {
		g.t.Fatal(err)
	}
}

func (g *gateway) status() Status { return g.engine.Status() }

func (g *gateway) download(releaseID string) (DownloadStatus, bool) {
	for _, entry := range g.status().Downloads {
		if entry.ReleaseID == releaseID {
			return entry, true
		}
	}
	return DownloadStatus{}, false
}

// stored returns the artifact bytes the store holds for a release, or nil.
func (g *gateway) stored(releaseID string) []byte {
	g.t.Helper()
	release, err := g.store.Release(releaseID)
	if err != nil || !release.ArtifactReady {
		return nil
	}
	file, _, err := g.store.OpenArtifact(releaseID)
	if err != nil {
		return nil
	}
	defer file.Close()
	content, err := os.ReadFile(file.Name())
	if err != nil {
		g.t.Fatal(err)
	}
	return content
}

func (g *gateway) cacheEntries() []string {
	g.t.Helper()
	entries, err := os.ReadDir(g.cacheDir)
	if err != nil {
		g.t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// settle runs the network and the engines' clocks-less work until nothing
// moves: every datagram delivered, every finished download imported.
func settle(net *network, gateways ...*gateway) {
	for round := 0; round < 200; round++ {
		moved := net.pump()
		for _, g := range gateways {
			g.engine.Tick()
		}
		if moved == 0 && net.pump() == 0 {
			return
		}
	}
	net.t.Fatal("the gateways did not settle")
}

// drive lets time pass in steps, settling after each.
func drive(net *network, shared *clock, step time.Duration, steps int, gateways ...*gateway) {
	for index := 0; index < steps; index++ {
		shared.advance(step)
		settle(net, gateways...)
	}
}

// requireImported checks that a release arrived whole, through the store's
// own import, marked as coming from a peer, and that the cache is empty.
func requireImported(t *testing.T, g *gateway, releaseID string, artifact []byte) {
	t.Helper()
	release, err := g.store.Release(releaseID)
	if err != nil {
		t.Fatalf("%s is not in the store: %v (status %+v)", releaseID, err, g.status())
	}
	if !release.ArtifactReady || release.Gateway != nassimhubnode.GatewayVerified {
		t.Fatalf("%s is stored but not complete and verified: %+v", releaseID, release)
	}
	if release.Source != nassimhubnode.NodeReleaseSourcePeer {
		t.Fatalf("a release fetched from a peer must say so, got source %q", release.Source)
	}
	if !bytes.Equal(g.stored(releaseID), artifact) {
		t.Fatalf("%s was imported with different bytes than were published", releaseID)
	}
	if entries := g.cacheEntries(); len(entries) != 0 {
		t.Fatalf("the download cache must be empty after an import, holds %v", entries)
	}
	if status := g.status(); status.CacheBytes != 0 {
		t.Fatalf("the cache still accounts for %d bytes", status.CacheBytes)
	}
}

func requireNothingStored(t *testing.T, g *gateway) {
	t.Helper()
	if releases := g.store.Releases(); len(releases) != 0 {
		t.Fatalf("nothing may be imported, the store holds %+v", releases)
	}
}

// ---------------------------------------------------------------------------
// A scripted peer, for the ones that lie
// ---------------------------------------------------------------------------

// scriptedSeeder speaks the seeding side of the protocol with no rules of its
// own: it offers whatever manifest it is given, describes whatever table it
// is told to, and serves whatever bytes it is told to.
type scriptedSeeder struct {
	t         *testing.T
	net       *network
	address   string
	releaseID string
	manifest  []byte
	// tableOf is what the advertised chunk table is computed from; serve is
	// what chunk requests are answered from. An honest peer has them equal.
	tableOf   []byte
	serve     []byte
	chunkSize int
	// totalSize overrides len(tableOf) in the offer when non-zero.
	totalSize int64
	// sentTable replaces the table bytes actually sent, when set.
	sentTable []byte
	// silent makes the peer offer and then never answer.
	silent bool
	chunks atomic.Int64
}

func newScriptedSeeder(t *testing.T, net *network, address, releaseID string, manifest, artifact []byte) *scriptedSeeder {
	seeder := &scriptedSeeder{
		t: t, net: net, address: address, releaseID: releaseID, manifest: manifest,
		tableOf: artifact, serve: artifact, chunkSize: testChunk,
	}
	net.attach(address, seeder)
	return seeder
}

func (s *scriptedSeeder) size() int64 {
	if s.totalSize != 0 {
		return s.totalSize
	}
	return int64(len(s.tableOf))
}

func (s *scriptedSeeder) table() []byte {
	hashes, err := buildTable(func(buffer []byte, offset int64) error {
		copy(buffer, s.tableOf[offset:])
		return nil
	}, int64(len(s.tableOf)), s.chunkSize)
	if err != nil {
		s.t.Fatal(err)
	}
	return hashes
}

func (s *scriptedSeeder) count() int {
	return int((s.size() + int64(s.chunkSize) - 1) / int64(s.chunkSize))
}

func (s *scriptedSeeder) tableSHA() [sha256.Size]byte {
	return tableDigest(s.size(), s.chunkSize, s.count(), s.table())
}

func (s *scriptedSeeder) key() string {
	return TransferKey(s.releaseID, sha256.Sum256(s.manifest), s.tableSHA())
}

func (s *scriptedSeeder) offer(nonce string) Message {
	sum := s.tableSHA()
	return Message{
		Type: TypeOffer, Nonce: nonce, ReleaseID: s.releaseID, Transfer: s.key(),
		ManifestBase64: base64.StdEncoding.EncodeToString(s.manifest),
		ChunkSize:      s.chunkSize, ChunkCount: s.count(), TotalSize: s.size(),
		TableSHA256: hex.EncodeToString(sum[:]),
	}
}

func (s *scriptedSeeder) reply(to string, message Message) {
	data, err := json.Marshal(message)
	if err != nil {
		s.t.Fatal(err)
	}
	_ = s.net.senderFor(s.address)(to, data)
}

func (s *scriptedSeeder) Handle(from string, data []byte) bool {
	message, ok := decodeMessage(data)
	if !ok {
		return false
	}
	switch message.Type {
	case TypeQuery:
		s.reply(from, s.offer(message.Nonce))
	case TypeTableGet:
		if s.silent {
			return true
		}
		table := s.table()
		if s.sentTable != nil {
			table = s.sentTable
		}
		s.reply(from, Message{Type: TypeTable, ReleaseID: message.ReleaseID, Transfer: message.Transfer,
			TableBase64: base64.StdEncoding.EncodeToString(table)})
	case TypeChunkGet:
		if s.silent {
			return true
		}
		offset := int64(message.Index) * int64(s.chunkSize)
		if message.Index < 0 || offset >= int64(len(s.serve)) {
			return true
		}
		end := offset + int64(s.chunkSize)
		if end > int64(len(s.serve)) {
			end = int64(len(s.serve))
		}
		s.chunks.Add(1)
		s.reply(from, Message{Type: TypeChunk, ReleaseID: message.ReleaseID, Transfer: message.Transfer,
			Index: message.Index, DataBase64: base64.StdEncoding.EncodeToString(s.serve[offset:end])})
	}
	return true
}

// pairOfGateways is the common starting point: a seeder holding one complete
// release and a gateway that holds nothing.
func pairOfGateways(t *testing.T, options ...gatewayOption) (*network, *clock, *publisher, *gateway, *gateway, []byte, []byte) {
	t.Helper()
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	seeder := newGateway(t, net, shared, "seeder", signer)
	artifact := artifactBytes(7, testArtifact)
	raw := signer.release("r-1.1.0", "1.1.0", artifact)
	seeder.hold(raw, artifact)
	fetcher := newGateway(t, net, shared, "fetcher", signer, options...)
	return net, shared, signer, seeder, fetcher, raw, artifact
}

// decodeSignedForTest reads a manifest out of its signed envelope without
// verifying anything, for building test offers.
func decodeSignedForTest(raw []byte) (proto.OTAManifest, error) {
	var signed proto.OTASignedManifest
	if err := json.Unmarshal(raw, &signed); err != nil {
		return proto.OTAManifest{}, err
	}
	var manifest proto.OTAManifest
	err := json.Unmarshal(signed.Manifest, &manifest)
	return manifest, err
}

// probe is a bare endpoint that records what it is sent, for talking to a
// seeder directly.
type probe struct {
	net      *network
	address  string
	mu       sync.Mutex
	received []Message
}

func newProbe(net *network, address string) *probe {
	p := &probe{net: net, address: address}
	net.attach(address, p)
	return p
}

func (p *probe) Handle(_ string, data []byte) bool {
	message, ok := decodeMessage(data)
	if ok {
		p.mu.Lock()
		p.received = append(p.received, message)
		p.mu.Unlock()
	}
	return ok
}

func (p *probe) send(to string, message Message) {
	data, _ := json.Marshal(message)
	_ = p.net.senderFor(p.address)(to, data)
}

// take returns what arrived since the last call.
func (p *probe) take() []Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	received := p.received
	p.received = nil
	return received
}
