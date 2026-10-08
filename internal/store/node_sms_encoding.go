package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/human-agent65535/nassimhub-node/proto/mmclitext"
	"strings"
	"time"
)

// Repair only a previously observed Node delivery with the exact historical
// hash, SIM, timestamp and escaped body. Updating the same row preserves read,
// favorite and deletion state; the repair must never create a second delivery.
func repairLegacyNodeSMS(ctx context.Context, tx *sql.Tx, m HardwareMessage, reportedPeer string) (int64, error) {
	if m.Direction != "incoming" || !strings.HasPrefix(m.EndpointLineID, "nsh:") || !strings.HasPrefix(m.EndpointMessageID, "nsh-in:") || m.ICCID == "" || m.Timestamp.IsZero() {
		return 0, nil
	}
	encoded := mmclitext.Encode(m.Text)
	if encoded == m.Text {
		return 0, nil
	}
	// Ensure this is precisely the current versioned Node delivery identity.
	if nodeDeliveryID(m.ICCID, reportedPeer, m.Timestamp, m.Text) != m.EndpointMessageID {
		return 0, nil
	}
	oldID := nodeDeliveryID(m.ICCID, reportedPeer, m.Timestamp, encoded)
	var id int64
	var content, sim string
	err := tx.QueryRowContext(ctx, "SELECT id, content, iccid FROM sms WHERE endpoint_line_id=? AND endpoint_message_id=? AND type=1", m.EndpointLineID, oldID).Scan(&id, &content, &sim)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("query legacy Node SMS: %w", err)
	}
	if content != encoded || sim != m.ICCID {
		return 0, nil
	}
	if _, err = tx.ExecContext(ctx, "UPDATE sms SET content=?, endpoint_message_id=? WHERE id=?", m.Text, m.EndpointMessageID, id); err != nil {
		return 0, fmt.Errorf("repair legacy Node SMS: %w", err)
	}
	return id, nil
}
func nodeDeliveryID(sim, peer string, at time.Time, text string) string {
	d := sha256.New()
	fmt.Fprintf(d, "v1\x00%s\x00%s\x00%d\x00", sim, peer, at.UTC().Unix())
	d.Write([]byte(text))
	return "nsh-in:" + hex.EncodeToString(d.Sum(nil)[:16])
}
