package store

import (
	"context"
	"database/sql"
	"github.com/human-agent65535/nassimhub-node/proto/mmclitext"
	"testing"
	"time"
)

func TestLegacyNodeSMSRepairKeepsRowAndDeletion(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "visible", true: "deleted"}[deleted], func(t *testing.T) {
			s := newHardwareTestStore(t)
			ctx := context.Background()
			at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			m := HardwareMessage{EndpointLineID: "nsh:fixture", ICCID: "fixture-sim", Number: "10000", Timestamp: at, Text: "【你好】验证短信😀", Direction: "incoming"}
			m.EndpointMessageID = nodeDeliveryID(m.ICCID, m.Number, at, m.Text)
			encoded := mmclitext.Encode(m.Text)
			oldID := nodeDeliveryID(m.ICCID, m.Number, at, encoded)
			var deletedAt any
			if deleted {
				deletedAt = at
			}
			r, err := s.database.ExecContext(ctx, "INSERT INTO sms(endpoint_line_id,endpoint_message_id,iccid,peer,content,type,deleted_at) VALUES(?,?,?,?,?,1,?)", m.EndpointLineID, oldID, m.ICCID, m.Number, encoded, deletedAt)
			if err != nil {
				t.Fatal(err)
			}
			wantID, _ := r.LastInsertId()
			tx, err := s.database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			id, err := repairLegacyNodeSMS(ctx, tx, m, m.Number)
			if err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if id != wantID {
				t.Fatalf("row identity changed: %d != %d", id, wantID)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			var text, endpoint string
			var deletion sql.NullTime
			var count int
			err = s.database.QueryRowContext(ctx, "SELECT content,endpoint_message_id,deleted_at FROM sms WHERE id=?", id).Scan(&text, &endpoint, &deletion)
			if err != nil {
				t.Fatal(err)
			}
			if text != m.Text || endpoint != m.EndpointMessageID || deletion.Valid != deleted {
				t.Fatal("content repair changed message state")
			}
			s.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM sms").Scan(&count)
			if count != 1 {
				t.Fatal("duplicated message")
			}
			tx, _ = s.database.BeginTx(ctx, nil)
			id, err = repairLegacyNodeSMS(ctx, tx, m, m.Number)
			tx.Rollback()
			if err != nil || id != 0 {
				t.Fatal("repair was not idempotent")
			}
		})
	}
}

func TestLegacyNodeSMSRepairCannotCrossLineSIMOrBody(t *testing.T) {
	for _, kind := range []string{"line", "sim", "body", "outgoing", "host", "identity"} {
		t.Run(kind, func(t *testing.T) {
			s := newHardwareTestStore(t)
			ctx := context.Background()
			at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			m := HardwareMessage{EndpointLineID: "nsh:fixture", ICCID: "fixture-sim", Number: "10000", Timestamp: at, Text: "原短信", Direction: "incoming"}
			m.EndpointMessageID = nodeDeliveryID(m.ICCID, m.Number, at, m.Text)
			encoded := mmclitext.Encode(m.Text)
			oldID := nodeDeliveryID(m.ICCID, m.Number, at, encoded)
			line, sim, body := m.EndpointLineID, m.ICCID, encoded
			switch kind {
			case "line":
				line = "nsh:other"
			case "sim":
				sim = "other-sim"
			case "body":
				body = "different"
			case "outgoing":
				m.Direction = "outgoing"
			case "host":
				m.EndpointLineID = "modemmanager:1"
			case "identity":
				m.EndpointMessageID = "nsh-in:invalid"
			}
			_, err := s.database.ExecContext(ctx, "INSERT INTO sms(endpoint_line_id,endpoint_message_id,iccid,content,type) VALUES(?,?,?,?,1)", line, oldID, sim, body)
			if err != nil {
				t.Fatal(err)
			}
			tx, _ := s.database.BeginTx(ctx, nil)
			defer tx.Rollback()
			id, err := repairLegacyNodeSMS(ctx, tx, m, m.Number)
			if err != nil || id != 0 {
				t.Fatal("unrelated message was repaired")
			}
		})
	}
}
