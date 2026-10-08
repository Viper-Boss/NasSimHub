package database

import (
	"context"
	"database/sql"
	"fmt"
)

// Security additions: tables that record trust the gateway has established and
// the events that changed it.
//
// The same rules as the Node migration apply, for the same reasons: every
// change is a new table, a new index or an added column, nothing is dropped,
// recreated or backfilled, and the DDL text here is byte-identical to schema.sql so a fresh
// database and an upgraded one cannot differ.
//
// It runs after migrateNasSimHubNodeSchema because the pin table references
// nassimhub_nodes, and before the schemaContains check for the reason recorded
// in nassimhub_node_migration.go.

// securityTables are created in order.
var securityTables = []struct {
	name       string
	definition string
}{
	{
		// A Node's ADDITIONAL post-quantum identity key, recorded the first
		// time the Node proved possession of it with a signature that
		// verified. It is a separate table rather than columns on
		// nassimhub_nodes so that an older gateway build, restored over this
		// database, sees a registry row of exactly the shape it always had.
		//
		// The row goes with the Node: unpairing deletes the registry row and
		// the pin with it.
		name: "nassimhub_node_pq_pins",
		definition: `CREATE TABLE nassimhub_node_pq_pins (
			node_id TEXT PRIMARY KEY,
			algorithm TEXT NOT NULL,
			public_key BLOB NOT NULL,
			pinned_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (node_id) REFERENCES nassimhub_nodes(node_id) ON DELETE CASCADE ON UPDATE CASCADE
		)`,
	},
	{
		// One row per device this gateway has ever adopted: how far it may be
		// used, and the counters that decided it. Deliberately NOT a child of
		// nassimhub_nodes: unpairing a device and adopting it again must not
		// hand it a clean record. Hashes and counters only - no SIM identifier.
		name: "trust_records",
		definition: `CREATE TABLE trust_records (
			subject_id TEXT PRIMARY KEY,
			state TEXT NOT NULL,
			binding_hash TEXT NOT NULL DEFAULT '',
			sim_hash TEXT NOT NULL DEFAULT '',
			stable_seconds INTEGER NOT NULL DEFAULT 0,
			required_seconds INTEGER NOT NULL DEFAULT 0,
			trusted_seconds INTEGER NOT NULL DEFAULT 0,
			sim_changes INTEGER NOT NULL DEFAULT 0,
			identity_changes INTEGER NOT NULL DEFAULT 0,
			integrity_failures INTEGER NOT NULL DEFAULT 0,
			risk_score INTEGER NOT NULL DEFAULT 0,
			otp_level INTEGER NOT NULL DEFAULT 0,
			otp_limited INTEGER NOT NULL DEFAULT 0,
			manual INTEGER NOT NULL DEFAULT 0,
			reason TEXT NOT NULL DEFAULT '',
			generation INTEGER NOT NULL DEFAULT 0,
			last_event_hash TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL,
			signature BLOB NOT NULL,
			policy_generation INTEGER NOT NULL DEFAULT 0,
			hardware_hash TEXT NOT NULL DEFAULT '',
			modem_changes INTEGER NOT NULL DEFAULT 0
		)`,
	},
	{
		// The security journal: an append-only hash chain. Rows are never
		// updated or deleted by the gateway.
		name: "security_events",
		definition: `CREATE TABLE security_events (
			seq INTEGER PRIMARY KEY,
			at TEXT NOT NULL,
			subject_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL,
			cause TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT '',
			prev_hash TEXT NOT NULL,
			hash TEXT NOT NULL
		)`,
	},
	{
		// The journal's signed head, and one-time markers.
		name: "security_meta",
		definition: `CREATE TABLE security_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	},
}

// securityColumns are added to security tables that an earlier release
// already created (r128: what a device is told to enforce, and the hardware
// identifier it reports). The same columns close the CREATE TABLE above, so a
// database created today and one upgraded from r127 have the same columns.
//
// Each DEFAULT is the value that means "nothing yet" - no policy was ever
// signed, no hardware identifier was ever reported, no module change was ever
// seen - which is exactly what is true of a row written before the column
// existed. A row whose three new columns hold these defaults keeps its
// original signature (see trustengine.Record.signedBytes), so adding them does
// not invalidate a single stored record.
var securityColumns = []struct {
	table      string
	name       string
	definition string
}{
	{table: "trust_records", name: "policy_generation", definition: "INTEGER NOT NULL DEFAULT 0"},
	{table: "trust_records", name: "hardware_hash", definition: "TEXT NOT NULL DEFAULT ''"},
	{table: "trust_records", name: "modem_changes", definition: "INTEGER NOT NULL DEFAULT 0"},
}

var securityIndexes = []struct {
	name       string
	definition string
}{
	{name: "idx_security_events_subject", definition: `CREATE INDEX idx_security_events_subject ON security_events(subject_id, seq)`},
}

func migrateSecurityAdditions(
	ctx context.Context,
	database *sql.DB,
	actual schemaShape,
) (bool, error) {
	if _, ok := actual.tables["sms"]; !ok {
		// A fresh database is created from schema.sql, which already has these.
		return false, nil
	}
	if _, ok := actual.tables[nodeRegistryTable]; !ok {
		// The Node migration has not produced its registry; leave the shape for
		// ValidateSchema to report rather than create a child with no parent.
		return false, nil
	}
	missingTables := securityTables[:0:0]
	for _, table := range securityTables {
		if _, exists := actual.tables[table.name]; !exists {
			missingTables = append(missingTables, table)
		}
	}
	missingIndexes := securityIndexes[:0:0]
	for _, index := range securityIndexes {
		if _, exists := actual.indexes[index.name]; !exists {
			missingIndexes = append(missingIndexes, index)
		}
	}
	// Columns are only added to a table that already exists; a table created
	// in this run comes with them.
	missingColumns := securityColumns[:0:0]
	for _, column := range securityColumns {
		columns, exists := actual.tables[column.table]
		if !exists {
			continue
		}
		if _, present := columns[column.name]; !present {
			missingColumns = append(missingColumns, column)
		}
	}
	if len(missingTables) == 0 && len(missingIndexes) == 0 && len(missingColumns) == 0 {
		return false, nil
	}

	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = transaction.Rollback() }()
	for _, table := range missingTables {
		if _, err := transaction.ExecContext(ctx, table.definition); err != nil {
			return false, fmt.Errorf("create security table %s: %w", table.name, err)
		}
	}
	for _, column := range missingColumns {
		// ADD COLUMN with a constant default is a metadata-only change in
		// SQLite: no existing row is rewritten.
		statement := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", column.table, column.name, column.definition)
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return false, fmt.Errorf("migrate security column %s.%s: %w", column.table, column.name, err)
		}
	}
	for _, index := range missingIndexes {
		if _, err := transaction.ExecContext(ctx, index.definition); err != nil {
			return false, fmt.Errorf("create security index %s: %w", index.name, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
