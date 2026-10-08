package trustengine

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SQLStore keeps records and the journal in the gateway database (tables
// trust_records, security_events, security_meta).
type SQLStore struct{ database *sql.DB }

// NewSQLStore wraps the gateway database.
func NewSQLStore(database *sql.DB) (*SQLStore, error) {
	if database == nil {
		return nil, errors.New("trust store requires a database")
	}
	return &SQLStore{database: database}, nil
}

const headKey = "head"

type storedHead struct {
	Seq        int64  `json:"seq"`
	Hash       string `json:"hash"`
	Signature  string `json:"signature"`
	SealedFrom int64  `json:"sealed_from,omitempty"`
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readHead(ctx context.Context, querier rowQuerier) (Head, bool, error) {
	var raw string
	err := querier.QueryRowContext(ctx, `SELECT value FROM security_meta WHERE key = ?`, headKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Head{}, false, nil
	}
	if err != nil {
		return Head{}, false, fmt.Errorf("read security journal head: %w", err)
	}
	var stored storedHead
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		// Present but unreadable: report a head that cannot verify rather than
		// "no head", which would read as an empty journal.
		return Head{Seq: -1}, true, nil
	}
	signature, _ := base64.StdEncoding.DecodeString(stored.Signature)
	return Head{Seq: stored.Seq, Hash: stored.Hash, Signature: signature, SealedFrom: stored.SealedFrom}, true, nil
}

// Head returns the signed head.
func (s *SQLStore) Head(ctx context.Context) (Head, bool, error) { return readHead(ctx, s.database) }

const recordColumns = `subject_id, state, binding_hash, sim_hash, stable_seconds, required_seconds, trusted_seconds,
	sim_changes, identity_changes, integrity_failures, risk_score, otp_level, otp_limited, manual, reason, generation,
	last_event_hash, updated_at, signature, policy_generation, hardware_hash, modem_changes`

// Records returns every trust record.
func (s *SQLStore) Records(ctx context.Context) ([]Record, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT `+recordColumns+` FROM trust_records ORDER BY subject_id`)
	if err != nil {
		return nil, fmt.Errorf("read trust records: %w", err)
	}
	defer rows.Close()
	var records []Record
	for rows.Next() {
		var (
			record    Record
			state     string
			updatedAt string
		)
		if err := rows.Scan(&record.SubjectID, &state, &record.BindingHash, &record.SIMHash, &record.StableSeconds,
			&record.RequiredSeconds, &record.TrustedSeconds, &record.SIMChanges, &record.IdentityChanges,
			&record.IntegrityFailures, &record.RiskScore, &record.OTPLevel, &record.OTPLimited, &record.Manual,
			&record.Reason, &record.Generation, &record.LastEventHash, &updatedAt, &record.Signature,
			&record.PolicyGeneration, &record.HardwareHash, &record.ModemChanges); err != nil {
			return nil, fmt.Errorf("read trust record: %w", err)
		}
		record.State = State(state)
		// A time that does not parse leaves the zero time, which no signature
		// was made over: the record then fails verification, as it should.
		record.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		records = append(records, record)
	}
	return records, rows.Err()
}

func scanEvent(scan func(...any) error) (Event, error) {
	var (
		event Event
		at    string
	)
	if err := scan(&event.Seq, &at, &event.SubjectID, &event.Kind, &event.Cause, &event.Source, &event.Result,
		&event.PrevHash, &event.Hash); err != nil {
		return Event{}, err
	}
	event.At, _ = time.Parse(time.RFC3339Nano, at)
	return event, nil
}

const eventColumns = `seq, at, subject_id, kind, cause, source, result, prev_hash, hash`

// Events returns up to limit events after afterSeq, oldest first.
func (s *SQLStore) Events(ctx context.Context, afterSeq int64, limit int, subject string) ([]Event, error) {
	query, arguments := `SELECT `+eventColumns+` FROM security_events WHERE seq > ?`, []any{afterSeq}
	if subject != "" {
		query += ` AND subject_id = ?`
		arguments = append(arguments, subject)
	}
	rows, err := s.database.QueryContext(ctx, query+` ORDER BY seq LIMIT ?`, append(arguments, limit)...)
	if err != nil {
		return nil, fmt.Errorf("read security events: %w", err)
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		event, err := scanEvent(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("read security event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// EventsBefore returns up to limit events with seq < beforeSeq, newest first.
// beforeSeq <= 0 starts from the newest event.
func (s *SQLStore) EventsBefore(ctx context.Context, beforeSeq int64, limit int, subject string) ([]Event, error) {
	query, arguments := `SELECT `+eventColumns+` FROM security_events WHERE 1 = 1`, []any{}
	if beforeSeq > 0 {
		query += ` AND seq < ?`
		arguments = append(arguments, beforeSeq)
	}
	if subject != "" {
		query += ` AND subject_id = ?`
		arguments = append(arguments, subject)
	}
	rows, err := s.database.QueryContext(ctx, query+` ORDER BY seq DESC LIMIT ?`, append(arguments, limit)...)
	if err != nil {
		return nil, fmt.Errorf("read security events: %w", err)
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		event, err := scanEvent(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("read security event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// EventsFrom visits every event with seq >= fromSeq in order.
func (s *SQLStore) EventsFrom(ctx context.Context, fromSeq int64, visit func(Event) error) error {
	rows, err := s.database.QueryContext(ctx, `SELECT `+eventColumns+` FROM security_events WHERE seq >= ? ORDER BY seq`, fromSeq)
	if err != nil {
		return fmt.Errorf("read security events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		event, err := scanEvent(rows.Scan)
		if err != nil {
			return fmt.Errorf("read security event: %w", err)
		}
		if err := visit(event); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Commit appends events, writes the record and replaces the head, atomically.
func (s *SQLStore) Commit(
	ctx context.Context, record *Record, events []Event,
	seal func(previous Head, events []Event, record *Record) (Head, error),
) error {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	previous, exists, err := readHead(ctx, transaction)
	if err != nil {
		return err
	}
	// A journal with events and no readable head is damaged. Recording
	// continues from its tail - a refusal must still be journalled - but no
	// new head is written for it: only an administrator's reseal may vouch
	// for a journal again. Writing one here would quietly turn a removed tail
	// into a journal that verifies.
	healthy := exists && previous.Seq >= 0
	if !healthy {
		var seq sql.NullInt64
		var hash sql.NullString
		if err := transaction.QueryRowContext(ctx, `SELECT seq, hash FROM security_events ORDER BY seq DESC LIMIT 1`).Scan(&seq, &hash); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read security journal tail: %w", err)
		}
		healthy = !exists && !seq.Valid // a journal that has never held anything
		previous = Head{Seq: seq.Int64, Hash: hash.String}
	}
	head, err := seal(previous, events, record)
	if err != nil {
		return err
	}
	for _, event := range events {
		if _, err := transaction.ExecContext(ctx, `INSERT INTO security_events (`+eventColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			event.Seq, event.At.UTC().Format(time.RFC3339Nano), event.SubjectID, event.Kind, event.Cause, event.Source,
			event.Result, event.PrevHash, event.Hash); err != nil {
			return fmt.Errorf("append security event: %w", err)
		}
	}
	if record != nil {
		if _, err := transaction.ExecContext(ctx, `INSERT INTO trust_records (`+recordColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(subject_id) DO UPDATE SET state = excluded.state, binding_hash = excluded.binding_hash,
				sim_hash = excluded.sim_hash, stable_seconds = excluded.stable_seconds,
				required_seconds = excluded.required_seconds, trusted_seconds = excluded.trusted_seconds,
				sim_changes = excluded.sim_changes, identity_changes = excluded.identity_changes,
				integrity_failures = excluded.integrity_failures, risk_score = excluded.risk_score,
				otp_level = excluded.otp_level, otp_limited = excluded.otp_limited, manual = excluded.manual,
				reason = excluded.reason, generation = excluded.generation, last_event_hash = excluded.last_event_hash,
				updated_at = excluded.updated_at, signature = excluded.signature,
				policy_generation = excluded.policy_generation, hardware_hash = excluded.hardware_hash,
				modem_changes = excluded.modem_changes`,
			record.SubjectID, string(record.State), record.BindingHash, record.SIMHash, record.StableSeconds,
			record.RequiredSeconds, record.TrustedSeconds, record.SIMChanges, record.IdentityChanges,
			record.IntegrityFailures, record.RiskScore, record.OTPLevel, record.OTPLimited, record.Manual, record.Reason,
			record.Generation, record.LastEventHash, record.UpdatedAt.UTC().Format(time.RFC3339Nano), record.Signature,
			record.PolicyGeneration, record.HardwareHash, record.ModemChanges); err != nil {
			return fmt.Errorf("write trust record: %w", err)
		}
	}
	if resealed := head.SealedFrom == head.Seq && head.Seq > 0; len(events) > 0 && (healthy || resealed) {
		encoded, err := json.Marshal(storedHead{Seq: head.Seq, Hash: head.Hash,
			Signature: base64.StdEncoding.EncodeToString(head.Signature), SealedFrom: head.SealedFrom})
		if err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `INSERT INTO security_meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, headKey, string(encoded)); err != nil {
			return fmt.Errorf("write security journal head: %w", err)
		}
	}
	return transaction.Commit()
}

// Meta reads a marker.
func (s *SQLStore) Meta(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.database.QueryRowContext(ctx, `SELECT value FROM security_meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// SetMeta writes a marker.
func (s *SQLStore) SetMeta(ctx context.Context, key, value string) error {
	if key == headKey {
		return errors.New("the journal head is written only with the journal")
	}
	_, err := s.database.ExecContext(ctx, `INSERT INTO security_meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
