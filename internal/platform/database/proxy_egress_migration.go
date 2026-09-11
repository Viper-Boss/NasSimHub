package database

import (
	"context"
	"database/sql"
	"fmt"
)

// Proxies gained an egress selector so one can leave through a WireGuard
// interface instead of a modem data bearer.
//
// Existing rows migrate to 'line', which is what every proxy did before, so an
// upgrade never silently changes where a running proxy's traffic goes.

const proxyInstancesTable = "modemdeck_proxy_instances"

var proxyEgressColumns = []struct {
	name       string
	definition string
}{
	{name: "egress", definition: "TEXT NOT NULL DEFAULT 'line'"},
	{name: "egress_interface", definition: "TEXT NOT NULL DEFAULT ''"},
	// Stored as a comma-separated list. A separate table would be the tidier
	// shape, but at most four resolvers per proxy it would cost a join on every
	// read for no benefit.
	{name: "egress_dns", definition: "TEXT NOT NULL DEFAULT ''"},
}

func migrateProxyEgressSchema(
	ctx context.Context,
	database *sql.DB,
	actual schemaShape,
) (bool, error) {
	columns, ok := actual.tables[proxyInstancesTable]
	if !ok {
		// No proxy table yet: a fresh database is created from schema.sql, which
		// already carries these columns.
		return false, nil
	}
	missing := proxyEgressColumns[:0:0]
	for _, column := range proxyEgressColumns {
		if _, exists := columns[column.name]; !exists {
			missing = append(missing, column)
		}
	}
	if len(missing) == 0 {
		return false, nil
	}

	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = transaction.Rollback() }()

	for _, column := range missing {
		statement := fmt.Sprintf(
			"ALTER TABLE %s ADD COLUMN %s %s",
			proxyInstancesTable,
			column.name,
			column.definition,
		)
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return false, fmt.Errorf("migrate proxy column %s: %w", column.name, err)
		}
	}
	// SQLite cannot add a CHECK constraint to an existing table, so the value is
	// normalised here and enforced by the application layer. Anything unexpected
	// becomes 'line', the behaviour these rows already had.
	if _, err := transaction.ExecContext(ctx, fmt.Sprintf(
		"UPDATE %s SET egress = 'line' WHERE egress NOT IN ('line', 'wireguard')",
		proxyInstancesTable,
	)); err != nil {
		return false, fmt.Errorf("normalise proxy egress: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
