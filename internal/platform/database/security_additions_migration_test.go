package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// preSecuritySchema is the current schema with the security additions removed,
// built by subtraction so it cannot drift from what the migration must produce.
func preSecuritySchema(t *testing.T) string {
	t.Helper()
	legacy := currentSchemaSQL
	for _, table := range securityTables {
		before := legacy
		legacy = strings.Replace(legacy, "\n"+table.definition+";\n", "\n", 1)
		if legacy == before {
			t.Fatalf("could not remove table %s from schema.sql; the DDL in "+
				"security_additions_migration.go and schema.sql have drifted", table.name)
		}
	}
	for _, index := range securityIndexes {
		before := legacy
		legacy = strings.Replace(legacy, "\n"+index.definition+";\n", "\n", 1)
		if legacy == before {
			t.Fatalf("could not remove index %s from schema.sql; the DDL in "+
				"security_additions_migration.go and schema.sql have drifted", index.name)
		}
	}
	return legacy
}

func openPreSecurityDatabase(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(preSecuritySchema(t)); err != nil {
		t.Fatalf("create pre-security schema: %v", err)
	}
	// A paired Node and a message, so the upgrade is exercised against rows.
	if _, err := database.Exec(
		`INSERT INTO nassimhub_nodes (node_id, public_key, pair_state) VALUES ('NSH-410-0A0B0C', x'01', 'paired')`,
	); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSecurityAdditionsUpgradeAnExistingDatabaseInPlace(t *testing.T) {
	t.Parallel()

	path := openPreSecurityDatabase(t, "security-upgrade.db")
	for attempt := 1; attempt <= 2; attempt++ {
		opened, err := Open(context.Background(), Config{TargetPath: path})
		if err != nil {
			t.Fatalf("Open() attempt %d error = %v", attempt, err)
		}
		var nodes int
		if err := opened.QueryRow(`SELECT COUNT(*) FROM nassimhub_nodes`).Scan(&nodes); err != nil || nodes != 1 {
			t.Fatalf("attempt %d: existing node rows = %d, %v", attempt, nodes, err)
		}
		var pins int
		if err := opened.QueryRow(`SELECT COUNT(*) FROM nassimhub_node_pq_pins`).Scan(&pins); err != nil || pins != 0 {
			t.Fatalf("attempt %d: pin rows = %d, %v", attempt, pins, err)
		}
		if err := opened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSecurityAdditionsMatchAFreshDatabase(t *testing.T) {
	t.Parallel()

	upgraded, err := Open(context.Background(), Config{TargetPath: openPreSecurityDatabase(t, "security-upgraded.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	fresh, err := Open(context.Background(), Config{TargetPath: filepath.Join(t.TempDir(), "security-fresh.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })

	freshDDL, upgradedDDL := storedDDL(t, fresh), storedDDL(t, upgraded)
	for _, table := range securityTables {
		if freshDDL[table.name] == "" || freshDDL[table.name] != upgradedDDL[table.name] {
			t.Fatalf("%s differs depending on how the database was created:\nfresh:\n%s\nupgraded:\n%s",
				table.name, freshDDL[table.name], upgradedDDL[table.name])
		}
	}
	for _, index := range securityIndexes {
		if freshDDL[index.name] == "" || freshDDL[index.name] != upgradedDDL[index.name] {
			t.Fatalf("%s differs depending on how the database was created", index.name)
		}
	}
}

// The pin belongs to the pairing: forgetting the Node forgets the pin, and a
// pin cannot exist for a Node that was never adopted.
func TestNodePQPinFollowsTheNodeRow(t *testing.T) {
	t.Parallel()

	database, err := Open(context.Background(), Config{TargetPath: filepath.Join(t.TempDir(), "pin.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if _, err := database.Exec(
		`INSERT INTO nassimhub_node_pq_pins (node_id, algorithm, public_key) VALUES ('NSH-410-0A0B0C', 'ml-dsa-65', x'01')`,
	); err == nil {
		t.Fatal("a pin was stored for a node that does not exist")
	}
	if _, err := database.Exec(
		`INSERT INTO nassimhub_nodes (node_id, public_key, pair_state) VALUES ('NSH-410-0A0B0C', x'01', 'paired')`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`INSERT INTO nassimhub_node_pq_pins (node_id, algorithm, public_key) VALUES ('NSH-410-0A0B0C', 'ml-dsa-65', x'01')`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DELETE FROM nassimhub_nodes WHERE node_id = 'NSH-410-0A0B0C'`); err != nil {
		t.Fatal(err)
	}
	var pins int
	if err := database.QueryRow(`SELECT COUNT(*) FROM nassimhub_node_pq_pins`).Scan(&pins); err != nil || pins != 0 {
		t.Fatalf("pins after the node was removed: %d, %v", pins, err)
	}
}

// r127Schema is the current schema with the columns r128 added to existing
// security tables removed: the shape a gateway upgraded from r127 starts from.
func r127Schema(t *testing.T) string {
	t.Helper()
	legacy := currentSchemaSQL
	for _, column := range securityColumns {
		line := ",\n\t\t\t" + column.name + " " + column.definition
		before := legacy
		legacy = strings.Replace(legacy, line, "", 1)
		if legacy == before {
			t.Fatalf("could not remove %s.%s from schema.sql; schema.sql and securityColumns have drifted", column.table, column.name)
		}
	}
	return legacy
}

type columnInfo struct {
	columnType string
	notNull    int
	fallback   sql.NullString
}

func columnsOf(t *testing.T, database *sql.DB, table string) map[string]columnInfo {
	t.Helper()
	rows, err := database.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := map[string]columnInfo{}
	for rows.Next() {
		var (
			sequence, primaryKey int
			name                 string
			info                 columnInfo
		)
		if err := rows.Scan(&sequence, &name, &info.columnType, &info.notNull, &info.fallback, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = info
	}
	return columns
}

// The r128 columns arrive on an r127 database by ALTER TABLE, without
// rewriting or losing a stored trust record, and leave the table with the
// same columns a fresh database has.
func TestSecurityColumnsAreAddedToAnR127DatabaseInPlace(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "r127.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(r127Schema(t)); err != nil {
		t.Fatalf("create r127 schema: %v", err)
	}
	if before := columnsOf(t, legacy, "trust_records"); len(before) != 19 {
		t.Fatalf("the r127 fixture should have the 19 original columns, has %d", len(before))
	}
	if _, err := legacy.Exec(`INSERT INTO trust_records (subject_id, state, sim_hash, risk_score, generation, updated_at, signature)
		VALUES ('NSH-410-0A0B0C', 'TRUSTED', 'fake-sim-hash', 2, 7, '2026-09-01T08:00:00Z', x'0102')`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	fresh, err := Open(context.Background(), Config{TargetPath: filepath.Join(t.TempDir(), "fresh.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	want := columnsOf(t, fresh, "trust_records")

	for attempt := 1; attempt <= 2; attempt++ { // the second open must find nothing to do
		upgraded, err := Open(context.Background(), Config{TargetPath: path})
		if err != nil {
			t.Fatalf("Open() attempt %d error = %v", attempt, err)
		}
		got := columnsOf(t, upgraded, "trust_records")
		if len(got) != len(want) {
			t.Fatalf("attempt %d: %d columns, a fresh database has %d", attempt, len(got), len(want))
		}
		for name, info := range want {
			if got[name] != info {
				t.Fatalf("attempt %d: column %s is %+v, a fresh database has %+v", attempt, name, got[name], info)
			}
		}
		var (
			state, simHash, hardware    string
			risk, generation            int
			policyGeneration, modemSwap int
			signature                   []byte
		)
		if err := upgraded.QueryRow(`SELECT state, sim_hash, risk_score, generation, signature, policy_generation, hardware_hash, modem_changes
			FROM trust_records WHERE subject_id = 'NSH-410-0A0B0C'`).Scan(&state, &simHash, &risk, &generation, &signature, &policyGeneration, &hardware, &modemSwap); err != nil {
			t.Fatal(err)
		}
		if state != "TRUSTED" || simHash != "fake-sim-hash" || risk != 2 || generation != 7 || string(signature) != "\x01\x02" {
			t.Fatalf("attempt %d: the stored record changed: %s %s %d %d %x", attempt, state, simHash, risk, generation, signature)
		}
		// "Nothing yet", which is what is true of a row from before.
		if policyGeneration != 0 || hardware != "" || modemSwap != 0 {
			t.Fatalf("attempt %d: new columns must default to nothing: %d %q %d", attempt, policyGeneration, hardware, modemSwap)
		}
		if err := upgraded.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// The column definitions the migration adds are the ones schema.sql declares.
func TestSecurityColumnDefinitionsMatchTheSchema(t *testing.T) {
	t.Parallel()
	for _, column := range securityColumns {
		line := "\t\t\t" + column.name + " " + column.definition
		inSchema := strings.Contains(currentSchemaSQL, line+",\n") || strings.Contains(currentSchemaSQL, line+"\n\t\t);")
		inMigration := false
		for _, table := range securityTables {
			if table.name == column.table && (strings.Contains(table.definition, line+",\n") || strings.Contains(table.definition, line+"\n\t\t)")) {
				inMigration = true
			}
		}
		if !inSchema || !inMigration {
			t.Errorf("%s.%s: schema.sql=%v CREATE TABLE in the migration=%v; the three definitions must be byte-identical", column.table, column.name, inSchema, inMigration)
		}
	}
}
