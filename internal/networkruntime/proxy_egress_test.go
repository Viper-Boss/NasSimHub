package networkruntime

import (
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
	"github.com/human-agent65535/modemdeck/internal/store"
)

// Listens on loopback with no authentication: these tests are about the egress
// rules, and the credential path needs a secrets service that is exercised by
// its own tests.
func tunnelFields() proxyFields {
	return proxyFields{
		Name:            "隧道代理",
		Enabled:         true,
		Mode:            agentclient.ProxyModeSOCKS5,
		ListenAddress:   "127.0.0.1",
		ListenPort:      1080,
		Egress:          proxyEgressWireGuard,
		EgressInterface: "wg-nassim",
		EgressDNS:       []string{"10.77.0.2"},
	}
}

func buildTunnelRecord(t *testing.T, fields proxyFields) (store.ProxyInstanceRecord, error) {
	t.Helper()
	service := &Service{}
	return service.buildRecord("proxy-1", store.ProxyInstanceRecord{}, fields)
}

// A tunnel proxy is not tied to a SIM. Requiring one would put a meaningless
// "which SIM?" choice in front of the operator, and would make the proxy fail
// whenever that SIM went away.
func TestWireGuardProxyNeedsNoLine(t *testing.T) {
	fields := tunnelFields()
	fields.LineID = ""

	record, err := buildTunnelRecord(t, fields)
	if err != nil {
		t.Fatalf("a tunnel proxy was rejected for having no line: %v", err)
	}
	if record.LineID != "" {
		t.Fatalf("line_id = %q; a tunnel proxy must not carry one", record.LineID)
	}
	if record.Egress != proxyEgressWireGuard || record.EgressInterface != "wg-nassim" {
		t.Fatalf("egress not stored: %+v", record)
	}
}

// A line proxy still requires its line: that is the interface its traffic
// leaves through.
func TestLineProxyStillRequiresItsLine(t *testing.T) {
	fields := tunnelFields()
	fields.Egress = proxyEgressLine
	fields.LineID = ""

	if _, err := buildTunnelRecord(t, fields); err == nil {
		t.Fatal("a line proxy with no line was accepted")
	}
}

// Switching a tunnel proxy back to a line must drop the tunnel settings, so a
// stale interface cannot be resurrected by a later edit.
func TestSwitchingToLineClearsTunnelSettings(t *testing.T) {
	fields := tunnelFields()
	fields.Egress = proxyEgressLine
	fields.LineID = "line-a"

	record, err := buildTunnelRecord(t, fields)
	if err != nil {
		t.Fatal(err)
	}
	if record.EgressInterface != "" || len(record.EgressDNS) != 0 {
		t.Fatalf("tunnel settings survived: %+v", record)
	}
}

func TestWireGuardProxyRequiresResolvers(t *testing.T) {
	fields := tunnelFields()
	fields.EgressDNS = nil

	_, err := buildTunnelRecord(t, fields)
	if err == nil {
		t.Fatal("a tunnel proxy with no DNS was accepted")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "dns") {
		t.Fatalf("the error should name DNS: %v", err)
	}
}

func TestWireGuardProxyValidatesInterfaceAndResolvers(t *testing.T) {
	bad := tunnelFields()
	bad.EgressInterface = "wg nassim"
	if _, err := buildTunnelRecord(t, bad); err == nil {
		t.Fatal("an interface name with a space was accepted")
	}

	bad = tunnelFields()
	bad.EgressDNS = []string{"10.77.0.2", "not-an-ip"}
	if _, err := buildTunnelRecord(t, bad); err == nil {
		t.Fatal("a non-IP resolver was accepted")
	}

	bad = tunnelFields()
	bad.EgressDNS = []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4", "10.0.0.5"}
	if _, err := buildTunnelRecord(t, bad); err == nil {
		t.Fatal("five resolvers were accepted")
	}

	bad = tunnelFields()
	bad.Egress = "carrier-pigeon"
	if _, err := buildTunnelRecord(t, bad); err == nil {
		t.Fatal("an unknown egress was accepted")
	}
}

// An existing proxy has no egress recorded at all. It must read as a line
// proxy, because that is what it has been doing.
func TestEmptyEgressStaysOnTheLine(t *testing.T) {
	fields := tunnelFields()
	fields.Egress = ""
	fields.EgressInterface = ""
	fields.EgressDNS = nil
	fields.LineID = "line-a"

	record, err := buildTunnelRecord(t, fields)
	if err != nil {
		t.Fatal(err)
	}
	if record.Egress != proxyEgressLine {
		t.Fatalf("egress = %q; an upgrade must not move a proxy off its line", record.Egress)
	}
}
