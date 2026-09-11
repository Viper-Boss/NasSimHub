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
// in subscribes its own NKN client identity to shared public topics; each
// subscription is an on-chain record (fee 0) that the instance renews while it
// runs. Public, verifiable by anyone through any seed node, with zero
// ModemDeck-operated infrastructure and zero cost. On by default (users may
// still switch it off); the listed identity is an anonymous NKN public key with
// no real-world attachment.
//
// The trick that makes the counts real is that THE SUBSCRIPTION DURATION IS THE
// STATISTICS WINDOW. A subscription that is not renewed expires on its own, so
// the subscriber set of a topic is exactly "instances that were online at some
// point within that duration" -- which is the definition of an active-user
// window. One topic per window, and the count is a chain query rather than
// anything this gateway accumulated.
//
// Sizing the duration is where it is easy to go wrong. An instance that goes
// offline keeps its subscription only until its LAST RENEWAL plus the duration,
// and that renewal was up to one renewal interval before it went quiet. So the
// coverage a window actually guarantees is
//
//	duration - renewal interval
//
// not the duration. A window advertised as 30 days therefore needs a
// 31-day subscription renewed daily, not a 30-day one renewed weekly -- the
// latter guarantees only 23 days and silently under-reports the very number it
// is named after. Every duration below is its advertised span plus one renewal
// interval, and a test asserts that floor rather than the raw duration.
//
// This replaced a local observations file that counted identities THIS gateway
// happened to poll. That file could only ever under-report: whatever happened
// while the NAS was off was invisible to it, a fresh install started at zero,
// and the numbers were not comparable between two installs. Nothing about it
// was verifiable by anyone else either.

const (
	nknNetworkIdentifier    = "modemdeck"
	nknNetworkRenewInterval = 6 * time.Hour
	nknNetworkRetryInterval = time.Minute
	nknNetworkSettingsFile  = "nkn-network.json"
	// nknNetworkSeedEnv names the environment variable holding the gateway's
	// own NKN identity seed (same account the SMS forwarding client uses).
	nknNetworkSeedEnv = "HOMESIM_NKN_SEED"
	// nknNetworkStateFile records when this instance first joined the longer
	// windows, so the UI can say a window is still filling instead of
	// presenting a partial count as a settled one.
	nknNetworkStateFile = "nkn-network-windows.json"
)

// nknNetworkWindow is one statistics window: a topic whose subscription
// duration defines what its subscriber count means.
type nknNetworkWindow struct {
	// Key is the field name this window fills in the API response.
	Key string
	// Topic is the on-chain topic. Each window needs its own, because one
	// identity has a single subscription (and so a single expiry) per topic.
	Topic string
	// Blocks is the subscription duration. NKN blocks are 20s
	// (config.ConsensusDuration) and the protocol caps a subscription at
	// 400000 blocks (config.MaxSubscribeDuration), which is ~92.6 days.
	//
	// This is Span plus one Renew, never Span itself: see the sizing note above.
	Blocks int
	// Renew is how often to push the expiry back out.
	Renew time.Duration
	// Span is the coverage this count is ADVERTISED as, and the floor it must
	// actually guarantee. The labels in the UI name this number.
	Span time.Duration
}

// nknNetworkTopic is the 24-hour topic, kept under its original name so
// existing installs stay in the same directory they already joined.
const nknNetworkTopic = "modemdecknet"

var nknNetworkWindows = []nknNetworkWindow{
	// 48h renewed daily: guarantees the advertised 24 hours.
	{Key: "active_24h", Topic: nknNetworkTopic, Blocks: 8640, Renew: 24 * time.Hour, Span: 24 * time.Hour},
	// 31d renewed daily: guarantees the advertised 30 days.
	{Key: "active_month", Topic: "modemdecknet-30d", Blocks: 133920, Renew: 24 * time.Hour, Span: 30 * 24 * time.Hour},
	// 91d renewed daily: guarantees the advertised 90 days. 393120 blocks
	// leaves ~1.6 days under the protocol's 400000 maximum, which is the
	// longest window that can exist at all.
	//
	// This is NOT "all time": an instance that stopped renewing more than 90
	// days ago is gone from the chain and no amount of querying brings it back.
	// Presenting it as a lifetime total would be inventing a number nothing
	// measures.
	{Key: "active_90d", Topic: "modemdecknet-90d", Blocks: 393120, Renew: 24 * time.Hour, Span: 90 * 24 * time.Hour},
}

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
	// CoverageStartedAt is when THIS instance first joined the longer windows.
	// The chain does not know when a topic started being used globally, so this
	// is only a hint that a window may still be filling -- not a claim about
	// anyone else.
	CoverageStartedAt time.Time `json:"coverage_started_at,omitempty"`
	// StatisticsScope names where the counts come from, so a reader never has
	// to guess whether a number is local or global.
	StatisticsScope string `json:"statistics_scope"`
	// Each count is the subscriber count of that window's topic, straight from
	// the chain. A nil value means the query failed; it never means zero.
	Active24h   *int             `json:"active_24h"`
	ActiveMonth *int             `json:"active_month"`
	Active90d   *int             `json:"active_90d"`
	Topic       string           `json:"topic"`
	Window      string           `json:"window"`
	Count       int              `json:"count"`
	Peers       []nknNetworkPeer `json:"peers"`
	FetchedAt   time.Time        `json:"fetched_at"`
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
	// Default ON (绥哥's call): a fresh install joins the network directory
	// without visiting settings. An existing settings file always wins.
	settings := nknNetworkSettings{Enabled: true}
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
	renewed := make(map[string]time.Time, len(nknNetworkWindows))
	for {
		wait := nknNetworkRenewInterval
		entry, clientErr := api.homeSIMNKNClient(seed)
		if clientErr != nil {
			m.setError(fmt.Sprintf("NKN client unavailable: %v", clientErr))
			wait = nknNetworkRetryInterval
		} else {
			// Each window is renewed on its own schedule. A 30-day window does
			// not need a transaction every six hours, and putting one on chain
			// anyway would be noise nobody benefits from.
			var failures []string
			joined := 0
			for _, window := range nknNetworkWindows {
				last, seen := renewed[window.Topic]
				if seen && time.Since(last) < window.Renew {
					joined++
					continue
				}
				if _, err := entry.client.Subscribe(
					nknNetworkIdentifier,
					window.Topic,
					window.Blocks,
					"",
					nil,
				); err != nil {
					failures = append(failures, fmt.Sprintf("%s: %v", window.Topic, err))
					continue
				}
				renewed[window.Topic] = time.Now()
				joined++
			}
			if len(failures) > 0 {
				m.setError("subscribe failed: " + strings.Join(failures, "; "))
				// One window failing must not stop the others from being
				// renewed: the 24-hour directory is the one people actually
				// look at, and it should not expire because a longer window
				// could not be reached.
				if joined == 0 {
					wait = nknNetworkRetryInterval
				}
			}
			if joined > 0 {
				m.mu.Lock()
				m.subscribed = true
				m.lastRenew = time.Now()
				if len(failures) == 0 {
					m.lastErr = ""
				}
				m.mu.Unlock()
				m.recordWindowStart()
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

// recordWindowStart notes the first time this instance joined the windows, once.
//
// It is only ever used to say "this window may still be filling". The chain
// cannot tell us when a topic started being used globally, so this is a fact
// about this install and is labelled as such rather than dressed up as
// coverage of the network.
func (m *nknNetworkManager) recordWindowStart() {
	path := filepath.Join(m.dir, nknNetworkStateFile)
	if _, err := os.Stat(path); err == nil {
		return
	}
	_ = writeHomeSIMJSON(path, map[string]any{"joined_at": time.Now().UTC()})
}

// windowStart reports when this instance first joined, or the zero time.
func (m *nknNetworkManager) windowStart() time.Time {
	raw, err := os.ReadFile(filepath.Join(m.dir, nknNetworkStateFile))
	if err != nil {
		return time.Time{}
	}
	var state struct {
		JoinedAt time.Time `json:"joined_at"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return time.Time{}
	}
	return state.JoinedAt
}

func (m *nknNetworkManager) unsubscribeBestEffort(seed []byte, api *API) {
	entry, err := api.homeSIMNKNClient(seed)
	if err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, window := range nknNetworkWindows {
			if _, err := entry.client.Unsubscribe(nknNetworkIdentifier, window.Topic, nil); err != nil {
				m.logger.Warn(
					"nkn-network: unsubscribe on shutdown failed (subscription will simply expire)",
					"topic", window.Topic,
					"err", err,
				)
			}
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
		// Count is the length of this page and may be truncated at the limit
		// above; the headline figures come from getsubscriberscount instead,
		// which is exact regardless of how many subscribers there are.
		return nknNetworkPeersResponse{
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

// fetchNKNSubscriberCount asks the chain how many identities currently hold a
// subscription to topic. This is the exact figure: unlike paging the subscriber
// list it has no limit to be truncated at, and the seed nodes cache it.
func fetchNKNSubscriberCount(topic string, seeds []string) (int, error) {
	params, _ := json.Marshal(map[string]any{"topic": topic})
	body := fmt.Sprintf(`{"jsonrpc":"2.0","method":"getsubscriberscount","params":%s,"id":1}`, params)
	client := &http.Client{Timeout: 4 * time.Second}
	var lastErr error
	for _, seed := range seeds {
		response, err := client.Post(seed, "application/json", strings.NewReader(body))
		if err != nil {
			lastErr = err
			continue
		}
		var payload struct {
			Result *int `json:"result"`
			Err    *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&payload)
		response.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if payload.Err != nil {
			lastErr = fmt.Errorf("seed %s: %s", seed, payload.Err.Message)
			continue
		}
		if payload.Result == nil {
			lastErr = fmt.Errorf("seed %s returned no count", seed)
			continue
		}
		return *payload.Result, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no seed answered")
	}
	return 0, lastErr
}

// nknWindowCounts is a short-lived cache of the per-window figures. Several
// browser tabs polling the dashboard must not turn into several chain queries
// each; the windows move on the scale of hours, so a minute of staleness costs
// nothing and being a polite client costs the seeds nothing either.
var (
	nknWindowCountsMu   sync.Mutex
	nknWindowCountsAt   time.Time
	nknWindowCountsData map[string]int
)

const nknWindowCountsTTL = time.Minute

// fillNKNWindowCounts asks each window's topic for its subscriber count.
//
// A window whose query fails is left nil rather than zero. Zero is a real
// answer that means "nobody is out there", and reporting it for "the seed did
// not respond" would be a lie the reader has no way to detect.
func fillNKNWindowCounts(result *nknNetworkPeersResponse) {
	nknWindowCountsMu.Lock()
	fresh := time.Since(nknWindowCountsAt) < nknWindowCountsTTL && nknWindowCountsData != nil
	counts := nknWindowCountsData
	nknWindowCountsMu.Unlock()

	if !fresh {
		counts = nknWindowCountsFrom(nknSeedRPCList())
		nknWindowCountsMu.Lock()
		nknWindowCountsData = counts
		nknWindowCountsAt = time.Now()
		nknWindowCountsMu.Unlock()
	}
	applyNKNWindowCounts(result, counts)
}

// nknWindowCountsFrom queries each window once. A window whose query fails is
// absent from the map rather than present as zero.
func nknWindowCountsFrom(seeds []string) map[string]int {
	counts := make(map[string]int, len(nknNetworkWindows))
	for _, window := range nknNetworkWindows {
		count, err := fetchNKNSubscriberCount(window.Topic, seeds)
		if err != nil {
			continue
		}
		counts[window.Key] = count
	}
	return counts
}

func applyNKNWindowCounts(result *nknNetworkPeersResponse, counts map[string]int) {
	if value, ok := counts["active_24h"]; ok {
		result.Active24h = &value
	}
	if value, ok := counts["active_month"]; ok {
		result.ActiveMonth = &value
	}
	if value, ok := counts["active_90d"]; ok {
		result.Active90d = &value
	}
	result.StatisticsScope = "onchain_topic_subscribers"
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
	fillNKNWindowCounts(&peers)
	peers.CoverageStartedAt = api.nknNetwork.windowStart()
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
