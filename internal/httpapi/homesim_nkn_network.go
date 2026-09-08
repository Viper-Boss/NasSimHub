package httpapi

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	nkn "github.com/nknorg/nkn-sdk-go"
)

// Optional "network directory" for ModemDeck instances. An instance that opts
// in subscribes its own NKN client identity to one shared public topic; the
// subscription is an on-chain record (fee 0) whose duration (~24h of blocks)
// the instance renews while it runs. The topic's subscriber list therefore IS
// the "devices seen online in the last 24 hours" directory: public, verifiable
// by anyone through any seed node, with zero ModemDeck-operated infrastructure
// and zero cost. Off by default; the listed identity is an anonymous NKN
// public key with no real-world attachment.

const (
	nknNetworkTopic          = "modemdecknet"
	nknNetworkIdentifier     = "modemdeck"
	nknNetworkDurationBlocks = 4320 // ~24h at 20s blocks
	nknNetworkRenewInterval  = 6 * time.Hour
	nknNetworkRetryInterval  = time.Minute
	nknNetworkSettingsFile   = "nkn-network.json"
	// nknNetworkSeedEnv names the environment variable holding the gateway's
	// own NKN identity seed (same account the SMS forwarding client uses).
	nknNetworkSeedEnv = "HOMESIM_NKN_SEED"
)

var errNKNNetworkRevisionConflict = errors.New("nkn network settings changed since they were loaded")

type nknNetworkSettings struct {
	Enabled  bool  `json:"enabled"`
	Revision int64 `json:"revision"`
}

type nknNetworkRuntime struct {
	State     string    `json:"state"` // disabled | joining | active | error
	MyAddress string    `json:"my_address,omitempty"`
	LastRenew time.Time `json:"last_renew,omitempty"`
	LastError string    `json:"last_error,omitempty"`
}

type nknNetworkStatusResponse struct {
	Settings nknNetworkSettings `json:"settings"`
	Runtime  nknNetworkRuntime  `json:"runtime"`
	Topic    string             `json:"topic"`
}

type nknNetworkUpdateRequest struct {
	Enabled          bool  `json:"enabled"`
	ExpectedRevision int64 `json:"expected_revision"`
}

type nknNetworkPeer struct {
	Address string `json:"address"`
	Meta    string `json:"meta,omitempty"`
	IsSelf  bool   `json:"is_self,omitempty"`
}

type nknNetworkPeersResponse struct {
	CoverageStartedAt time.Time        `json:"coverage_started_at,omitempty"`
	StatisticsScope   string           `json:"statistics_scope"`
	Active24h         *int             `json:"active_24h"`
	ActiveMonth       *int             `json:"active_month"`
	RegisteredTotal   *int             `json:"registered_total"`
	Topic             string           `json:"topic"`
	Window            string           `json:"window"`
	Count             int              `json:"count"`
	Peers             []nknNetworkPeer `json:"peers"`
	FetchedAt         time.Time        `json:"fetched_at"`
}

type nknNetworkManager struct {
	applyMu    sync.Mutex
	mu         sync.Mutex
	logger     *slog.Logger
	dir        string
	stopCh     chan struct{}
	done       chan struct{}
	subscribed bool
	lastRenew  time.Time
	lastErr    string
}

func newNKNNetworkManager(logger *slog.Logger) *nknNetworkManager {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &nknNetworkManager{
		logger: logger,
		dir:    nknNodeDataDir(),
	}
}

func (m *nknNetworkManager) loadSettings() (nknNetworkSettings, error) {
	settings := nknNetworkSettings{}
	raw, err := os.ReadFile(filepath.Join(m.dir, nknNetworkSettingsFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return settings, nil
		}
		return settings, err
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return settings, fmt.Errorf("corrupt %s: %w", nknNetworkSettingsFile, err)
	}
	return settings, nil
}

func (m *nknNetworkManager) saveSettings(settings nknNetworkSettings) error {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.dir, nknNetworkSettingsFile), raw, 0o600)
}

func (m *nknNetworkManager) setError(message string) {
	m.mu.Lock()
	m.lastErr = message
	m.mu.Unlock()
	m.logger.Error("nkn-network: " + message)
}

// nknNetworkSelfAddress derives the instance's directory identity locally
// (identifier + "." + pubkey hex) — no network involved. Empty when no seed
// is configured.
func nknNetworkSelfAddress() string {
	seed, err := resolveNKNSeed(nknNetworkSeedEnv)
	if err != nil {
		return ""
	}
	account, err := nkn.NewAccount(seed)
	if err != nil {
		return ""
	}
	return nknNetworkIdentifier + "." + hex.EncodeToString(account.PubKey())
}

func (m *nknNetworkManager) status() nknNetworkStatusResponse {
	settings, err := m.loadSettings()
	if err != nil {
		settings = nknNetworkSettings{}
	}
	m.mu.Lock()
	runtime := nknNetworkRuntime{
		LastRenew: m.lastRenew,
		LastError: m.lastErr,
	}
	subscribed := m.subscribed
	workerUp := m.stopCh != nil
	m.mu.Unlock()
	runtime.MyAddress = nknNetworkSelfAddress()
	switch {
	case subscribed:
		runtime.State = "active"
		if runtime.LastError != "" {
			runtime.State = "error"
		}
	case settings.Enabled && workerUp:
		runtime.State = "joining"
	case settings.Enabled:
		runtime.State = "error"
	default:
		runtime.State = "disabled"
	}
	return nknNetworkStatusResponse{
		Settings: settings,
		Runtime:  runtime,
		Topic:    nknNetworkTopic,
	}
}

// apply validates and persists the toggle, then reconciles the renew worker.
func (m *nknNetworkManager) apply(api *API, request nknNetworkUpdateRequest) (nknNetworkStatusResponse, error) {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	current, err := m.loadSettings()
	if err != nil {
		return nknNetworkStatusResponse{}, err
	}
	if request.ExpectedRevision != current.Revision {
		return nknNetworkStatusResponse{}, errNKNNetworkRevisionConflict
	}
	next := nknNetworkSettings{Enabled: request.Enabled, Revision: current.Revision + 1}
	if err := m.saveSettings(next); err != nil {
		return nknNetworkStatusResponse{}, err
	}
	if next.Enabled {
		m.startWorker(api)
	} else {
		m.stopWorker()
	}
	return m.status(), nil
}

func (m *nknNetworkManager) startWorker(api *API) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopCh != nil {
		return
	}
	m.stopCh = make(chan struct{})
	m.done = make(chan struct{})
	stopCh := m.stopCh
	go m.supervise(api, stopCh, m.done)
}

func (m *nknNetworkManager) stopWorker() {
	m.mu.Lock()
	if m.stopCh == nil {
		m.mu.Unlock()
		return
	}
	close(m.stopCh)
	done := m.done
	m.stopCh = nil
	m.done = nil
	m.mu.Unlock()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		m.logger.Warn("nkn-network: worker did not stop in time")
	}
}

// supervise subscribes the instance identity to the shared topic and renews
// it while enabled. A failed attempt retries quickly; a successful one waits
// for the renew interval. On shutdown it unsubscribes (best effort) so the
// device leaves the directory immediately instead of lingering up to 24h.
func (m *nknNetworkManager) supervise(api *API, stopCh chan struct{}, done chan struct{}) {
	defer close(done)
	seed, err := resolveNKNSeed(nknNetworkSeedEnv)
	if err != nil {
		m.setError(fmt.Sprintf("identity seed unavailable: %v (set %s to join the network directory)", err, nknNetworkSeedEnv))
		return
	}
	for {
		wait := nknNetworkRenewInterval
		entry, clientErr := api.homeSIMNKNClient(seed)
		if clientErr != nil {
			m.setError(fmt.Sprintf("NKN client unavailable: %v", clientErr))
			wait = nknNetworkRetryInterval
		} else {
			if _, err := entry.client.Subscribe(nknNetworkIdentifier, nknNetworkTopic, nknNetworkDurationBlocks, "", nil); err != nil {
				m.setError(fmt.Sprintf("subscribe failed: %v", err))
				wait = nknNetworkRetryInterval
			} else {
				m.mu.Lock()
				m.subscribed = true
				m.lastRenew = time.Now()
				m.lastErr = ""
				m.mu.Unlock()
			}
		}

		select {
		case <-stopCh:
			m.unsubscribeBestEffort(seed, api)
			return
		case <-time.After(wait):
		}
	}
}

func (m *nknNetworkManager) unsubscribeBestEffort(seed []byte, api *API) {
	entry, err := api.homeSIMNKNClient(seed)
	if err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := entry.client.Unsubscribe(nknNetworkIdentifier, nknNetworkTopic, nil); err != nil {
			m.logger.Warn("nkn-network: unsubscribe on shutdown failed (subscription will simply expire)", "err", err)
			return
		}
		m.mu.Lock()
		m.subscribed = false
		m.mu.Unlock()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
}

// fetchNKNNetworkPeers queries the shared topic's subscriber list directly
// over JSON-RPC, rotating official seeds until one answers. No connection or
// identity is needed to look — the directory is public by design.
func fetchNKNNetworkPeers(selfAddress string) (nknNetworkPeersResponse, error) {
	return fetchNKNNetworkPeersFrom(selfAddress, nknSeedRPCList())
}

func fetchNKNNetworkPeersFrom(selfAddress string, seeds []string) (nknNetworkPeersResponse, error) {
	params, _ := json.Marshal(map[string]any{
		"topic":  nknNetworkTopic,
		"offset": 0,
		"limit":  1000,
		"meta":   true,
		"txPool": false,
	})
	requestBody := fmt.Sprintf(`{"jsonrpc":"2.0","method":"getsubscribers","params":%s,"id":1}`, params)
	client := &http.Client{Timeout: 4 * time.Second}
	var lastErr error
	for _, seed := range seeds {
		response, err := client.Post(seed, "application/json", strings.NewReader(requestBody))
		if err != nil {
			lastErr = err
			continue
		}
		var payload struct {
			Result struct {
				Subscribers map[string]string `json:"subscribers"`
			} `json:"result"`
			Err *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		err = json.NewDecoder(response.Body).Decode(&payload)
		response.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if payload.Err != nil {
			lastErr = fmt.Errorf("seed %s: %s", seed, payload.Err.Message)
			continue
		}
		peers := make([]nknNetworkPeer, 0, len(payload.Result.Subscribers))
		for address, meta := range payload.Result.Subscribers {
			peers = append(peers, nknNetworkPeer{
				Address: address,
				Meta:    meta,
				IsSelf:  selfAddress != "" && strings.EqualFold(address, selfAddress),
			})
		}
		sort.Slice(peers, func(i, j int) bool { return peers[i].Address < peers[j].Address })
		// A full page may be truncated. Do not publish it as a complete total.
		var active24h *int
		if len(peers) < 1000 {
			count := len(peers)
			active24h = &count
		}
		return nknNetworkPeersResponse{
			Active24h: active24h,
			Topic:     nknNetworkTopic,
			Window:    "24h",
			Count:     len(peers),
			Peers:     peers,
			FetchedAt: time.Now(),
		}, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no seed answered")
	}
	return nknNetworkPeersResponse{}, lastErr
}

func (api *API) nknNetworkSettings(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeJSON(response, http.StatusOK, api.nknNetwork.status())
	case http.MethodPut:
		var input nknNetworkUpdateRequest
		if !decodeJSONBody(response, request, &input) {
			return
		}
		result, err := api.nknNetwork.apply(api, input)
		if err != nil {
			if errors.Is(err, errNKNNetworkRevisionConflict) {
				writeError(response, http.StatusConflict, "revision_conflict", err.Error(), "")
				return
			}
			writeError(response, http.StatusInternalServerError, "nkn_network", err.Error(), "")
			return
		}
		writeJSON(response, http.StatusOK, result)
	default:
		writeError(response, http.StatusMethodNotAllowed, "method", "use GET or PUT", "")
	}
}

func (api *API) nknNetworkPeers(response http.ResponseWriter, request *http.Request) {
	runtime := api.nknNetwork.status().Runtime
	peers, err := fetchNKNNetworkPeers(runtime.MyAddress)
	if err != nil {
		writeError(response, http.StatusBadGateway, "nkn_network", fmt.Sprintf("could not reach any NKN seed: %v", err), "")
		return
	}
	if err := api.recordNKNObservations(&peers); err != nil {
		// Statistics persistence must never take down the device directory:
		// a corrupt or unwritable observations file only means the monthly
		// and historical counters stay stale, so log and serve the peers.
		api.logger.Warn("record NKN network observations failed; serving peers without updated statistics", "error", err)
	}
	writeJSON(response, http.StatusOK, peers)
}

// nknNetworkNodeProbeCount caps how many seed-pool candidates the diagnostics
// endpoint probes per request (seed cache first, then officials — the merge
// in seedCandidates already orders by priority).
const nknNetworkNodeProbeCount = 12

// nknNodeProbeWithConnections dials a candidate RPC endpoint with
// getconnectioncount and returns both the round-trip milliseconds and the
// node's reported connection count; failures return rtt = -1.
func nknNodeProbeWithConnections(addr string) (int, *int) {
	body := strings.NewReader(`{"jsonrpc":"2.0","method":"getconnectioncount","params":{},"id":1}`)
	start := time.Now()
	response, err := nknRPCClient.Post(addr, "application/json", body)
	if err != nil {
		return -1, nil
	}
	defer response.Body.Close()
	var payload struct {
		Result *float64 `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 512)).Decode(&payload); err != nil || payload.Result == nil {
		return -1, nil
	}
	rtt := int(time.Since(start).Milliseconds())
	connections := int(*payload.Result)
	return rtt, &connections
}

type nknNetworkNodeReport struct {
	Address     string `json:"address"`
	RTTMs       int    `json:"rtt_ms"`
	Connections *int   `json:"connections,omitempty"`
}

type nknNetworkNodesResponse struct {
	Nodes     []nknNetworkNodeReport `json:"nodes"`
	Reachable int                    `json:"reachable"`
	Probed    int                    `json:"probed"`
}

// nknNetworkNodes probes the gateway's merged seed pool (custom overrides >
// cached low-latency nodes > official seeds) concurrently and reports which
// nodes are reachable right now, so the header popover can show real
// connectivity instead of a guess. The whole probe is bounded by the shared
// 1.5s RPC client timeout, so worst case is one timeout window.
func (api *API) nknNetworkNodes(response http.ResponseWriter, request *http.Request) {
	candidates := api.nknNode.seedCandidates(nknNetworkNodeProbeCount)
	reports := make([]nknNetworkNodeReport, 0, len(candidates))
	if len(candidates) > 0 {
		next := make(chan string, len(candidates))
		for _, addr := range candidates {
			next <- addr
		}
		close(next)
		results := make(chan nknNetworkNodeReport, len(candidates))
		workers := nknNodeSeedProbeWorkers
		if len(candidates) < workers {
			workers = len(candidates)
		}
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for addr := range next {
					rtt, connections := nknNodeProbeWithConnections(addr)
					results <- nknNetworkNodeReport{Address: addr, RTTMs: rtt, Connections: connections}
				}
			}()
		}
		wg.Wait()
		close(results)
		for report := range results {
			reports = append(reports, report)
		}
		sort.Slice(reports, func(i, j int) bool { return reports[i].RTTMs < reports[j].RTTMs })
	}
	reachable := 0
	for _, report := range reports {
		if report.RTTMs >= 0 {
			reachable++
		}
	}
	writeJSON(response, http.StatusOK, nknNetworkNodesResponse{Nodes: reports, Reachable: reachable, Probed: len(candidates)})
}
