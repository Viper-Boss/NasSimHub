package networking

import (
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

func wireGuardProxy() domain.ProxyConfiguration {
	return domain.ProxyConfiguration{
		ID:              "proxy-wg",
		LineID:          "line-a",
		Enabled:         true,
		Mode:            domain.ProxyModeSOCKS5,
		// A proxy listening on the LAN must require authentication; the
		// normalizer enforces that, and this fixture has to honour it.
		ListenAddress:   "192.168.1.10",
		ListenPort:      1080,
		AuthEnabled:     true,
		Username:        "lan-user",
		Password:        "lan-password-1234",
		Egress:          domain.ProxyEgressWireGuard,
		EgressInterface: "wg-nassim",
		EgressDNS:       []string{"10.77.0.2"},
	}
}

// A configuration written before WireGuard existed has no egress field at all,
// and must keep behaving exactly as it did: out the modem line.
func TestEmptyEgressDefaultsToTheModemLine(t *testing.T) {
	input := wireGuardProxy()
	input.Egress = ""
	input.EgressInterface = ""
	input.EgressDNS = nil

	normalized, err := normalizeProxyConfiguration(input)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Egress != domain.ProxyEgressLine {
		t.Fatalf("egress = %q; want line", normalized.Egress)
	}
}

// Name lookups must leave through the same path as the traffic. A resolver
// reachable only on the modem side would reveal what the LAN device is browsing
// even while its traffic went through the tunnel, so there is no default.
func TestWireGuardEgressRequiresItsOwnDNS(t *testing.T) {
	input := wireGuardProxy()
	input.EgressDNS = nil

	_, err := normalizeProxyConfiguration(input)
	if err == nil {
		t.Fatal("a WireGuard egress with no DNS was accepted")
	}
	if !strings.Contains(err.Error(), "egress_dns") {
		t.Fatalf("error should name the missing field: %v", err)
	}
}

func TestWireGuardEgressValidatesInterfaceAndDNS(t *testing.T) {
	bad := wireGuardProxy()
	bad.EgressInterface = "wg nassim"
	if _, err := normalizeProxyConfiguration(bad); err == nil {
		t.Fatal("an interface name with a space was accepted")
	}

	bad = wireGuardProxy()
	bad.EgressDNS = []string{"not-an-ip"}
	if _, err := normalizeProxyConfiguration(bad); err == nil {
		t.Fatal("a non-IP resolver was accepted")
	}

	bad = wireGuardProxy()
	bad.Egress = "carrier-pigeon"
	if _, err := normalizeProxyConfiguration(bad); err == nil {
		t.Fatal("an unknown egress kind was accepted")
	}
}

// Switching back to a line egress must drop the tunnel fields, so a stale
// interface name cannot be resurrected by a later edit.
func TestLineEgressClearsTunnelFields(t *testing.T) {
	input := wireGuardProxy()
	input.Egress = domain.ProxyEgressLine

	normalized, err := normalizeProxyConfiguration(input)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.EgressInterface != "" || len(normalized.EgressDNS) != 0 {
		t.Fatalf("tunnel fields survived a switch to line egress: %+v", normalized)
	}
}

// The comparison used to be a plain `==`. It is now explicit, so it has to
// notice each field — a missed one would leave a running proxy on stale
// settings after an edit.
func TestConfigurationComparisonNoticesEveryField(t *testing.T) {
	base := wireGuardProxy()
	if !sameProxyConfiguration(base, wireGuardProxy()) {
		t.Fatal("identical configurations compared unequal")
	}

	changes := map[string]func(*domain.ProxyConfiguration){
		"id":            func(c *domain.ProxyConfiguration) { c.ID = "other" },
		"line":          func(c *domain.ProxyConfiguration) { c.LineID = "line-b" },
		"enabled":       func(c *domain.ProxyConfiguration) { c.Enabled = false },
		"mode":          func(c *domain.ProxyConfiguration) { c.Mode = domain.ProxyModeHTTP },
		"listen addr":   func(c *domain.ProxyConfiguration) { c.ListenAddress = "127.0.0.1" },
		"listen port":   func(c *domain.ProxyConfiguration) { c.ListenPort = 1081 },
		"auth":          func(c *domain.ProxyConfiguration) { c.AuthEnabled = false },
		"username":      func(c *domain.ProxyConfiguration) { c.Username = "someone" },
		"password":      func(c *domain.ProxyConfiguration) { c.Password = "secret" },
		"egress":        func(c *domain.ProxyConfiguration) { c.Egress = domain.ProxyEgressLine },
		"egress iface":  func(c *domain.ProxyConfiguration) { c.EgressInterface = "wg-other" },
		"egress dns":    func(c *domain.ProxyConfiguration) { c.EgressDNS = []string{"10.77.0.3"} },
		"egress dns +1": func(c *domain.ProxyConfiguration) { c.EgressDNS = append(c.EgressDNS, "10.77.0.4") },
	}
	for name, mutate := range changes {
		changed := wireGuardProxy()
		changed.EgressDNS = append([]string(nil), changed.EgressDNS...)
		mutate(&changed)
		if sameProxyConfiguration(base, changed) {
			t.Fatalf("a change to %s went unnoticed, so the proxy would keep stale settings", name)
		}
	}
}

// A tunnel proxy must not depend on the modem: it exists precisely for the case
// where traffic should leave through the tunnel instead of the SIM.
func TestWireGuardEgressResolvesWithoutAModemLine(t *testing.T) {
	manager := &Manager{interfaceReadiness: func(bearer) error { return nil }}

	resolutions, err := manager.resolveDesiredBearers(
		[]domain.ProxyConfiguration{wireGuardProxy()},
		networkObservation{
			discovered:     map[string]struct{}{},
			configurations: map[string]domain.LineNetworkConfiguration{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	resolution := resolutions["proxy-wg"]
	if !resolution.connected {
		t.Fatalf("tunnel proxy did not resolve: %q", resolution.waitReason)
	}
	if resolution.bearer.Interface != "wg-nassim" {
		t.Fatalf("bound to %q; want the WireGuard interface", resolution.bearer.Interface)
	}
	if len(resolution.bearer.DNS) != 1 || resolution.bearer.DNS[0] != "10.77.0.2" {
		t.Fatalf("resolver not carried through: %+v", resolution.bearer.DNS)
	}
}

// When the interface is missing the proxy waits with a reason, rather than
// starting and quietly sending traffic out the default route — which would be
// the modem, the exact path the operator asked it not to use.
func TestWireGuardEgressWaitsWhenTheInterfaceIsMissing(t *testing.T) {
	manager := &Manager{interfaceReadiness: func(bearer) error {
		return errInterfaceMissingForTest
	}}

	resolutions, err := manager.resolveDesiredBearers(
		[]domain.ProxyConfiguration{wireGuardProxy()},
		networkObservation{
			discovered:     map[string]struct{}{},
			configurations: map[string]domain.LineNetworkConfiguration{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	resolution := resolutions["proxy-wg"]
	if resolution.connected {
		t.Fatal("proxy started although its tunnel interface was missing")
	}
	if !strings.Contains(resolution.waitReason, "wg-nassim") {
		t.Fatalf("wait reason must name the interface: %q", resolution.waitReason)
	}
}

var errInterfaceMissingForTest = errTestInterfaceMissing{}

type errTestInterfaceMissing struct{}

func (errTestInterfaceMissing) Error() string { return "interface does not exist" }
