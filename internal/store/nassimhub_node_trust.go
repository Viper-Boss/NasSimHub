package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// NasSimHub Node trust records.
//
// A row in nassimhub_nodes with pair_state 'paired' is the NAS side of an
// adopted Node: the device public key and the exact certificate it presented
// when the administrator confirmed it. Every later connection is pinned to that
// key. The certificate is kept so the pinned TLS configuration can run standard
// chain verification against it; a renewed certificate with the same key is
// re-pinned, a different key is refused.
//
// These rows are never returned by the general dashboard listing
// (NasSimHubNodes), which exposes operational fields only.

var (
	ErrNasSimHubNodeNotFound = errors.New("nassimhub node not found")
	errNasSimHubNodeInvalid  = errors.New("nassimhub node record is invalid")
)

// NasSimHub pair states, as stored. They match the table CHECK constraint.
const (
	NasSimHubPairStateUnpaired = "unpaired"
	NasSimHubPairStatePairing  = "pairing"
	NasSimHubPairStatePaired   = "paired"
)

// NasSimHubNodeEndpointPrefix is the endpoint-line namespace every Node line
// uses ("nsh:<device_id>:<slot>"). Host-agent endpoints never start with it.
const NasSimHubNodeEndpointPrefix = "nsh:"

type NasSimHubNodeRecord struct {
	ID           string
	Platform     string
	DisplayName  string
	PublicKey    []byte
	Certificate  []byte
	PairState    string
	Transport    string
	USBEndpoint  string
	WiFiEndpoint string
	Capabilities string
	LastSeenAt   time.Time
}

func (r NasSimHubNodeRecord) validate() error {
	if strings.TrimSpace(r.ID) == "" || len(r.PublicKey) == 0 {
		return fmt.Errorf("%w: id and public key are required", errNasSimHubNodeInvalid)
	}
	switch r.PairState {
	case NasSimHubPairStateUnpaired, NasSimHubPairStatePairing, NasSimHubPairStatePaired:
	default:
		return fmt.Errorf("%w: pair state %q", errNasSimHubNodeInvalid, r.PairState)
	}
	return nil
}

// SaveNasSimHubNode inserts or replaces one Node trust record.
func (s *Store) SaveNasSimHubNode(ctx context.Context, record NasSimHubNodeRecord) error {
	if s == nil || s.database == nil {
		return ErrDatabaseRequired
	}
	if err := record.validate(); err != nil {
		return err
	}
	var lastSeen any
	if !record.LastSeenAt.IsZero() {
		lastSeen = databaseTime(record.LastSeenAt)
	}
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO nassimhub_nodes (
			node_id, platform, display_name, public_key, certificate, pair_state,
			transport, usb_endpoint, wifi_endpoint, capabilities, last_seen_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(node_id) DO UPDATE SET
			platform = excluded.platform,
			display_name = excluded.display_name,
			public_key = excluded.public_key,
			certificate = excluded.certificate,
			pair_state = excluded.pair_state,
			transport = excluded.transport,
			usb_endpoint = excluded.usb_endpoint,
			wifi_endpoint = excluded.wifi_endpoint,
			capabilities = excluded.capabilities,
			last_seen_at = COALESCE(excluded.last_seen_at, nassimhub_nodes.last_seen_at),
			revision = nassimhub_nodes.revision + 1,
			updated_at = CURRENT_TIMESTAMP`,
		strings.TrimSpace(record.ID),
		strings.TrimSpace(record.Platform),
		strings.TrimSpace(record.DisplayName),
		record.PublicKey,
		record.Certificate,
		record.PairState,
		strings.TrimSpace(record.Transport),
		strings.TrimSpace(record.USBEndpoint),
		strings.TrimSpace(record.WiFiEndpoint),
		record.Capabilities,
		lastSeen,
	)
	if err != nil {
		return fmt.Errorf("save nassimhub node: %w", err)
	}
	return nil
}

// NasSimHubNodeRecords returns every stored trust record, including keys.
// Callers are the Node fleet, never an HTTP response.
func (s *Store) NasSimHubNodeRecords(ctx context.Context) ([]NasSimHubNodeRecord, error) {
	if s == nil || s.database == nil {
		return nil, ErrDatabaseRequired
	}
	rows, err := s.database.QueryContext(ctx, `
		SELECT node_id, platform, display_name, public_key, COALESCE(certificate, x''),
		       pair_state, transport, usb_endpoint, wifi_endpoint, capabilities,
		       COALESCE(CAST(last_seen_at AS TEXT), '')
		FROM nassimhub_nodes ORDER BY node_id LIMIT 129`)
	if err != nil {
		return nil, fmt.Errorf("list nassimhub node records: %w", err)
	}
	defer rows.Close()
	records := make([]NasSimHubNodeRecord, 0)
	for rows.Next() {
		if len(records) == 128 {
			return nil, errors.New("more than 128 NasSimHub nodes")
		}
		record, err := scanNasSimHubNodeRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read nassimhub node records: %w", err)
	}
	return records, nil
}

// NasSimHubNodeRecord returns one trust record.
func (s *Store) NasSimHubNodeRecord(ctx context.Context, nodeID string) (NasSimHubNodeRecord, error) {
	if s == nil || s.database == nil {
		return NasSimHubNodeRecord{}, ErrDatabaseRequired
	}
	row := s.database.QueryRowContext(ctx, `
		SELECT node_id, platform, display_name, public_key, COALESCE(certificate, x''),
		       pair_state, transport, usb_endpoint, wifi_endpoint, capabilities,
		       COALESCE(CAST(last_seen_at AS TEXT), '')
		FROM nassimhub_nodes WHERE node_id = ?`, strings.TrimSpace(nodeID))
	record, err := scanNasSimHubNodeRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return NasSimHubNodeRecord{}, ErrNasSimHubNodeNotFound
	}
	return record, err
}

func scanNasSimHubNodeRecord(row rowScanner) (NasSimHubNodeRecord, error) {
	var (
		record   NasSimHubNodeRecord
		lastSeen string
	)
	if err := row.Scan(
		&record.ID, &record.Platform, &record.DisplayName, &record.PublicKey,
		&record.Certificate, &record.PairState, &record.Transport,
		&record.USBEndpoint, &record.WiFiEndpoint, &record.Capabilities, &lastSeen,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NasSimHubNodeRecord{}, err
		}
		return NasSimHubNodeRecord{}, fmt.Errorf("scan nassimhub node record: %w", err)
	}
	if lastSeen != "" {
		if parsed, ok := parseNasSimHubTime(lastSeen); ok {
			record.LastSeenAt = parsed
		}
	}
	return record, nil
}

func parseNasSimHubTime(value string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

// DeleteNasSimHubNode removes the trust record. SIM mappings and sync rows go
// with it (ON DELETE CASCADE); message history keeps its node_id so the inbox
// does not lose what the device already delivered.
func (s *Store) DeleteNasSimHubNode(ctx context.Context, nodeID string) error {
	if s == nil || s.database == nil {
		return ErrDatabaseRequired
	}
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin nassimhub node delete: %w", err)
	}
	defer transaction.Rollback()
	nodeID = strings.TrimSpace(nodeID)
	for _, statement := range []string{
		`DELETE FROM nassimhub_node_sims WHERE node_id = ?`,
		`DELETE FROM nassimhub_node_hardware_sync WHERE node_id = ?`,
		`DELETE FROM nassimhub_nodes WHERE node_id = ?`,
	} {
		if _, err := transaction.ExecContext(ctx, statement, nodeID); err != nil {
			return fmt.Errorf("delete nassimhub node: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit nassimhub node delete: %w", err)
	}
	return nil
}

// TouchNasSimHubNode records liveness and the endpoints currently known.
func (s *Store) TouchNasSimHubNode(
	ctx context.Context,
	nodeID, transport, usbEndpoint, wifiEndpoint string,
	seenAt time.Time,
) error {
	if s == nil || s.database == nil {
		return ErrDatabaseRequired
	}
	_, err := s.database.ExecContext(ctx, `
		UPDATE nassimhub_nodes
		SET transport = ?, usb_endpoint = ?, wifi_endpoint = ?,
		    last_seen_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE node_id = ? AND pair_state = 'paired'`,
		strings.TrimSpace(transport), strings.TrimSpace(usbEndpoint),
		strings.TrimSpace(wifiEndpoint), databaseTime(seenAt), strings.TrimSpace(nodeID))
	if err != nil {
		return fmt.Errorf("touch nassimhub node: %w", err)
	}
	return nil
}

// NasSimHubNodeSIM is what a Node reported about the card in one slot.
type NasSimHubNodeSIM struct {
	NodeID       string
	SIMID        string
	Slot         int
	ICCID        string
	IMSI         string
	LocalPhone   string
	OperatorName string
	Present      bool
}

// RecordNasSimHubNodeSIM stores the card a Node reports and binds it to the
// stable line the hardware projection resolved for the same ICCID, so a SIM
// moved between a Node and a host modem keeps one line and one history.
func (s *Store) RecordNasSimHubNodeSIM(ctx context.Context, sim NasSimHubNodeSIM) error {
	if s == nil || s.database == nil {
		return ErrDatabaseRequired
	}
	sim.NodeID = strings.TrimSpace(sim.NodeID)
	sim.SIMID = strings.TrimSpace(sim.SIMID)
	if sim.NodeID == "" || sim.SIMID == "" {
		return fmt.Errorf("%w: node and sim ids are required", errNasSimHubNodeInvalid)
	}
	lineID := ""
	if iccid := strings.TrimSpace(sim.ICCID); iccid != "" {
		err := s.database.QueryRowContext(ctx,
			`SELECT line_id FROM sim_cards WHERE iccid = ?`, iccid).Scan(&lineID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("resolve nassimhub sim line: %w", err)
		}
	}
	_, err := s.database.ExecContext(ctx, `
		INSERT INTO nassimhub_node_sims (
			node_id, sim_id, slot, iccid, imsi, local_phone, operator_name, present,
			line_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(node_id, sim_id) DO UPDATE SET
			slot = excluded.slot, iccid = excluded.iccid, imsi = excluded.imsi,
			local_phone = excluded.local_phone, operator_name = excluded.operator_name,
			present = excluded.present,
			line_id = CASE WHEN excluded.line_id <> '' THEN excluded.line_id ELSE nassimhub_node_sims.line_id END,
			revision = nassimhub_node_sims.revision + 1,
			updated_at = CURRENT_TIMESTAMP`,
		sim.NodeID, sim.SIMID, sim.Slot, strings.TrimSpace(sim.ICCID),
		strings.TrimSpace(sim.IMSI), strings.TrimSpace(sim.LocalPhone),
		strings.TrimSpace(sim.OperatorName), sim.Present, lineID)
	if err != nil {
		return fmt.Errorf("record nassimhub node sim: %w", err)
	}
	return nil
}

// TagNasSimHubMessages stamps node_id and sim_id on messages that arrived
// through a Node line. The hardware projection writes them like any other
// line; this is the one Node-specific column pair, filled after commit.
func (s *Store) TagNasSimHubMessages(ctx context.Context) (int64, error) {
	if s == nil || s.database == nil {
		return 0, ErrDatabaseRequired
	}
	result, err := s.database.ExecContext(ctx, `
		UPDATE sms
		SET node_id = substr(endpoint_line_id, 5, instr(substr(endpoint_line_id, 5), ':') - 1),
		    sim_id = iccid
		WHERE endpoint_line_id LIKE 'nsh:%:%' AND node_id = ''`)
	if err != nil {
		return 0, fmt.Errorf("tag nassimhub messages: %w", err)
	}
	return result.RowsAffected()
}
