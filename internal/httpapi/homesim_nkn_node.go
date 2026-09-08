package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	nknNodeLogCap       = 400
	nknNodeRestartDelay = 5 * time.Second
	nknNodeStopGrace    = 8 * time.Second
	nknWebSource        = "/usr/local/lib/nkn/web"

	nknNodeStateUnavailable = "unavailable"
	nknNodeStateStopped     = "stopped"
	nknNodeStateRunning     = "running"
	nknNodeStateError       = "error"
)

var nknNodeOfficialSeeds = []string{
	"http://seed.nkn.org:30003",
	"http://mainnet-seed-0001.nkn.org:30003",
	"http://mainnet-seed-0002.nkn.org:30003",
	"http://mainnet-seed-0003.nkn.org:30003",
	"http://mainnet-seed-0004.nkn.org:30003",
}

// nknNodeSettings is the persisted administrator configuration. It lives in
// the node data directory next to wallet.json so the whole feature state is
// contained in one folder that survives container upgrades.
type nknNodeSettings struct {
	Enabled         bool   `json:"enabled"`
	BeneficiaryAddr string `json:"beneficiary_addr"`
	Revision        int64  `json:"revision"`
}

type nknNodeRuntime struct {
	State         string    `json:"state"`
	WalletAddress string    `json:"wallet_address,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	SyncState     string    `json:"sync_state,omitempty"`
	Error         string    `json:"error,omitempty"`
}

type nknNodeStatusResponse struct {
	Settings       nknNodeSettings `json:"settings"`
	Runtime        nknNodeRuntime  `json:"runtime"`
	WalletPassword string          `json:"wallet_password,omitempty"`
	BinaryPresent  bool            `json:"binary_present"`
}

type nknNodeLogsResponse struct {
	Lines []string `json:"lines"`
}

type nknNodeUpdateRequest struct {
	Enabled          bool   `json:"enabled"`
	BeneficiaryAddr  string `json:"beneficiary_addr"`
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
	mu        sync.Mutex
	logger    *slog.Logger
	dir       string
	cmd       *exec.Cmd
	done      chan struct{}
	running   bool
	stopReq   bool
	startedAt time.Time
	lastErr   string
	state     string
	logs      []string
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

// writeNodeConfig regenerates config.json from the persisted settings. Called
// on every launch so a BeneficiaryAddr change takes effect on restart.
func (m *nknNodeManager) writeNodeConfig(settings nknNodeSettings) error {
	config := nknNodeConfig{
		BeneficiaryAddr:     strings.TrimSpace(settings.BeneficiaryAddr),
		SeedList:            append([]string(nil), nknNodeOfficialSeeds...),
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

// nknNodeGeneratePassword returns 32 random alphanumeric characters, matching
// the official container entrypoint's wallet.pswd generator.
func nknNodeGeneratePassword() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	out := make([]byte, 32)
	for i, b := range raw {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}

// ensureWallet creates wallet.pswd (random password) and wallet.json exactly
// like the official entrypoint: nknc wallet -c reads the password twice from
// stdin (password + confirmation).
func (m *nknNodeManager) ensureWallet() error {
	passwordPath := filepath.Join(m.dir, nknNodePasswordFile)
	walletPath := filepath.Join(m.dir, nknNodeWalletFile)
	if walletExists(walletPath) {
		return nil
	}
	password, err := os.ReadFile(passwordPath)
	if err != nil || len(bytes.TrimSpace(password)) == 0 {
		generated, genErr := nknNodeGeneratePassword()
		if genErr != nil {
			return genErr
		}
		generated = generated + "\n"
		if err := os.WriteFile(passwordPath, []byte(generated), 0o600); err != nil {
			return err
		}
		password = []byte(generated)
	}
	nknc, err := exec.LookPath("nknc")
	if err != nil {
		return fmt.Errorf("nknc binary not found: %w", err)
	}
	command := exec.Command(nknc, "wallet", "-c")
	command.Dir = m.dir
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	output := &bytes.Buffer{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		return err
	}
	secret := strings.TrimRight(string(password), "\n") + "\n"
	if _, err := io.WriteString(stdin, secret+secret); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return err
	}
	_ = stdin.Close()
	if err := command.Wait(); err != nil {
		return fmt.Errorf("nknc wallet -c failed: %w: %s", err, strings.TrimSpace(output.String()))
	}
	if !walletExists(walletPath) {
		return fmt.Errorf("nknc wallet -c finished but %s was not created", nknNodeWalletFile)
	}
	m.appendLog("wallet created: " + strings.TrimSpace(output.String()))
	return nil
}

func walletExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
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

func (m *nknNodeManager) walletPassword() string {
	raw, err := os.ReadFile(filepath.Join(m.dir, nknNodePasswordFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// probeNodeState queries the local nknd JSON-RPC for its sync state; failures
// are non-fatal and simply leave the field empty.
func probeNKNNodeState() string {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	body := strings.NewReader(`{"jsonrpc":"2.0","method":"getnodestate","params":{},"id":1}`)
	response, err := client.Post(fmt.Sprintf("http://127.0.0.1:%d", nknNodeRPCPort), "application/json", body)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	var payload struct {
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return ""
	}
	for _, key := range []string{"syncState", "SyncState", "sync_state"} {
		if value, ok := payload.Result[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
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
	if err := m.ensureWallet(); err != nil {
		return nil, nil, err
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
	time.Sleep(nknNodeRestartDelay)
	return !m.stopRequested()
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
	if !nknNodeBinaryAvailable() && request.Enabled {
		return nknNodeStatusResponse{}, errors.New("the NKN node binaries are not present in this build; the embedded node is only available in the container image")
	}
	beneficiary := strings.TrimSpace(request.BeneficiaryAddr)
	if beneficiary != "" && (!strings.HasPrefix(beneficiary, "NKN") || len(beneficiary) < 20 || len(beneficiary) > 100) {
		return nknNodeStatusResponse{}, errors.New("beneficiary_addr must be an NKN wallet address (starts with NKN) or empty for rewards to accumulate in the node wallet")
	}
	m.mu.Lock()
	current, err := m.loadSettings()
	m.mu.Unlock()
	if err != nil {
		return nknNodeStatusResponse{}, err
	}
	if request.ExpectedRevision > 0 && request.ExpectedRevision != current.Revision {
		return nknNodeStatusResponse{}, errNKNNodeRevisionConflict
	}
	next := nknNodeSettings{
		Enabled:         request.Enabled,
		BeneficiaryAddr: beneficiary,
		Revision:        current.Revision + 1,
	}
	if err := m.saveSettings(next); err != nil {
		return nknNodeStatusResponse{}, err
	}
	m.mu.Lock()
	running := m.running
	m.mu.Unlock()
	beneficiaryChanged := beneficiary != strings.TrimSpace(current.BeneficiaryAddr)

	if next.Enabled {
		if running && beneficiaryChanged {
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
	if running {
		runtime.SyncState = probeNKNNodeState()
	}
	runtime.WalletAddress = m.walletAddress()
	return nknNodeStatusResponse{
		Settings:       settings,
		Runtime:        runtime,
		WalletPassword: m.walletPassword(),
		BinaryPresent:  nknNodeBinaryAvailable(),
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
