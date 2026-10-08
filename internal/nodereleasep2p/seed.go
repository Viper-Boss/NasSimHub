package nodereleasep2p

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/nassimhub-node/proto"
)

// Seeding: answering other gateways.
//
// What is served is exactly what the release store holds AND this gateway
// verified against its own keys: the stored manifest byte for byte, and
// pieces of the stored artifact, which the store hashes against that manifest
// again each time a release is (re)opened here. Nothing is read by a name a
// peer supplies -- a release id is validated with the store's own rule and
// then only ever handed to the store -- and nothing is relayed for anyone: a
// download in progress is never served from, only finished, imported
// releases.
//
// The limits are on what this gateway SENDS: payload bytes per minute to any
// one peer and to everybody together, and how often a query is answered. A
// peer over its limit is told so once in a while and otherwise ignored.

const (
	maxSeedEntries     = 8
	maxServeWindows    = 256
	refusalsPerMinute  = 10
	offersPerAnswer    = 2
	seedWindowDuration = time.Minute
)

// ServedCounters describes the seeding side since the process started.
type ServedCounters struct {
	Queries int64 `json:"queries"`
	Offers  int64 `json:"offers"`
	Tables  int64 `json:"tables"`
	Chunks  int64 `json:"chunks"`
	Bytes   int64 `json:"bytes"`
	// RefusedRate counts requests turned away by a rate limit; RefusedInvalid
	// those naming a release, transfer or chunk this gateway does not serve.
	RefusedRate    int64 `json:"refused_rate"`
	RefusedInvalid int64 `json:"refused_invalid"`
}

type seedEntry struct {
	releaseID   string
	manifest    []byte
	manifestSHA [sha256.Size]byte
	tableSHA    [sha256.Size]byte
	key         string
	hashes      []byte
	chunkSize   int
	chunkCount  int
	totalSize   int64
	file        *os.File
	checkedAt   time.Time
}

type serveWindow struct {
	start    time.Time
	bytes    int64
	refusals int
}

type seeder struct {
	engine *Engine

	mu          sync.Mutex
	entries     map[string]*seedEntry
	windows     map[string]*serveWindow
	global      serveWindow
	answered    map[string]time.Time
	queryMinute time.Time
	queryCount  int
	stats       ServedCounters
	closed      bool
}

func (s *seeder) init(engine *Engine) {
	s.engine = engine
	s.entries = make(map[string]*seedEntry)
	s.windows = make(map[string]*serveWindow)
	s.answered = make(map[string]time.Time)
}

func (s *seeder) counters() ServedCounters {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *seeder) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for id, entry := range s.entries {
		_ = entry.file.Close()
		delete(s.entries, id)
	}
}

func (s *seeder) handle(from string, message Message) {
	if on, _ := s.engine.seedState(); !on {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	now := s.engine.now()
	var out []outbound
	switch message.Type {
	case TypeQuery:
		out = s.answerQueryLocked(from, message, now)
	case TypeTableGet:
		out = s.serveLocked(from, message, now, true)
	case TypeChunkGet:
		out = s.serveLocked(from, message, now, false)
	}
	s.mu.Unlock()
	s.engine.flush(out)
}

func (s *seeder) answerQueryLocked(from string, message Message, now time.Time) []outbound {
	if message.Nonce == "" || len(message.Nonce) > maxNonceLength {
		return nil
	}
	tuning := s.engine.tuning
	if last, asked := s.answered[from]; asked && now.Sub(last) < tuning.QueryInterval {
		s.stats.RefusedRate++
		return nil
	}
	if now.Sub(s.queryMinute) >= time.Minute {
		s.queryMinute, s.queryCount = now, 0
	}
	if s.queryCount >= tuning.QueriesPerMinute {
		s.stats.RefusedRate++
		return nil
	}
	for other, at := range s.answered {
		if now.Sub(at) >= tuning.QueryInterval {
			delete(s.answered, other)
		}
	}
	if len(s.answered) >= maxServeWindows {
		s.stats.RefusedRate++
		return nil
	}
	s.answered[from] = now
	s.queryCount++
	s.stats.Queries++

	var out []outbound
	// Newest first, as the store lists them; a peer wants the newest.
	for _, release := range s.engine.store.Releases() {
		if len(out) >= offersPerAnswer {
			break
		}
		entry := s.entryLocked(release.ReleaseID, now)
		if entry == nil {
			continue
		}
		out = append(out, outbound{to: from, message: Message{
			Type:           TypeOffer,
			Nonce:          message.Nonce,
			ReleaseID:      entry.releaseID,
			Transfer:       entry.key,
			ManifestBase64: base64.StdEncoding.EncodeToString(entry.manifest),
			ChunkSize:      entry.chunkSize,
			ChunkCount:     entry.chunkCount,
			TotalSize:      entry.totalSize,
			TableSHA256:    hex.EncodeToString(entry.tableSHA[:]),
		}})
		s.stats.Offers++
	}
	return out
}

// serveLocked answers a request for the chunk table or for one chunk.
func (s *seeder) serveLocked(from string, message Message, now time.Time, table bool) []outbound {
	refusal := func(reason string, retryAfter int) []outbound {
		window := s.windowLocked(from, now)
		if window == nil || window.refusals >= refusalsPerMinute {
			return nil
		}
		window.refusals++
		return []outbound{{to: from, message: Message{
			Type: TypeRefuse, ReleaseID: message.ReleaseID, Transfer: message.Transfer,
			Index: message.Index, Reason: reason, RetryAfterSeconds: retryAfter,
		}}}
	}
	// The release id is checked with the store's own rule before it is used
	// for anything, and after that it only ever reaches the store.
	if proto.ValidateOTAReleaseID(message.ReleaseID) != nil {
		s.stats.RefusedInvalid++
		return nil
	}
	entry := s.entryLocked(message.ReleaseID, now)
	if entry == nil || entry.key != message.Transfer {
		s.stats.RefusedInvalid++
		return refusal(refuseUnknown, 0)
	}
	if !table && (message.Index < 0 || message.Index >= entry.chunkCount) {
		s.stats.RefusedInvalid++
		return nil
	}
	size := int64(len(entry.hashes))
	if !table {
		size = int64(chunkLength(entry.totalSize, entry.chunkSize, message.Index))
	}
	if wait, ok := s.allowLocked(from, size, now); !ok {
		s.stats.RefusedRate++
		return refusal(refuseBusy, wait)
	}
	if table {
		s.stats.Tables++
		s.stats.Bytes += size
		return []outbound{{to: from, message: Message{
			Type: TypeTable, ReleaseID: entry.releaseID, Transfer: entry.key,
			TableBase64: base64.StdEncoding.EncodeToString(entry.hashes),
		}}}
	}
	data := make([]byte, size)
	if _, err := entry.file.ReadAt(data, int64(message.Index)*int64(entry.chunkSize)); err != nil {
		// The file went away underneath. Stop serving it; the next request
		// looks the release up afresh.
		s.dropLocked(entry.releaseID)
		return refusal(refuseUnknown, 0)
	}
	s.stats.Chunks++
	s.stats.Bytes += size
	return []outbound{{to: from, message: Message{
		Type: TypeChunk, ReleaseID: entry.releaseID, Transfer: entry.key,
		Index: message.Index, DataBase64: base64.StdEncoding.EncodeToString(data),
	}}}
}

func (s *seeder) windowLocked(peer string, now time.Time) *serveWindow {
	window, known := s.windows[peer]
	if known {
		if now.Sub(window.start) >= seedWindowDuration {
			*window = serveWindow{start: now}
		}
		return window
	}
	if len(s.windows) >= maxServeWindows {
		for other, candidate := range s.windows {
			if now.Sub(candidate.start) >= seedWindowDuration {
				delete(s.windows, other)
			}
		}
		if len(s.windows) >= maxServeWindows {
			// More distinct askers in one minute than are tracked. The
			// newcomer waits; the overall limit still holds meanwhile.
			return nil
		}
	}
	window = &serveWindow{start: now}
	s.windows[peer] = window
	return window
}

// allowLocked charges size bytes to the peer and to the overall budget, or
// reports how many seconds until there is room.
func (s *seeder) allowLocked(peer string, size int64, now time.Time) (int, bool) {
	tuning := s.engine.tuning
	if now.Sub(s.global.start) >= seedWindowDuration {
		s.global = serveWindow{start: now}
	}
	untilReset := func(start time.Time) int {
		seconds := int((seedWindowDuration - now.Sub(start) + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		return seconds
	}
	window := s.windowLocked(peer, now)
	if window == nil {
		return untilReset(s.global.start), false
	}
	if window.bytes+size > tuning.ServePeerBytesPerMinute {
		return untilReset(window.start), false
	}
	if s.global.bytes+size > tuning.ServeGlobalBytesPerMinute {
		return untilReset(s.global.start), false
	}
	window.bytes += size
	s.global.bytes += size
	return 0, true
}

func (s *seeder) dropLocked(releaseID string) {
	if entry, known := s.entries[releaseID]; known {
		_ = entry.file.Close()
		delete(s.entries, releaseID)
	}
}

// entryLocked returns what is served for one release, or nil when it is not
// served. An entry is trusted for SeedRevalidate and then looked up in the
// store again, so a release that was removed, damaged, or no longer verifies
// stops being served within that time.
func (s *seeder) entryLocked(releaseID string, now time.Time) *seedEntry {
	if proto.ValidateOTAReleaseID(releaseID) != nil {
		return nil
	}
	previous, known := s.entries[releaseID]
	if known && now.Sub(previous.checkedAt) < s.engine.tuning.SeedRevalidate {
		return previous
	}
	store := s.engine.store
	manifest, release, err := store.SignedManifest(releaseID)
	// Verified HERE, and complete. An unverified release is one this gateway
	// merely carries for an administrator; it vouches for nothing.
	if err != nil || release.Gateway != nassimhubnode.GatewayVerified || !release.ArtifactReady ||
		release.Size <= 0 || release.Size > proto.OTAMaxArtifactBytes {
		s.dropLocked(releaseID)
		return nil
	}
	// OpenArtifact hashes the whole file against the manifest before it
	// returns it, so what is cut into chunks below is what was signed for.
	file, _, err := store.OpenArtifact(releaseID)
	if err != nil {
		s.dropLocked(releaseID)
		return nil
	}
	entry := &seedEntry{
		releaseID:   releaseID,
		manifest:    manifest,
		manifestSHA: sha256.Sum256(manifest),
		chunkSize:   s.engine.tuning.SeedChunkSize,
		totalSize:   release.Size,
		file:        file,
		checkedAt:   now,
	}
	entry.chunkCount = int((entry.totalSize + int64(entry.chunkSize) - 1) / int64(entry.chunkSize))
	if known && previous.manifestSHA == entry.manifestSHA && previous.totalSize == entry.totalSize {
		// Same manifest, and the artifact just matched it again: the table
		// built last time still describes these bytes.
		entry.hashes = previous.hashes
	} else {
		entry.hashes, err = buildTable(func(buffer []byte, offset int64) error {
			_, readErr := file.ReadAt(buffer, offset)
			return readErr
		}, entry.totalSize, entry.chunkSize)
		if err != nil {
			_ = file.Close()
			s.dropLocked(releaseID)
			return nil
		}
	}
	entry.tableSHA = tableDigest(entry.totalSize, entry.chunkSize, entry.chunkCount, entry.hashes)
	entry.key = TransferKey(releaseID, entry.manifestSHA, entry.tableSHA)
	s.dropLocked(releaseID)
	if len(s.entries) >= maxSeedEntries {
		oldest := ""
		for id, candidate := range s.entries {
			if oldest == "" || candidate.checkedAt.Before(s.entries[oldest].checkedAt) {
				oldest = id
			}
		}
		s.dropLocked(oldest)
	}
	s.entries[releaseID] = entry
	return entry
}
