// Package nodereleasep2p moves Node agent releases between gateways.
//
// # WHAT TRAVELS, AND WHY IT IS NOT THE GATEWAY'S OWN UPDATE
//
// A gateway updates itself by pulling container images by digest from a
// registry; the peer networks only tell it which signed manifest is newest
// (internal/releasechannel) and that path is untouched by this package.
//
// A Node -- the agent on a UFI003 stick -- is different. Its release is a
// signed manifest plus one artifact of around thirteen megabytes, and until
// now both reached a gateway only when an administrator uploaded them. This
// package lets a gateway that holds a complete, verified Node release hand it
// to one that does not, over the peer channel that already exists.
//
// # THE ONE RULE
//
// A peer is anonymous and chooses the bytes, so nothing it says is believed:
//
//   - No payload byte is asked for until the manifest verifies against this
//     gateway's Node release keys and passes admission (admission.go). A
//     gateway without those keys does not take part at all.
//   - The chunk table a peer supplies is a convenience, not evidence. It lets
//     a damaged or hostile chunk be thrown away as it arrives and names who
//     sent it. It is not signed by anybody.
//   - The assembled file is hashed and compared with the SHA-256 in the signed
//     manifest. That comparison, and only that, makes the bytes acceptable.
//   - What passes is handed to the existing release store through the same two
//     functions an upload uses, which verify it again. Nothing is pushed to or
//     applied on any Node: that stays the administrator's explicit action over
//     the pairing link.
//
// # SHAPE
//
// Engine is deliberately synchronous: Handle takes one inbound datagram, Tick
// advances time-driven work, and neither starts a goroutine. Whoever owns the
// real transport decides how they are called (internal/httpapi runs both from
// one worker). That is what lets the tests replay loss, duplication,
// reordering and power cuts exactly.
package nodereleasep2p

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/nassimhub-node/proto"
)

// Store is the gateway's Node release store, as much of it as the exchange
// uses. *nassimhubnode.NodeReleaseStore is the implementation.
type Store interface {
	VerificationKeys() (proto.OTAKeyring, proto.OTAPQKeyring, proto.OTASignaturePolicy)
	Capacity() (stored, maximum int)
	Releases() []nassimhubnode.NodeRelease
	Release(releaseID string) (nassimhubnode.NodeRelease, error)
	SignedManifest(releaseID string) ([]byte, nassimhubnode.NodeRelease, error)
	OpenArtifact(releaseID string) (*os.File, nassimhubnode.NodeRelease, error)
	ImportManifestFrom(raw []byte, source string) (nassimhubnode.NodeRelease, error)
	ImportArtifact(releaseID string, body io.Reader) (nassimhubnode.NodeRelease, error)
}

// Defaults.
const (
	DefaultCacheCapBytes = 96 << 20
	DefaultMaxDownloads  = 1
	DefaultMaxOffers     = 32
)

// Tuning holds the protocol's timers and bounds. The zero value of any field
// means its default; tests shorten them.
type Tuning struct {
	// ChunkTimeout and TableTimeout are how long one request may go
	// unanswered before it is given to another peer.
	ChunkTimeout time.Duration
	TableTimeout time.Duration
	// TableAttempts bounds how often the chunk table is asked for before the
	// download is given up.
	TableAttempts int
	// PeerWindow is how many chunk requests one peer has outstanding at a
	// time; MaxInflight the same for a whole download.
	PeerWindow  int
	MaxInflight int
	// MaxPeersPerDownload bounds the peers one download draws from.
	MaxPeersPerDownload int
	// MaxPeerFailures is how many requests in a row a peer may leave
	// unanswered before it is dropped from the download. Between failures it
	// is rested for RetryBase, doubling up to RetryMax.
	MaxPeerFailures int
	RetryBase       time.Duration
	RetryMax        time.Duration
	// StallAfter: a download with no progress for this long may be evicted to
	// make room for another. AbandonAfter: it is given up regardless.
	StallAfter   time.Duration
	AbandonAfter time.Duration
	// QueryTTL is how long answers to one query are accepted; OffersPerQuery
	// how many offers one peer may answer it with; MaxQueries how many
	// queries are outstanding at once.
	QueryTTL       time.Duration
	OffersPerQuery int
	MaxQueries     int
	// OfferMemo is how long a refused offer is remembered, so the same offer
	// is not verified again; ForgedBackoff the first rest of a peer caught
	// with something forged (it quadruples, up to ForgedBackoffMax);
	// BadTableBan how long a chunk table proven false is remembered.
	OfferMemo        time.Duration
	ForgedBackoff    time.Duration
	ForgedBackoffMax time.Duration
	BadTableBan      time.Duration
	// ImportRetry and ImportAttempts govern a verified payload the store
	// could not take for a reason that may pass (an I/O error).
	ImportRetry    time.Duration
	ImportAttempts int
	// Seeding limits: payload bytes per minute to one peer and to everybody,
	// how often one peer's query is answered, and how many queries are
	// answered per minute overall.
	ServePeerBytesPerMinute   int64
	ServeGlobalBytesPerMinute int64
	QueryInterval             time.Duration
	QueriesPerMinute          int
	// SeedRevalidate is how long a release checked against the store is
	// served before it is checked again.
	SeedRevalidate time.Duration
	// SeedChunkSize is the chunk size this gateway seeds in; anything outside
	// the protocol's bounds means DefaultChunkSize.
	SeedChunkSize int
}

func (tuning Tuning) withDefaults() Tuning {
	duration := func(value *time.Duration, fallback time.Duration) {
		if *value <= 0 {
			*value = fallback
		}
	}
	count := func(value *int, fallback int) {
		if *value <= 0 {
			*value = fallback
		}
	}
	duration(&tuning.ChunkTimeout, 30*time.Second)
	duration(&tuning.TableTimeout, 30*time.Second)
	count(&tuning.TableAttempts, 3)
	count(&tuning.PeerWindow, 4)
	count(&tuning.MaxInflight, 16)
	count(&tuning.MaxPeersPerDownload, 8)
	count(&tuning.MaxPeerFailures, 5)
	duration(&tuning.RetryBase, 15*time.Second)
	duration(&tuning.RetryMax, 15*time.Minute)
	duration(&tuning.StallAfter, 30*time.Minute)
	duration(&tuning.AbandonAfter, 24*time.Hour)
	duration(&tuning.QueryTTL, 30*time.Second)
	count(&tuning.OffersPerQuery, 2)
	count(&tuning.MaxQueries, 64)
	duration(&tuning.OfferMemo, 30*time.Minute)
	duration(&tuning.ForgedBackoff, time.Hour)
	duration(&tuning.ForgedBackoffMax, 24*time.Hour)
	duration(&tuning.BadTableBan, 24*time.Hour)
	duration(&tuning.ImportRetry, 5*time.Minute)
	count(&tuning.ImportAttempts, 6)
	if tuning.ServePeerBytesPerMinute <= 0 {
		tuning.ServePeerBytesPerMinute = 8 << 20
	}
	if tuning.ServeGlobalBytesPerMinute <= 0 {
		tuning.ServeGlobalBytesPerMinute = 16 << 20
	}
	duration(&tuning.QueryInterval, time.Minute)
	count(&tuning.QueriesPerMinute, 30)
	duration(&tuning.SeedRevalidate, time.Minute)
	if tuning.SeedChunkSize < MinChunkSize || tuning.SeedChunkSize > MaxChunkSize {
		tuning.SeedChunkSize = DefaultChunkSize
	}
	return tuning
}

// Options configure an Engine.
type Options struct {
	// Dir holds downloads in progress. It is this package's alone.
	Dir   string
	Store Store
	// PairedNodes lists this gateway's Nodes now. Nil means none.
	PairedNodes func() []PairedNode
	// AllowedTargets is the operator's allowlist, in addition to the
	// platforms of the paired Nodes.
	AllowedTargets []Target
	// CacheCapBytes bounds Dir. Zero disables downloading; negative means
	// the default.
	CacheCapBytes int64
	MaxDownloads  int
	MaxOffers     int
	// Send delivers one datagram to one peer. It may be set later with
	// SetSender; until then nothing is sent.
	Send   func(to string, data []byte) error
	Now    func() time.Time
	Logger *slog.Logger
	Tuning Tuning
}

// Engine is one gateway's side of the exchange: downloader and seeder.
type Engine struct {
	dir          string
	store        Store
	nodes        func() []PairedNode
	allowed      []Target
	cacheCap     int64
	maxDownloads int
	maxOffers    int
	now          func() time.Time
	log          *slog.Logger
	tuning       Tuning
	keys         proto.OTAKeyring
	pqKeys       proto.OTAPQKeyring
	policy       proto.OTASignaturePolicy
	startedAt    time.Time

	gateMu  sync.RWMutex
	send    func(to string, data []byte) error
	enabled func() bool
	sharing func() bool

	mu        sync.Mutex
	downloads map[string]*download // by release id
	queries   map[string]*query    // by nonce
	peers     map[string]*peerRecord
	memo      map[string]*offerMemo // refused offers, by transfer key
	badTables map[string]time.Time  // transfer keys proven false
	history   []DownloadStatus
	counters  counters
	outbox    []outbound
	// crashed is set by a test hook that simulates the process dying; every
	// later call is then a no-op, as it would be for a process that is gone.
	crashed bool
	hooks   hooks

	seed seeder
}

// hooks let a test stop the engine at the points where a power cut matters.
// A hook that returns an error "kills the process": the engine does nothing
// further, and the test opens a new one on the same directories.
type hooks struct {
	afterChunkWrite func() error // chunk bytes are on disk, the bitmap is not
	beforeImport    func() error // the whole file verified, nothing imported
	betweenImports  func() error // manifest imported, artifact not
	afterImport     func() error // both imported, the download not yet removed
}

type outbound struct {
	to      string
	message Message
}

type query struct {
	peer      string
	expires   time.Time
	remaining int
}

type counters struct {
	refused             map[Reason]int64
	offersReceived      int64
	offersUnsolicited   int64
	offersDropped       int64
	alreadyHeld         int64
	mixups              int64
	duplicateChunks     int64
	unexpectedMessages  int64
	sendErrors          int64
	downloadsCompleted  int64
	downloadsFailed     int64
	downloadsAbandoned  int64
	downloadsEvicted    int64
	chunksReceived      int64
	bytesReceived       int64
	requestTimeouts     int64
	peersBackedOffTotal int64
}

// ErrNoStore reports an engine opened without a release store.
var ErrNoStore = errors.New("nodereleasep2p: a release store is required")

// Open builds an engine and picks up whatever downloads a previous run left
// in Dir, after checking every byte of them again.
func Open(options Options) (*Engine, error) {
	if options.Store == nil {
		return nil, ErrNoStore
	}
	if strings.TrimSpace(options.Dir) == "" {
		return nil, errors.New("nodereleasep2p: a download directory is required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	engine := &Engine{
		dir:          options.Dir,
		store:        options.Store,
		nodes:        options.PairedNodes,
		allowed:      append([]Target(nil), options.AllowedTargets...),
		cacheCap:     options.CacheCapBytes,
		maxDownloads: options.MaxDownloads,
		maxOffers:    options.MaxOffers,
		now:          now,
		log:          logger,
		tuning:       options.Tuning.withDefaults(),
		send:         options.Send,
		startedAt:    now().UTC(),
		downloads:    make(map[string]*download),
		queries:      make(map[string]*query),
		peers:        make(map[string]*peerRecord),
		memo:         make(map[string]*offerMemo),
		badTables:    make(map[string]time.Time),
		counters:     counters{refused: make(map[Reason]int64)},
	}
	if engine.cacheCap < 0 {
		engine.cacheCap = DefaultCacheCapBytes
	}
	if engine.maxDownloads <= 0 {
		engine.maxDownloads = DefaultMaxDownloads
	}
	if engine.maxOffers <= 0 {
		engine.maxOffers = DefaultMaxOffers
	}
	engine.keys, engine.pqKeys, engine.policy = options.Store.VerificationKeys()
	engine.seed.init(engine)
	if err := os.MkdirAll(engine.dir, 0o700); err != nil {
		return nil, err
	}
	engine.mu.Lock()
	engine.resumeLocked()
	engine.mu.Unlock()
	return engine, nil
}

// SetSender sets how datagrams leave.
func (e *Engine) SetSender(send func(to string, data []byte) error) {
	e.gateMu.Lock()
	e.send = send
	e.gateMu.Unlock()
}

// SetGates sets the two switches that live outside this package: enabled is
// the operator's consent to exchanging payloads at all, sharing their consent
// to handing updates to other gateways. Until both are set, both are off.
func (e *Engine) SetGates(enabled, sharing func() bool) {
	e.gateMu.Lock()
	e.enabled, e.sharing = enabled, sharing
	e.gateMu.Unlock()
}

func (e *Engine) switchedOn() bool {
	e.gateMu.RLock()
	gate := e.enabled
	e.gateMu.RUnlock()
	return gate != nil && gate()
}

func (e *Engine) sharingOn() bool {
	e.gateMu.RLock()
	gate := e.sharing
	e.gateMu.RUnlock()
	return gate != nil && gate()
}

// Why downloading is off, when it is.
const (
	DisabledSwitchedOff   = "switched_off"
	DisabledNoReleaseKeys = "no_release_keys"
	DisabledCache         = "cache_disabled"
	DisabledSharingOff    = "sharing_off"
)

// downloadState reports whether this gateway fetches payloads, and why not.
// The order is from what cannot be changed at run time to what can.
func (e *Engine) downloadState() (bool, string) {
	switch {
	case len(e.keys) == 0:
		// Unlike an upload, where the device still verifies, here a stranger
		// picks the bytes. Without keys there is nothing to hold them to.
		return false, DisabledNoReleaseKeys
	case e.cacheCap == 0:
		return false, DisabledCache
	case !e.switchedOn():
		return false, DisabledSwitchedOff
	}
	return true, ""
}

func (e *Engine) seedState() (bool, string) {
	switch {
	case len(e.keys) == 0:
		// Only releases that passed THIS gateway's verification are served,
		// and without keys none has.
		return false, DisabledNoReleaseKeys
	case !e.switchedOn():
		return false, DisabledSwitchedOff
	case !e.sharingOn():
		return false, DisabledSharingOff
	}
	return true, ""
}

func (e *Engine) pairedNodes() []PairedNode {
	if e.nodes == nil {
		return nil
	}
	return e.nodes()
}

// targetsLocked is every kind of device a release may be for: the operator's
// allowlist and the platforms of the Nodes paired now.
func (e *Engine) targetsLocked() []Target {
	targets := append([]Target(nil), e.allowed...)
	seen := make(map[Target]struct{}, len(targets))
	for _, target := range targets {
		seen[target] = struct{}{}
	}
	for _, node := range e.pairedNodes() {
		if node.Revoked || node.Platform == "" {
			continue
		}
		target := Target{Platform: node.Platform, Arch: platformArch[node.Platform]}
		if _, dup := seen[target]; dup {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	return targets
}

// ---------------------------------------------------------------------------
// Inbound
// ---------------------------------------------------------------------------

// Handle takes one datagram from one peer. It reports whether the datagram
// belonged to this protocol, whatever was then done with it.
func (e *Engine) Handle(from string, data []byte) bool {
	message, ok := decodeMessage(data)
	if !ok {
		return false
	}
	if from == "" {
		return true
	}
	switch message.Type {
	case TypeQuery, TypeTableGet, TypeChunkGet:
		e.seed.handle(from, message)
		return true
	}
	e.mu.Lock()
	if e.crashed {
		e.mu.Unlock()
		return true
	}
	switch message.Type {
	case TypeOffer:
		e.onOfferLocked(from, message)
	case TypeTable:
		e.onTableLocked(from, message)
	case TypeChunk:
		e.onChunkLocked(from, message)
	case TypeRefuse:
		e.onRefuseLocked(from, message)
	}
	out := e.takeOutboxLocked()
	e.mu.Unlock()
	e.flush(out)
	return true
}

func (e *Engine) takeOutboxLocked() []outbound {
	out := e.outbox
	e.outbox = nil
	return out
}

func (e *Engine) queueLocked(to string, message Message) {
	e.outbox = append(e.outbox, outbound{to: to, message: message})
}

// flush sends what a locked section queued, outside the lock: a transport
// that blocks must not hold up the status page.
func (e *Engine) flush(out []outbound) {
	if len(out) == 0 {
		return
	}
	e.gateMu.RLock()
	send := e.send
	e.gateMu.RUnlock()
	failed := int64(0)
	for _, item := range out {
		data, err := json.Marshal(item.message)
		if err != nil || send == nil || send(item.to, data) != nil {
			failed++
		}
	}
	if failed > 0 {
		// Nothing is retried here. A request that never left is found by its
		// timeout like one that was never answered, and asked for again then.
		e.mu.Lock()
		e.counters.sendErrors += failed
		e.mu.Unlock()
	}
}

// Ask sends a query to each peer: which Node releases do you hold? Only the
// peers asked may answer, each within QueryTTL and with a bounded number of
// offers; everything else that claims to be an offer is dropped unread.
func (e *Engine) Ask(peers []string) {
	e.mu.Lock()
	if e.crashed {
		e.mu.Unlock()
		return
	}
	if on, _ := e.downloadState(); !on {
		e.mu.Unlock()
		return
	}
	now := e.now()
	e.expireQueriesLocked(now)
	for _, peer := range peers {
		peer = strings.TrimSpace(peer)
		if peer == "" || e.blockedLocked(peer, now) || len(e.queries) >= e.tuning.MaxQueries {
			continue
		}
		nonce, err := newNonce()
		if err != nil {
			continue
		}
		e.queries[nonce] = &query{peer: peer, expires: now.Add(e.tuning.QueryTTL), remaining: e.tuning.OffersPerQuery}
		e.queueLocked(peer, Message{Type: TypeQuery, Nonce: nonce})
	}
	out := e.takeOutboxLocked()
	e.mu.Unlock()
	e.flush(out)
}

func (e *Engine) expireQueriesLocked(now time.Time) {
	for nonce, pending := range e.queries {
		if !now.Before(pending.expires) {
			delete(e.queries, nonce)
		}
	}
}

// WantsPeers reports whether a download is waiting for somebody to fetch
// from, so the owner of the transport knows to ask around sooner than its
// ordinary round.
func (e *Engine) WantsPeers() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if on, _ := e.downloadState(); !on || e.crashed {
		return false
	}
	now := e.now()
	for _, active := range e.downloads {
		if (active.state == stateDownloading || active.state == stateFetchingTable) && e.usablePeersLocked(active, now) == 0 {
			return true
		}
	}
	return false
}

func newNonce() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// ---------------------------------------------------------------------------
// Refusals and peer standing
// ---------------------------------------------------------------------------

type peerRecord struct {
	strikes      int
	blockedUntil time.Time
	touched      time.Time
}

type offerMemo struct {
	refusal *refusal
	until   time.Time
	at      time.Time
}

const maxPeerRecords = 256

func (e *Engine) blockedLocked(peer string, now time.Time) bool {
	record, ok := e.peers[peer]
	return ok && now.Before(record.blockedUntil)
}

// strikeLocked records that a peer sent something forged or false, rests it
// for a time that quadruples with each strike, and takes it out of every
// download it was serving.
func (e *Engine) strikeLocked(peer string, now time.Time) {
	record, ok := e.peers[peer]
	if !ok {
		if len(e.peers) >= maxPeerRecords {
			e.prunePeersLocked(now)
		}
		record = &peerRecord{}
		e.peers[peer] = record
	}
	record.strikes++
	backoff := e.tuning.ForgedBackoff
	for step := 1; step < record.strikes && backoff < e.tuning.ForgedBackoffMax; step++ {
		backoff *= 4
	}
	if backoff > e.tuning.ForgedBackoffMax {
		backoff = e.tuning.ForgedBackoffMax
	}
	record.blockedUntil, record.touched = now.Add(backoff), now
	e.counters.peersBackedOffTotal++
	for _, active := range e.downloads {
		e.detachLocked(active, peer)
	}
	for nonce, pending := range e.queries {
		if pending.peer == peer {
			delete(e.queries, nonce)
		}
	}
}

// prunePeersLocked makes room in the table of peers: first those whose rest
// is over, then whoever was touched longest ago.
func (e *Engine) prunePeersLocked(now time.Time) {
	for peer, record := range e.peers {
		if !now.Before(record.blockedUntil) {
			delete(e.peers, peer)
		}
	}
	for len(e.peers) >= maxPeerRecords {
		oldest, at := "", time.Time{}
		for peer, record := range e.peers {
			if oldest == "" || record.touched.Before(at) {
				oldest, at = peer, record.touched
			}
		}
		delete(e.peers, oldest)
	}
}

// refuseLocked counts and logs one refusal and charges the peer for it.
func (e *Engine) refuseLocked(peer, releaseID string, refused *refusal, now time.Time) {
	e.counters.refused[refused.reason]++
	if proto.ValidateOTAReleaseID(releaseID) != nil {
		// Not yet anything but a string a stranger sent.
		releaseID = "(invalid)"
	}
	detail := refused.detail
	if len(detail) > 200 {
		detail = detail[:200]
	}
	e.log.Warn("node release from a peer refused", "component", "updates",
		"reason", string(refused.reason), "detail", detail, "release_id", releaseID, "peer", peerTag(peer))
	if refused.penalty == penaltyForged {
		e.strikeLocked(peer, now)
	}
}

// peerTag shortens a peer address for a log line. The log is readable from
// the administration page, which shows no more of a peer than this elsewhere.
func peerTag(peer string) string {
	if len(peer) <= 8 {
		return peer
	}
	return peer[len(peer)-8:]
}

func (e *Engine) rememberRefusalLocked(key string, refused *refusal, now time.Time) {
	if refused.penalty == penaltyNone {
		// A refusal about this gateway's own state (no room, busy) can stop
		// being true at any moment and is decided afresh each time.
		return
	}
	if _, known := e.memo[key]; !known && len(e.memo) >= e.maxOffers {
		oldest, at := "", time.Time{}
		for candidate, entry := range e.memo {
			if oldest == "" || entry.at.Before(at) {
				oldest, at = candidate, entry.at
			}
		}
		delete(e.memo, oldest)
		e.counters.offersDropped++
	}
	e.memo[key] = &offerMemo{refusal: refused, until: now.Add(e.tuning.OfferMemo), at: now}
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// DownloadStatus describes one download. Peers is a count: which gateways
// they are is not shown.
type DownloadStatus struct {
	ReleaseID      string    `json:"release_id"`
	Version        string    `json:"version"`
	Platform       string    `json:"platform"`
	Arch           string    `json:"arch,omitempty"`
	ReceivedChunks int       `json:"received_chunks"`
	ChunkCount     int       `json:"chunk_count"`
	ReceivedBytes  int64     `json:"received_bytes"`
	TotalBytes     int64     `json:"total_bytes"`
	Peers          int       `json:"peers"`
	State          string    `json:"state"`
	Error          string    `json:"error,omitempty"`
	StartedAt      time.Time `json:"started_at,omitzero"`
	ProgressAt     time.Time `json:"progress_at,omitzero"`
	FinishedAt     time.Time `json:"finished_at,omitzero"`
}

// Refused counts refusals by reason since the process started.
type Refused struct {
	ManifestSize  int64 `json:"manifest_size"`
	Signature     int64 `json:"signature"`
	Schema        int64 `json:"schema"`
	Platform      int64 `json:"platform"`
	Channel       int64 `json:"channel"`
	Downgrade     int64 `json:"downgrade"`
	Artifact      int64 `json:"artifact"`
	StoreFull     int64 `json:"store_full"`
	Busy          int64 `json:"busy"`
	CacheFull     int64 `json:"cache_full"`
	Mixed         int64 `json:"mixed"`
	TableMismatch int64 `json:"table_mismatch"`
	ChunkMismatch int64 `json:"chunk_mismatch"`
	HashMismatch  int64 `json:"hash_mismatch"`
}

// OfferCounters describes offers since the process started.
type OfferCounters struct {
	Received    int64 `json:"received"`
	Unsolicited int64 `json:"unsolicited"`
	AlreadyHeld int64 `json:"already_held"`
	// Tracked is how many refused offers are remembered now, of at most Max;
	// Dropped how many were forgotten early to stay within it.
	Tracked int   `json:"tracked"`
	Max     int   `json:"max"`
	Dropped int64 `json:"dropped"`
}

// TransferCounters describes the downloading side since the process started.
type TransferCounters struct {
	ChunksReceived  int64 `json:"chunks_received"`
	BytesReceived   int64 `json:"bytes_received"`
	DuplicateChunks int64 `json:"duplicate_chunks"`
	// Mixups counts tables and chunks that named a transfer other than the
	// one being assembled; they are dropped unread.
	Mixups          int64 `json:"mixups"`
	RequestTimeouts int64 `json:"request_timeouts"`
	SendErrors      int64 `json:"send_errors"`
	Completed       int64 `json:"completed"`
	Failed          int64 `json:"failed"`
	Abandoned       int64 `json:"abandoned"`
	Evicted         int64 `json:"evicted"`
}

// PeerCounters describes peers by count only.
type PeerCounters struct {
	// BackedOff is how many peers are being rested now for sending something
	// forged or false; BackedOffTotal how often that has happened.
	BackedOff      int   `json:"backed_off"`
	BackedOffTotal int64 `json:"backed_off_total"`
}

// Status is the whole exchange as the administration API reports it.
type Status struct {
	// Enabled is whether this gateway fetches Node releases from peers;
	// Reason says why not. Seeding and SeedingReason are the same for
	// handing them out.
	Enabled       bool   `json:"enabled"`
	Reason        string `json:"reason,omitempty"`
	Seeding       bool   `json:"seeding"`
	SeedingReason string `json:"seeding_reason,omitempty"`
	// Targets are the device kinds a release is accepted for.
	Targets       []string         `json:"targets"`
	CacheBytes    int64            `json:"cache_bytes"`
	CacheCapBytes int64            `json:"cache_cap_bytes"`
	MaxDownloads  int              `json:"max_downloads"`
	Downloads     []DownloadStatus `json:"downloads"`
	Refused       Refused          `json:"refused"`
	Offers        OfferCounters    `json:"offers"`
	Transfers     TransferCounters `json:"transfers"`
	Served        ServedCounters   `json:"served"`
	Peers         PeerCounters     `json:"peers"`
	// CountersSince is when the counters started: they are kept in memory and
	// begin again with the process.
	CountersSince time.Time `json:"counters_since"`
}

// Status reports the exchange now.
func (e *Engine) Status() Status {
	e.mu.Lock()
	now := e.now()
	status := Status{
		Targets:       []string{},
		CacheBytes:    e.reservedLocked(),
		CacheCapBytes: e.cacheCap,
		MaxDownloads:  e.maxDownloads,
		Downloads:     []DownloadStatus{},
		CountersSince: e.startedAt,
	}
	status.Enabled, status.Reason = e.downloadState()
	status.Seeding, status.SeedingReason = e.seedState()
	for _, target := range e.targetsLocked() {
		status.Targets = append(status.Targets, target.String())
	}
	sort.Strings(status.Targets)
	ids := make([]string, 0, len(e.downloads))
	for id := range e.downloads {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		status.Downloads = append(status.Downloads, e.describeLocked(e.downloads[id], now))
	}
	status.Downloads = append(status.Downloads, e.history...)
	refused := e.counters.refused
	status.Refused = Refused{
		ManifestSize: refused[ReasonManifestSize], Signature: refused[ReasonSignature], Schema: refused[ReasonSchema],
		Platform: refused[ReasonPlatform], Channel: refused[ReasonChannel], Downgrade: refused[ReasonDowngrade],
		Artifact: refused[ReasonArtifact], StoreFull: refused[ReasonStoreFull], Busy: refused[ReasonBusy],
		CacheFull: refused[ReasonCacheFull], Mixed: refused[ReasonMixed], TableMismatch: refused[ReasonTableMismatch],
		ChunkMismatch: refused[ReasonChunkMismatch], HashMismatch: refused[ReasonHashMismatch],
	}
	status.Offers = OfferCounters{
		Received: e.counters.offersReceived, Unsolicited: e.counters.offersUnsolicited,
		AlreadyHeld: e.counters.alreadyHeld, Tracked: len(e.memo), Max: e.maxOffers, Dropped: e.counters.offersDropped,
	}
	status.Transfers = TransferCounters{
		ChunksReceived: e.counters.chunksReceived, BytesReceived: e.counters.bytesReceived,
		DuplicateChunks: e.counters.duplicateChunks, Mixups: e.counters.mixups,
		RequestTimeouts: e.counters.requestTimeouts, SendErrors: e.counters.sendErrors,
		Completed: e.counters.downloadsCompleted, Failed: e.counters.downloadsFailed,
		Abandoned: e.counters.downloadsAbandoned, Evicted: e.counters.downloadsEvicted,
	}
	status.Peers.BackedOffTotal = e.counters.peersBackedOffTotal
	for _, record := range e.peers {
		if now.Before(record.blockedUntil) {
			status.Peers.BackedOff++
		}
	}
	e.mu.Unlock()
	status.Served = e.seed.counters()
	return status
}

const maxHistory = 8

func (e *Engine) recordHistoryLocked(entry DownloadStatus) {
	e.history = append([]DownloadStatus{entry}, e.history...)
	if len(e.history) > maxHistory {
		e.history = e.history[:maxHistory]
	}
}

// Close releases the files the engine holds open. Downloads stay on disk and
// are resumed by the next Open.
func (e *Engine) Close() {
	e.mu.Lock()
	for _, active := range e.downloads {
		active.closeFiles()
	}
	e.crashed = true
	e.mu.Unlock()
	e.seed.close()
}
