package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// legacyNodeSchema is the schema as it was before Nodes existed: no node_id or
// sim_id on sms and call_history, and none of the nassimhub_* tables.
//
// It is built by removing the additions from the current schema rather than by
// pasting an old copy, so the fixture cannot drift away from what the migration
// is expected to produce.
func legacyNodeSchema(t *testing.T) string {
	t.Helper()

	legacy := currentSchemaSQL
	for _, table := range nodeTables {
		before := legacy
		legacy = strings.Replace(legacy, "\n"+table.definition+";\n", "\n", 1)
		if legacy == before {
			t.Fatalf("legacy fixture could not remove table %s from schema.sql; "+
				"the DDL in nassimhub_node_migration.go and schema.sql have drifted",
				table.name)
		}
	}
	for _, index := range nodeIndexes {
		before := legacy
		legacy = strings.Replace(legacy, "\n"+index.definition+";\n", "\n", 1)
		if legacy == before {
			t.Fatalf("legacy fixture could not remove index %s from schema.sql; "+
				"the DDL in nassimhub_node_migration.go and schema.sql have drifted",
				index.name)
		}
	}

	// The columns are the last entries in their CREATE TABLE bodies, so removing
	// them also means giving the preceding column back its missing comma.
	removed := make(map[string]bool, len(nodeScopedColumns))
	for _, column := range nodeScopedColumns {
		removed[column.name+" "+column.definition] = true
	}
	lines := strings.Split(legacy, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSuffix(strings.TrimSpace(line), ",")
		if removed[trimmed] {
			if len(kept) > 0 && strings.HasSuffix(kept[len(kept)-1], ",") {
				kept[len(kept)-1] = strings.TrimSuffix(kept[len(kept)-1], ",")
			}
			continue
		}
		kept = append(kept, line)
	}
	legacy = strings.Join(kept, "\n")

	if legacy == currentSchemaSQL {
		t.Fatal("legacy schema fixture removed nothing")
	}
	for _, sentinel := range []string{
		"nassimhub_nodes",
		"nassimhub_node_sims",
		"nassimhub_node_hardware_sync",
		"idx_sms_node_sim_timestamp",
	} {
		if strings.Contains(legacy, sentinel) {
			t.Fatalf("legacy schema fixture still mentions %s", sentinel)
		}
	}
	return legacy
}

func openLegacyNodeDatabase(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(legacyNodeSchema(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO sms
			(id, request_id, line_id, endpoint_line_id, endpoint_message_id,
			 iccid, imsi, peer, content, timestamp, created_at)
		VALUES
			(1, 'req-1', 'line-a', 'ep-a', 'm-1', '8986001', '460001',
			 '+8613800000001', '升级前的短信', '2025-01-01T10:00:00Z',
			 '2025-01-01T10:00:00Z')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO call_history
			(id, request_id, line_id, endpoint_line_id, remote_number, ended_at)
		VALUES ('call-1', 'creq-1', 'line-a', 'ep-a', '+8613800000001',
			'2025-01-01T11:00:00Z')
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// An upgrade must not move an existing message onto some other device. Rows
// written before Nodes existed came from the local modem deck, and an empty
// node_id is what the local modem deck is called.
func TestOpenMigratesExistingRowsToTheLocalDeck(t *testing.T) {
	t.Parallel()

	path := openLegacyNodeDatabase(t, "node-scope.db")
	database, err := Open(context.Background(), Config{TargetPath: path})
	if err != nil {
		t.Fatalf("Open() migration error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	var (
		nodeID  string
		simID   string
		content string
	)
	if err := database.QueryRow(
		`SELECT node_id, sim_id, content FROM sms WHERE id = 1`,
	).Scan(&nodeID, &simID, &content); err != nil {
		t.Fatal(err)
	}
	if nodeID != "" || simID != "" {
		t.Fatalf("an existing message was attributed to node %q sim %q", nodeID, simID)
	}
	if content != "升级前的短信" {
		t.Fatalf("existing row was damaged: content = %q", content)
	}

	var callNode, callSIM, remote string
	if err := database.QueryRow(
		`SELECT node_id, sim_id, remote_number FROM call_history WHERE id = 'call-1'`,
	).Scan(&callNode, &callSIM, &remote); err != nil {
		t.Fatal(err)
	}
	if callNode != "" || callSIM != "" {
		t.Fatalf("an existing call was attributed to node %q sim %q", callNode, callSIM)
	}
	if remote != "+8613800000001" {
		t.Fatalf("existing call row was damaged: remote_number = %q", remote)
	}
}

// migrateSchema calls this migration before its legacy block, and that ordering
// is the whole reason the legacy block still works. If the call were moved
// after it, schemaMatchesSupportedMigration would find node_id missing, return
// false, and the legacy upgrade would be skipped without an error - which shows
// up only as ErrSchemaOutdated at startup. Opening a legacy database is the
// cheapest way to keep that from regressing unnoticed.
func TestOpenStillUpgradesALegacyDatabase(t *testing.T) {
	t.Parallel()

	path := openLegacyNodeDatabase(t, "node-legacy-order.db")
	database, err := Open(context.Background(), Config{TargetPath: path})
	if err != nil {
		t.Fatalf("Open() error = %v; the node migration may be ordered after the legacy block", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if err := ValidateSchema(context.Background(), database); err != nil {
		t.Fatalf("ValidateSchema() after upgrade = %v", err)
	}
}

// Running Open twice must not fail: the migration has to notice its own work.
func TestNodeMigrationIsIdempotent(t *testing.T) {
	t.Parallel()

	path := openLegacyNodeDatabase(t, "node-twice.db")
	for attempt := 1; attempt <= 2; attempt++ {
		opened, err := Open(context.Background(), Config{TargetPath: path})
		if err != nil {
			t.Fatalf("Open() attempt %d error = %v", attempt, err)
		}
		if err := opened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// A database created today and one upgraded today must have the same schema.
// They are built from different sources - schema.sql and the DDL in the
// migration - so this is the check that keeps those two honest.
func TestUpgradedSchemaMatchesAFreshOne(t *testing.T) {
	t.Parallel()

	upgradedPath := openLegacyNodeDatabase(t, "node-upgraded.db")
	upgraded, err := Open(context.Background(), Config{TargetPath: upgradedPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })

	fresh, err := Open(
		context.Background(),
		Config{TargetPath: filepath.Join(t.TempDir(), "node-fresh.db")},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })

	freshDDL := storedDDL(t, fresh)
	upgradedDDL := storedDDL(t, upgraded)
	for _, table := range nodeTables {
		if freshDDL[table.name] != upgradedDDL[table.name] {
			t.Fatalf(
				"%s differs depending on how the database was created:\nfresh:\n%s\nupgraded:\n%s",
				table.name, freshDDL[table.name], upgradedDDL[table.name],
			)
		}
	}
	for _, index := range nodeIndexes {
		if freshDDL[index.name] != upgradedDDL[index.name] {
			t.Fatalf(
				"%s differs depending on how the database was created:\nfresh:\n%s\nupgraded:\n%s",
				index.name, freshDDL[index.name], upgradedDDL[index.name],
			)
		}
	}
}

func storedDDL(t *testing.T, database *sql.DB) map[string]string {
	t.Helper()

	rows, err := database.Query(
		`SELECT name, sql FROM sqlite_schema WHERE sql IS NOT NULL`,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	definitions := make(map[string]string)
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		definitions[name] = definition
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return definitions
}

// Two Nodes can carry SIMs that report the same message identifiers, and the
// local deck may already have used those identifiers too. Core namespaces
// endpoint_line_id with the node_id when it ingests, which is what keeps
// ux_sms_endpoint_line_message correct without widening it. This test pins both
// halves: the namespaced inserts coexist, and the un-namespaced ones collide.
func TestTwoNodesCoexistWhenIdentifiersAreNamespaced(t *testing.T) {
	t.Parallel()

	database, err := Open(
		context.Background(),
		Config{TargetPath: filepath.Join(t.TempDir(), "node-multi.db")},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	for _, node := range []string{"NSH-410-A83F29", "NSH-410-B71C04"} {
		if _, err := database.Exec(`
			INSERT INTO nassimhub_nodes (node_id, platform, public_key, pair_state)
			VALUES (?, 'msm8916', X'00', 'paired')
		`, node); err != nil {
			t.Fatalf("register node %s: %v", node, err)
		}
		if _, err := database.Exec(`
			INSERT INTO nassimhub_node_sims (node_id, sim_id, slot, iccid, present)
			VALUES (?, 'sim-0', 0, '8986999', 1)
		`, node); err != nil {
			t.Fatalf("register sim for %s: %v", node, err)
		}
		if _, err := database.Exec(`
			INSERT INTO sms
				(request_id, line_id, endpoint_line_id, endpoint_message_id,
				 node_id, sim_id, peer, content, timestamp)
			VALUES (?, ?, ?, 'm-1', ?, 'sim-0', '+8613800000007', '来自节点',
				'2025-03-01T10:00:00Z')
		`, "req-"+node, "line-"+node, "nsh:"+node+":line-0", node); err != nil {
			t.Fatalf("ingest message from %s: %v", node, err)
		}
	}

	var count int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM sms WHERE node_id = 'NSH-410-A83F29'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("node A83F29 has %d messages, want 1", count)
	}

	if _, err := database.Exec(`
		INSERT INTO sms (endpoint_line_id, endpoint_message_id, node_id, sim_id)
		VALUES ('ep-shared', 'm-9', 'NSH-410-A83F29', 'sim-0')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO sms (endpoint_line_id, endpoint_message_id, node_id, sim_id)
		VALUES ('ep-shared', 'm-9', 'NSH-410-B71C04', 'sim-0')
	`); err == nil {
		t.Fatal("two Nodes shared an endpoint identifier without a unique violation; " +
			"cross-Node deduplication no longer relies on namespacing and the " +
			"ingestion rule needs revisiting")
	}
}

// Unpairing a Node removes the device and its SIM inventory. It must not remove
// the messages and calls that came from it - the user's history is theirs, not
// the device's.
func TestRemovingANodeKeepsItsHistory(t *testing.T) {
	t.Parallel()

	database, err := Open(
		context.Background(),
		Config{TargetPath: filepath.Join(t.TempDir(), "node-unpair.db")},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if _, err := database.Exec(`
		INSERT INTO nassimhub_nodes (node_id, platform, public_key, pair_state)
		VALUES ('NSH-410-A83F29', 'msm8916', X'00', 'paired')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO nassimhub_node_sims (node_id, sim_id, iccid, present)
		VALUES ('NSH-410-A83F29', 'sim-0', '8986999', 1)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO nassimhub_node_hardware_sync (node_id, boot_epoch, sequence)
		VALUES ('NSH-410-A83F29', 'epoch-1', 1)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO sms (endpoint_line_id, endpoint_message_id, node_id, sim_id, content)
		VALUES ('nsh:NSH-410-A83F29:line-0', 'm-1', 'NSH-410-A83F29', 'sim-0', '节点短信')
	`); err != nil {
		t.Fatal(err)
	}

	if _, err := database.Exec(
		`DELETE FROM nassimhub_nodes WHERE node_id = 'NSH-410-A83F29'`,
	); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"nassimhub_node_sims", "nassimhub_node_hardware_sync"} {
		var remaining int
		if err := database.QueryRow(
			`SELECT COUNT(*) FROM ` + table + ` WHERE node_id = 'NSH-410-A83F29'`,
		).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 0 {
			t.Fatalf("%s kept %d rows after the node was removed", table, remaining)
		}
	}
	var messages int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM sms WHERE node_id = 'NSH-410-A83F29'`,
	).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if messages != 1 {
		t.Fatalf("removing a node destroyed %d of its messages", 1-messages)
	}
}

// The pair states are the three the protocol defines and nothing else.
func TestNodePairStateIsConstrained(t *testing.T) {
	t.Parallel()

	database, err := Open(
		context.Background(),
		Config{TargetPath: filepath.Join(t.TempDir(), "node-pairstate.db")},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if _, err := database.Exec(`
		INSERT INTO nassimhub_nodes (node_id, platform, public_key, pair_state)
		VALUES ('NSH-410-BADBAD', 'msm8916', X'00', 'half-paired')
	`); err == nil {
		t.Fatal("an unknown pair state was accepted by the database")
	}
}
