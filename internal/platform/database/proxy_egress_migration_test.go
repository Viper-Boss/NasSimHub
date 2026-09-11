package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// legacyProxySchema is the schema as it was before proxies could leave through
// a WireGuard interface.
func legacyProxySchema(t *testing.T) string {
	t.Helper()
	legacy := strings.Replace(
		currentSchemaSQL,
		`			egress TEXT NOT NULL DEFAULT 'line'
				CHECK (egress IN ('line', 'wireguard')),
			egress_interface TEXT NOT NULL DEFAULT '',
			egress_dns TEXT NOT NULL DEFAULT '',
`,
		"",
		1,
	)
	if legacy == currentSchemaSQL {
		t.Fatal("legacy schema fixture did not remove the egress columns")
	}
	return legacy
}

// An upgrade must never silently move a running proxy's traffic somewhere else.
// Existing rows become 'line', which is exactly what they did before.
func TestOpenMigratesProxiesToLineEgress(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "proxy-egress.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(legacyProxySchema(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO modemdeck_proxy_instances
			(id, name, line_id, enabled, mode, listen_address, listen_port)
		VALUES
			('proxy-1', '家里代理', 'line-a', 1, 'socks5', '192.168.1.10', 1080)
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = Open(context.Background(), Config{TargetPath: path})
	if err != nil {
		t.Fatalf("Open() migration error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	var (
		egress          string
		egressInterface string
		egressDNS       string
		name            string
	)
	if err := database.QueryRow(`
		SELECT egress, egress_interface, egress_dns, name
		FROM modemdeck_proxy_instances WHERE id = 'proxy-1'
	`).Scan(&egress, &egressInterface, &egressDNS, &name); err != nil {
		t.Fatal(err)
	}
	if egress != "line" {
		t.Fatalf("egress = %q; an upgrade must not move an existing proxy off the modem line", egress)
	}
	if egressInterface != "" || egressDNS != "" {
		t.Fatalf("tunnel fields were populated on a line proxy: %q / %q", egressInterface, egressDNS)
	}
	if name != "家里代理" {
		t.Fatalf("existing row was damaged: name = %q", name)
	}
}

// Running Open twice must not fail: the migration has to notice the columns are
// already there.
func TestProxyEgressMigrationIsIdempotent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "proxy-egress-twice.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(legacyProxySchema(t)); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

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

// A WireGuard egress written by the application must survive a round trip.
func TestProxyEgressColumnsStoreTunnelSettings(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "proxy-egress-write.db")
	database, err := Open(context.Background(), Config{TargetPath: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if _, err := database.Exec(`
		INSERT INTO modemdeck_proxy_instances
			(id, name, line_id, enabled, mode, listen_address, listen_port,
			 egress, egress_interface, egress_dns)
		VALUES
			('proxy-2', '隧道代理', 'line-a', 1, 'http', '192.168.1.10', 3128,
			 'wireguard', 'wg-nassim', '10.77.0.2,10.77.0.3')
	`); err != nil {
		t.Fatal(err)
	}
	var egress, iface, dns string
	if err := database.QueryRow(`
		SELECT egress, egress_interface, egress_dns
		FROM modemdeck_proxy_instances WHERE id = 'proxy-2'
	`).Scan(&egress, &iface, &dns); err != nil {
		t.Fatal(err)
	}
	if egress != "wireguard" || iface != "wg-nassim" || dns != "10.77.0.2,10.77.0.3" {
		t.Fatalf("tunnel settings did not round trip: %q / %q / %q", egress, iface, dns)
	}

	// The column only accepts the two known kinds on a fresh database.
	if _, err := database.Exec(`
		INSERT INTO modemdeck_proxy_instances
			(id, name, line_id, enabled, mode, listen_address, listen_port, egress)
		VALUES ('proxy-3', 'x', 'line-a', 0, 'http', '127.0.0.1', 3129, 'carrier-pigeon')
	`); err == nil {
		t.Fatal("an unknown egress kind was accepted by the database")
	}
}
