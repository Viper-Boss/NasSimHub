// Package wireguard brings a WireGuard interface up on the host and reports
// what it is actually doing.
//
// This runs in the privileged, host-networked hardware container: the api
// container drops every capability and cannot create network interfaces, which
// is why applying configuration is an agent operation rather than something the
// API server does itself.
//
// Two rules shape everything here:
//
//   - The private key is generated on this machine, stored 0600, and never
//     leaves it. It is not returned by any accessor, not written to a log, and
//     not included in any status structure.
//   - Nothing pretends to work. When the kernel has no WireGuard support, or
//     the tools are missing, the state says so instead of reporting a healthy
//     tunnel that does not exist.
package wireguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	commandTimeout = 10 * time.Second
	// A peer is considered current when its last handshake is recent. WireGuard
	// rekeys about every two minutes, so three minutes of silence means the
	// path is gone even though the interface is still up.
	handshakeFreshness = 3 * time.Minute
)

// State is the honest set of things this interface can be.
type State string

const (
	// StateUnsupported means the kernel cannot do WireGuard at all.
	StateUnsupported State = "unsupported"
	// StateToolsMissing means the kernel may be fine but `wg`/`ip` are absent.
	StateToolsMissing State = "tools_missing"
	// StateDisabled means WireGuard is supported but switched off in settings.
	StateDisabled State = "disabled"
	// StateDown means it should be up but the interface does not exist.
	StateDown State = "down"
	// StateUp means the interface exists and carries the configured peers.
	StateUp State = "up"
	// StateError means applying the configuration failed; Error says why.
	StateError State = "error"
)

// Peer is one configured counterpart. Endpoint may be empty, which is the
// normal case for a peer behind NAT that has to reach us first.
type Peer struct {
	Name                string   `json:"name"`
	PublicKey           string   `json:"public_key"`
	PresharedKeyPath    string   `json:"-"`
	Endpoint            string   `json:"endpoint,omitempty"`
	AllowedIPs          []string `json:"allowed_ips"`
	PersistentKeepalive int      `json:"persistent_keepalive,omitempty"`
}

// Config is everything needed to bring the interface up. It deliberately holds
// no private key: the key lives in the store and is read at apply time.
type Config struct {
	InterfaceName string `json:"interface_name"`
	// Address is the tunnel address with prefix, e.g. "fd88:1::1/128".
	Address    string `json:"address"`
	ListenPort int    `json:"listen_port"`
	MTU        int    `json:"mtu,omitempty"`
	Peers      []Peer `json:"peers"`
	Enabled    bool   `json:"enabled"`
	Revision   int64  `json:"revision"`
}

// PeerStatus is what the running interface reports back about a peer. It never
// carries key material beyond the public key, which is not secret.
type PeerStatus struct {
	Name            string `json:"name"`
	PublicKey       string `json:"public_key"`
	Endpoint        string `json:"endpoint,omitempty"`
	LastHandshake   int64  `json:"last_handshake_unix,omitempty"`
	TransferRxBytes int64  `json:"transfer_rx_bytes"`
	TransferTxBytes int64  `json:"transfer_tx_bytes"`
	// Current is true when a handshake happened recently enough that traffic
	// would actually flow right now.
	Current bool `json:"current"`
}

// Status is the reported state of the interface. There is no private key field
// here by design, so a status object can be logged or returned safely.
type Status struct {
	State         State        `json:"state"`
	InterfaceName string       `json:"interface_name,omitempty"`
	PublicKey     string       `json:"public_key,omitempty"`
	ListenPort    int          `json:"listen_port,omitempty"`
	Peers         []PeerStatus `json:"peers,omitempty"`
	Error         string       `json:"error,omitempty"`
	Revision      int64        `json:"revision"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

// Runner executes a command and returns its combined output. It exists so the
// control logic can be tested without root, a kernel module, or real tools.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
	// RunStdin feeds input on stdin. Key material goes through here rather than
	// through argv, which is world-readable in /proc.
	RunStdin(ctx context.Context, input string, name string, args ...string) (string, error)
	Look(name string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := exec.CommandContext(bounded, name, args...).CombinedOutput()
	text := strings.TrimSpace(string(output))
	if bounded.Err() != nil {
		return text, bounded.Err()
	}
	if err != nil {
		if text == "" {
			return "", err
		}
		return text, fmt.Errorf("%w: %s", err, firstLine(text))
	}
	return text, nil
}

// RunStdin never places the input on the command line: argv is visible to every
// process on the host through /proc, so a private key passed as an argument
// would be readable by anything running on the NAS.
func (execRunner) RunStdin(ctx context.Context, input string, name string, args ...string) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	command := exec.CommandContext(bounded, name, args...)
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if bounded.Err() != nil {
		return text, bounded.Err()
	}
	if err != nil {
		if text == "" {
			return "", err
		}
		return text, fmt.Errorf("%w: %s", err, firstLine(text))
	}
	return text, nil
}

func (execRunner) Look(name string) (string, error) { return exec.LookPath(name) }

func firstLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index]
	}
	return text
}

// Manager owns one interface and the key that belongs to it.
type Manager struct {
	mu       sync.Mutex
	runner   Runner
	stateDir string
	// Kernel support does not change while the process runs, and the probe
	// creates and deletes a real interface — far too rude to repeat on every
	// status poll.
	probed      bool
	probedState State
	probedWhy   string
}

// New builds a manager storing its key under stateDir.
func New(stateDir string) *Manager {
	return &Manager{runner: execRunner{}, stateDir: stateDir}
}

// NewWithRunner is the seam used by tests.
func NewWithRunner(stateDir string, runner Runner) *Manager {
	return &Manager{runner: runner, stateDir: stateDir}
}

func (m *Manager) privateKeyPath() string {
	return filepath.Join(m.stateDir, "private.key")
}

// ErrNoPrivateKey reports that no key has been generated yet.
var ErrNoPrivateKey = errors.New("no WireGuard private key")

// EnsurePrivateKey returns the public key, generating the private key on first
// use. The private key is never returned: callers have no legitimate reason to
// hold it, and the ones that did in earlier designs were the ones that leaked
// it into logs and QR codes.
func (m *Manager) EnsurePrivateKey(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensurePrivateKeyLocked(ctx)
}

func (m *Manager) ensurePrivateKeyLocked(ctx context.Context) (string, error) {
	if err := os.MkdirAll(m.stateDir, 0o700); err != nil {
		return "", err
	}
	path := m.privateKeyPath()
	raw, err := os.ReadFile(path)
	if err == nil {
		key := strings.TrimSpace(string(raw))
		if key == "" {
			return "", fmt.Errorf("stored WireGuard private key is empty")
		}
		return m.publicKeyOf(ctx, key)
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	generated, err := m.runner.Run(ctx, "wg", "genkey")
	if err != nil {
		return "", fmt.Errorf("generate WireGuard key: %w", err)
	}
	key := strings.TrimSpace(generated)
	if key == "" {
		return "", fmt.Errorf("generate WireGuard key: empty output")
	}
	// 0600 and written before anything else can read the directory.
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		return "", err
	}
	return m.publicKeyOf(ctx, key)
}

func (m *Manager) publicKeyOf(ctx context.Context, privateKey string) (string, error) {
	output, err := m.runner.RunStdin(ctx, privateKey, "wg", "pubkey")
	if err != nil {
		return "", fmt.Errorf("derive WireGuard public key: %w", err)
	}
	return strings.TrimSpace(output), nil
}

// Supported reports whether this kernel and userland can run WireGuard at all,
// and why not when they cannot. The answer is probed once and remembered.
func (m *Manager) Supported(ctx context.Context) (State, string) {
	if m.probed {
		return m.probedState, m.probedWhy
	}
	state, reason := m.probe(ctx)
	m.probed = true
	m.probedState = state
	m.probedWhy = reason
	return state, reason
}

func (m *Manager) probe(ctx context.Context) (State, string) {
	if _, err := m.runner.Look("wg"); err != nil {
		return StateToolsMissing, "wg command not found (install wireguard-tools)"
	}
	if _, err := m.runner.Look("ip"); err != nil {
		return StateToolsMissing, "ip command not found (install iproute2)"
	}
	// The only honest test is asking the kernel to make one.
	probe := "wgprobe0"
	if _, err := m.runner.Run(ctx, "ip", "link", "add", "dev", probe, "type", "wireguard"); err != nil {
		return StateUnsupported, "kernel refused to create a WireGuard interface: " + err.Error()
	}
	_, _ = m.runner.Run(ctx, "ip", "link", "del", "dev", probe)
	return StateUp, ""
}

// Apply makes the host match cfg and returns what actually happened.
//
// It is idempotent: calling it twice with the same configuration leaves the
// interface alone the second time apart from refreshing peers, so the caller
// can drive it from a settings watcher without tearing the tunnel down on every
// tick.
func (m *Manager) Apply(ctx context.Context, cfg Config) Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	name := strings.TrimSpace(cfg.InterfaceName)
	if name == "" {
		name = "wg-nassim"
	}
	status := Status{InterfaceName: name, Revision: cfg.Revision, UpdatedAt: now}

	if !cfg.Enabled {
		// Switching it off must actually remove the interface, not just stop
		// reporting on it: a tunnel left up after being disabled is a hole the
		// operator believes is closed.
		if m.interfaceExists(ctx, name) {
			if _, err := m.runner.Run(ctx, "ip", "link", "del", "dev", name); err != nil {
				status.State = StateError
				status.Error = "remove interface: " + err.Error()
				return status
			}
		}
		status.State = StateDisabled
		return status
	}

	if state, reason := m.Supported(ctx); state != StateUp {
		status.State = state
		status.Error = reason
		return status
	}

	publicKey, err := m.ensurePrivateKeyLocked(ctx)
	if err != nil {
		status.State = StateError
		status.Error = err.Error()
		return status
	}
	status.PublicKey = publicKey

	if err := m.bringUp(ctx, name, cfg); err != nil {
		status.State = StateError
		status.Error = err.Error()
		return status
	}

	live, err := m.readStatus(ctx, name, cfg)
	if err != nil {
		status.State = StateError
		status.Error = err.Error()
		return status
	}
	live.Revision = cfg.Revision
	live.UpdatedAt = now
	live.PublicKey = publicKey
	return live
}

func (m *Manager) interfaceExists(ctx context.Context, name string) bool {
	_, err := m.runner.Run(ctx, "ip", "link", "show", "dev", name)
	return err == nil
}

func (m *Manager) bringUp(ctx context.Context, name string, cfg Config) error {
	if !m.interfaceExists(ctx, name) {
		if _, err := m.runner.Run(ctx, "ip", "link", "add", "dev", name, "type", "wireguard"); err != nil {
			return fmt.Errorf("create interface: %w", err)
		}
	}
	if cfg.Address != "" {
		// `ip address replace` is idempotent where `add` fails on a second run.
		family := "-4"
		if strings.Contains(cfg.Address, ":") {
			family = "-6"
		}
		if _, err := m.runner.Run(ctx, "ip", family, "address", "replace", cfg.Address, "dev", name); err != nil {
			return fmt.Errorf("assign address: %w", err)
		}
	}
	if cfg.MTU > 0 {
		if _, err := m.runner.Run(ctx, "ip", "link", "set", "dev", name, "mtu", strconv.Itoa(cfg.MTU)); err != nil {
			return fmt.Errorf("set MTU: %w", err)
		}
	}
	// The private key goes to `wg setconf` on stdin, never as an argument.
	conf, err := m.renderConf(cfg)
	if err != nil {
		return err
	}
	if _, err := m.runner.RunStdin(ctx, conf, "wg", "setconf", name, "/dev/stdin"); err != nil {
		return fmt.Errorf("apply configuration: %w", err)
	}
	if _, err := m.runner.Run(ctx, "ip", "link", "set", "dev", name, "up"); err != nil {
		return fmt.Errorf("bring interface up: %w", err)
	}
	return nil
}

// renderConf builds a wg(8) configuration file. Peers with no AllowedIPs are
// skipped rather than defaulted: an empty AllowedIPs would silently become a
// peer that can send nothing, and defaulting it to a wide range would hand a
// peer more of the network than the operator asked for.
func (m *Manager) renderConf(cfg Config) (string, error) {
	raw, err := os.ReadFile(m.privateKeyPath())
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoPrivateKey, err)
	}
	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString("PrivateKey = " + strings.TrimSpace(string(raw)) + "\n")
	if cfg.ListenPort > 0 {
		b.WriteString("ListenPort = " + strconv.Itoa(cfg.ListenPort) + "\n")
	}
	for _, peer := range cfg.Peers {
		key := strings.TrimSpace(peer.PublicKey)
		if key == "" || len(peer.AllowedIPs) == 0 {
			continue
		}
		b.WriteString("\n[Peer]\n")
		b.WriteString("PublicKey = " + key + "\n")
		b.WriteString("AllowedIPs = " + strings.Join(peer.AllowedIPs, ", ") + "\n")
		if endpoint := strings.TrimSpace(peer.Endpoint); endpoint != "" {
			b.WriteString("Endpoint = " + endpoint + "\n")
		}
		if peer.PersistentKeepalive > 0 {
			b.WriteString("PersistentKeepalive = " + strconv.Itoa(peer.PersistentKeepalive) + "\n")
		}
	}
	return b.String(), nil
}

// SetPeerEndpoint retargets one peer without touching the rest of the tunnel.
// This is the operation an endpoint renegotiation performs when a peer moves
// between networks.
func (m *Manager) SetPeerEndpoint(ctx context.Context, name, publicKey, endpoint string) error {
	if strings.TrimSpace(publicKey) == "" {
		return fmt.Errorf("peer public key is required")
	}
	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		return fmt.Errorf("endpoint must be host:port: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.runner.Run(ctx, "wg", "set", name, "peer", publicKey, "endpoint", endpoint); err != nil {
		return fmt.Errorf("set peer endpoint: %w", err)
	}
	return nil
}

// Status reports the live interface without changing it.
func (m *Manager) Status(ctx context.Context, cfg Config) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	name := strings.TrimSpace(cfg.InterfaceName)
	if name == "" {
		name = "wg-nassim"
	}
	now := time.Now().UTC()
	if !cfg.Enabled {
		return Status{State: StateDisabled, InterfaceName: name, Revision: cfg.Revision, UpdatedAt: now}
	}
	if state, reason := m.Supported(ctx); state != StateUp {
		return Status{State: state, InterfaceName: name, Error: reason, Revision: cfg.Revision, UpdatedAt: now}
	}
	status, err := m.readStatus(ctx, name, cfg)
	if err != nil {
		return Status{State: StateError, InterfaceName: name, Error: err.Error(), Revision: cfg.Revision, UpdatedAt: now}
	}
	status.Revision = cfg.Revision
	status.UpdatedAt = now
	return status
}

func (m *Manager) readStatus(ctx context.Context, name string, cfg Config) (Status, error) {
	if !m.interfaceExists(ctx, name) {
		return Status{State: StateDown, InterfaceName: name}, nil
	}
	output, err := m.runner.Run(ctx, "wg", "show", name, "dump")
	if err != nil {
		return Status{}, fmt.Errorf("read interface: %w", err)
	}
	status := parseDump(output, time.Now())
	status.InterfaceName = name
	// Names come from the configuration; `wg` only knows public keys.
	for index := range status.Peers {
		for _, peer := range cfg.Peers {
			if strings.TrimSpace(peer.PublicKey) == status.Peers[index].PublicKey {
				status.Peers[index].Name = peer.Name
				break
			}
		}
	}
	return status, nil
}

// parseDump reads `wg show <if> dump`. The first line describes the interface
// (private key, public key, listen port, fwmark) and each following line is a
// peer. The interface private key in field 0 is deliberately discarded here so
// it cannot travel any further.
func parseDump(output string, now time.Time) Status {
	status := Status{State: StateUp}
	for index, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) < 2 {
			continue
		}
		if index == 0 {
			if len(fields) >= 3 {
				status.PublicKey = fields[1]
				status.ListenPort, _ = strconv.Atoi(fields[2])
			}
			continue
		}
		peer := PeerStatus{PublicKey: fields[0]}
		if len(fields) >= 3 && fields[2] != "(none)" {
			peer.Endpoint = fields[2]
		}
		if len(fields) >= 5 {
			peer.LastHandshake, _ = strconv.ParseInt(fields[4], 10, 64)
		}
		if len(fields) >= 7 {
			peer.TransferRxBytes, _ = strconv.ParseInt(fields[5], 10, 64)
			peer.TransferTxBytes, _ = strconv.ParseInt(fields[6], 10, 64)
		}
		peer.Current = peer.LastHandshake > 0 &&
			now.Sub(time.Unix(peer.LastHandshake, 0)) < handshakeFreshness
		status.Peers = append(status.Peers, peer)
	}
	return status
}
