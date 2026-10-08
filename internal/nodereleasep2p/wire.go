package nodereleasep2p

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"

	"github.com/human-agent65535/nassimhub-node/proto"
)

// The wire protocol: seven small JSON datagrams over the same peer channel the
// signed gateway manifests already use. A datagram is the only primitive that
// channel offers -- there are no streams and no sessions -- so every message
// is self-describing and every answer names the transfer it belongs to.
//
//	query      -> "which Node releases do you hold?"
//	offer      <- one per release: the signed manifest and the transfer's shape
//	table.get  -> "send the chunk table of this transfer"
//	table      <- the per-chunk SHA-256 list
//	chunk.get  -> "send chunk N of this transfer"
//	chunk      <- the bytes
//	refuse     <- "not now" (rate limit) or "not from me" (no such transfer)
const (
	typePrefix   = "homesim.noderelease."
	TypeQuery    = typePrefix + "query.v1"
	TypeOffer    = typePrefix + "offer.v1"
	TypeTableGet = typePrefix + "table.get.v1"
	TypeTable    = typePrefix + "table.v1"
	TypeChunkGet = typePrefix + "chunk.get.v1"
	TypeChunk    = typePrefix + "chunk.v1"
	TypeRefuse   = typePrefix + "refuse.v1"
)

const (
	// DefaultChunkSize is what this gateway cuts an artifact into when it
	// seeds. Every gateway of one version uses the same size, which is what
	// lets two honest seeders arrive at the identical chunk table and so be
	// used together for one download.
	DefaultChunkSize = 64 << 10
	// MinChunkSize and MaxChunkSize bound what a peer may propose. The lower
	// bound keeps the chunk table of the largest artifact inside one message;
	// the upper bound keeps one chunk inside one message.
	MinChunkSize = 16 << 10
	MaxChunkSize = 256 << 10
	// MaxChunkCount follows from the two: the largest artifact in the
	// smallest chunks.
	MaxChunkCount = proto.OTAMaxArtifactBytes / MinChunkSize

	// MessageLimit refuses a message too big to be any of the seven before it
	// is decoded: one chunk at its size limit, base64-encoded, plus the
	// envelope.
	MessageLimit = MaxChunkSize*4/3 + 2048

	maxNonceLength = 64
)

// Refusal reasons a seeder gives.
const (
	refuseBusy    = "busy"
	refuseUnknown = "unknown"
)

// Message is every datagram of the protocol; Type says which fields count.
type Message struct {
	Type  string `json:"type"`
	Nonce string `json:"nonce,omitempty"`

	ReleaseID string `json:"release_id,omitempty"`
	// Transfer names one download unambiguously; see TransferKey.
	Transfer string `json:"transfer,omitempty"`

	// An offer.
	ManifestBase64 string `json:"manifest_base64,omitempty"`
	ChunkSize      int    `json:"chunk_size,omitempty"`
	ChunkCount     int    `json:"chunk_count,omitempty"`
	TotalSize      int64  `json:"total_size,omitempty"`
	TableSHA256    string `json:"table_sha256,omitempty"`

	// A table: the chunk digests, concatenated.
	TableBase64 string `json:"table_base64,omitempty"`

	// A chunk request or a chunk. Index is always written: zero is a chunk.
	Index      int    `json:"index"`
	DataBase64 string `json:"data_base64,omitempty"`

	// A refusal.
	Reason            string `json:"reason,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}

// Claims reports whether data looks like a message of this protocol, cheaply
// and without decoding it, so the receive loop can pass everything else on.
func Claims(data []byte) bool {
	return len(data) > 0 && len(data) <= MessageLimit && bytes.Contains(data, []byte(`"`+typePrefix))
}

func decodeMessage(data []byte) (Message, bool) {
	var message Message
	if !Claims(data) || json.Unmarshal(data, &message) != nil {
		return Message{}, false
	}
	switch message.Type {
	case TypeQuery, TypeOffer, TypeTableGet, TypeTable, TypeChunkGet, TypeChunk, TypeRefuse:
		return message, true
	}
	return Message{}, false
}

// validGeometry reports whether a transfer's shape is one this protocol
// allows: an artifact within the Node protocol's own bound, cut into chunks
// of an allowed size, with exactly the number of chunks that implies.
func validGeometry(totalSize int64, chunkSize, chunkCount int) bool {
	if totalSize <= 0 || totalSize > proto.OTAMaxArtifactBytes {
		return false
	}
	if chunkSize < MinChunkSize || chunkSize > MaxChunkSize {
		return false
	}
	expected := (totalSize + int64(chunkSize) - 1) / int64(chunkSize)
	return int64(chunkCount) == expected && chunkCount <= MaxChunkCount
}

// chunkLength is how many bytes chunk index holds: chunkSize, except the last.
func chunkLength(totalSize int64, chunkSize, index int) int {
	offset := int64(index) * int64(chunkSize)
	if remaining := totalSize - offset; remaining < int64(chunkSize) {
		return int(remaining)
	}
	return chunkSize
}

// tableDigest hashes a chunk table together with the shape it describes, so
// two tables that agree on every digest but not on the chunk size are two
// different tables.
func tableDigest(totalSize int64, chunkSize, chunkCount int, hashes []byte) [sha256.Size]byte {
	digest := sha256.New()
	digest.Write([]byte("nassimhub.node-release.table.v1\x00"))
	var shape [16]byte
	binary.BigEndian.PutUint64(shape[0:8], uint64(totalSize))
	binary.BigEndian.PutUint32(shape[8:12], uint32(chunkSize))
	binary.BigEndian.PutUint32(shape[12:16], uint32(chunkCount))
	digest.Write(shape[:])
	digest.Write(hashes)
	var sum [sha256.Size]byte
	digest.Sum(sum[:0])
	return sum
}

// TransferKey names one transfer: this release id, these exact manifest bytes,
// this chunk table. Chunks are only ever combined under one key, so bytes cut
// from another release, another signing of the same release, or another
// peer's idea of the table can never end up in the same file.
func TransferKey(releaseID string, manifestSHA, tableSHA [sha256.Size]byte) string {
	digest := sha256.New()
	digest.Write([]byte("nassimhub.node-release.transfer.v1\x00"))
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(releaseID)))
	digest.Write(length[:])
	digest.Write([]byte(releaseID))
	digest.Write(manifestSHA[:])
	digest.Write(tableSHA[:])
	return hex.EncodeToString(digest.Sum(nil))
}

// buildTable hashes an artifact chunk by chunk.
func buildTable(read func(buffer []byte, offset int64) error, totalSize int64, chunkSize int) ([]byte, error) {
	count := int((totalSize + int64(chunkSize) - 1) / int64(chunkSize))
	hashes := make([]byte, 0, count*sha256.Size)
	buffer := make([]byte, chunkSize)
	for index := 0; index < count; index++ {
		piece := buffer[:chunkLength(totalSize, chunkSize, index)]
		if err := read(piece, int64(index)*int64(chunkSize)); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(piece)
		hashes = append(hashes, sum[:]...)
	}
	return hashes, nil
}

func decodeDigest(value string) ([sha256.Size]byte, bool) {
	var sum [sha256.Size]byte
	if len(value) != hex.EncodedLen(sha256.Size) {
		return sum, false
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return sum, false
	}
	copy(sum[:], decoded)
	return sum, true
}

// decodeBounded decodes base64 that must not exceed limit bytes once decoded.
// The length is judged from the encoded form first, so an oversized field is
// refused without being decoded.
func decodeBounded(value string, limit int) ([]byte, bool) {
	if value == "" || base64.StdEncoding.DecodedLen(len(value)) > limit+3 {
		return nil, false
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) == 0 || len(decoded) > limit {
		return nil, false
	}
	return decoded, true
}
