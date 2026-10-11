package main

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/communication"
	"github.com/human-agent65535/modemdeck/internal/httpapi"
	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/modemdeck/internal/store"
	"github.com/human-agent65535/modemdeck/internal/trustengine"
	"github.com/human-agent65535/nassimhub-node/proto"
)

// NasSimHub Node settings. All optional.
//
//	MODEMDECK_NASSIMHUB_NODES=off        disable Node support entirely
//	MODEMDECK_NASSIMHUB_USB_URL=...      override https://10.55.0.2:7580
//	MODEMDECK_NASSIMHUB_POLL_SECONDS=10  poll interval (3..300)
//	MODEMDECK_NASSIMHUB_SECURITY_LEVEL=STANDARD|PQ|PQ_EXTREME
//	                                     key agreement and identity policy
//	                                     towards Nodes (default STANDARD)
//	MODEMDECK_TRUST_ENFORCEMENT=enforce|monitor
//	                                     enforce (default): a device under
//	                                     observation, restricted or quarantined
//	                                     is not used for outgoing calls or
//	                                     messages. monitor: the same states are
//	                                     computed, shown and journalled, and
//	                                     nothing is refused - for bringing up
//	                                     hardware; the UI says it is not enforced
//	MODEMDECK_NASSIMHUB_OTA_KEYS=/path   release PUBLIC keys this gateway
//	                                     checks Node agent releases against
//	                                     before carrying them; unset = the
//	                                     gateway verifies nothing itself and
//	                                     says so (devices always verify)
const (
	nassimhubDirectoryName     = "nassimhub"
	defaultNodePollInterval    = 10 * time.Second
	nodeTriggeredRefreshBudget = 30 * time.Second
)

type nassimhubNodes struct {
	otpMu    sync.Mutex
	trust    *trustengine.Engine
	stats    nodeStatsSource
	fleet    *nassimhubnode.Fleet
	interval time.Duration
	refresh  chan struct{}
}

func startNasSimHubNodes(
	ctx context.Context,
	logger *slog.Logger,
	dataDirectory string,
	repository *store.Store,
	communications *communication.Service,
	coreVersion string,
	db *sql.DB,
) (*nassimhubNodes, error) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MODEMDECK_NASSIMHUB_NODES")), "off") {
		logger.Info("NasSimHub Node support disabled", "component", "nassimhub")
		return nil, nil
	}
	identity, err := nassimhubnode.LoadOrCreateIdentity(filepath.Join(dataDirectory, nassimhubDirectoryName))
	if err != nil {
		return nil, fmt.Errorf("load NasSimHub core identity: %w", err)
	}
	level, err := nodeSecurityLevel(os.Getenv("MODEMDECK_NASSIMHUB_SECURITY_LEVEL"))
	if err != nil {
		return nil, err
	}
	// The real ML-DSA provider, on a build that has one. Verification of what
	// Nodes present does not depend on the level; whether this gateway offers
	// a key of its own does.
	proto.EnableStandardPQ()
	corePQ := nassimhubnode.CorePQNotOffered
	if level != proto.LevelStandard {
		corePQ, err = nassimhubnode.EnsureCorePQ(
			filepath.Join(dataDirectory, nassimhubDirectoryName), &identity, proto.PQAlgorithmFor(level))
		if err != nil {
			// Not replaced, not ignored: Nodes that pinned the stored key would
			// refuse any other, so starting without it only moves the failure
			// to every Node at once.
			return nil, fmt.Errorf("load NasSimHub core post-quantum key: %w", err)
		}
		if corePQ == nassimhubnode.CorePQUnavailable && level == proto.LevelPQExtreme {
			return nil, fmt.Errorf("security level %s needs ML-DSA, which this build does not have; build with %s or newer",
				level, proto.GoVersionForMLDSA)
		}
	}
	fleet, err := nassimhubnode.New(ctx, nassimhubnode.Options{
		Identity:      identity,
		Repository:    repository,
		CoreVersion:   nodeCoreVersion(coreVersion),
		USBBaseURL:    os.Getenv("MODEMDECK_NASSIMHUB_USB_URL"),
		SecurityLevel: level,
		Logger:        logger.With("component", "nassimhub"),
	})
	if err != nil {
		return nil, fmt.Errorf("open NasSimHub nodes: %w", err)
	}
	if err := communications.SetNodeSource(fleet); err != nil {
		return nil, err
	}
	releases, err := openNodeReleaseStore(filepath.Join(dataDirectory, nassimhubDirectoryName, "node-releases"),
		os.Getenv("MODEMDECK_NASSIMHUB_OTA_KEYS"), level)
	if err != nil {
		return nil, err
	}
	fleet.SetReleaseStore(releases)
	trust, err := startTrustEngine(ctx, logger, db, identity.PrivateKey, fleet, os.Getenv("MODEMDECK_TRUST_ENFORCEMENT"))
	if err != nil {
		return nil, err
	}
	nodes := &nassimhubNodes{
		trust:    trust,
		stats:    repository,
		fleet:    fleet,
		interval: nodePollInterval(),
		refresh:  make(chan struct{}, 1),
	}
	communications.SetOTPForwardGate(nodes.allowOTPForward)
	// A Node change triggers one hardware refresh. Changes arriving while a
	// refresh is pending coalesce into it, so a burst of polls cannot queue a
	// burst of snapshot commits.
	fleet.OnChange(func() {
		select {
		case nodes.refresh <- struct{}{}:
		default:
		}
	})
	logger.Info("NasSimHub Node support enabled", "component", "nassimhub", "core_id", identity.CoreID,
		"security_level", string(level), "core_pq", corePQ, "pq_backend", proto.PQBackendName())
	return nodes, nil
}

func (n *nassimhubNodes) run(ctx context.Context, communications *communication.Service, logger *slog.Logger) {
	if n == nil {
		return
	}
	go n.fleet.Run(ctx, n.interval)
	go n.watchVerificationCodes(ctx, logger)
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.refresh:
			refreshCtx, cancel := context.WithTimeout(ctx, nodeTriggeredRefreshBudget)
			if _, err := communications.Refresh(refreshCtx); err != nil {
				logger.Warn("node-triggered refresh failed", "component", "nassimhub", "error", err)
			}
			cancel()
		}
	}
}

func (n *nassimhubNodes) service() httpapi.NasSimHubNodeService {
	if n == nil {
		return nil
	}
	return n.fleet
}

// nodeSecurityLevel reads the configured level. An unrecognised value is an
// error rather than a default: a typo must not become a silent downgrade.
func nodeSecurityLevel(raw string) (proto.SecurityLevel, error) {
	value := strings.ToUpper(strings.TrimSpace(raw))
	if value == "" {
		return proto.LevelStandard, nil
	}
	level := proto.SecurityLevel(value)
	if err := proto.ValidateSecurityLevel(level); err != nil {
		return "", fmt.Errorf("MODEMDECK_NASSIMHUB_SECURITY_LEVEL: %w", err)
	}
	return level, nil
}

func nodePollInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("MODEMDECK_NASSIMHUB_POLL_SECONDS"))
	if raw == "" {
		return defaultNodePollInterval
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 3 || seconds > 300 {
		return defaultNodePollInterval
	}
	return time.Duration(seconds) * time.Second
}

// nodeCoreVersion is the version a Node's min_core_version is compared with.
//
// The protocol exempts a development Core ("" or "dev") from that check, and
// treats any other version it cannot parse as fatal - the Node's minimum is a
// demand that cannot be assumed met. Local NAS builds carry labels such as
// "fnos-local-0.1", which are not versions at all; passing one through would
// make every pairing fail with "version_unreadable". Such a label is reported
// as a development build instead. The protocol major/minor check still applies.
func nodeCoreVersion(version string) string {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" || version == "dev" {
		return ""
	}
	if _, err := proto.ParseVersion(version); err != nil {
		return ""
	}
	return version
}

// openNodeReleaseStore opens the store of Node agent releases this gateway can
// carry to devices. A key file that is named and cannot be read in full is an
// error: silently carrying on without it would turn "verified here" into
// "not checked" with nobody told.
func openNodeReleaseStore(directory, keyFile string, level proto.SecurityLevel) (*nassimhubnode.NodeReleaseStore, error) {
	options := nassimhubnode.NodeReleaseStoreOptions{Dir: directory, SignaturePolicy: proto.OTASignaturePreferred}
	if level == proto.LevelPQExtreme {
		options.SignaturePolicy = proto.OTASignatureRequired
	}
	if keyFile = strings.TrimSpace(keyFile); keyFile != "" {
		info, err := os.Stat(keyFile)
		if err != nil {
			return nil, fmt.Errorf("NasSimHub release key file: %w", err)
		}
		if info.Size() > proto.OTAMaxKeyFileBytes {
			return nil, fmt.Errorf("NasSimHub release key file is too large")
		}
		raw, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("NasSimHub release key file: %w", err)
		}
		classical, postQuantum, err := proto.ParseOTAKeyFile(raw)
		if err != nil {
			return nil, fmt.Errorf("NasSimHub release key file: %w", err)
		}
		if len(classical) == 0 {
			return nil, fmt.Errorf("NasSimHub release key file holds no ed25519 release key")
		}
		options.Keyring, options.PQKeyring = classical, postQuantum
	}
	if options.SignaturePolicy == proto.OTASignatureRequired && len(options.Keyring) > 0 && len(options.PQKeyring) == 0 {
		return nil, fmt.Errorf("security level %s requires dual-signed Node releases, but the release key file has no ML-DSA key", level)
	}
	return nassimhubnode.OpenNodeReleaseStore(options)
}

// startTrustEngine opens the trust records and the security journal and
// attaches them to the fleet. Devices paired before the records existed are
// recorded as trusted, once, so an upgrade does not take away calls and
// messages that already worked; a device adopted afterwards is observed.
func startTrustEngine(
	ctx context.Context, logger *slog.Logger, db *sql.DB, key ed25519.PrivateKey,
	fleet *nassimhubnode.Fleet, enforcement string,
) (*trustengine.Engine, error) {
	if db == nil {
		return nil, nil
	}
	mode, err := trustEnforcement(enforcement)
	if err != nil {
		return nil, err
	}
	trustStore, err := trustengine.NewSQLStore(db)
	if err != nil {
		return nil, err
	}
	engine, err := trustengine.New(ctx, trustengine.Options{Store: trustStore, Key: key, Enforcement: mode, HouseholdSIMDevices: strings.FieldsFunc(os.Getenv("MODEMDECK_HOUSEHOLD_SIM_DEVICES"), func(r rune) bool { return r == ',' || r == ' ' })})
	if err != nil {
		return nil, fmt.Errorf("open trust records: %w", err)
	}
	fleet.SetTrustEngine(engine)
	if err := fleet.GrandfatherTrust(ctx); err != nil {
		return nil, fmt.Errorf("record existing devices as trusted: %w", err)
	}
	fleet.OnIdentityEvent(func(event nassimhubnode.IdentityEvent) {
		result := event.Outcome
		if event.Refused {
			result += "; the device is refused"
		}
		noteCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := engine.Note(noteCtx, event.NodeID, trustengine.EventIdentityVerification,
			firstNonEmptyString(event.Reason, "identity verification outcome changed from "+firstNonEmptyString(event.Previous, "none")),
			"gateway", result+", pin "+event.PinState); err != nil {
			logger.Warn("identity event was not journalled", "component", "nassimhub", "error", err)
		}
	})
	chain := engine.Chain()
	level := slog.LevelInfo
	if !chain.OK || mode == trustengine.Monitor {
		level = slog.LevelWarn
	}
	logger.Log(ctx, level, "trust records open", "component", "nassimhub", "enforcement", string(mode),
		"journal_ok", chain.OK, "journal_events", chain.Events, "journal_problem", chain.Problem)
	return engine, nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// trustEnforcement reads the mode. An unrecognised value is an error: a typo
// must not silently become "nothing is enforced".
func trustEnforcement(raw string) (trustengine.Enforcement, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "enforce":
		return trustengine.Enforce, nil
	case "monitor":
		return trustengine.Monitor, nil
	}
	return "", fmt.Errorf("MODEMDECK_TRUST_ENFORCEMENT must be enforce or monitor, not %q", raw)
}

type nodeStatsSource interface {
	CommunicationStatsForNode(context.Context, time.Time, int, string) (store.CommunicationStats, error)
}

// verificationCodeInterval is how often each device's 24-hour count of
// verification codes is read from stored messages and given to the trust
// engine. The count is a rolling window, so reading it every few minutes
// loses nothing.
const verificationCodeInterval = 5 * time.Minute

func (n *nassimhubNodes) watchVerificationCodes(ctx context.Context, logger *slog.Logger) {
	if n.trust == nil || n.stats == nil {
		return
	}
	ticker := time.NewTicker(verificationCodeInterval)
	defer ticker.Stop()
	for {
		n.observeVerificationCodes(ctx, logger)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (n *nassimhubNodes) observeVerificationCodes(ctx context.Context, logger *slog.Logger) {
	for _, node := range n.fleet.Nodes() {
		n.otpMu.Lock()
		sampleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		stats, err := n.stats.CommunicationStatsForNode(sampleCtx, time.Now(), 24, node.DeviceID)
		if err != nil {
			cancel()
			n.otpMu.Unlock()
			logger.Warn("verification code count was not read", "component", "nassimhub", "error", err)
			continue
		}
		if err := n.trust.ObserveOTP(sampleCtx, node.DeviceID, stats.SMSVerification); err != nil && !errors.Is(err, trustengine.ErrUnknownSubject) {
			logger.Warn("verification code count was not recorded", "component", "nassimhub", "error", err)
		}
		cancel()
		n.otpMu.Unlock()
	}
}
