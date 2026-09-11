package wireguard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRunner records every command and replays scripted answers, so the control
// logic is exercised without root, a kernel module, or wireguard-tools.
type fakeRunner struct {
	calls    []string
	stdin    []string
	answers  map[string]string
	failures map[string]error
	missing  map[string]bool
	// links models the host's interface table, so `ip link show` answers
	// correctly after an add or a delete. Pinning `show` to a fixed answer
	// instead would let a broken create/teardown sequence pass.
	links map[string]bool
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		answers:  map[string]string{},
		failures: map[string]error{},
		missing:  map[string]bool{},
		links:    map[string]bool{},
	}
}

func key(name string, args ...string) string {
	return strings.TrimSpace(name + " " + strings.Join(args, " "))
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	call := key(name, args...)
	f.calls = append(f.calls, call)
	if err, ok := f.failures[call]; ok {
		return "", err
	}
	if name == "ip" && len(args) >= 4 && args[0] == "link" {
		device := args[len(args)-1]
		switch args[1] {
		case "add":
			device = args[3]
			f.links[device] = true
		case "del":
			delete(f.links, device)
		case "show":
			if !f.links[device] {
				return "", fmt.Errorf("Device \"%s\" does not exist", device)
			}
		}
	}
	return f.answers[call], nil
}

func (f *fakeRunner) RunStdin(_ context.Context, input string, name string, args ...string) (string, error) {
	call := key(name, args...)
	f.calls = append(f.calls, call)
	f.stdin = append(f.stdin, input)
	if err, ok := f.failures[call]; ok {
		return "", err
	}
	return f.answers[call], nil
}

func (f *fakeRunner) Look(name string) (string, error) {
	if f.missing[name] {
		return "", fmt.Errorf("not found")
	}
	return "/usr/bin/" + name, nil
}

func (f *fakeRunner) ran(call string) bool {
	for _, item := range f.calls {
		if item == call {
			return true
		}
	}
	return false
}

func readyRunner() *fakeRunner {
	runner := newFakeRunner()
	runner.answers["wg genkey"] = "cHJpdmF0ZS1rZXktZm9yLXRlc3RpbmctMzJieXRlcw=="
	runner.answers["wg pubkey"] = "cHVibGljLWtleS1mb3ItdGVzdGluZy0zMmJ5dGVzLg=="
	return runner
}

func TestApplyBringsTheInterfaceUpAndReportsPeers(t *testing.T) {
	runner := readyRunner()
	handshake := time.Now().Add(-30 * time.Second).Unix()
	runner.answers["wg show wg0 dump"] = strings.Join([]string{
		"PRIVATEKEYSHOULDNOTLEAK=\tcHVibGljLWtleS1mb3ItdGVzdGluZy0zMmJ5dGVzLg==\t51820\toff",
		fmt.Sprintf("peerkeyAAA=\t(none)\t203.0.113.9:51820\tfd88::2/128\t%d\t2048\t4096\toff", handshake),
	}, "\n")

	manager := NewWithRunner(t.TempDir(), runner)
	status := manager.Apply(context.Background(), Config{
		InterfaceName: "wg0",
		Address:       "fd88::1/128",
		ListenPort:    51820,
		Enabled:       true,
		Revision:      7,
		Peers: []Peer{{
			Name:                "phone",
			PublicKey:           "peerkeyAAA=",
			AllowedIPs:          []string{"fd88::2/128"},
			PersistentKeepalive: 25,
		}},
	})

	if status.State != StateUp {
		t.Fatalf("state = %q, error = %q", status.State, status.Error)
	}
	if !runner.ran("ip link add dev wg0 type wireguard") {
		t.Fatalf("interface was never created: %v", runner.calls)
	}
	if !runner.ran("ip -6 address replace fd88::1/128 dev wg0") {
		t.Fatalf("address was never assigned: %v", runner.calls)
	}
	if !runner.ran("ip link set dev wg0 up") {
		t.Fatal("interface was never brought up")
	}
	if status.Revision != 7 {
		t.Fatalf("revision = %d; want 7", status.Revision)
	}
	if len(status.Peers) != 1 {
		t.Fatalf("peers = %+v", status.Peers)
	}
	peer := status.Peers[0]
	if peer.Name != "phone" {
		t.Fatalf("peer name = %q; configuration names are not being matched to keys", peer.Name)
	}
	if !peer.Current {
		t.Fatal("a 30-second-old handshake must count as current")
	}
	if peer.Endpoint != "203.0.113.9:51820" || peer.TransferRxBytes != 2048 || peer.TransferTxBytes != 4096 {
		t.Fatalf("peer = %+v", peer)
	}
}

// The private key must reach `wg` on stdin and never appear in argv, which is
// readable by every process on the host through /proc.
func TestPrivateKeyNeverAppearsInArguments(t *testing.T) {
	runner := readyRunner()
	runner.answers["wg show wg0 dump"] = "PRIV=\tPUB=\t51820\toff"

	directory := t.TempDir()
	manager := NewWithRunner(directory, runner)
	manager.Apply(context.Background(), Config{InterfaceName: "wg0", Enabled: true})

	stored, err := os.ReadFile(filepath.Join(directory, "private.key"))
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.TrimSpace(string(stored))
	if secret == "" {
		t.Fatal("no private key was stored")
	}
	for _, call := range runner.calls {
		if strings.Contains(call, secret) {
			t.Fatalf("private key leaked into a command line: %q", call)
		}
	}
	found := false
	for _, input := range runner.stdin {
		if strings.Contains(input, secret) {
			found = true
		}
	}
	if !found {
		t.Fatal("the private key never reached wg on stdin")
	}
}

func TestPrivateKeyFileIsOwnerOnlyAndReused(t *testing.T) {
	runner := readyRunner()
	directory := t.TempDir()
	manager := NewWithRunner(directory, runner)

	first, err := manager.EnsurePrivateKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(directory, "private.key"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("private key mode = %o; want 600", mode)
	}

	// A second call must reuse the stored key rather than rolling it, which
	// would silently invalidate every peer that already pinned the public key.
	second, err := manager.EnsurePrivateKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("the key was regenerated on the second call")
	}
	generated := 0
	for _, call := range runner.calls {
		if call == "wg genkey" {
			generated++
		}
	}
	if generated != 1 {
		t.Fatalf("wg genkey ran %d times; want 1", generated)
	}
}

// Disabling must remove the interface. Leaving it up while reporting "disabled"
// would leave a path open that the operator believes is closed.
func TestDisablingRemovesTheInterface(t *testing.T) {
	runner := readyRunner()
	runner.links["wg0"] = true
	manager := NewWithRunner(t.TempDir(), runner)

	status := manager.Apply(context.Background(), Config{InterfaceName: "wg0", Enabled: false})
	if status.State != StateDisabled {
		t.Fatalf("state = %q", status.State)
	}
	if !runner.ran("ip link del dev wg0") {
		t.Fatalf("interface was left up: %v", runner.calls)
	}
}

func TestMissingToolsAreReportedNotFaked(t *testing.T) {
	runner := readyRunner()
	runner.missing["wg"] = true
	manager := NewWithRunner(t.TempDir(), runner)

	status := manager.Apply(context.Background(), Config{InterfaceName: "wg0", Enabled: true})
	if status.State != StateToolsMissing {
		t.Fatalf("state = %q; want tools_missing", status.State)
	}
	if status.Error == "" {
		t.Fatal("a missing tool must say which one")
	}
}

func TestKernelWithoutWireGuardIsReportedUnsupported(t *testing.T) {
	runner := readyRunner()
	runner.failures["ip link add dev wgprobe0 type wireguard"] = fmt.Errorf("operation not supported")
	manager := NewWithRunner(t.TempDir(), runner)

	status := manager.Apply(context.Background(), Config{InterfaceName: "wg0", Enabled: true})
	if status.State != StateUnsupported {
		t.Fatalf("state = %q; want unsupported", status.State)
	}
}

// A peer with no AllowedIPs must be skipped rather than defaulted: defaulting
// it to a wide range would hand that peer more of the network than anyone asked
// for, and 0.0.0.0/0 here would expose the whole NAS.
func TestPeersWithoutAllowedIPsAreSkipped(t *testing.T) {
	runner := readyRunner()
	manager := NewWithRunner(t.TempDir(), runner)
	if _, err := manager.EnsurePrivateKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	conf, err := manager.renderConf(Config{
		InterfaceName: "wg0",
		Peers: []Peer{
			{Name: "broken", PublicKey: "peerkeyAAA="},
			{Name: "good", PublicKey: "peerkeyBBB=", AllowedIPs: []string{"fd88::2/128"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(conf, "peerkeyAAA=") {
		t.Fatal("a peer with no AllowedIPs was written into the configuration")
	}
	if !strings.Contains(conf, "peerkeyBBB=") {
		t.Fatal("the valid peer is missing")
	}
	if strings.Contains(conf, "0.0.0.0/0") || strings.Contains(conf, "::/0") {
		t.Fatal("a default route was invented for a peer")
	}
}

func TestStaleHandshakeIsNotCurrent(t *testing.T) {
	stale := time.Now().Add(-10 * time.Minute).Unix()
	dump := "PRIV=\tPUB=\t51820\toff\n" +
		fmt.Sprintf("peerkeyAAA=\t(none)\t(none)\tfd88::2/128\t%d\t0\t0\toff", stale)

	status := parseDump(dump, time.Now())
	if len(status.Peers) != 1 {
		t.Fatalf("peers = %+v", status.Peers)
	}
	if status.Peers[0].Current {
		t.Fatal("a 10-minute-old handshake must not count as a live path")
	}
	if status.Peers[0].Endpoint != "" {
		t.Fatalf("endpoint = %q; (none) must not become a literal endpoint", status.Peers[0].Endpoint)
	}
}

// The dump's first field is the interface private key. It must never survive
// parsing into anything a caller can reach.
func TestParseDumpDiscardsThePrivateKey(t *testing.T) {
	dump := "SECRETPRIVATEKEY=\tPUBLICKEY=\t51820\toff"
	status := parseDump(dump, time.Now())
	if status.PublicKey != "PUBLICKEY=" {
		t.Fatalf("public key = %q", status.PublicKey)
	}
	if strings.Contains(fmt.Sprintf("%+v", status), "SECRETPRIVATEKEY") {
		t.Fatal("the interface private key survived into the status structure")
	}
	if status.ListenPort != 51820 {
		t.Fatalf("listen port = %d", status.ListenPort)
	}
}

func TestSetPeerEndpointRejectsMalformedInput(t *testing.T) {
	runner := readyRunner()
	manager := NewWithRunner(t.TempDir(), runner)
	if err := manager.SetPeerEndpoint(context.Background(), "wg0", "peerkeyAAA=", "no-port"); err == nil {
		t.Fatal("an endpoint without a port must be rejected")
	}
	if err := manager.SetPeerEndpoint(context.Background(), "wg0", "", "203.0.113.9:51820"); err == nil {
		t.Fatal("an empty peer key must be rejected")
	}
	if err := manager.SetPeerEndpoint(context.Background(), "wg0", "peerkeyAAA=", "203.0.113.9:51820"); err != nil {
		t.Fatal(err)
	}
	if !runner.ran("wg set wg0 peer peerkeyAAA= endpoint 203.0.113.9:51820") {
		t.Fatalf("endpoint was not applied: %v", runner.calls)
	}
}

// Applying twice must not tear the tunnel down and build it again: the second
// pass should find the interface present and leave it alone.
func TestApplyIsIdempotent(t *testing.T) {
	runner := readyRunner()
	runner.answers["wg show wg0 dump"] = "PRIV=\tPUB=\t51820\toff"
	manager := NewWithRunner(t.TempDir(), runner)
	cfg := Config{InterfaceName: "wg0", Address: "10.99.0.1/32", Enabled: true}

	manager.Apply(context.Background(), cfg)
	manager.Apply(context.Background(), cfg)

	creates := 0
	for _, call := range runner.calls {
		if call == "ip link add dev wg0 type wireguard" {
			creates++
		}
		if call == "ip link del dev wg0" {
			t.Fatal("a re-apply tore the interface down")
		}
	}
	if creates != 1 {
		t.Fatalf("interface created %d times across two applies; want exactly 1", creates)
	}
	if !runner.ran("ip -4 address replace 10.99.0.1/32 dev wg0") {
		t.Fatal("an IPv4 address must use the -4 family")
	}
}

// The capability probe creates and deletes a real interface, so it must run
// once per process rather than on every status poll.
func TestCapabilityIsProbedOnce(t *testing.T) {
	runner := readyRunner()
	runner.answers["wg show wg0 dump"] = "PRIV=\tPUB=\t51820\toff"
	manager := NewWithRunner(t.TempDir(), runner)
	cfg := Config{InterfaceName: "wg0", Enabled: true}

	manager.Apply(context.Background(), cfg)
	manager.Status(context.Background(), cfg)
	manager.Status(context.Background(), cfg)

	probes := 0
	for _, call := range runner.calls {
		if call == "ip link add dev wgprobe0 type wireguard" {
			probes++
		}
	}
	if probes != 1 {
		t.Fatalf("kernel probed %d times; want 1", probes)
	}
}
