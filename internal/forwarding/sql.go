// SPDX-License-Identifier: AGPL-3.0-only
package forwarding

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type SQLStore struct{ DB *sql.DB }
type Task struct {
	ID         int64  `json:"id"`
	EventID    string `json:"event_id"`
	LineID     string `json:"line_id"`
	RuleID     string `json:"rule_id"`
	ChannelID  string `json:"channel_id"`
	Position   int    `json:"position"`
	Logic      string `json:"logic"`
	Revision   int64  `json:"revision"`
	Status     string `json:"status"`
	Attempts   int    `json:"attempts"`
	ErrorClass string `json:"error_class"`
	NextAt     int64  `json:"next_at"`
	LeaseUntil int64  `json:"lease_until"`
	Token      string `json:"-"`
	Event      Event  `json:"-"`
}

func (s *SQLStore) Init(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return ErrUnavailable
	}
	// The trigger is inside the original SMS/call commit. AUTOINCREMENT is
	// separate from event rowid, so pruning cannot rewind an ingestion cursor.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	raw, _ := json.Marshal(DefaultConfig())
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS modemdeck_forwarding_config(id INTEGER PRIMARY KEY CHECK(id=1),revision INTEGER NOT NULL,config TEXT NOT NULL)`)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO modemdeck_forwarding_config(id,revision,config) VALUES(1,1,?)`, string(raw)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
 CREATE TABLE IF NOT EXISTS modemdeck_forwarding_config(id INTEGER PRIMARY KEY CHECK(id=1),revision INTEGER NOT NULL,config TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS modemdeck_forwarding_cursor(id INTEGER PRIMARY KEY CHECK(id=1),seq INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS modemdeck_forwarding_events(seq INTEGER PRIMARY KEY AUTOINCREMENT,event_id TEXT NOT NULL UNIQUE,revision INTEGER NOT NULL,enabled INTEGER NOT NULL,payload TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS modemdeck_forwarding_tasks(
 id INTEGER PRIMARY KEY AUTOINCREMENT,event_id TEXT NOT NULL,line_id TEXT NOT NULL,rule_id TEXT NOT NULL,channel_id TEXT NOT NULL,position INTEGER NOT NULL,logic TEXT NOT NULL,revision INTEGER NOT NULL,
 status TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,error_class TEXT NOT NULL DEFAULT '',next_at INTEGER NOT NULL,lease_until INTEGER NOT NULL DEFAULT 0,token TEXT NOT NULL DEFAULT '',payload TEXT NOT NULL,
 UNIQUE(event_id,rule_id,channel_id));
 CREATE INDEX IF NOT EXISTS modemdeck_forwarding_due ON modemdeck_forwarding_tasks(status,next_at,lease_until);
 CREATE INDEX IF NOT EXISTS modemdeck_forwarding_group ON modemdeck_forwarding_tasks(event_id,rule_id,position,status);
 CREATE TRIGGER IF NOT EXISTS modemdeck_forwarding_event AFTER INSERT ON modemdeck_notification_events BEGIN
 INSERT OR IGNORE INTO modemdeck_forwarding_events(event_id,revision,enabled,payload) VALUES(NEW.event_key,
 (SELECT revision FROM modemdeck_forwarding_config WHERE id=1),
 (SELECT COALESCE(json_extract(config,'$.enabled'),0) FROM modemdeck_forwarding_config WHERE id=1),json_object(
 'id',NEW.event_key,'type',NEW.event_type,'resource_id',NEW.resource_id,'line_id',NEW.line_id,'peer',NEW.peer,'body',NEW.body,'occurred_at',NEW.occurred_at,
 'endpoint_id',COALESCE((SELECT endpoint_line_id FROM sms WHERE CAST(id AS TEXT)=NEW.resource_id AND NEW.event_type='incoming_sms'),''),
 'endpoint_message_id',COALESCE((SELECT endpoint_message_id FROM sms WHERE CAST(id AS TEXT)=NEW.resource_id AND NEW.event_type='incoming_sms'),'')));
 END;
 INSERT OR IGNORE INTO modemdeck_forwarding_cursor(id,seq) VALUES(1,0);`)
	if err != nil {
		return fmt.Errorf("initialize forwarding journal: %w", err)
	}
	return tx.Commit()
}
func loadConfig(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (Config, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT config FROM modemdeck_forwarding_config WHERE id=1`).Scan(&raw)
	var c Config
	if err == nil {
		err = json.Unmarshal([]byte(raw), &c)
	}
	return c, err
}
func (s *SQLStore) Config(ctx context.Context) (Config, error) { return loadConfig(ctx, s.DB) }
func (s *SQLStore) Save(ctx context.Context, c Config) (Config, error) {
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Config{}, err
	}
	defer tx.Rollback()
	prior := c.Revision
	c.Revision++
	raw, err := json.Marshal(c)
	if err != nil {
		return Config{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE modemdeck_forwarding_config SET revision=?,config=? WHERE id=1 AND revision=?`, c.Revision, string(raw), prior)
	if err != nil {
		return Config{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return Config{}, ErrConflict
	}
	// Configuration changes must not make queued snapshots deliver under newly
	// assigned recipients. Running network operations may already have escaped.
	if _, err = tx.ExecContext(ctx, `UPDATE modemdeck_forwarding_tasks SET status='cancelled',error_class='configuration_changed',payload='{}' WHERE status='pending'`); err != nil {
		return Config{}, err
	}
	return c, tx.Commit()
}
func (s *SQLStore) Ingest(ctx context.Context, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := loadConfig(ctx, tx)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT seq,payload,revision,enabled FROM modemdeck_forwarding_events WHERE seq>(SELECT seq FROM modemdeck_forwarding_cursor WHERE id=1) ORDER BY seq LIMIT 100`)
	if err != nil {
		return err
	}
	type entry struct {
		seq      int64
		raw      string
		revision int64
		enabled  bool
	}
	batch := []entry{}
	for rows.Next() {
		var item entry
		if err = rows.Scan(&item.seq, &item.raw, &item.revision, &item.enabled); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range batch {
		var e Event
		if err = json.Unmarshal([]byte(item.raw), &e); err != nil {
			return err
		}
		e.Seq = item.seq
		// Preserve the original endpoint identities in PRIVATE durable payloads.
		if c.Enabled && item.enabled && item.revision == c.Revision {
			for _, r := range c.Rules {
				if !r.Matches(e) {
					continue
				}
				for position, id := range r.ChannelIDs {
					for _, channel := range c.Channels {
						if channel.ID != id || !channel.Enabled {
							continue
						}
						_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO modemdeck_forwarding_tasks(event_id,line_id,rule_id,channel_id,position,logic,revision,status,next_at,payload) VALUES(?,?,?,?,?,?,?,'pending',?,?)`, e.ID, e.LineID, r.ID, id, position, r.Logic, c.Revision, r.Quiet.Next(now).UnixMilli(), item.raw)
						if err != nil {
							return err
						}
					}
				}
				if r.Stop {
					break
				}
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE modemdeck_forwarding_cursor SET seq=? WHERE id=1`, item.seq); err != nil {
			return err
		}
	}
	// Keep a bounded private journal; task payloads remain available for explicit
	// failed retries. Only records consumed in this transaction may be removed.
	if _, err = tx.ExecContext(ctx, `DELETE FROM modemdeck_forwarding_events WHERE seq<=(SELECT seq FROM modemdeck_forwarding_cursor WHERE id=1)`); err != nil {
		return err
	}
	return tx.Commit()
}

const taskColumns = `id,event_id,line_id,rule_id,channel_id,position,logic,revision,status,attempts,error_class,next_at,lease_until,token,payload`

func scanTask(scan func(...any) error) (Task, error) {
	var t Task
	var raw string
	err := scan(&t.ID, &t.EventID, &t.LineID, &t.RuleID, &t.ChannelID, &t.Position, &t.Logic, &t.Revision, &t.Status, &t.Attempts, &t.ErrorClass, &t.NextAt, &t.LeaseUntil, &t.Token, &raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &t.Event)
	}
	return t, err
}
func (s *SQLStore) Due(ctx context.Context, now time.Time) ([]Task, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+taskColumns+` FROM modemdeck_forwarding_tasks t WHERE
 ((status='pending' AND next_at<=?) OR (status='sending' AND lease_until<=?))
 AND (logic='all' OR NOT EXISTS(SELECT 1 FROM modemdeck_forwarding_tasks prior WHERE prior.event_id=t.event_id AND prior.rule_id=t.rule_id AND prior.position<t.position AND prior.status IN ('pending','sending')))
 ORDER BY id LIMIT 100`, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *SQLStore) Claim(ctx context.Context, t Task, now time.Time) (Task, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return t, false, err
	}
	defer tx.Rollback()
	if t.Logic != "all" {
		var blocked, stopped int
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(CASE WHEN (?='until_success' AND status='sent') OR (?='until_fail' AND status IN ('failed','unknown','expired')) THEN 1 ELSE 0 END),0) FROM modemdeck_forwarding_tasks WHERE event_id=? AND rule_id=? AND position<? AND (status IN ('pending','sending') OR (?='until_success' AND status='sent') OR (?='until_fail' AND status IN ('failed','unknown','expired')))`, t.Logic, t.Logic, t.EventID, t.RuleID, t.Position, t.Logic, t.Logic).Scan(&blocked, &stopped)
		if err != nil {
			return t, false, err
		}
		if stopped != 0 {
			_, err = tx.ExecContext(ctx, `UPDATE modemdeck_forwarding_tasks SET status='cancelled',error_class='sender_logic_stopped',payload='{}' WHERE event_id=? AND rule_id=? AND position>=? AND status='pending'`, t.EventID, t.RuleID, t.Position)
			if err != nil {
				return t, false, err
			}
			return t, false, tx.Commit()
		}
		if blocked > 0 {
			return t, false, nil
		}
	}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		return t, false, err
	}
	t.Token = hex.EncodeToString(token)
	result, err := tx.ExecContext(ctx, `UPDATE modemdeck_forwarding_tasks SET status='sending',attempts=attempts+1,token=?,lease_until=? WHERE id=? AND attempts=? AND revision=(SELECT revision FROM modemdeck_forwarding_config WHERE id=1) AND ((status='pending' AND next_at<=?) OR (status='sending' AND lease_until<=?))`, t.Token, now.Add(45*time.Second).UnixMilli(), t.ID, t.Attempts, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return t, false, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return t, false, nil
	}
	t.Attempts++
	t.Status = "sending"
	return t, true, tx.Commit()
}
func stopFollowing(ctx context.Context, tx *sql.Tx, t Task, status string) error {
	if t.Logic == "until_success" && status == "sent" || t.Logic == "until_fail" && (status == "failed" || status == "expired" || status == "unknown") {
		_, err := tx.ExecContext(ctx, `UPDATE modemdeck_forwarding_tasks SET status='cancelled',error_class='sender_logic_stopped',payload='{}' WHERE event_id=? AND rule_id=? AND position>? AND status='pending'`, t.EventID, t.RuleID, t.Position)
		return err
	}
	return nil
}
func (s *SQLStore) Finish(ctx context.Context, t Task, status, code string, next time.Time) error {
	if status != "sent" && status != "pending" && status != "failed" && status != "unknown" {
		return ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE modemdeck_forwarding_tasks SET status=?,error_class=?,next_at=?,token='',lease_until=0,payload=CASE WHEN ? IN ('sent','unknown') THEN '{}' ELSE payload END WHERE id=? AND status='sending' AND token=?`, status, code, next.UnixMilli(), status, t.ID, t.Token)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	if err = stopFollowing(ctx, tx, t, status); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *SQLStore) Cancel(ctx context.Context, t Task, reason string) error {
	status := "cancelled"
	if reason == "expired" {
		status = "expired"
	}
	if reason == "attempt_limit" {
		status = "failed"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE modemdeck_forwarding_tasks SET status=?,error_class=?,token='',lease_until=0,payload=CASE WHEN ?='failed' THEN payload ELSE '{}' END WHERE id=? AND attempts=? AND status IN ('pending','sending')`, status, reason, status, t.ID, t.Attempts)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 1 {
		if err = stopFollowing(ctx, tx, t, status); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *SQLStore) History(ctx context.Context, before int64) ([]Task, error) {
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,event_id,line_id,rule_id,channel_id,position,logic,revision,status,attempts,error_class,next_at,lease_until FROM modemdeck_forwarding_tasks WHERE id<? ORDER BY id DESC LIMIT 100`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var t Task
		if err = rows.Scan(&t.ID, &t.EventID, &t.LineID, &t.RuleID, &t.ChannelID, &t.Position, &t.Logic, &t.Revision, &t.Status, &t.Attempts, &t.ErrorClass, &t.NextAt, &t.LeaseUntil); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *SQLStore) Retry(ctx context.Context, id int64, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := scanTask(tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM modemdeck_forwarding_tasks WHERE id=?`, id).Scan)
	if err != nil {
		return ErrDenied
	}
	c, err := loadConfig(ctx, tx)
	if err != nil {
		return err
	}
	if !c.Enabled || t.Revision != c.Revision || t.Status != "failed" || expired(t.Event, now) {
		return ErrDenied
	}
	_, err = tx.ExecContext(ctx, `UPDATE modemdeck_forwarding_tasks SET status='pending',attempts=0,error_class='manual_retry',next_at=?,token='',lease_until=0 WHERE id=? AND status='failed'`, now.UnixMilli(), id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *SQLStore) Task(ctx context.Context, id int64) (Task, error) {
	return scanTask(s.DB.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM modemdeck_forwarding_tasks WHERE id=?`, id).Scan)
}

// Keep private payloads no longer than the original 24h replay window plus a
// seven-day diagnostic retention. No payload is returned by History.
func (s *SQLStore) Prune(ctx context.Context, now time.Time) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE modemdeck_forwarding_tasks SET payload='{}' WHERE status='failed' AND julianday(json_extract(payload,'$.occurred_at'))<=julianday(?)`, now.Add(-MaxAge).Format(time.RFC3339Nano)); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM modemdeck_forwarding_tasks WHERE status NOT IN ('pending','sending') AND next_at<?`, now.Add(-7*24*time.Hour).UnixMilli())
	return err
}
