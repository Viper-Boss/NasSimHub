package database

import (
	"context"
	"database/sql"
	"fmt"
)

// NasSimHub gained external Nodes: small devices that carry their own SIMs and
// reach the NAS over USB or Wi-Fi. Before them the product had exactly one
// modem deck, so "which device did this message come from" was not a question
// anyone could ask and the schema did not record it.
//
// This migration makes that question answerable without disturbing anything
// that already works. Three rules shaped it:
//
//   - Nothing is dropped or recreated. Every change is an additive ALTER TABLE
//     ADD COLUMN, a new table, or a new index. SQLite's ADD COLUMN is a
//     metadata-only operation, so the cost does not grow with the size of an
//     existing sms table.
//   - Existing rows are not rewritten. node_id and sim_id arrive with a DEFAULT
//     of '' and every existing row therefore reads back as ''. That empty string
//     is not "unknown" - it is the identifier of the built-in local modem deck,
//     which is exactly where every pre-existing message and call did come from.
//     A backfill UPDATE would have written the same meaning more slowly and less
//     safely.
//   - The singleton in modemdeck_hardware_sync is left alone. It is declared
//     CHECK (singleton = 1), and SQLite cannot relax a CHECK constraint without
//     rebuilding the table. Rather than rebuild a production table, per-Node
//     sync state lives in its own table and the singleton keeps meaning what it
//     has always meant: the state of the local deck.
//
// Ordering note for whoever adds the next migration: this one must run before
// migrateSchema reaches its schemaContains check and the legacy block below it.
// That block calls schemaMatchesSupportedMigration, which returns false - and
// then silently skips the whole legacy upgrade - if any table or column in the
// expected shape is missing from the database. Adding node_id to sms without
// running this first would therefore not fail loudly; it would quietly abandon
// the legacy upgrade path and surface later as ErrSchemaOutdated at startup.

const (
	nodeRegistryTable     = "nassimhub_nodes"
	nodeSIMTable          = "nassimhub_node_sims"
	nodeHardwareSyncTable = "nassimhub_node_hardware_sync"
)

// nodeScopedColumns are the columns added to tables that previously assumed a
// single modem deck.
//
// The DEFAULT is load-bearing: it is what lets an existing row keep its
// meaning. See the note above about the empty string being the local deck
// rather than a null.
var nodeScopedColumns = []struct {
	table      string
	name       string
	definition string
}{
	{table: "sms", name: "node_id", definition: "TEXT NOT NULL DEFAULT ''"},
	{table: "sms", name: "sim_id", definition: "TEXT NOT NULL DEFAULT ''"},
	{table: "call_history", name: "node_id", definition: "TEXT NOT NULL DEFAULT ''"},
	{table: "call_history", name: "sim_id", definition: "TEXT NOT NULL DEFAULT ''"},
}

// nodeTables are created in order; the two child tables reference the registry
// and the connection runs with foreign keys on.
//
// The DDL text here is byte-identical to the corresponding statements in
// schema.sql. That is not a stylistic preference: a fresh database is built
// from schema.sql and an upgraded one from these statements, and if the two
// drifted, the same product version would have two different schemas depending
// on when the database was created.
var nodeTables = []struct {
	name       string
	definition string
}{
	{
		name: nodeRegistryTable,
		definition: `CREATE TABLE nassimhub_nodes (
			node_id TEXT PRIMARY KEY,
			platform TEXT NOT NULL DEFAULT '',
			display_name TEXT NOT NULL DEFAULT '',
			public_key BLOB NOT NULL,
			certificate BLOB,
			pair_state TEXT NOT NULL DEFAULT 'unpaired'
				CHECK (pair_state IN ('unpaired', 'pairing', 'paired')),
			transport TEXT NOT NULL DEFAULT '',
			usb_endpoint TEXT NOT NULL DEFAULT '',
			wifi_endpoint TEXT NOT NULL DEFAULT '',
			capabilities TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL DEFAULT 1,
			last_seen_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	},
	{
		name: nodeSIMTable,
		definition: `CREATE TABLE nassimhub_node_sims (
			node_id TEXT NOT NULL,
			sim_id TEXT NOT NULL,
			slot INTEGER NOT NULL DEFAULT 0,
			iccid TEXT NOT NULL DEFAULT '',
			imsi TEXT NOT NULL DEFAULT '',
			local_phone TEXT NOT NULL DEFAULT '',
			operator_name TEXT NOT NULL DEFAULT '',
			present NUMERIC NOT NULL DEFAULT 0,
			line_id TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (node_id, sim_id),
			FOREIGN KEY (node_id) REFERENCES nassimhub_nodes(node_id) ON DELETE CASCADE ON UPDATE CASCADE
		)`,
	},
	{
		// The per-Node replacement for the modemdeck_hardware_sync singleton.
		// Same columns, keyed by node instead of by the number 1.
		name: nodeHardwareSyncTable,
		definition: `CREATE TABLE nassimhub_node_hardware_sync (
			node_id TEXT PRIMARY KEY,
			boot_epoch TEXT NOT NULL DEFAULT '',
			snapshot_revision TEXT NOT NULL DEFAULT '',
			sequence INTEGER NOT NULL DEFAULT 0,
			observed_at DATETIME,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (node_id) REFERENCES nassimhub_nodes(node_id) ON DELETE CASCADE ON UPDATE CASCADE
		)`,
	},
}

// nodeIndexes cover the one query shape Nodes add: "everything from this SIM on
// this Node, newest first".
//
// No unique index is added and none is changed. Cross-Node collisions in
// endpoint_line_id, endpoint_message_id, request_id and call_history.id are
// prevented by namespacing those identifiers with the node_id when Core ingests
// a Node's data, which keeps ux_sms_endpoint_line_message and
// ux_call_history_request_id doing exactly what they do today. Dropping and
// recreating a unique index to widen it would have been a change to how the
// local deck deduplicates, for no gain.
var nodeIndexes = []struct {
	name       string
	definition string
}{
	{
		name:       "idx_sms_node_sim_timestamp",
		definition: `CREATE INDEX idx_sms_node_sim_timestamp ON sms(node_id, sim_id, COALESCE(timestamp, '') DESC, id DESC)`,
	},
	{
		name:       "idx_call_history_node_sim_ended_at",
		definition: `CREATE INDEX idx_call_history_node_sim_ended_at ON call_history(node_id, sim_id, COALESCE(ended_at, '') DESC, id DESC)`,
	},
	{
		name:       "idx_nassimhub_node_sims_line",
		definition: `CREATE INDEX idx_nassimhub_node_sims_line ON nassimhub_node_sims(line_id) WHERE line_id <> ''`,
	},
}

func migrateNasSimHubNodeSchema(
	ctx context.Context,
	database *sql.DB,
	actual schemaShape,
) (bool, error) {
	if _, ok := actual.tables["sms"]; !ok {
		// No sms table yet: a fresh database is created from schema.sql, which
		// already carries all of this.
		return false, nil
	}

	missingColumns := nodeScopedColumns[:0:0]
	for _, column := range nodeScopedColumns {
		columns, ok := actual.tables[column.table]
		if !ok {
			// The table itself is absent. That is not a shape this migration
			// knows how to repair, and guessing would be worse than leaving
			// ValidateSchema to report it.
			continue
		}
		if _, exists := columns[column.name]; !exists {
			missingColumns = append(missingColumns, column)
		}
	}
	missingTables := nodeTables[:0:0]
	for _, table := range nodeTables {
		if _, exists := actual.tables[table.name]; !exists {
			missingTables = append(missingTables, table)
		}
	}
	missingIndexes := nodeIndexes[:0:0]
	for _, index := range nodeIndexes {
		if _, exists := actual.indexes[index.name]; !exists {
			missingIndexes = append(missingIndexes, index)
		}
	}
	if len(missingColumns) == 0 && len(missingTables) == 0 && len(missingIndexes) == 0 {
		return false, nil
	}

	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = transaction.Rollback() }()

	for _, table := range missingTables {
		if _, err := transaction.ExecContext(ctx, table.definition); err != nil {
			return false, fmt.Errorf("create node table %s: %w", table.name, err)
		}
	}
	for _, column := range missingColumns {
		statement := fmt.Sprintf(
			"ALTER TABLE %s ADD COLUMN %s %s",
			column.table,
			column.name,
			column.definition,
		)
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return false, fmt.Errorf(
				"migrate node column %s.%s: %w",
				column.table,
				column.name,
				err,
			)
		}
	}
	// Indexes come last so they are built against the final column set.
	for _, index := range missingIndexes {
		if _, err := transaction.ExecContext(ctx, index.definition); err != nil {
			return false, fmt.Errorf("create node index %s: %w", index.name, err)
		}
	}
	// Deliberately no backfill UPDATE. The DEFAULT above already gives every
	// existing row node_id = '' and sim_id = '', which is the correct value:
	// those rows came from the local modem deck. Rewriting millions of rows to
	// store the same meaning would turn a metadata-only migration into a long
	// one that can fail halfway on a NAS with a full disk.
	if err := transaction.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
