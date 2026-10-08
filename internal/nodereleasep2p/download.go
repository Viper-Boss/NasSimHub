package nodereleasep2p

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/nassimhub-node/proto"
)

// A download on disk.
//
//	<dir>/<release_id>/manifest.json   the signed manifest, byte for byte
//	<dir>/<release_id>/part            the artifact being assembled, full size
//	<dir>/<release_id>/bitmap          one bit per chunk that is in part
//	<dir>/<release_id>/state.json      when it started and last progressed
//	<dir>/<release_id>/table.json      the transfer key and the chunk table
//
// table.json is written last and atomically; a directory without it is not a
// download and is removed. For each chunk the bytes are written and synced
// BEFORE its bit is, so a power cut can leave a chunk present and unmarked
// (it is fetched again) but never marked and absent. And because a disk can
// lie about that too, nothing on it is taken on trust after a restart: every
// marked chunk is hashed against the table again, and the finished file is
// hashed against the signed manifest before it goes anywhere.
const (
	manifestFileName = "manifest.json"
	partFileName     = "part"
	bitmapFileName   = "bitmap"
	stateFileName    = "state.json"
	tableFileName    = "table.json"

	tableFileVersion = 1
	// progressSaveEvery is how often the time of last progress is written.
	// It only decides when a download counts as abandoned, a matter of hours.
	progressSaveEvery = time.Minute
)

// States of a download, as the status document names them.
const (
	stateFetchingTable = "fetching_table"
	stateDownloading   = "downloading"
	stateWaiting       = "waiting_for_peers"
	statePaused        = "paused"
	stateVerifying     = "verifying"
	stateImporting     = "importing"
	stateImportPending = "import_pending"
	stateImported      = "imported"
	stateFailed        = "failed"
	stateAbandoned     = "abandoned"
	stateEvicted       = "evicted"
)

type download struct {
	releaseID   string
	key         string
	manifest    []byte
	manifestSHA [sha256.Size]byte
	tableSHA    [sha256.Size]byte
	artifactSHA string
	version     string
	platform    string
	arch        string
	channel     string
	chunkSize   int
	chunkCount  int
	totalSize   int64

	dir        string
	hashes     []byte // chunkCount digests; nil until the table arrived
	bitmap     []byte
	received   int
	part       *os.File
	bitmapFile *os.File

	state string
	// busy is set while the finished file is being verified and imported
	// outside the lock. A busy download is never evicted, abandoned or
	// written to.
	busy           bool
	peers          map[string]*downloadPeer
	everPeers      map[string]struct{} // everyone who vouched for this table
	inflight       map[int]*request
	tableRequest   *request
	tableAttempts  int
	importAttempts int
	importRetryAt  time.Time
	lastError      string
	startedAt      time.Time
	progressAt     time.Time
	savedAt        time.Time
}

type downloadPeer struct {
	inflight    int
	failures    int
	pausedUntil time.Time
}

type request struct {
	peer   string
	sentAt time.Time
}

type tableFile struct {
	Version        int      `json:"version"`
	ReleaseID      string   `json:"release_id"`
	Transfer       string   `json:"transfer"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	TableSHA256    string   `json:"table_sha256"`
	ChunkSize      int      `json:"chunk_size"`
	ChunkCount     int      `json:"chunk_count"`
	TotalSize      int64    `json:"total_size"`
	Hashes         []string `json:"hashes"`
}

type stateFile struct {
	StartedAt  time.Time `json:"started_at"`
	ProgressAt time.Time `json:"progress_at"`
}

func (d *download) has(index int) bool { return d.bitmap[index/8]&(1<<(index%8)) != 0 }

func (d *download) chunkHash(index int) []byte {
	return d.hashes[index*sha256.Size : (index+1)*sha256.Size]
}

func (d *download) closeFiles() {
	if d.part != nil {
		_ = d.part.Close()
		d.part = nil
	}
	if d.bitmapFile != nil {
		_ = d.bitmapFile.Close()
		d.bitmapFile = nil
	}
}

// reservedLocked is the cache in use: the full size of every download, from
// the moment it is admitted. Reserving the whole artifact up front is what
// makes "cache full" a refusal at the door rather than a file that stops
// half way.
func (e *Engine) reservedLocked() int64 {
	total := int64(0)
	for _, active := range e.downloads {
		total += active.totalSize
	}
	return total
}

func (e *Engine) usablePeersLocked(d *download, now time.Time) int {
	usable := 0
	for peer, state := range d.peers {
		if !e.blockedLocked(peer, now) && !now.Before(state.pausedUntil) {
			usable++
		}
	}
	return usable
}

func (e *Engine) describeLocked(d *download, now time.Time) DownloadStatus {
	status := DownloadStatus{
		ReleaseID: d.releaseID, Version: d.version, Platform: d.platform, Arch: d.arch,
		ReceivedChunks: d.received, ChunkCount: d.chunkCount, TotalBytes: d.totalSize,
		Peers: len(d.peers), State: d.state, Error: d.lastError,
		StartedAt: d.startedAt.UTC(), ProgressAt: d.progressAt.UTC(),
	}
	status.ReceivedBytes = int64(d.received) * int64(d.chunkSize)
	if d.received > 0 && d.bitmap != nil && d.has(d.chunkCount-1) {
		status.ReceivedBytes -= int64(d.chunkSize - chunkLength(d.totalSize, d.chunkSize, d.chunkCount-1))
	}
	if d.state == stateDownloading || d.state == stateFetchingTable {
		if on, _ := e.downloadState(); !on {
			status.State = statePaused
		} else if e.usablePeersLocked(d, now) == 0 {
			status.State = stateWaiting
		}
	}
	return status
}

// ---------------------------------------------------------------------------
// Offers
// ---------------------------------------------------------------------------

func (e *Engine) onOfferLocked(from string, message Message) {
	now := e.now()
	e.expireQueriesLocked(now)
	pending, asked := e.queries[message.Nonce]
	if !asked || pending.peer != from || pending.remaining <= 0 {
		// Not an answer to a question this gateway asked that peer: dropped
		// unread, however well signed. This is what bounds a flood of offers
		// to the peers asked, times the offers each may answer with.
		e.counters.offersUnsolicited++
		return
	}
	pending.remaining--
	e.counters.offersReceived++
	if on, _ := e.downloadState(); !on || e.blockedLocked(from, now) {
		return
	}

	proposal := offer{
		releaseID:  message.ReleaseID,
		chunkSize:  message.ChunkSize,
		chunkCount: message.ChunkCount,
		totalSize:  message.TotalSize,
	}
	proposal.tableSHA, proposal.tableOK = decodeDigest(message.TableSHA256)
	if base64.StdEncoding.DecodedLen(len(message.ManifestBase64)) > proto.OTAMaxManifestBytes+3 {
		proposal.rawOversize = true
	} else if raw, err := base64.StdEncoding.DecodeString(message.ManifestBase64); err == nil {
		proposal.raw = raw
	}

	// Three things can be decided by comparing with bytes this gateway has
	// ALREADY verified, without verifying anything again.
	key := ""
	if proto.ValidateOTAReleaseID(proposal.releaseID) == nil && proposal.tableOK &&
		len(proposal.raw) > 0 && len(proposal.raw) <= proto.OTAMaxManifestBytes {
		key = TransferKey(proposal.releaseID, sha256.Sum256(proposal.raw), proposal.tableSHA)
		if until, proven := e.badTables[key]; proven && now.Before(until) {
			e.refuseLocked(from, proposal.releaseID, refuse(ReasonTableMismatch, penaltyForged,
				"the offer vouches for a chunk table already proven false"), now)
			return
		}
		if active, downloading := e.downloads[proposal.releaseID]; downloading {
			if active.key == key {
				// The same manifest bytes and the same table: one more peer
				// to draw the same chunks from.
				e.attachLocked(active, from)
				e.scheduleLocked(active, now)
				return
			}
			// The same release under another manifest or another table.
			// Its chunks cannot be told apart from this download's by index,
			// so they are never mixed in.
			e.refuseLocked(from, proposal.releaseID, refuse(ReasonMixed, penaltyNone,
				"the offer is for another transfer of a release already being fetched"), now)
			return
		}
		if stored, err := e.store.Release(proposal.releaseID); err == nil && stored.ArtifactReady {
			if held, _, err := e.store.SignedManifest(proposal.releaseID); err == nil && bytes.Equal(held, proposal.raw) {
				// An honest peer offering what is already here. Not a refusal.
				e.counters.alreadyHeld++
				return
			}
		}
		if remembered, known := e.memo[key]; known && now.Before(remembered.until) {
			e.refuseLocked(from, proposal.releaseID, remembered.refusal, now)
			return
		}
	}

	admitted, refused := e.admitLocked(proposal)
	if refused != nil {
		e.refuseLocked(from, proposal.releaseID, refused, now)
		if key != "" {
			e.rememberRefusalLocked(key, refused, now)
		}
		return
	}
	active := &download{
		releaseID:   admitted.releaseID,
		key:         key,
		manifest:    admitted.raw,
		manifestSHA: sha256.Sum256(admitted.raw),
		tableSHA:    admitted.tableSHA,
		artifactSHA: strings.ToLower(admitted.artifact.SHA256),
		version:     admitted.manifest.Version,
		platform:    string(admitted.manifest.Platform),
		arch:        admitted.artifact.Arch,
		channel:     string(admitted.manifest.Channel),
		chunkSize:   admitted.chunkSize,
		chunkCount:  admitted.chunkCount,
		totalSize:   admitted.totalSize,
		dir:         filepath.Join(e.dir, admitted.releaseID),
		state:       stateFetchingTable,
		peers:       make(map[string]*downloadPeer),
		everPeers:   make(map[string]struct{}),
		inflight:    make(map[int]*request),
		startedAt:   now,
		progressAt:  now,
	}
	e.downloads[active.releaseID] = active
	e.attachLocked(active, from)
	e.requestTableLocked(active, now)
	e.log.Info("fetching a node release from peers", "component", "updates",
		"release_id", active.releaseID, "version", active.version, "bytes", active.totalSize)
}

const maxEverPeers = 32

func (e *Engine) attachLocked(d *download, peer string) {
	if _, attached := d.peers[peer]; attached || len(d.peers) >= e.tuning.MaxPeersPerDownload {
		return
	}
	d.peers[peer] = &downloadPeer{}
	if len(d.everPeers) < maxEverPeers {
		d.everPeers[peer] = struct{}{}
	}
}

func (e *Engine) detachLocked(d *download, peer string) {
	if _, attached := d.peers[peer]; !attached {
		return
	}
	delete(d.peers, peer)
	for index, pending := range d.inflight {
		if pending.peer == peer {
			delete(d.inflight, index)
		}
	}
	if d.tableRequest != nil && d.tableRequest.peer == peer {
		d.tableRequest = nil
	}
}

// restLocked notes that a peer left a request unanswered: it is rested, for
// longer each time, and dropped from the download after too many in a row.
func (e *Engine) restLocked(d *download, peer string, now time.Time) {
	state, attached := d.peers[peer]
	if !attached {
		return
	}
	state.failures++
	if state.failures >= e.tuning.MaxPeerFailures {
		e.detachLocked(d, peer)
		return
	}
	backoff := e.tuning.RetryBase
	for step := 1; step < state.failures && backoff < e.tuning.RetryMax; step++ {
		backoff *= 2
	}
	if backoff > e.tuning.RetryMax {
		backoff = e.tuning.RetryMax
	}
	state.pausedUntil = now.Add(backoff)
}

// sortedUsableLocked lists the peers a request may go to now, in a fixed
// order so that the same situation always produces the same requests.
func (e *Engine) sortedUsableLocked(d *download, now time.Time) []string {
	names := make([]string, 0, len(d.peers))
	for peer, state := range d.peers {
		if !e.blockedLocked(peer, now) && !now.Before(state.pausedUntil) {
			names = append(names, peer)
		}
	}
	sort.Strings(names)
	return names
}

// ---------------------------------------------------------------------------
// The chunk table
// ---------------------------------------------------------------------------

func (e *Engine) requestTableLocked(d *download, now time.Time) {
	if d.tableRequest != nil {
		return
	}
	names := e.sortedUsableLocked(d, now)
	if len(names) == 0 {
		return
	}
	// Rotate through the peers on successive attempts.
	peer := names[d.tableAttempts%len(names)]
	d.tableAttempts++
	d.tableRequest = &request{peer: peer, sentAt: now}
	e.queueLocked(peer, Message{Type: TypeTableGet, ReleaseID: d.releaseID, Transfer: d.key})
}

func (e *Engine) onTableLocked(from string, message Message) {
	now := e.now()
	d, known := e.downloads[message.ReleaseID]
	if !known || d.key != message.Transfer {
		e.counters.mixups++
		return
	}
	if _, attached := d.peers[from]; !attached || d.state != stateFetchingTable || d.busy {
		e.counters.unexpectedMessages++
		return
	}
	if on, _ := e.downloadState(); !on {
		return
	}
	hashes, ok := decodeBounded(message.TableBase64, MaxChunkCount*sha256.Size)
	if !ok || len(hashes) != d.chunkCount*sha256.Size ||
		tableDigest(d.totalSize, d.chunkSize, d.chunkCount, hashes) != d.tableSHA {
		// The peer advertised one table and sent another.
		e.refuseLocked(from, d.releaseID, refuse(ReasonTableMismatch, penaltyForged,
			"the chunk table does not match the hash it was offered under"), now)
		if len(d.peers) == 0 {
			e.failLocked(d, "no peer supplied the chunk table it advertised", now)
		}
		return
	}
	d.hashes = hashes
	d.bitmap = make([]byte, (d.chunkCount+7)/8)
	if err := e.createLocked(d); err != nil {
		e.failLocked(d, "the download cache could not be written", now)
		return
	}
	d.tableRequest = nil
	d.state = stateDownloading
	d.progressAt = now
	e.scheduleLocked(d, now)
}

// createLocked lays a download out on disk. table.json goes last: until it
// exists the directory is not a download, so a power cut anywhere in here
// leaves something the next start simply removes.
func (e *Engine) createLocked(d *download) error {
	if err := os.RemoveAll(d.dir); err != nil {
		return err
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(d.dir, manifestFileName), d.manifest); err != nil {
		return err
	}
	part, err := os.OpenFile(filepath.Join(d.dir, partFileName), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	d.part = part
	if err := part.Truncate(d.totalSize); err != nil {
		return err
	}
	bitmapFile, err := os.OpenFile(filepath.Join(d.dir, bitmapFileName), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	d.bitmapFile = bitmapFile
	if _, err := bitmapFile.WriteAt(d.bitmap, 0); err != nil {
		return err
	}
	if err := bitmapFile.Sync(); err != nil {
		return err
	}
	if err := e.saveStateLocked(d); err != nil {
		return err
	}
	table := tableFile{
		Version: tableFileVersion, ReleaseID: d.releaseID, Transfer: d.key,
		ManifestSHA256: hex.EncodeToString(d.manifestSHA[:]), TableSHA256: hex.EncodeToString(d.tableSHA[:]),
		ChunkSize: d.chunkSize, ChunkCount: d.chunkCount, TotalSize: d.totalSize,
		Hashes: make([]string, d.chunkCount),
	}
	for index := range table.Hashes {
		table.Hashes[index] = hex.EncodeToString(d.chunkHash(index))
	}
	encoded, err := json.Marshal(table)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(d.dir, tableFileName), encoded)
}

func (e *Engine) saveStateLocked(d *download) error {
	encoded, err := json.Marshal(stateFile{StartedAt: d.startedAt.UTC(), ProgressAt: d.progressAt.UTC()})
	if err != nil {
		return err
	}
	d.savedAt = d.progressAt
	return writeAtomic(filepath.Join(d.dir, stateFileName), encoded)
}

// ---------------------------------------------------------------------------
// Chunks
// ---------------------------------------------------------------------------

// scheduleLocked asks for missing chunks, lowest first, spreading them over
// the peers that vouched for this exact transfer.
func (e *Engine) scheduleLocked(d *download, now time.Time) {
	if d.state != stateDownloading || d.busy {
		return
	}
	if on, _ := e.downloadState(); !on {
		return
	}
	names := e.sortedUsableLocked(d, now)
	if len(names) == 0 {
		return
	}
	for index := 0; index < d.chunkCount && len(d.inflight) < e.tuning.MaxInflight; index++ {
		if d.has(index) || d.inflight[index] != nil {
			continue
		}
		chosen := ""
		for _, peer := range names {
			if state := d.peers[peer]; state.inflight < e.tuning.PeerWindow &&
				(chosen == "" || state.inflight < d.peers[chosen].inflight) {
				chosen = peer
			}
		}
		if chosen == "" {
			return
		}
		d.inflight[index] = &request{peer: chosen, sentAt: now}
		d.peers[chosen].inflight++
		e.queueLocked(chosen, Message{Type: TypeChunkGet, ReleaseID: d.releaseID, Transfer: d.key, Index: index})
	}
}

func (e *Engine) settleLocked(d *download, index int, from string) {
	if pending := d.inflight[index]; pending != nil && pending.peer == from {
		delete(d.inflight, index)
		if state := d.peers[from]; state != nil && state.inflight > 0 {
			state.inflight--
		}
	}
}

func (e *Engine) onChunkLocked(from string, message Message) {
	now := e.now()
	d, known := e.downloads[message.ReleaseID]
	if !known || d.key != message.Transfer {
		// A chunk of another release, another signing of this release, or
		// another table: refused before its bytes are looked at.
		e.counters.mixups++
		return
	}
	state, attached := d.peers[from]
	if !attached || d.state != stateDownloading || d.busy {
		e.counters.unexpectedMessages++
		return
	}
	if on, _ := e.downloadState(); !on {
		return
	}
	index := message.Index
	if index < 0 || index >= d.chunkCount {
		e.counters.unexpectedMessages++
		return
	}
	e.settleLocked(d, index, from)
	if d.has(index) {
		// Already here and already checked. Arriving twice changes nothing.
		e.counters.duplicateChunks++
		e.scheduleLocked(d, now)
		return
	}
	data, ok := decodeBounded(message.DataBase64, MaxChunkSize)
	if ok && len(data) == chunkLength(d.totalSize, d.chunkSize, index) {
		sum := sha256.Sum256(data)
		ok = bytes.Equal(sum[:], d.chunkHash(index))
	} else {
		ok = false
	}
	if !ok {
		// The table says what this chunk hashes to and this is not it. That
		// is all the table is for: throwing the chunk away now, and knowing
		// which peer sent it.
		e.refuseLocked(from, d.releaseID, refuse(ReasonChunkMismatch, penaltyForged,
			"chunk %d does not match the chunk table", index), now)
		e.scheduleLocked(d, now)
		return
	}

	// Bytes first, and durable, then the bit.
	if _, err := d.part.WriteAt(data, int64(index)*int64(d.chunkSize)); err != nil {
		e.failLocked(d, "the download cache could not be written", now)
		return
	}
	if err := d.part.Sync(); err != nil {
		e.failLocked(d, "the download cache could not be written", now)
		return
	}
	if hook := e.hooks.afterChunkWrite; hook != nil && hook() != nil {
		e.crashed = true
		return
	}
	d.bitmap[index/8] |= 1 << (index % 8)
	if _, err := d.bitmapFile.WriteAt(d.bitmap[index/8:index/8+1], int64(index/8)); err != nil {
		e.failLocked(d, "the download cache could not be written", now)
		return
	}
	if err := d.bitmapFile.Sync(); err != nil {
		e.failLocked(d, "the download cache could not be written", now)
		return
	}
	d.received++
	d.progressAt = now
	state.failures, state.pausedUntil = 0, time.Time{}
	e.counters.chunksReceived++
	e.counters.bytesReceived += int64(len(data))
	if d.received == d.chunkCount {
		d.state = stateVerifying
		_ = e.saveStateLocked(d)
		return
	}
	if now.Sub(d.savedAt) >= progressSaveEvery {
		_ = e.saveStateLocked(d)
	}
	e.scheduleLocked(d, now)
}

func (e *Engine) onRefuseLocked(from string, message Message) {
	now := e.now()
	d, known := e.downloads[message.ReleaseID]
	if !known || d.key != message.Transfer {
		e.counters.mixups++
		return
	}
	state, attached := d.peers[from]
	if !attached || d.busy {
		e.counters.unexpectedMessages++
		return
	}
	if message.Reason != refuseBusy {
		// The peer no longer serves this transfer. Not held against it: a
		// release can be removed from a store at any time.
		e.detachLocked(d, from)
		e.scheduleLocked(d, now)
		return
	}
	// Rate limited. Wait as asked, within reason, and give its outstanding
	// requests to whoever else can serve them.
	wait := time.Duration(message.RetryAfterSeconds) * time.Second
	if wait < 5*time.Second {
		wait = 5 * time.Second
	}
	if wait > 5*time.Minute {
		wait = 5 * time.Minute
	}
	state.pausedUntil = now.Add(wait)
	state.inflight = 0
	for index, pending := range d.inflight {
		if pending.peer == from {
			delete(d.inflight, index)
		}
	}
	if d.tableRequest != nil && d.tableRequest.peer == from {
		d.tableRequest = nil
		d.tableAttempts--
	}
	e.scheduleLocked(d, now)
}

// ---------------------------------------------------------------------------
// Time
// ---------------------------------------------------------------------------

// Tick does everything that happens because time passed: requests that went
// unanswered are given to another peer, downloads that stopped are given up,
// and a download whose last chunk arrived is verified and imported.
func (e *Engine) Tick() {
	e.mu.Lock()
	if e.crashed {
		e.mu.Unlock()
		return
	}
	now := e.now()
	e.expireQueriesLocked(now)
	for key, entry := range e.memo {
		if !now.Before(entry.until) {
			delete(e.memo, key)
		}
	}
	for key, until := range e.badTables {
		if !now.Before(until) {
			delete(e.badTables, key)
		}
	}
	enabled, _ := e.downloadState()
	ids := make([]string, 0, len(e.downloads))
	for id := range e.downloads {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var ready []*download
	for _, id := range ids {
		d := e.downloads[id]
		if d.busy {
			continue
		}
		if now.Sub(d.progressAt) >= e.tuning.AbandonAfter {
			e.counters.downloadsAbandoned++
			e.removeLocked(d, stateAbandoned, "no progress for too long", now)
			continue
		}
		switch d.state {
		case stateFetchingTable:
			if !enabled {
				continue
			}
			if pending := d.tableRequest; pending != nil && now.Sub(pending.sentAt) >= e.tuning.TableTimeout {
				e.counters.requestTimeouts++
				d.tableRequest = nil
				e.restLocked(d, pending.peer, now)
			}
			if d.tableRequest == nil {
				if d.tableAttempts >= e.tuning.TableAttempts {
					e.failLocked(d, "the chunk table could not be fetched", now)
					continue
				}
				e.requestTableLocked(d, now)
			}
		case stateDownloading:
			if !enabled {
				continue
			}
			// Several requests to one peer running out together are one
			// silence, not several: the peer is rested once for the sweep.
			quiet := map[string]struct{}{}
			for index, pending := range d.inflight {
				if now.Sub(pending.sentAt) < e.tuning.ChunkTimeout {
					continue
				}
				e.counters.requestTimeouts++
				delete(d.inflight, index)
				if state := d.peers[pending.peer]; state != nil && state.inflight > 0 {
					state.inflight--
				}
				quiet[pending.peer] = struct{}{}
			}
			for peer := range quiet {
				e.restLocked(d, peer, now)
			}
			e.scheduleLocked(d, now)
		case stateVerifying:
			ready = append(ready, d)
		case stateImportPending:
			if !now.Before(d.importRetryAt) {
				ready = append(ready, d)
			}
		}
	}
	out := e.takeOutboxLocked()
	e.mu.Unlock()
	e.flush(out)
	for _, d := range ready {
		e.finalize(d)
	}
}

// evictStalledLocked removes the download that has gone longest without
// progress, if any has gone long enough, and reports whether it did. A
// download being verified or imported is never a candidate.
func (e *Engine) evictStalledLocked() bool {
	now := e.now()
	var victim *download
	for _, d := range e.downloads {
		if d.busy || d.state == stateVerifying || d.state == stateImporting {
			continue
		}
		if now.Sub(d.progressAt) < e.tuning.StallAfter {
			continue
		}
		if victim == nil || d.progressAt.Before(victim.progressAt) {
			victim = d
		}
	}
	if victim == nil {
		return false
	}
	e.counters.downloadsEvicted++
	e.removeLocked(victim, stateEvicted, "stalled; evicted to make room", now)
	return true
}

func (e *Engine) failLocked(d *download, reason string, now time.Time) {
	e.counters.downloadsFailed++
	e.removeLocked(d, stateFailed, reason, now)
}

// removeLocked ends a download: its files go, its reservation with them, and
// what became of it is kept for the status page.
func (e *Engine) removeLocked(d *download, state, reason string, now time.Time) {
	final := e.describeLocked(d, now)
	final.State, final.Error, final.Peers, final.FinishedAt = state, reason, 0, now.UTC()
	d.closeFiles()
	_ = os.RemoveAll(d.dir)
	delete(e.downloads, d.releaseID)
	e.recordHistoryLocked(final)
	if state != stateImported {
		e.log.Warn("node release download ended", "component", "updates",
			"release_id", d.releaseID, "state", state, "reason", reason)
	}
}

// ---------------------------------------------------------------------------
// The finished file
// ---------------------------------------------------------------------------

type outcome int

const (
	outcomeImported outcome = iota
	outcomeForged
	outcomeRejected
	outcomeRetry
	outcomeCrashed
)

// finalize verifies a fully assembled artifact against the signed manifest
// and hands it to the release store. The hashing and the import run outside
// the engine's lock; busy keeps everything else off the download meanwhile.
func (e *Engine) finalize(d *download) {
	e.mu.Lock()
	if e.crashed || d.busy || e.downloads[d.releaseID] != d ||
		(d.state != stateVerifying && d.state != stateImportPending) {
		e.mu.Unlock()
		return
	}
	d.busy = true
	d.state = stateVerifying
	hooks := e.hooks
	e.mu.Unlock()

	result, detail := e.verifyAndImport(d, hooks)

	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	d.busy = false
	switch result {
	case outcomeCrashed:
		e.crashed = true
	case outcomeImported:
		e.counters.downloadsCompleted++
		e.removeLocked(d, stateImported, "", now)
		e.log.Info("node release fetched from peers and imported", "component", "updates",
			"release_id", d.releaseID, "version", d.version)
	case outcomeForged:
		// Every chunk matched the table and the whole does not match the
		// manifest: the table was false. Whoever vouched for it vouched for
		// garbage, and nothing obtained under it is kept.
		e.counters.refused[ReasonHashMismatch]++
		e.badTables[d.key] = now.Add(e.tuning.BadTableBan)
		peers := make([]string, 0, len(d.everPeers))
		for peer := range d.everPeers {
			peers = append(peers, peer)
		}
		e.counters.downloadsFailed++
		e.removeLocked(d, stateFailed, "the assembled artifact does not match the signed manifest", now)
		for _, peer := range peers {
			e.strikeLocked(peer, now)
		}
	case outcomeRejected:
		e.failLocked(d, "the release store refused it: "+detail, now)
	case outcomeRetry:
		d.importAttempts++
		if d.importAttempts >= e.tuning.ImportAttempts {
			e.failLocked(d, "the release could not be imported: "+detail, now)
			return
		}
		d.state, d.lastError = stateImportPending, detail
		d.importRetryAt = now.Add(e.tuning.ImportRetry)
	}
}

func (e *Engine) verifyAndImport(d *download, hooks hooks) (outcome, string) {
	path := filepath.Join(d.dir, partFileName)
	file, err := os.Open(path)
	if err != nil {
		return outcomeRetry, "the assembled artifact could not be read"
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return outcomeRetry, "the assembled artifact could not be read"
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, io.LimitReader(file, d.totalSize+1)); err != nil {
		return outcomeRetry, "the assembled artifact could not be read"
	}
	// THE check. Everything before it was bookkeeping.
	if info.Size() != d.totalSize || hex.EncodeToString(digest.Sum(nil)) != d.artifactSHA {
		return outcomeForged, ""
	}
	if hooks.beforeImport != nil && hooks.beforeImport() != nil {
		return outcomeCrashed, ""
	}
	e.mu.Lock()
	d.state = stateImporting
	e.mu.Unlock()
	// The same two doors an upload goes through. Both verify again.
	if _, err := e.store.ImportManifestFrom(d.manifest, nassimhubnode.NodeReleaseSourcePeer); err != nil {
		return importFailure(err)
	}
	if hooks.betweenImports != nil && hooks.betweenImports() != nil {
		return outcomeCrashed, ""
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return outcomeRetry, "the assembled artifact could not be read"
	}
	if _, err := e.store.ImportArtifact(d.releaseID, file); err != nil {
		return importFailure(err)
	}
	if hooks.afterImport != nil && hooks.afterImport() != nil {
		return outcomeCrashed, ""
	}
	return outcomeImported, ""
}

func importFailure(err error) (outcome, string) {
	detail := err.Error()
	if len(detail) > 200 {
		detail = detail[:200]
	}
	if errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) || errors.Is(err, nassimhubnode.ErrNodeReleaseUnknown) {
		return outcomeRejected, detail
	}
	return outcomeRetry, detail
}

// ---------------------------------------------------------------------------
// Picking up after a restart
// ---------------------------------------------------------------------------

// resumeLocked reads back the downloads a previous run left behind. Anything
// that does not check out in full is removed; what remains is re-verified
// chunk by chunk and waits for a peer to offer the same transfer again.
func (e *Engine) resumeLocked() {
	entries, err := os.ReadDir(e.dir)
	if err != nil {
		return
	}
	// Without release keys or with no cache allowed, nothing here can ever be
	// finished, and a bounded cache does not keep what it cannot use.
	purge := len(e.keys) == 0 || e.cacheCap == 0
	now := e.now()
	for _, entry := range entries {
		path := filepath.Join(e.dir, entry.Name())
		if purge || !entry.IsDir() || proto.ValidateOTAReleaseID(entry.Name()) != nil {
			_ = os.RemoveAll(path)
			continue
		}
		d, err := e.loadLocked(entry.Name(), path, now)
		if err != nil {
			e.log.Warn("discarding a node release download that cannot be resumed", "component", "updates",
				"release_id", entry.Name(), "error", err)
			_ = os.RemoveAll(path)
			continue
		}
		e.downloads[d.releaseID] = d
	}
	// The limits may be tighter than when these were started.
	for len(e.downloads) > e.maxDownloads || e.reservedLocked() > e.cacheCap {
		var victim *download
		for _, d := range e.downloads {
			if victim == nil || d.progressAt.Before(victim.progressAt) {
				victim = d
			}
		}
		e.counters.downloadsEvicted++
		e.removeLocked(victim, stateEvicted, "over the configured cache limits", now)
	}
}

func (e *Engine) loadLocked(releaseID, path string, now time.Time) (*download, error) {
	encoded, err := readBounded(filepath.Join(path, tableFileName), 1<<20)
	if err != nil {
		return nil, errors.New("no readable chunk table")
	}
	var table tableFile
	if json.Unmarshal(encoded, &table) != nil || table.Version != tableFileVersion || table.ReleaseID != releaseID ||
		!validGeometry(table.TotalSize, table.ChunkSize, table.ChunkCount) || len(table.Hashes) != table.ChunkCount {
		return nil, errors.New("the chunk table is damaged")
	}
	hashes := make([]byte, 0, table.ChunkCount*sha256.Size)
	for _, value := range table.Hashes {
		sum, ok := decodeDigest(value)
		if !ok {
			return nil, errors.New("the chunk table is damaged")
		}
		hashes = append(hashes, sum[:]...)
	}
	raw, err := readBounded(filepath.Join(path, manifestFileName), proto.OTAMaxManifestBytes)
	if err != nil {
		return nil, errors.New("no readable manifest")
	}
	manifestSHA := sha256.Sum256(raw)
	tableSHA := tableDigest(table.TotalSize, table.ChunkSize, table.ChunkCount, hashes)
	if hex.EncodeToString(manifestSHA[:]) != table.ManifestSHA256 || hex.EncodeToString(tableSHA[:]) != table.TableSHA256 ||
		TransferKey(releaseID, manifestSHA, tableSHA) != table.Transfer {
		return nil, errors.New("the manifest and the chunk table do not belong together")
	}
	// The manifest sat on disk; the keys may have changed. It is verified as
	// if it had just arrived.
	checked := &candidate{offer: offer{
		releaseID: releaseID, raw: raw, chunkSize: table.ChunkSize, chunkCount: table.ChunkCount,
		totalSize: table.TotalSize, tableSHA: tableSHA, tableOK: true,
	}}
	for _, check := range []func(*Engine, *candidate) *refusal{
		(*Engine).checkManifestSize, (*Engine).checkSignature, (*Engine).checkSchema, (*Engine).checkArtifact,
	} {
		if refused := check(e, checked); refused != nil {
			return nil, fmt.Errorf("%s: %s", refused.reason, refused.detail)
		}
	}
	if stored, err := e.store.Release(releaseID); err == nil {
		held, _, err := e.store.SignedManifest(releaseID)
		if err != nil || !bytes.Equal(held, raw) {
			return nil, errors.New("a different manifest is stored under this release id")
		}
		if stored.ArtifactReady {
			return nil, errors.New("the release is already stored")
		}
	}

	d := &download{
		releaseID: releaseID, key: table.Transfer, manifest: raw, manifestSHA: manifestSHA, tableSHA: tableSHA,
		artifactSHA: strings.ToLower(checked.artifact.SHA256), version: checked.manifest.Version,
		platform: string(checked.manifest.Platform), arch: checked.artifact.Arch, channel: string(checked.manifest.Channel),
		chunkSize: table.ChunkSize, chunkCount: table.ChunkCount, totalSize: table.TotalSize,
		dir: path, hashes: hashes, state: stateDownloading,
		peers: make(map[string]*downloadPeer), everPeers: make(map[string]struct{}), inflight: make(map[int]*request),
		startedAt: now, progressAt: now,
	}
	var saved stateFile
	if encoded, err := readBounded(filepath.Join(path, stateFileName), 4<<10); err == nil &&
		json.Unmarshal(encoded, &saved) == nil && !saved.ProgressAt.IsZero() && !saved.ProgressAt.After(now) {
		d.progressAt, d.savedAt = saved.ProgressAt, saved.ProgressAt
		if !saved.StartedAt.IsZero() {
			d.startedAt = saved.StartedAt
		}
	}

	part, err := os.OpenFile(filepath.Join(path, partFileName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	d.part = part
	// Whatever length a power cut left it at, it is the artifact's length
	// again; anything cut off reads as zeros and fails its chunk's hash.
	if err := part.Truncate(d.totalSize); err != nil {
		d.closeFiles()
		return nil, err
	}
	d.bitmap = make([]byte, (d.chunkCount+7)/8)
	if stored, err := readBounded(filepath.Join(path, bitmapFileName), int64(len(d.bitmap))); err == nil && len(stored) == len(d.bitmap) {
		copy(d.bitmap, stored)
	}
	// The bitmap says which chunks to LOOK at. The table says which are there.
	buffer := make([]byte, d.chunkSize)
	for index := 0; index < d.chunkCount; index++ {
		if !d.has(index) {
			continue
		}
		piece := buffer[:chunkLength(d.totalSize, d.chunkSize, index)]
		_, readErr := io.ReadFull(io.NewSectionReader(part, int64(index)*int64(d.chunkSize), int64(len(piece))), piece)
		sum := sha256.Sum256(piece)
		if readErr != nil || !bytes.Equal(sum[:], d.chunkHash(index)) {
			d.bitmap[index/8] &^= 1 << (index % 8)
			continue
		}
		d.received++
	}
	// Bits past the last chunk mean nothing and are not kept.
	for index := d.chunkCount; index < len(d.bitmap)*8; index++ {
		d.bitmap[index/8] &^= 1 << (index % 8)
	}
	bitmapFile, err := os.OpenFile(filepath.Join(path, bitmapFileName), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		d.closeFiles()
		return nil, err
	}
	d.bitmapFile = bitmapFile
	if _, err := bitmapFile.WriteAt(d.bitmap, 0); err == nil {
		err = bitmapFile.Sync()
	}
	if err != nil {
		d.closeFiles()
		return nil, err
	}
	if d.received == d.chunkCount {
		d.state = stateVerifying
	}
	return d, nil
}

// ---------------------------------------------------------------------------
// Files
// ---------------------------------------------------------------------------

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, errors.New("file is larger than expected")
	}
	return content, nil
}

// writeAtomic replaces a small file so that a reader, or a restart, sees the
// old content or the new and never a mixture.
func writeAtomic(path string, content []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".tmp-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if handle, err := os.Open(directory); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}
