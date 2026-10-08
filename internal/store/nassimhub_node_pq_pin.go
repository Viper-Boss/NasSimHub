package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// NasSimHubNodePQPin is a Node's pinned post-quantum identity key: the key the
// Node proved possession of, alongside its Ed25519 key, the first time a dual
// signature verified. The Ed25519 pin in nassimhub_nodes is unchanged and
// remains what identifies the device.
type NasSimHubNodePQPin struct {
	NodeID    string
	Algorithm string
	PublicKey []byte
	PinnedAt  time.Time
}

// ErrNasSimHubNodePQPinNotFound reports a Node with no post-quantum pin.
var ErrNasSimHubNodePQPinNotFound = errors.New("nassimhub node has no post-quantum pin")

// NasSimHubNodePQPin returns the pin for one Node.
func (s *Store) NasSimHubNodePQPin(ctx context.Context, nodeID string) (NasSimHubNodePQPin, error) {
	if s == nil || s.database == nil {
		return NasSimHubNodePQPin{}, ErrDatabaseRequired
	}
	var (
		pin      NasSimHubNodePQPin
		pinnedAt string
	)
	err := s.database.QueryRowContext(ctx, `
		SELECT node_id, algorithm, public_key, COALESCE(CAST(pinned_at AS TEXT), '')
		FROM nassimhub_node_pq_pins WHERE node_id = ?`, strings.TrimSpace(nodeID),
	).Scan(&pin.NodeID, &pin.Algorithm, &pin.PublicKey, &pinnedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return NasSimHubNodePQPin{}, ErrNasSimHubNodePQPinNotFound
	}
	if err != nil {
		return NasSimHubNodePQPin{}, fmt.Errorf("read nassimhub node post-quantum pin: %w", err)
	}
	if parsed, ok := parseNasSimHubTime(pinnedAt); ok {
		pin.PinnedAt = parsed
	}
	return pin, nil
}

// PinNasSimHubNodePQ records a Node's post-quantum key if, and only if, the
// Node has none recorded.
//
// It never replaces a pin. Replacing one is the administrator's decision -
// ResetNasSimHubNodePQPin followed by a fresh verification - and making the
// insert conditional in SQL rather than in the caller is what keeps two
// concurrent polls from each believing they pinned a different key. The result
// reports whether this call created the pin.
func (s *Store) PinNasSimHubNodePQ(ctx context.Context, pin NasSimHubNodePQPin) (bool, error) {
	if s == nil || s.database == nil {
		return false, ErrDatabaseRequired
	}
	if strings.TrimSpace(pin.NodeID) == "" || strings.TrimSpace(pin.Algorithm) == "" || len(pin.PublicKey) == 0 {
		return false, errors.New("a post-quantum pin requires a node, an algorithm and a key")
	}
	pinnedAt := pin.PinnedAt
	if pinnedAt.IsZero() {
		pinnedAt = time.Now()
	}
	result, err := s.database.ExecContext(ctx, `
		INSERT INTO nassimhub_node_pq_pins (node_id, algorithm, public_key, pinned_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(node_id) DO NOTHING`,
		strings.TrimSpace(pin.NodeID), strings.TrimSpace(pin.Algorithm), pin.PublicKey, databaseTime(pinnedAt),
	)
	if err != nil {
		return false, fmt.Errorf("pin nassimhub node post-quantum key: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// ResetNasSimHubNodePQPin forgets a Node's post-quantum pin. The next verified
// dual signature pins whatever key the Node then presents.
func (s *Store) ResetNasSimHubNodePQPin(ctx context.Context, nodeID string) (bool, error) {
	if s == nil || s.database == nil {
		return false, ErrDatabaseRequired
	}
	result, err := s.database.ExecContext(ctx,
		`DELETE FROM nassimhub_node_pq_pins WHERE node_id = ?`, strings.TrimSpace(nodeID))
	if err != nil {
		return false, fmt.Errorf("reset nassimhub node post-quantum pin: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}
