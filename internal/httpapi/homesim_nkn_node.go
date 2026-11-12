package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Optional embedded NKN relay node (nknd) for HomeSIM deployments. The node is
// disabled by default; an administrator can enable it from the settings page,
// optionally naming a BeneficiaryAddr so mining rewards flow to their own NKN
// wallet. nknd runs as a supervised child process of the API server inside the
// same container (official static binaries copied into the runtime image), so
// no Docker socket, extra container, or host port mapping is required — the
// node only serves local NKN clients on the same host.

const (
	nknNodeDirEnv       = "MODEMDECK_NKN_NODE_DIR"
	nknNodeSettingsFile = "settings.json"
	nknNodeConfigFile   = "config.json"
	nknNodeWalletFile   = "wallet.json"
	nknNodePasswordFile = "wallet.pswd"
	nknNodeLogFile      = "nknd.log"
	nknNodeLogMaxBytes  = 8 << 20
	nknNodeRPCPort      = 30003
	// nknNodeRPCAddressEnv overrides how the gateway reaches the built-in
	// node's RPC from this process. Inside a container the host loopback
	// (127.0.0.1) is the container itself, so compose.fnos.yml sets this to
	// http://host.docker.internal:30003 (host-gateway); plain-host installs
	// keep the loopback default.
	nknNodeRPCAddressEnv = "HOMESIM_NKN_NODE_RPC"
	nknNodeLogCap        = 400
	nknNodeRestartDelay  = 5 * time.Second
	nknNodeStopGrace     = 8 * time.Second
	nknWebSource         = "/usr/local/lib/nkn/web"

	nknNodeStateUnavailable = "unavailable"
	nknNodeStateStopped     = "stopped"
	nknNodeStateRunning     = "running"
	nknNodeStateError       = "error"
)

// nknNodeOfficialSeeds mirrors the official mainnet config template
// (config.mainnet.json ships mainnet-seed-0001..0044). nknd probes seed
// candidates concurrently, so a longer list directly improves the odds that
// the FIRST launch reaches a reachable seed quickly — notably from networks
// where some overseas seeds are flaky. Order is irrelevant to correctness.
// The China community seed sits first anyway: the official 44 seeds all
// resolve overseas (verified 2026-09-08), so a domestic candidate wins the
// race from China deployments when it is up. See dev/collect_nkn_cn_nodes.py.
var nknNodeOfficialSeeds = []string{
	"http://183.53.109.45:30003", // China community seed, verified 2026-09-08
	"http://seed.nkn.org:30003",
	"http://mainnet-seed-0001.nkn.org:30003",
	"http://mainnet-seed-0002.nkn.org:30003",
	"http://mainnet-seed-0003.nkn.org:30003",
	"http://mainnet-seed-0004.nkn.org:30003",
	"http://mainnet-seed-0005.nkn.org:30003",
	"http://mainnet-seed-0006.nkn.org:30003",
	"http://mainnet-seed-0007.nkn.org:30003",
	"http://mainnet-seed-0008.nkn.org:30003",
	"http://mainnet-seed-0009.nkn.org:30003",
	"http://mainnet-seed-0010.nkn.org:30003",
	"http://mainnet-seed-0011.nkn.org:30003",
	"http://mainnet-seed-0012.nkn.org:30003",
	"http://mainnet-seed-0013.nkn.org:30003",
	"http://mainnet-seed-0014.nkn.org:30003",
	"http://mainnet-seed-0015.nkn.org:30003",
	"http://mainnet-seed-0016.nkn.org:30003",
	"http://mainnet-seed-0017.nkn.org:30003",
	"http://mainnet-seed-0018.nkn.org:30003",
	"http://mainnet-seed-0019.nkn.org:30003",
	"http://mainnet-seed-0020.nkn.org:30003",
	"http://mainnet-seed-0021.nkn.org:30003",
	"http://mainnet-seed-0022.nkn.org:30003",
	"http://mainnet-seed-0023.nkn.org:30003",
	"http://mainnet-seed-0024.nkn.org:30003",
	"http://mainnet-seed-0025.nkn.org:30003",
	"http://mainnet-seed-0026.nkn.org:30003",
	"http://mainnet-seed-0027.nkn.org:30003",
	"http://mainnet-seed-0028.nkn.org:30003",
	"http://mainnet-seed-0029.nkn.org:30003",
	"http://mainnet-seed-0030.nkn.org:30003",
	"http://mainnet-seed-0031.nkn.org:30003",
	"http://mainnet-seed-0032.nkn.org:30003",
	"http://mainnet-seed-0033.nkn.org:30003",
	"http://mainnet-seed-0034.nkn.org:30003",
	"http://mainnet-seed-0035.nkn.org:30003",
	"http://mainnet-seed-0036.nkn.org:30003",
	"http://mainnet-seed-0037.nkn.org:30003",
	"http://mainnet-seed-0038.nkn.org:30003",
	"http://mainnet-seed-0039.nkn.org:30003",
	"http://mainnet-seed-0040.nkn.org:30003",
	"http://mainnet-seed-0041.nkn.org:30003",
	"http://mainnet-seed-0042.nkn.org:30003",
	"http://mainnet-seed-0043.nkn.org:30003",
	"http://mainnet-seed-0044.nkn.org:30003",
}

// nknNodeSettings is the persisted administrator configuration. It lives in
// the node data directory next to wallet.json so the whole feature state is
// contained in one folder that survives container upgrades.
type nknNodeSettings struct {
	Enabled         bool   `json:"enabled"`
	BeneficiaryAddr string `json:"beneficiary_addr"`
	// SeedOverrides optionally lists user-supplied seed RPC addresses
	// (comma/space separated, http(s)://host[:port]). They are placed ahead
	// of the cached and official seeds in the generated node config so
	// self-hosted or regionally closer seeds win the first-connect race.
	SeedOverrides string `json:"seed_overrides,omitempty"`
	Revision      int64  `json:"revision"`
}

type nknNodeRuntime struct {
	State         string    `json:"state"`
	WalletAddress string    `json:"wallet_address,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	SyncState     string    `json:"sync_state,omitempty"`
	// WalletBalance is the current balance of the address that receives
	// mining rewards (BeneficiaryAddr, or the node wallet when unset),
	// formatted like "12.34 NKN". RelayCount is the node's lifetime relayed
	// message count — the direct driver of mining rewards.
	WalletBalance string `json:"wallet_balance,omitempty"`
	RelayCount    uint64 `json:"relay_count,omitempty"`
	Error         string `json:"error,omitempty"`
}

type nknNodeStatusResponse struct {
	Settings nknNodeSettings `json:"settings"`
	Runtime  nknNodeRuntime  `json:"runtime"`
	// WalletExists reports whether wallet.json is present. The plaintext
	// password is deliberately NOT exposed here (removed in the wallet
	// self-custody rework): it is only known to the administrator who chose
	// it when creating or importing the wallet.
	WalletExists  bool `json:"wallet_exists"`
	BinaryPresent bool `json:"binary_present"`
}

type nknNodeLogsResponse struct {
	Lines []string `json:"lines"`
}

type nknNodeUpdateRequest struct {
	Enabled          bool   `json:"enabled"`
	BeneficiaryAddr  string `json:"beneficiary_addr"`
	SeedOverrides    string `json:"seed_overrides"`
	ExpectedRevision int64  `json:"expected_revision"`
}

// nknNodeConfig mirrors the official nknd config.json (mainnet template) so
// the generated file stays familiar to anyone who has run a standalone node.
type nknNodeConfig struct {
	BeneficiaryAddr     string   `json:"BeneficiaryAddr"`
	SeedList            []string `json:"SeedList"`
	ChordPort           int      `json:"ChordPort"`
	NodePort            int      `json:"NodePort"`
	HttpWsPort          int      `json:"HttpWsPort"`
	HttpJsonPort        int      `json:"HttpJsonPort"`
	LogLevel            int      `json:"LogLevel"`
	SyncMode            string   `json:"SyncMode"`
	WebGuiCreateWallet  bool     `json:"WebGuiCreateWallet"`
	PasswordFile        string   `json:"PasswordFile"`
	TxPoolTotalTxCap    int      `json:"TxPoolTotalTxCap"`
	TxPoolMaxMemorySize int      `json:"TxPoolMaxMemorySize"`
	NumTxnPerBlock      int      `json:"NumTxnPerBlock"`
	MaxBlockSize        int      `json:"MaxBlockSize"`
}

type nknWalletFile struct {
	Address string `json:"Address"`
}

type nknNodeManager struct {
	operationMu sync.Mutex
	mu          sync.Mutex
	logger      *slog.Logger
	dir         string
	cmd         *exec.Cmd
	done        chan struct{}
	running     bool
	stopReq     bool
	startedAt   time.Time
	lastErr     string
	state       string
	logs        []string
}

func nknNodeDataDir() string {
	if dir := strings.TrimSpace(os.Getenv(nknNodeDirEnv)); dir != "" {
		return dir
	}
	if db := strings.TrimSpace(os.Getenv("MODEMDECK_DATABASE_PATH")); db != "" {
		return filepath.Join(filepath.Dir(db), "nkn-node")
	}
	return "/data/nkn-node"
}

func newNKNNodeManager(logger *slog.Logger) *nknNodeManager {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &nknNodeManager{
		logger: logger,
		dir:    nknNodeDataDir(),
		state:  nknNodeStateStopped,
	}
}

// nknNodeBinaryAvailable reports whether the official nknd binaries are
// present. Outside the container runtime (development machines) they are not,
// and the feature surfaces as unavailable instead of failing silently.
func nknNodeBinaryAvailable() bool {
	nknd, err := exec.LookPath("nknd")
	if err != nil || nknd == "" {
		return false
	}
	_, err = exec.LookPath("nknc")
	return err == nil
}

func (m *nknNodeManager) appendLog(line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, line)
	if len(m.logs) > nknNodeLogCap {
		m.logs = m.logs[len(m.logs)-nknNodeLogCap:]
	}
}

func (m *nknNodeManager) setError(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastErr = message
	if m.running {
		m.state = nknNodeStateError
	}
	m.logger.Error("nkn-node: " + message)
}

// loadSettings reads settings.json; a missing file means the factory default
// (disabled, empty beneficiary).
func (m *nknNodeManager) loadSettings() (nknNodeSettings, error) {
	settings := nknNodeSettings{}
	raw, err := os.ReadFile(filepath.Join(m.dir, nknNodeSettingsFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return settings, nil
		}
		return settings, err
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return settings, fmt.Errorf("corrupt %s: %w", nknNodeSettingsFile, err)
	}
	return settings, nil
}

func (m *nknNodeManager) saveSettings(settings nknNodeSettings) error {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.dir, nknNodeSettingsFile), raw, 0o600)
}

// nknNodeParseSeedOverrides splits a raw comma/space separated list of seed
// addresses, validates each entry, and returns them in order. An empty input
// yields nil. Invalid entries produce an error naming the offender.
func nknNodeParseSeedOverrides(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ';'
	})
	var seeds []string
	for _, field := range fields {
		seed := strings.TrimSpace(field)
		if seed == "" {
			continue
		}
		if !nknNodeSeedAddrPattern.MatchString(seed) {
			return nil, fmt.Errorf("invalid seed address %q: use http://host:port (official seeds use port 30003)", seed)
		}
		if idx := strings.LastIndex(seed, ":"); idx >= 0 && idx > len("https://") {
			if port, err := strconv.Atoi(seed[idx+1:]); err == nil && (port < 1 || port > 65535) {
				return nil, fmt.Errorf("invalid seed address %q: port out of range", seed)
			}
		}
		seeds = append(seeds, seed)
	}
	return seeds, nil
}

var nknNodeSeedAddrPattern = regexp.MustCompile(`^https?://[A-Za-z0-9._\-]+(:[0-9]{1,5})?$`)

// writeNodeConfig regenerates config.json from the persisted settings. Called
// on every launch so a BeneficiaryAddr change takes effect on restart. The
// seed list is assembled in priority order: user-supplied seed overrides,
// then locally cached low-latency nodes discovered by the running node (see
// refreshSeedCache), then the official mainnet seeds as fallback.
func (m *nknNodeManager) writeNodeConfig(settings nknNodeSettings) error {
	// Priority order: user seed overrides (explicit intent) > locally cached
	// low-latency nodes > official mainnet seeds as fallback. Built
	// explicitly into a fresh slice to keep the ordering unambiguous.
	var custom []string
	if overrides, err := nknNodeParseSeedOverrides(settings.SeedOverrides); err != nil {
		return err
	} else {
		custom = overrides
	}
	cached := m.loadSeedCache()
	seen := make(map[string]struct{}, len(nknNodeOfficialSeeds)+len(custom)+len(cached))
	merged := make([]string, 0, len(nknNodeOfficialSeeds)+len(custom)+len(cached))
	add := func(seed string) {
		if _, dup := seen[seed]; dup {
			return
		}
		seen[seed] = struct{}{}
		merged = append(merged, seed)
	}
	for _, seed := range custom {
		add(seed)
	}
	for _, seed := range cached {
		add(seed)
	}
	for _, seed := range nknNodeOfficialSeeds {
		add(seed)
	}
	seedList := merged
	config := nknNodeConfig{
		BeneficiaryAddr:     strings.TrimSpace(settings.BeneficiaryAddr),
		SeedList:            seedList,
		ChordPort:           30000,
		NodePort:            30001,
		HttpWsPort:          30002,
		HttpJsonPort:        nknNodeRPCPort,
		LogLevel:            1,
		SyncMode:            "light",
		WebGuiCreateWallet:  false,
		PasswordFile:        nknNodePasswordFile,
		TxPoolTotalTxCap:    32768,
		TxPoolMaxMemorySize: 8,
		NumTxnPerBlock:      4096,
		MaxBlockSize:        2097152,
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.dir, nknNodeConfigFile), raw, 0o600)
}

// errNKNWalletMissing is returned when nknd is launched without a wallet.
// Unlike transient launch failures the supervisor stops retrying: a wallet
// must be created or imported explicitly by the administrator first
// (self-custody: the gateway never fabricates keys behind the user's back).
var errNKNWalletMissing = errors.New("no node wallet: create or import one in the NKN node settings first")

// nknWalletMinPasswordLen mirrors the sanity threshold used by mainstream
// self-custody wallets. nknd itself enforces no minimum; we do, because the
// password is the only protection of the keystore on disk.
const nknWalletMinPasswordLen = 8

// validateNKNKeystore performs a structural sanity check on an uploaded
// wallet.json (NKN keystore): JSON object, Address starting with NKN, and a
// non-empty Crypto section. It does NOT verify the password — nknd would
// refuse to start with a wrong one and the error shows up in the node log.
func validateNKNKeystore(raw string) (string, error) {
	var generic map[string]any
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return "", fmt.Errorf("keystore is not valid JSON: %w", err)
	}
	var parsed struct {
		Address string         `json:"Address"`
		Crypto  map[string]any `json:"Crypto"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return "", fmt.Errorf("keystore is not a valid wallet file: %w", err)
	}
	address := strings.TrimSpace(parsed.Address)
	if !strings.HasPrefix(address, "NKN") || len(address) < 20 {
		return "", errors.New("keystore Address field missing or not an NKN address")
	}
	if len(parsed.Crypto) == 0 {
		return "", errors.New("keystore Crypto section missing — refusing to store a keyless file")
	}
	return address, nil
}

// createWallet creates wallet.json with a user-chosen password (written to
// wallet.pswd so nknd can unlock itself after an unattended restart) plus the
// keystore itself, exactly like the official entrypoint does: nknc wallet -c
// reads the password twice from stdin (password + confirmation).
func (m *nknNodeManager) createWallet(password string) (string, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if strings.ContainsAny(password, "\r\n") {
		return "", errors.New("password must not contain line breaks")
	}
	if len(password) < nknWalletMinPasswordLen {
		return "", fmt.Errorf("password must be at least %d characters", nknWalletMinPasswordLen)
	}
	walletPath := filepath.Join(m.dir, nknNodeWalletFile)
	if walletExists(walletPath) {
		return "", errors.New("a node wallet already exists; delete it first if you really want a new one")
	}
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return "", err
	}
	nknc, err := exec.LookPath("nknc")
	if err != nil {
		return "", fmt.Errorf("nknc binary not found: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, nknc, "wallet", "-c")
	command.Dir = m.dir
	stdin, err := command.StdinPipe()
	if err != nil {
		return "", err
	}
	output := &bytes.Buffer{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		return "", err
	}
	secret := password + "\n"
	if _, err := io.WriteString(stdin, secret+secret); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return "", err
	}
	_ = stdin.Close()
	if err := command.Wait(); err != nil {
		return "", fmt.Errorf("wallet creation failed: %w", err)
	}
	if !walletExists(walletPath) {
		return "", fmt.Errorf("nknc wallet -c finished but %s was not created", nknNodeWalletFile)
	}
	if err := os.WriteFile(filepath.Join(m.dir, nknNodePasswordFile), []byte(password+"\n"), 0o600); err != nil {
		return "", err
	}
	m.appendLog("wallet created by administrator")
	return m.walletAddress(), nil
}

// importWallet stores an administrator-provided keystore (wallet.json
// contents, e.g. exported from another NKN wallet) and its password. The
// password is required because nknd needs wallet.pswd to unlock the node
// after an unattended restart; it never leaves the NAS.
func (m *nknNodeManager) importWallet(keystoreJSON, password string) (string, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if walletExists(filepath.Join(m.dir, nknNodeWalletFile)) {
		return "", errors.New("a wallet already exists; back it up before explicitly deleting it")
	}
	m.mu.Lock()
	running := m.running
	m.mu.Unlock()
	if running {
		return "", errors.New("stop the node before importing a wallet")
	}
	keystoreJSON = strings.TrimSpace(keystoreJSON)
	if keystoreJSON == "" {
		return "", errors.New("keystore contents required")
	}
	if len(keystoreJSON) > 64<<10 {
		return "", errors.New("keystore too large")
	}
	if strings.ContainsAny(password, "\r\n") {
		return "", errors.New("password must not contain line breaks")
	}
	if len(password) < nknWalletMinPasswordLen {
		return "", fmt.Errorf("password must be at least %d characters", nknWalletMinPasswordLen)
	}
	address, err := validateNKNKeystore(keystoreJSON)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(m.dir, nknNodeWalletFile), []byte(keystoreJSON), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(m.dir, nknNodePasswordFile), []byte(password+"\n"), 0o600); err != nil {
		return "", err
	}
	m.appendLog("wallet imported by administrator: " + address)
	return address, nil
}

// deleteWallet irreversibly removes wallet.json and wallet.pswd after
// stopping the node. This is the self-custody "wipe keys" action: once gone,
// funds can only be recovered from an external backup of the keystore.
func (m *nknNodeManager) deleteWallet(confirm bool) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if !confirm {
		return errors.New("confirm=true is required to delete the node wallet")
	}
	m.stop()
	removed := 0
	for _, name := range []string{nknNodeWalletFile, nknNodePasswordFile} {
		if err := os.Remove(filepath.Join(m.dir, name)); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		removed++
	}
	if removed == 0 {
		return errors.New("no node wallet to delete")
	}
	m.appendLog("wallet deleted by administrator")
	return nil
}

func walletExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// ---------------------------------------------------------------------------
// Seed cache: a tiny DHT-flavored persistence layer. While nknd runs we ask
// its local JSON-RPC for the Chord ring neighbours, probe each candidate's
// RPC endpoint for latency, and keep the fastest ones in seed-cache.json.
// The next launch reads the cache and puts those nodes ahead of the official
// seeds, so a restart no longer starts cold from overseas seeds.
// ---------------------------------------------------------------------------

const (
	nknNodeSeedCacheFile    = "seed-cache.json"
	nknNodeSeedCacheSize    = 16
	nknNodeSeedCacheMaxAge  = 14 * 24 * time.Hour
	nknNodeSeedProbeTimeout = 1500 * time.Millisecond
	nknNodeSeedProbeMax     = 24
	nknNodeSeedProbeWorkers = 8
	nknNodeCacheInterval    = 10 * time.Minute
)

// nknSeedCache is the persisted latency snapshot of recently reachable NKN
// RPC endpoints. It lives in the node data directory and only influences the
// generated config — deleting it is always safe.
type nknSeedCache struct {
	UpdatedAt time.Time          `json:"updated_at"`
	Nodes     []nknSeedCacheNode `json:"nodes"`
}

type nknSeedCacheNode struct {
	Addr  string `json:"addr"` // http://ip:30003
	RTTms int    `json:"rtt_ms"`
}

// runSeedCacheSampler periodically refreshes seed-cache.json while the node
// supervisor is alive. It exits when the supervisor winds down (done closed)
// or a stop was requested.
func (m *nknNodeManager) runSeedCacheSampler(done <-chan struct{}) {
	// First sample shortly after launch (the ring needs a moment to fill,
	// so give nknd 90 seconds), then refresh on a fixed interval.
	select {
	case <-time.After(90 * time.Second):
	case <-done:
		return
	}
	m.refreshSeedCache()
	ticker := time.NewTicker(nknNodeCacheInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if m.stopRequested() {
				return
			}
			m.refreshSeedCache()
		case <-done:
			return
		}
	}
}

// nknRPCClient is a variable so tests can stub the probe endpoint.
var nknRPCClient = &http.Client{Timeout: nknNodeSeedProbeTimeout}

// nknRPCProbe dials a candidate RPC endpoint with getconnectioncount and
// returns the round-trip milliseconds; failures return -1.
func nknRPCProbe(addr string) int {
	body := strings.NewReader(`{"jsonrpc":"2.0","method":"getconnectioncount","params":{},"id":1}`)
	start := time.Now()
	response, err := nknRPCClient.Post(addr, "application/json", body)
	if err != nil {
		return -1
	}
	defer response.Body.Close()
	var payload struct {
		Result any `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 512)).Decode(&payload); err != nil {
		return -1
	}
	return int(time.Since(start).Milliseconds())
}

// collectRingCandidates asks the local nknd for its Chord ring info and
// extracts candidate RPC addresses (IP with the standard seed port 30003).
// Failures return an empty slice — the cache simply keeps its old contents.
var nknRingInfoEndpoint = fmt.Sprintf("http://127.0.0.1:%d", nknNodeRPCPort)

func collectRingCandidates() []string {
	body := strings.NewReader(`{"jsonrpc":"2.0","method":"getchordringinfo","params":{},"id":1}`)
	response, err := nknRPCClient.Post(nknRingInfoEndpoint, "application/json", body)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	var payload struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return nil
	}
	matches := nknNodeAddrPattern.FindAllStringSubmatch(string(payload.Result), -1)
	seen := make(map[string]struct{}, len(matches))
	candidates := make([]string, 0, len(matches))
	for _, match := range matches {
		addr := "http://" + match[1] + ":" + strconv.Itoa(nknNodeRPCPort)
		if _, dup := seen[addr]; dup {
			continue
		}
		seen[addr] = struct{}{}
		candidates = append(candidates, addr)
		if len(candidates) >= nknNodeSeedProbeMax {
			break
		}
	}
	return candidates
}

var nknNodeAddrPattern = regexp.MustCompile(`(\d{1,3}(?:\.\d{1,3}){3}):(\d{2,5})`)

// refreshSeedCache probes ring candidates concurrently, ranks the reachable
// ones by latency, and persists the best slice to seed-cache.json.
func (m *nknNodeManager) refreshSeedCache() {
	candidates := collectRingCandidates()
	if len(candidates) == 0 {
		return
	}
	type probeResult struct {
		addr string
		rtt  int
	}
	results := make(chan probeResult, len(candidates))
	workers := nknNodeSeedProbeWorkers
	if len(candidates) < workers {
		workers = len(candidates)
	}
	next := make(chan string, len(candidates))
	for _, addr := range candidates {
		next <- addr
	}
	close(next)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for addr := range next {
				if m.stopRequested() {
					return
				}
				if rtt := nknRPCProbe(addr); rtt >= 0 {
					results <- probeResult{addr: addr, rtt: rtt}
				}
			}
		}()
	}
	wg.Wait()
	close(results)
	alive := make([]nknSeedCacheNode, 0, len(results))
	for result := range results {
		alive = append(alive, nknSeedCacheNode{Addr: result.addr, RTTms: result.rtt})
	}
	if len(alive) == 0 {
		return
	}
	sort.Slice(alive, func(i, j int) bool { return alive[i].RTTms < alive[j].RTTms })
	if len(alive) > nknNodeSeedCacheSize {
		alive = alive[:nknNodeSeedCacheSize]
	}
	cache := nknSeedCache{UpdatedAt: time.Now().UTC(), Nodes: alive}
	raw, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return
	}
	target := filepath.Join(m.dir, nknNodeSeedCacheFile)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return
	}
	m.appendLog(fmt.Sprintf("seed cache refreshed: %d nodes, best %dms", len(alive), alive[0].RTTms))
}

// loadSeedCache returns the cached seed addresses if the file is readable and
// fresh enough; nil otherwise. A stale cache is worse than no cache only
// marginally (nknd probes candidates concurrently), but the age cap keeps
// long-dead nodes from ever dominating the list.
func (m *nknNodeManager) loadSeedCache() []string {
	raw, err := os.ReadFile(filepath.Join(m.dir, nknNodeSeedCacheFile))
	if err != nil {
		return nil
	}
	var cache nknSeedCache
	if err := json.Unmarshal(raw, &cache); err != nil {
		return nil
	}
	if cache.UpdatedAt.IsZero() || time.Since(cache.UpdatedAt) > nknNodeSeedCacheMaxAge {
		return nil
	}
	addrs := make([]string, 0, len(cache.Nodes))
	for _, node := range cache.Nodes {
		if addr := strings.TrimSpace(node.Addr); addr != "" {
			addrs = append(addrs, addr)
		}
	}
	if len(addrs) == 0 {
		return nil
	}
	return addrs
}

// seedCandidates merges the gateway's whole seed pool in priority order
// (user overrides > cached low-latency nodes > official mainnet seeds),
// deduplicated and capped at limit. Used by the network diagnostics endpoint
// to report which NKN nodes the gateway can currently reach, regardless of
// whether the embedded node is enabled.
func (m *nknNodeManager) seedCandidates(limit int) []string {
	if limit <= 0 {
		return nil
	}
	m.mu.Lock()
	settings, err := m.loadSettings()
	m.mu.Unlock()
	if err != nil {
		return nil
	}
	var custom []string
	if overrides, err := nknNodeParseSeedOverrides(settings.SeedOverrides); err == nil {
		custom = overrides
	}
	seen := make(map[string]struct{}, len(nknNodeOfficialSeeds)+len(custom)+nknNodeSeedCacheSize)
	merged := make([]string, 0, len(nknNodeOfficialSeeds)+len(custom)+nknNodeSeedCacheSize)
	add := func(seed string) {
		if seed == "" {
			return
		}
		if _, dup := seen[seed]; dup {
			return
		}
		seen[seed] = struct{}{}
		merged = append(merged, seed)
	}
	for _, seed := range custom {
		add(seed)
	}
	for _, seed := range m.loadSeedCache() {
		add(seed)
	}
	for _, seed := range nknNodeOfficialSeeds {
		add(seed)
	}
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

// copyWebAssets mirrors the official entrypoint which copies the bundled web
// directory into the working directory on first start.
func (m *nknNodeManager) copyWebAssets() error {
	target := filepath.Join(m.dir, "web")
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return nil
	}
	if info, err := os.Stat(nknWebSource); err != nil || !info.IsDir() {
		return nil // web GUI assets are optional; nknd runs without them
	}
	if err := copyDir(nknWebSource, target); err != nil {
		return fmt.Errorf("copy web assets: %w", err)
	}
	return nil
}

func copyDir(source, target string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		targetPath := filepath.Join(target, entry.Name())
		if entry.IsDir() {
			if err := copyDir(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(targetPath, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (m *nknNodeManager) walletAddress() string {
	raw, err := os.ReadFile(filepath.Join(m.dir, nknNodeWalletFile))
	if err != nil {
		return ""
	}
	var wallet nknWalletFile
	if err := json.Unmarshal(raw, &wallet); err != nil {
		return ""
	}
	return wallet.Address
}

// probeNKNNodeInfo queries the local nknd JSON-RPC for its sync state and
// lifetime relay count; failures are non-fatal and simply leave the fields
// empty/zero.
func probeNKNNodeInfo() (string, uint64) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	body := strings.NewReader(`{"jsonrpc":"2.0","method":"getnodestate","params":{},"id":1}`)
	response, err := client.Post(fmt.Sprintf("http://127.0.0.1:%d", nknNodeRPCPort), "application/json", body)
	if err != nil {
		return "", 0
	}
	defer response.Body.Close()
	var payload struct {
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", 0
	}
	return nknNodeParseNodeInfo(payload.Result)
}

// nknNodeParseNodeInfo extracts syncState and relayMessageCount from a
// getnodestate result object, tolerating the key casing variants seen across
// nknd versions.
func nknNodeParseNodeInfo(result map[string]any) (string, uint64) {
	var syncState string
	for _, key := range []string{"syncState", "SyncState", "sync_state"} {
		if value, ok := result[key].(string); ok && value != "" {
			syncState = value
			break
		}
	}
	var relayCount uint64
	for _, key := range []string{"relayMessageCount", "RelayMessageCount", "relay_message_count"} {
		if value, ok := result[key].(float64); ok && value >= 0 {
			relayCount = uint64(value)
			break
		}
	}
	return syncState, relayCount
}

// probeNKNWalletBalance returns the current balance of an address via the
// local nknd JSON-RPC, formatted like "12.34 NKN". nknd encodes Fixed64
// amounts as hex with 8 decimals; failures are non-fatal and return "".
func probeNKNWalletBalance(address string) string {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	params, _ := json.Marshal(map[string]string{"address": address})
	body := strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","method":"getbalancebyaddr","params":%s,"id":1}`, params))
	response, err := client.Post(fmt.Sprintf("http://127.0.0.1:%d", nknNodeRPCPort), "application/json", body)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	var payload struct {
		Result any `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return ""
	}
	raw, _ := payload.Result.(string)
	return nknNodeFormatBalance(raw)
}

// nknNodeFormatBalance converts a hex-encoded Fixed64 NKN amount (8 decimals)
// into a human string like "12.34 NKN". Empty or unparsable input yields "".
func nknNodeFormatBalance(raw string) string {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "0x"))
	if raw == "" {
		return ""
	}
	value, err := strconv.ParseUint(raw, 16, 64)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%.2f NKN", float64(value)/1e8)
}

type nknNodeLogWriter struct {
	manager *nknNodeManager
}

func (w nknNodeLogWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			w.manager.appendLog(line)
		}
	}
	return len(p), nil
}

// launch prepares the working directory and spawns nknd. It returns without
// waiting for the process; the caller owns exactly one Wait and must invoke
// the returned cleanup once the process has exited.
func (m *nknNodeManager) launch(settings nknNodeSettings) (*exec.Cmd, func(), error) {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return nil, nil, err
	}
	// Self-custody: the gateway never auto-generates a wallet. nknd only
	// launches with an administrator-created or imported keystore on disk.
	if !walletExists(filepath.Join(m.dir, nknNodeWalletFile)) {
		return nil, nil, errNKNWalletMissing
	}
	if err := m.writeNodeConfig(settings); err != nil {
		return nil, nil, err
	}
	if err := m.copyWebAssets(); err != nil {
		m.appendLog("warning: " + err.Error())
	}
	nknd, err := exec.LookPath("nknd")
	if err != nil {
		return nil, nil, errors.New("nknd binary not found")
	}
	logPath := filepath.Join(m.dir, nknNodeLogFile)
	if info, err := os.Stat(logPath); err == nil && info.Size() > nknNodeLogMaxBytes {
		_ = os.Remove(logPath) // bounded log: restart rewrites once it grows past the cap
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	writer := io.MultiWriter(logFile, nknNodeLogWriter{manager: m})
	command := exec.Command(nknd, "--password-file", nknNodePasswordFile, "--sync", "light")
	command.Dir = m.dir
	command.Stdout = writer
	command.Stderr = writer
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return nil, nil, err
	}
	cleanup := func() { _ = logFile.Close() }
	return command, cleanup, nil
}

// supervise keeps nknd running while enabled: relaunch on crash with a fixed
// delay, exit when stopped via API or when settings disable the node.
func (m *nknNodeManager) supervise() {
	defer func() {
		m.mu.Lock()
		close(m.done)
		if m.state == nknNodeStateRunning || m.state == nknNodeStateError {
			m.state = nknNodeStateStopped
		}
		m.mu.Unlock()
	}()
	for {
		if m.stopRequested() {
			return
		}
		settings, err := m.loadSettings()
		if err != nil {
			m.setError("load settings: " + err.Error())
			return
		}
		if !settings.Enabled {
			return
		}
		command, cleanup, err := m.launch(settings)
		if err != nil {
			m.setError("launch: " + err.Error())
			if !m.sleepBeforeRestart() {
				return
			}
			continue
		}
		m.mu.Lock()
		m.cmd = command
		m.running = true
		m.startedAt = time.Now()
		m.state = nknNodeStateRunning
		m.lastErr = ""
		m.mu.Unlock()
		waitErr := command.Wait()
		cleanup()
		m.mu.Lock()
		stoppedByRequest := m.stopReq
		m.running = false
		m.cmd = nil
		if stoppedByRequest {
			m.state = nknNodeStateStopped
			m.mu.Unlock()
			return
		}
		if waitErr != nil {
			m.lastErr = waitErr.Error()
			m.state = nknNodeStateError
			m.mu.Unlock()
			m.appendLog("nknd exited: " + waitErr.Error())
			if !m.sleepBeforeRestart() {
				return
			}
			continue
		}
		m.state = nknNodeStateStopped
		m.mu.Unlock()
		if !m.sleepBeforeRestart() {
			return
		}
	}
}

func (m *nknNodeManager) stopRequested() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopReq
}

// sleepBeforeRestart waits out the relaunch delay unless a stop was requested.
func (m *nknNodeManager) sleepBeforeRestart() bool {
	timer := time.NewTimer(nknNodeRestartDelay)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if m.stopRequested() {
			return false
		}
		select {
		case <-timer.C:
			return !m.stopRequested()
		case <-ticker.C:
		}
	}
}

// start launches the supervisor goroutine if the node is not already running.
func (m *nknNodeManager) start() error {
	if !nknNodeBinaryAvailable() {
		return errors.New("the NKN node binaries are not present in this build (container image required)")
	}
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	done := m.done
	m.mu.Unlock()
	if done != nil {
		// A previous supervisor may still be winding down after a forced
		// kill; never run two supervisors concurrently.
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			return errors.New("the previous NKN node process has not exited yet; try again shortly")
		}
	}
	m.mu.Lock()
	m.stopReq = false
	m.done = make(chan struct{})
	m.state = nknNodeStateRunning
	m.lastErr = ""
	m.mu.Unlock()
	go m.supervise()
	return nil
}

// stop signals nknd and waits (bounded) for the supervisor to wind down.
func (m *nknNodeManager) stop() {
	m.mu.Lock()
	m.stopReq = true
	command := m.cmd
	done := m.done
	m.mu.Unlock()
	if command == nil || done == nil {
		return
	}
	if command.Process != nil {
		_ = command.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-done:
	case <-time.After(nknNodeStopGrace):
		m.mu.Lock()
		current := m.cmd
		m.mu.Unlock()
		if current != nil && current.Process != nil {
			_ = current.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
}

// apply persists new settings and reconciles the runtime to match.
func (m *nknNodeManager) apply(request nknNodeUpdateRequest) (nknNodeStatusResponse, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if !nknNodeBinaryAvailable() && request.Enabled {
		return nknNodeStatusResponse{}, errors.New("the NKN node binaries are not present in this build; the embedded node is only available in the container image")
	}
	beneficiary := strings.TrimSpace(request.BeneficiaryAddr)
	if beneficiary != "" && (!strings.HasPrefix(beneficiary, "NKN") || len(beneficiary) < 20 || len(beneficiary) > 100) {
		return nknNodeStatusResponse{}, errors.New("beneficiary_addr must be an NKN wallet address (starts with NKN) or empty for rewards to accumulate in the node wallet")
	}
	seedOverrides := strings.TrimSpace(request.SeedOverrides)
	if seedOverrides != "" {
		if _, err := nknNodeParseSeedOverrides(seedOverrides); err != nil {
			return nknNodeStatusResponse{}, err
		}
	}
	m.mu.Lock()
	current, err := m.loadSettings()
	m.mu.Unlock()
	if err != nil {
		return nknNodeStatusResponse{}, err
	}
	if request.ExpectedRevision != current.Revision {
		return nknNodeStatusResponse{}, errNKNNodeRevisionConflict
	}
	next := nknNodeSettings{
		Enabled:         request.Enabled,
		BeneficiaryAddr: beneficiary,
		SeedOverrides:   seedOverrides,
		Revision:        current.Revision + 1,
	}
	if err := m.saveSettings(next); err != nil {
		return nknNodeStatusResponse{}, err
	}
	m.mu.Lock()
	running := m.running
	m.mu.Unlock()
	beneficiaryChanged := beneficiary != strings.TrimSpace(current.BeneficiaryAddr)
	seedsChanged := seedOverrides != strings.TrimSpace(current.SeedOverrides)

	if next.Enabled {
		if running && (beneficiaryChanged || seedsChanged) {
			m.stop()
			running = false
		}
		if !running {
			if err := m.start(); err != nil {
				m.setError(err.Error())
				status := m.status()
				return status, err
			}
		}
	} else if running {
		m.stop()
	}
	return m.status(), nil
}

func (m *nknNodeManager) status() nknNodeStatusResponse {
	settings, err := m.loadSettings()
	if err != nil {
		settings = nknNodeSettings{}
	}
	m.mu.Lock()
	runtime := nknNodeRuntime{
		State:     m.state,
		StartedAt: m.startedAt,
		Error:     m.lastErr,
	}
	running := m.running
	m.mu.Unlock()
	runtime.WalletAddress = m.walletAddress()
	if running {
		runtime.SyncState, runtime.RelayCount = probeNKNNodeInfo()
		// The reward receiver's balance: BeneficiaryAddr when set, the node
		// wallet otherwise.
		address := strings.TrimSpace(settings.BeneficiaryAddr)
		if address == "" {
			address = runtime.WalletAddress
		}
		if address != "" {
			runtime.WalletBalance = probeNKNWalletBalance(address)
		}
	}
	return nknNodeStatusResponse{
		Settings:      settings,
		Runtime:       runtime,
		WalletExists:  walletExists(filepath.Join(m.dir, nknNodeWalletFile)),
		BinaryPresent: nknNodeBinaryAvailable(),
	}
}

func (m *nknNodeManager) recentLogs(limit int) []string {
	if limit <= 0 || limit > nknNodeLogCap {
		limit = nknNodeLogCap
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.logs) > limit {
		return append([]string(nil), m.logs[len(m.logs)-limit:]...)
	}
	return append([]string(nil), m.logs...)
}

// seedRPCAddress reports the RPC endpoint of the built-in node while it is
// running, so the gateway's own NKN client can route through it first. An
// empty string means the node is off and callers should fall back to the
// environment-configured and official seeds.
func (m *nknNodeManager) seedRPCAddress() string {
	m.mu.Lock()
	running := m.running
	m.mu.Unlock()
	if !running {
		return ""
	}
	if override := strings.TrimSpace(os.Getenv(nknNodeRPCAddressEnv)); override != "" {
		return override
	}
	return fmt.Sprintf("http://127.0.0.1:%d", nknNodeRPCPort)
}

var errNKNNodeRevisionConflict = errors.New("nkn node settings changed since they were loaded")

func (api *API) nknNodeSettings(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeJSON(response, http.StatusOK, api.nknNode.status())
	case http.MethodPut:
		var input nknNodeUpdateRequest
		if !decodeJSONBody(response, request, &input) {
			return
		}
		result, err := api.nknNode.apply(input)
		if err != nil {
			if errors.Is(err, errNKNNodeRevisionConflict) {
				writeError(
					response,
					http.StatusConflict,
					"revision_conflict",
					"NKN node settings changed since they were loaded",
					"expected_revision",
				)
				return
			}
			writeError(response, http.StatusBadRequest, "invalid_argument", err.Error(), "")
			return
		}
		writeJSON(response, http.StatusOK, result)
	default:
		response.Header().Set("Allow", http.MethodGet+", "+http.MethodPut)
		writeError(
			response,
			http.StatusMethodNotAllowed,
			"method_not_allowed",
			"Only GET and PUT are supported",
			"",
		)
	}
}

func (api *API) nknNodeLogTail(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, nknNodeLogsResponse{Lines: api.nknNode.recentLogs(120)})
}

// nknNodeWalletResponse reports the wallet address after a create/import and
// always carries the fresh node status so the UI can re-render in one round
// trip.
type nknNodeWalletResponse struct {
	WalletAddress string                `json:"wallet_address,omitempty"`
	Status        nknNodeStatusResponse `json:"status"`
}

// nknNodeWallet handles the self-custody wallet lifecycle:
//
//	POST {"action":"create","password":"…"}          — first-time generation
//	POST {"action":"import","keystore":"…","password":"…"} — bring your own wallet
//	POST {"action":"delete","confirm":true}          — irreversibly wipe keys
func (api *API) nknNodeWallet(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST is supported", "")
		return
	}
	var input struct {
		Action   string `json:"action"`
		Password string `json:"password"`
		Keystore string `json:"keystore"`
		Confirm  bool   `json:"confirm"`
	}
	if !decodeJSONBody(response, request, &input) {
		return
	}
	var address string
	var err error
	switch input.Action {
	case "create":
		address, err = api.nknNode.createWallet(input.Password)
	case "import":
		address, err = api.nknNode.importWallet(input.Keystore, input.Password)
	case "delete":
		err = api.nknNode.deleteWallet(input.Confirm)
	default:
		writeError(response, http.StatusBadRequest, "invalid_argument", `action must be "create", "import" or "delete"`, "")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadRequest, "wallet_error", err.Error(), "")
		return
	}
	status := api.nknNode.status()
	writeJSON(response, http.StatusOK, nknNodeWalletResponse{WalletAddress: address, Status: status})
}

// nknNodeWalletBackup streams the keystore file (wallet.json) so the
// administrator can keep an encrypted offline copy. The keystore is already
// password-encrypted (standard NKN wallet format importable into other NKN
// wallet apps), so serving it is safe; the password itself never leaves the
// NAS.
func (api *API) nknNodeWalletBackup(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	api.nknNode.operationMu.Lock()
	defer api.nknNode.operationMu.Unlock()
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "Only GET is supported", "")
		return
	}
	raw, err := os.ReadFile(filepath.Join(api.nknNode.dir, nknNodeWalletFile))
	if err != nil {
		writeError(response, http.StatusNotFound, "wallet_missing", "no node wallet on this gateway", "")
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Content-Disposition", `attachment; filename="nkn-node-wallet.json"`)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(raw)
}
