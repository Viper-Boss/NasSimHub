package nassimhubnode

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/human-agent65535/modemdeck/internal/store"
	"github.com/human-agent65535/modemdeck/internal/trustengine"
	"github.com/human-agent65535/nassimhub-node/core/nodeclient"
	"github.com/human-agent65535/nassimhub-node/core/nodetrust"
	"github.com/human-agent65535/nassimhub-node/core/transport"
	"github.com/human-agent65535/nassimhub-node/proto"
)

// DefaultUSBBaseURL is the Node's fixed address on the USB NCM link. It is
// frozen by docs/FIRMWARE_INTERFACE.md; the NAS host side is 10.55.0.1/29.
const DefaultUSBBaseURL = "https://10.55.0.2:7580"

const (
	defaultTimeout      = 8 * time.Second
	defaultPollInterval = 10 * time.Second
	ticketLifetime      = 5 * time.Minute
	maxTickets          = 16
	touchInterval       = time.Minute
	maxSentRemembered   = 256
)

var (
	ErrUnknownNode          = errors.New("nassimhub node is not paired with this gateway")
	ErrTicketInvalid        = errors.New("pairing confirmation expired or was already used; discover the device again")
	ErrFingerprintMismatch  = errors.New("the device fingerprint does not match the one that was shown")
	ErrAlreadyPaired        = errors.New("this device is already paired with this gateway")
	ErrKeyChanged           = errors.New("a device with this ID was paired before with a different key; remove the old record first")
	ErrOwnedElsewhere       = errors.New("the device is paired with another NasSimHub; release it there or reset its pairing")
	ErrIncompatible         = errors.New("the device software is not compatible with this gateway")
	ErrNodeUnavailable      = errors.New("the device is not reachable")
	ErrNodeNotReady         = errors.New("the device SIM is not ready")
	ErrPairingNotConfirmed  = errors.New("the device did not confirm the pairing")
	ErrNodeNeedsPQ          = errors.New("the device has no post-quantum identity, which this gateway's security level requires")
	ErrNodeDemandsPQ        = errors.New("the device requires a post-quantum identity from this gateway; set the gateway security level to PQ or PQ_EXTREME")
	errIdentityDoesNotMatch = errors.New("the device answering now is not the device that was discovered")
)

// Repository is the persistence the fleet needs. *store.Store implements it.
type Repository interface {
	NasSimHubNodeRecords(context.Context) ([]store.NasSimHubNodeRecord, error)
	NasSimHubNodeRecord(context.Context, string) (store.NasSimHubNodeRecord, error)
	SaveNasSimHubNode(context.Context, store.NasSimHubNodeRecord) error
	DeleteNasSimHubNode(context.Context, string) error
	TouchNasSimHubNode(context.Context, string, string, string, string, time.Time) error
	RecordNasSimHubNodeSIM(context.Context, store.NasSimHubNodeSIM) error
}

type Options struct {
	Identity    nodeclient.Identity
	Repository  Repository
	CoreVersion string
	// USBBaseURL overrides the fixed USB address (tests, unusual setups).
	USBBaseURL string
	Security   nodetrust.Security
	// SecurityLevel is the outward-facing setting: STANDARD (the default), PQ
	// or PQ_EXTREME. It decides the key agreement policy when Security is left
	// empty, and how much post-quantum identity is demanded of each Node; see
	// pqidentity.go.
	SecurityLevel proto.SecurityLevel
	// PQPins stores each Node's pinned post-quantum identity key. When nil the
	// Repository is used if it can store them; otherwise identities are
	// verified and reported but not recorded.
	PQPins  PQIdentityRepository
	Timeout time.Duration
	Now     func() time.Time
	Logger  *slog.Logger
}

// Fleet owns every Node this gateway has adopted.
type Fleet struct {
	identity     nodeclient.Identity
	repository   Repository
	requirements proto.CoreRequirements
	usbBaseURL   string
	security     nodetrust.Security
	level        proto.SecurityLevel
	pins         PQIdentityRepository
	timeout      time.Duration
	now          func() time.Time
	logger       *slog.Logger

	mu         sync.Mutex
	nodes      map[string]*liveNode
	tickets    map[string]ticket
	onChange   func()
	onIdentity func(IdentityEvent)
	updating   map[string]struct{}
	releases   *NodeReleaseStore
	trust      *trustengine.Engine
}

type ticket struct {
	deviceID    string
	fingerprint string
	baseURL     string
	connection  proto.ConnectionType
	document    proto.Node
	pin         nodetrust.Pin
	certificate *x509.Certificate
	expires     time.Time
}

// New loads the paired Nodes and prepares a pinned client for each.
func New(ctx context.Context, options Options) (*Fleet, error) {
	if options.Repository == nil {
		return nil, errors.New("a node fleet requires a repository")
	}
	if len(options.Identity.PrivateKey) != ed25519.PrivateKeySize || options.Identity.CoreID == "" {
		return nil, errors.New("a node fleet requires the core identity")
	}
	level := options.SecurityLevel
	if level == "" {
		level = proto.LevelStandard
	}
	if err := proto.ValidateSecurityLevel(level); err != nil {
		return nil, err
	}
	security := options.Security
	if security.Profile == "" && security.Policy == "" {
		// STANDARD maps to exactly what DefaultSecurity has always been.
		security.Profile, security.Policy = proto.ProfileAndPolicyFor(level)
	}
	pins := options.PQPins
	if pins == nil {
		pins, _ = options.Repository.(PQIdentityRepository)
	}
	if err := security.Validate(); err != nil {
		return nil, err
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	usb := strings.TrimRight(strings.TrimSpace(options.USBBaseURL), "/")
	if usb == "" {
		usb = DefaultUSBBaseURL
	}
	fleet := &Fleet{
		identity:     options.Identity,
		repository:   options.Repository,
		requirements: proto.LocalCoreRequirements(options.CoreVersion),
		usbBaseURL:   usb,
		security:     security,
		level:        level,
		pins:         pins,
		timeout:      timeout,
		now:          now,
		logger:       logger,
		nodes:        map[string]*liveNode{},
		tickets:      map[string]ticket{},
	}
	records, err := options.Repository.NasSimHubNodeRecords(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		switch record.PairState {
		case store.NasSimHubPairStatePaired:
			live, err := fleet.buildLive(record, nil)
			if err != nil {
				// A record that cannot be turned into a pinned client is kept,
				// reported, and left for the administrator to remove. Dropping
				// it silently would un-pair a device without anyone asking.
				logger.Warn("stored node record is unusable", "component", "nassimhub", "node_id", record.ID, "error", err)
				continue
			}
			fleet.nodes[record.ID] = live
		case store.NasSimHubPairStatePairing:
			// A pairing interrupted by a restart never completed on our side.
			// The Node may or may not have accepted it; the record is removed
			// and the administrator pairs again (re-pairing the same gateway
			// is idempotent on the Node).
			if err := options.Repository.DeleteNasSimHubNode(ctx, record.ID); err != nil {
				return nil, fmt.Errorf("clear interrupted pairing: %w", err)
			}
		}
	}
	return fleet, nil
}

// OnChange registers the callback run after a poll changes what the Nodes
// contribute to the hardware snapshot. It runs on the polling goroutine.
func (f *Fleet) OnChange(callback func()) {
	f.mu.Lock()
	f.onChange = callback
	f.mu.Unlock()
}

// CoreID is this gateway's identity as Nodes see it.
func (f *Fleet) CoreID() string { return f.identity.CoreID }

// ---------------------------------------------------------------------------
// Discovery and pairing
// ---------------------------------------------------------------------------

// Candidate is one device found during discovery. It carries no SIM data.
type Candidate struct {
	Source          string    `json:"source"`
	DeviceID        string    `json:"device_id"`
	Platform        string    `json:"platform"`
	Model           string    `json:"model,omitempty"`
	AgentVersion    string    `json:"agent_version,omitempty"`
	Fingerprint     string    `json:"fingerprint"`
	PairingState    string    `json:"pairing_state"`
	OwnedByThisNAS  bool      `json:"owned_by_this_nas"`
	OwnedByOther    bool      `json:"owned_by_other"`
	Known           bool      `json:"known"`
	KeyMismatch     bool      `json:"key_mismatch"`
	Compatible      bool      `json:"compatible"`
	Incompatible    string    `json:"incompatible,omitempty"`
	Ticket          string    `json:"ticket,omitempty"`
	TicketExpiresAt time.Time `json:"ticket_expires_at,omitzero"`
	Error           string    `json:"error,omitempty"`
}

// Discover probes the USB link and reports what answers there, verified: the
// device ID derives from the key that completed the TLS handshake. A ticket is
// issued for a device that can be paired; Pair must present it.
func (f *Fleet) Discover(ctx context.Context) ([]Candidate, error) {
	candidates := make([]Candidate, 0, 1)
	candidate, reachable := f.discoverAt(ctx, "usb", proto.ConnectionUSB, f.usbBaseURL)
	if reachable {
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func (f *Fleet) discoverAt(
	ctx context.Context,
	source string,
	connection proto.ConnectionType,
	baseURL string,
) (Candidate, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	if !tcpReachable(probeCtx, baseURL) {
		return Candidate{}, false
	}
	candidate := Candidate{Source: source}
	document, pin, certificate, err := f.firstContact(probeCtx, baseURL)
	if err != nil {
		candidate.Error = "device answered but its identity could not be verified: " + err.Error()
		return candidate, true
	}
	candidate.DeviceID = document.DeviceID
	candidate.Platform = string(document.Platform)
	candidate.Model = document.Model
	candidate.AgentVersion = document.AgentVersion
	candidate.Fingerprint = Fingerprint(pin.PublicKey)
	candidate.PairingState = string(document.PairingState)
	candidate.OwnedByThisNAS = document.PairingState == proto.PairingPaired &&
		document.PairedCoreID == f.identity.CoreID
	candidate.OwnedByOther = document.PairingState == proto.PairingPaired &&
		document.PairedCoreID != f.identity.CoreID
	candidate.Compatible = true
	if err := proto.Negotiate(document.Compatibility(), f.requirements); err != nil {
		candidate.Compatible = false
		candidate.Incompatible = err.Error()
	}
	if record, err := f.repository.NasSimHubNodeRecord(ctx, document.DeviceID); err == nil {
		candidate.Known = record.PairState == store.NasSimHubPairStatePaired
		candidate.KeyMismatch = !ed25519.PublicKey(record.PublicKey).Equal(pin.PublicKey)
	}
	if candidate.Compatible && !candidate.OwnedByOther && !candidate.Known && !candidate.KeyMismatch {
		token, expires, err := f.issueTicket(ticket{
			deviceID:    document.DeviceID,
			fingerprint: candidate.Fingerprint,
			baseURL:     baseURL,
			connection:  connection,
			document:    document,
			pin:         pin,
			certificate: certificate,
		})
		if err == nil {
			candidate.Ticket = token
			candidate.TicketExpiresAt = expires
		}
	}
	return candidate, true
}

func (f *Fleet) firstContact(ctx context.Context, baseURL string) (proto.Node, nodetrust.Pin, *x509.Certificate, error) {
	observed := &nodetrust.Observation{}
	config, err := nodetrust.SecureFirstContactConfig(observed, f.security)
	if err != nil {
		return proto.Node{}, nodetrust.Pin{}, nil, err
	}
	roundTripper := nodetrust.Transport(config)
	roundTripper.DisableKeepAlives = true
	defer roundTripper.CloseIdleConnections()
	client := &http.Client{
		Timeout:       f.timeout,
		Transport:     roundTripper,
		CheckRedirect: refuseRedirect,
	}
	document, err := nodeclient.Describe(ctx, client, baseURL)
	if err != nil {
		return proto.Node{}, nodetrust.Pin{}, nil, err
	}
	key, err := proto.DecodePublicKey(document.PublicKey)
	if err != nil {
		return proto.Node{}, nodetrust.Pin{}, nil, err
	}
	pin, err := observed.Confirm(document.DeviceID, document.Platform, key)
	if err != nil {
		return proto.Node{}, nodetrust.Pin{}, nil, err
	}
	return document, pin, observed.Certificate(), nil
}

func (f *Fleet) issueTicket(value ticket) (string, time.Time, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	value.expires = f.now().Add(ticketLifetime)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruneTicketsLocked()
	for existing, candidate := range f.tickets {
		// One outstanding ticket per device: discovering again supersedes.
		if candidate.deviceID == value.deviceID {
			delete(f.tickets, existing)
		}
	}
	if len(f.tickets) >= maxTickets {
		return "", time.Time{}, errors.New("too many outstanding pairing confirmations")
	}
	f.tickets[token] = value
	return token, value.expires, nil
}

func (f *Fleet) pruneTicketsLocked() {
	now := f.now()
	for token, value := range f.tickets {
		if !now.Before(value.expires) {
			delete(f.tickets, token)
		}
	}
}

func (f *Fleet) takeTicket(token string) (ticket, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruneTicketsLocked()
	value, ok := f.tickets[token]
	if ok {
		delete(f.tickets, token) // single use, whatever happens next
	}
	return value, ok
}

// PairRequest is what the administrator confirms: the ticket from discovery,
// and the device ID and fingerprint they were shown, echoed back.
type PairRequest struct {
	Ticket      string
	DeviceID    string
	Fingerprint string
	DisplayName string
}

// Pair adopts a discovered Node.
//
// Order of writes, so a failure at any point leaves either nothing or a
// complete pairing:
//  1. the trust record is written as 'pairing';
//  2. the Node is asked to pair, over a connection pinned to the discovered
//     key (a device swapped in since discovery fails the handshake);
//  3. the record becomes 'paired'.
//
// A failure in 2 deletes the record. A failure in 3 releases the Node again
// and deletes the record. A restart between 1 and 3 clears the record at
// startup; pairing again is idempotent on the Node.
func (f *Fleet) Pair(ctx context.Context, request PairRequest) (NodeView, error) {
	value, ok := f.takeTicket(strings.TrimSpace(request.Ticket))
	if !ok {
		return NodeView{}, ErrTicketInvalid
	}
	if strings.TrimSpace(request.DeviceID) != value.deviceID {
		return NodeView{}, ErrTicketInvalid
	}
	if normalizeFingerprint(request.Fingerprint) != normalizeFingerprint(value.fingerprint) {
		return NodeView{}, ErrFingerprintMismatch
	}
	existing, err := f.repository.NasSimHubNodeRecord(ctx, value.deviceID)
	switch {
	case err == nil && !ed25519.PublicKey(existing.PublicKey).Equal(value.pin.PublicKey):
		return NodeView{}, ErrKeyChanged
	case err == nil && existing.PairState == store.NasSimHubPairStatePaired:
		return NodeView{}, ErrAlreadyPaired
	case err != nil && !errors.Is(err, store.ErrNasSimHubNodeNotFound):
		return NodeView{}, err
	}
	if value.document.PairingState == proto.PairingPaired && value.document.PairedCoreID != f.identity.CoreID {
		return NodeView{}, ErrOwnedElsewhere
	}
	if err := proto.Negotiate(value.document.Compatibility(), f.requirements); err != nil {
		return NodeView{}, fmt.Errorf("%w: %v", ErrIncompatible, err)
	}
	if f.identityPolicy() == proto.PQIdentityRequired && !value.document.HasPQIdentity() {
		// Refused before anything is written: adopting a device that the
		// first poll would refuse leaves the administrator with a paired row
		// that can never come online.
		return NodeView{}, ErrNodeNeedsPQ
	}
	if err := proto.CheckPQAlgorithmForLevel(f.level, value.document.PQIdentity); err != nil {
		return NodeView{}, fmt.Errorf("%w (%s)", ErrNodeNeedsPQ, err)
	}

	record := store.NasSimHubNodeRecord{
		ID:          value.deviceID,
		Platform:    string(value.document.Platform),
		DisplayName: strings.TrimSpace(request.DisplayName),
		PublicKey:   append([]byte(nil), value.pin.PublicKey...),
		Certificate: append([]byte(nil), value.certificate.Raw...),
		PairState:   store.NasSimHubPairStatePairing,
		Transport:   string(value.connection),
	}
	if value.connection == proto.ConnectionUSB {
		record.USBEndpoint = value.baseURL
	} else {
		record.WiFiEndpoint = value.baseURL
	}
	if err := f.repository.SaveNasSimHubNode(ctx, record); err != nil {
		return NodeView{}, err
	}
	live, err := f.buildLive(record, nil)
	if err != nil {
		_ = f.repository.DeleteNasSimHubNode(context.WithoutCancel(ctx), record.ID)
		return NodeView{}, err
	}
	pairCtx, cancel := context.WithTimeout(ctx, f.timeout)
	response, err := live.client.Pair(pairCtx)
	cancel()
	if err != nil {
		live.close()
		_ = f.repository.DeleteNasSimHubNode(context.WithoutCancel(ctx), record.ID)
		var incompatible *proto.IncompatibleError
		switch {
		case proto.CodeOf(err) == proto.ErrorConflict:
			return NodeView{}, ErrOwnedElsewhere
		case errors.As(err, &incompatible):
			return NodeView{}, fmt.Errorf("%w: %v", ErrIncompatible, err)
		case needsCorePQ(err):
			return NodeView{}, ErrNodeDemandsPQ
		}
		if isTLSIdentityError(err) {
			return NodeView{}, errIdentityDoesNotMatch
		}
		return NodeView{}, fmt.Errorf("%w: %v", ErrNodeUnavailable, err)
	}
	if response.DeviceID != record.ID || response.PairingState != proto.PairingPaired {
		live.releaseBestEffort(f.timeout)
		live.close()
		_ = f.repository.DeleteNasSimHubNode(context.WithoutCancel(ctx), record.ID)
		return NodeView{}, ErrPairingNotConfirmed
	}
	record.PairState = store.NasSimHubPairStatePaired
	record.LastSeenAt = f.now().UTC()
	if err := f.repository.SaveNasSimHubNode(context.WithoutCancel(ctx), record); err != nil {
		live.releaseBestEffort(f.timeout)
		live.close()
		_ = f.repository.DeleteNasSimHubNode(context.WithoutCancel(ctx), record.ID)
		return NodeView{}, fmt.Errorf("record pairing: %w", err)
	}
	live.record = record
	f.mu.Lock()
	if previous := f.nodes[record.ID]; previous != nil {
		previous.close()
	}
	f.nodes[record.ID] = live
	f.mu.Unlock()
	f.logger.Info("node paired", "component", "nassimhub", "node_id", record.ID)
	f.pollOne(ctx, live)
	f.notify()
	return f.viewOf(live), nil
}

// Unpair releases a Node and forgets it. The Node is told first so it becomes
// adoptable again; a Node that cannot be reached is still forgotten, and the
// result says so, because refusing to forget an unreachable device would leave
// a row nobody can delete.
func (f *Fleet) Unpair(ctx context.Context, deviceID string) (bool, error) {
	deviceID = strings.TrimSpace(deviceID)
	f.mu.Lock()
	live := f.nodes[deviceID]
	delete(f.nodes, deviceID)
	f.mu.Unlock()
	released := false
	if live != nil {
		released = live.releaseBestEffort(f.timeout)
		live.close()
	}
	if err := f.repository.DeleteNasSimHubNode(ctx, deviceID); err != nil {
		return released, err
	}
	f.notify()
	return released, nil
}

// ---------------------------------------------------------------------------
// Live Nodes
// ---------------------------------------------------------------------------

type liveNode struct {
	record      store.NasSimHubNodeRecord
	pin         nodetrust.Pin
	certificate *x509.Certificate
	selector    *transport.Selector
	client      *nodeclient.Client
	transport   *http.Transport
	httpClient  *http.Client

	// pinRejected is set by a health probe whose endpoint answered with a
	// certificate the pin refused. The selector keeps only the last probe
	// error, so without it a pin failure on USB is masked by an unreachable
	// Wi-Fi endpoint - both in the diagnosis shown to the administrator and in
	// the decision to attempt a same-key certificate renewal.
	pinRejected *atomic.Bool

	pollMu       sync.Mutex
	callMu       sync.Mutex
	callRevision uint64

	mu        sync.Mutex
	state     nodeState
	identity  identityState
	sent      map[string]sentMessage
	lastTouch time.Time
	lastSIM   string
	lastTrust time.Time

	// policy is the trust policy last signed for the device and what the
	// device reported back; see devicepolicy.go. It has its own lock.
	policy devicePolicyState
}

type sentMessage struct {
	requestID string
	peer      string
	text      string
	at        time.Time
}

type nodeState struct {
	Online         bool
	LastError      string
	LastSeen       time.Time
	Polled         bool
	AgentVersion   string
	Model          string
	BootID         string
	PairingState   proto.PairingState
	Revoked        bool
	Compatible     bool
	Incompatible   string
	Capabilities   proto.Capabilities
	Status         proto.ModemStatus
	StatusKnown    bool
	WiFi           proto.WiFiStatus
	Messages       []proto.SMS
	MessagesKnown  bool
	Calls          []proto.Call
	CallsKnown     bool
	ConnectionType proto.ConnectionType
	// LinkSecurity is the key agreement the Node reported for the connection
	// that carried the capability request.
	LinkSecurity      proto.SecurityCapability
	LinkSecurityKnown bool
	// IdentityRefused marks a Node that answered but whose identity could not
	// be established to the configured standard. Such a Node is reported
	// offline to everything that consumes lines, with the reason.
	IdentityRefused bool
}

func (f *Fleet) buildLive(record store.NasSimHubNodeRecord, pinRejected *atomic.Bool) (*liveNode, error) {
	if pinRejected == nil {
		pinRejected = &atomic.Bool{}
	}
	if len(record.PublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("stored node key is invalid")
	}
	certificate, err := x509.ParseCertificate(record.Certificate)
	if err != nil {
		return nil, fmt.Errorf("stored node certificate is unreadable: %w", err)
	}
	pin := nodetrust.Pin{
		DeviceID:  record.ID,
		Platform:  proto.Platform(record.Platform),
		PublicKey: ed25519.PublicKey(append([]byte(nil), record.PublicKey...)),
	}
	config, err := nodetrust.SecureClientConfig(pin, certificate, f.security)
	if err != nil {
		return nil, err
	}
	roundTripper := nodetrust.Transport(config)
	httpClient := &http.Client{Timeout: f.timeout, Transport: roundTripper, CheckRedirect: refuseRedirect}
	endpoints := endpointsFor(record)
	selector, err := transport.New(transport.Options{
		DeviceID:  record.ID,
		Endpoints: endpoints,
		Probe: func(ctx context.Context, endpoint transport.Endpoint) error {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.BaseURL+"/v1/health", nil)
			if err != nil {
				return err
			}
			response, err := httpClient.Do(request)
			if err != nil {
				if isTLSIdentityError(err) {
					pinRejected.Store(true)
				}
				return err
			}
			defer response.Body.Close()
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("health returned %d", response.StatusCode)
			}
			return nil
		},
		Now: f.now,
	})
	if err != nil {
		roundTripper.CloseIdleConnections()
		return nil, err
	}
	client, err := nodeclient.New(nodeclient.Options{
		Identity:  f.identity,
		Selector:  selector,
		Timeout:   f.timeout,
		Now:       f.now,
		Transport: roundTripper,
	})
	if err != nil {
		roundTripper.CloseIdleConnections()
		return nil, err
	}
	return &liveNode{
		record:      record,
		pin:         pin,
		certificate: certificate,
		selector:    selector,
		client:      client,
		transport:   roundTripper,
		httpClient:  httpClient,
		pinRejected: pinRejected,
		sent:        map[string]sentMessage{},
		state:       nodeState{Compatible: true},
	}, nil
}

func endpointsFor(record store.NasSimHubNodeRecord) []transport.Endpoint {
	endpoints := make([]transport.Endpoint, 0, 2)
	if record.USBEndpoint != "" {
		endpoints = append(endpoints, transport.Endpoint{Type: proto.ConnectionUSB, BaseURL: record.USBEndpoint})
	}
	if record.WiFiEndpoint != "" {
		endpoints = append(endpoints, transport.Endpoint{Type: proto.ConnectionWiFi, BaseURL: record.WiFiEndpoint})
	}
	return endpoints
}

func (l *liveNode) close() {
	if l != nil && l.transport != nil {
		l.transport.CloseIdleConnections()
	}
}

func (l *liveNode) releaseBestEffort(timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client, _ := l.handles()
	_, err := client.Unpair(ctx)
	return err == nil
}

// Run polls every Node until ctx ends.
func (f *Fleet) Run(ctx context.Context, interval time.Duration) {
	go f.runCalls(ctx)
	if interval <= 0 {
		interval = defaultPollInterval
	}
	f.PollAll(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.PollAll(ctx)
		}
	}
}

// PollAll polls every Node concurrently. One unreachable stick never delays
// another.
func (f *Fleet) PollAll(ctx context.Context) {
	f.mu.Lock()
	nodes := make([]*liveNode, 0, len(f.nodes))
	for _, live := range f.nodes {
		nodes = append(nodes, live)
	}
	f.mu.Unlock()
	before := f.contributionRevision()
	var group sync.WaitGroup
	for _, live := range nodes {
		group.Add(1)
		go func(live *liveNode) {
			defer group.Done()
			f.pollOne(ctx, live)
		}(live)
	}
	group.Wait()
	if f.contributionRevision() != before {
		f.notify()
	}
}

func (f *Fleet) notify() {
	f.mu.Lock()
	callback := f.onChange
	f.mu.Unlock()
	if callback != nil {
		callback()
	}
}

func (f *Fleet) pollOne(ctx context.Context, live *liveNode) {
	live.pollMu.Lock()
	defer live.pollMu.Unlock()
	pollCtx, cancel := context.WithTimeout(ctx, 3*f.timeout)
	defer cancel()

	live.pinRejected.Store(false)
	client, selector := live.handles()
	document, err := client.Node(pollCtx)
	if err != nil && (isTLSIdentityError(err) || live.pinRejected.Load()) && f.repin(pollCtx, live) {
		live.pinRejected.Store(false)
		client, selector = live.handles()
		document, err = client.Node(pollCtx)
	}
	if err != nil {
		diagnosis := publicError(err)
		if live.pinRejected.Load() {
			diagnosis = pinRejectedDiagnosis
		}
		live.mu.Lock()
		live.state.Online = false
		live.state.Polled = true
		live.state.LastError = diagnosis
		live.state.ConnectionType = proto.ConnectionNone
		live.mu.Unlock()
		return
	}
	next := nodeState{
		Online:         true,
		Polled:         true,
		LastSeen:       f.now().UTC(),
		AgentVersion:   document.AgentVersion,
		Model:          document.Model,
		BootID:         document.BootID,
		PairingState:   document.PairingState,
		Compatible:     true,
		ConnectionType: selector.ConnectionType(),
	}
	if document.DeviceID != live.record.ID {
		next.Online = false
		next.LastError = errIdentityDoesNotMatch.Error()
		live.setState(next)
		return
	}
	if err := proto.Negotiate(document.Compatibility(), f.requirements); err != nil {
		next.Compatible = false
		next.Incompatible = err.Error()
	}
	if document.PairingState != proto.PairingPaired || document.PairedCoreID != f.identity.CoreID {
		// Reset on the device, or adopted by another gateway. Stop reading;
		// the administrator sees it and decides.
		next.Revoked = true
		live.setState(next)
		return
	}
	if !next.Compatible {
		live.setState(next)
		return
	}
	if refused, reason := f.verifyIdentity(pollCtx, live, client, document.BootID); refused {
		next.Online = false
		next.IdentityRefused = true
		next.LastError = "identity: " + reason
		next.ConnectionType = proto.ConnectionNone
		live.setState(next)
		return
	}
	if reason := f.linkBelowLevel(client); reason != "" {
		next.Online = false
		next.LastError = "link: " + reason
		next.ConnectionType = proto.ConnectionNone
		live.setState(next)
		return
	}

	var failures []string
	if capabilities, err := client.Capabilities(pollCtx); err == nil {
		next.Capabilities = capabilities.Capabilities
		next.LinkSecurity = capabilities.Security
		next.LinkSecurityKnown = true
	} else {
		failures = append(failures, "capabilities: "+publicError(err))
	}
	if status, err := client.Status(pollCtx); err == nil {
		next.Status = status
		next.StatusKnown = true
	} else {
		failures = append(failures, "status: "+publicError(err))
	}
	live.mu.Lock()
	previousMessages := live.state.Messages
	previousKnown := live.state.MessagesKnown
	live.mu.Unlock()
	if messages, err := client.ListSMS(pollCtx); err == nil {
		next.Messages = messages
		next.MessagesKnown = true
	} else {
		// Keep the last good list: an intermittent read error must not look
		// like the messages vanished.
		next.Messages = previousMessages
		next.MessagesKnown = previousKnown
		if proto.CodeOf(err) != proto.ErrorFailedPrecondition {
			failures = append(failures, "sms: "+publicError(err))
		}
	}
	// Calls are short lived. A failed read must never replay a stale active
	// call into the gateway after the Node disconnects or the SIM changes.
	live.callMu.Lock()
	live.mu.Lock()
	callRevision := live.callRevision
	live.mu.Unlock()
	if calls, err := client.ListCalls(pollCtx); err == nil {
		next.Calls = calls
		next.CallsKnown = true
	} else if proto.CodeOf(err) != proto.ErrorNotSupported {
		failures = append(failures, "calls: "+publicError(err))
	}
	live.callMu.Unlock()
	if wifi, err := client.WiFi(pollCtx); err == nil {
		next.WiFi = wifi
		f.learnWiFiEndpoint(live, wifi)
	}
	next.ConnectionType = selector.ConnectionType()
	if len(failures) > 0 {
		next.LastError = strings.Join(failures, "; ")
	}
	live.mu.Lock()
	if live.callRevision != callRevision && live.state.Status.SIM.ICCID == next.Status.SIM.ICCID {
		next.Calls = live.state.Calls
		next.CallsKnown = live.state.CallsKnown
	}
	live.state = next
	live.mu.Unlock()
	f.persistObservation(ctx, live, next)
	f.observeTrust(ctx, live, next)
}

func (l *liveNode) handles() (*nodeclient.Client, *transport.Selector) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.client, l.selector
}

func (l *liveNode) setState(next nodeState) {
	l.mu.Lock()
	l.state = next
	l.mu.Unlock()
}

// learnWiFiEndpoint adds the Node's Wi-Fi address as a second link. Identity is
// the pinned key, not the address, so this cannot create a second device and
// cannot be abused to point the gateway at another one.
func (f *Fleet) learnWiFiEndpoint(live *liveNode, wifi proto.WiFiStatus) {
	if wifi.State != proto.WiFiConnected {
		return
	}
	address := net.ParseIP(strings.TrimSpace(wifi.IPv4))
	if address == nil || address.To4() == nil || address.IsLoopback() || address.IsUnspecified() {
		return
	}
	port := "7580"
	if parsed, err := url.Parse(f.usbBaseURL); err == nil && parsed.Port() != "" {
		port = parsed.Port()
	}
	endpoint := "https://" + net.JoinHostPort(address.String(), port)
	live.mu.Lock()
	changed := live.record.WiFiEndpoint != endpoint
	if changed {
		live.record.WiFiEndpoint = endpoint
	}
	record := live.record
	live.mu.Unlock()
	if changed {
		_, selector := live.handles()
		selector.SetEndpoints(endpointsFor(record))
	}
}

func (f *Fleet) persistObservation(ctx context.Context, live *liveNode, state nodeState) {
	live.mu.Lock()
	record := live.record
	due := f.now().Sub(live.lastTouch) >= touchInterval || live.lastTouch.IsZero()
	simKey := ""
	if state.StatusKnown && state.Status.SIM.ICCID != "" {
		simKey = state.Status.SIM.ICCID + "|" + state.Status.SIM.IMSI + "|" + state.Status.SIM.PhoneNumber + "|" + state.Status.SIM.OperatorName
	}
	simChanged := simKey != live.lastSIM
	live.mu.Unlock()
	if due {
		if err := f.repository.TouchNasSimHubNode(ctx, record.ID, string(state.ConnectionType),
			record.USBEndpoint, record.WiFiEndpoint, state.LastSeen); err == nil {
			live.mu.Lock()
			live.lastTouch = f.now()
			live.mu.Unlock()
		}
	}
	if simChanged && simKey != "" {
		sim := state.Status.SIM
		err := f.repository.RecordNasSimHubNodeSIM(ctx, store.NasSimHubNodeSIM{
			NodeID:       record.ID,
			SIMID:        sim.ICCID,
			Slot:         0,
			ICCID:        sim.ICCID,
			IMSI:         sim.IMSI,
			LocalPhone:   sim.PhoneNumber,
			OperatorName: sim.OperatorName,
			Present:      sim.State == proto.SIMReady,
		})
		if err == nil {
			live.mu.Lock()
			live.lastSIM = simKey
			live.mu.Unlock()
		}
	}
}

// repin handles a certificate the pinned configuration no longer accepts.
// The key must be the one already pinned; only the certificate may change.
//
// Every configured endpoint is tried directly rather than through the
// selector: the selector only returns an endpoint whose pinned health probe
// passed, which is exactly what a renewed certificate prevents.
func (f *Fleet) repin(ctx context.Context, live *liveNode) bool {
	_, selector := live.handles()
	var certificate *x509.Certificate
	for _, endpoint := range selector.Endpoints() {
		renewed, err := f.observeRenewal(ctx, live.pin, endpoint.BaseURL)
		if err == nil {
			certificate = renewed
			break
		}
	}
	if certificate == nil {
		return false
	}
	live.mu.Lock()
	record := live.record
	live.mu.Unlock()
	record.Certificate = append([]byte(nil), certificate.Raw...)
	if err := f.repository.SaveNasSimHubNode(ctx, record); err != nil {
		return false
	}
	rebuilt, err := f.buildLive(record, live.pinRejected)
	if err != nil {
		return false
	}
	live.close()
	live.mu.Lock()
	live.record = record
	live.certificate = rebuilt.certificate
	live.selector = rebuilt.selector
	live.client = rebuilt.client
	live.transport = rebuilt.transport
	live.httpClient = rebuilt.httpClient
	live.mu.Unlock()
	f.logger.Info("node certificate renewed and re-pinned", "component", "nassimhub", "node_id", record.ID)
	return true
}

// observeRenewal connects with the same-key renewal configuration and returns
// the certificate the device presents, if it carries the pinned key.
func (f *Fleet) observeRenewal(ctx context.Context, pin nodetrust.Pin, baseURL string) (*x509.Certificate, error) {
	observed := &nodetrust.Observation{}
	config, err := nodetrust.SecureRepinConfig(pin, observed, f.security)
	if err != nil {
		return nil, err
	}
	roundTripper := nodetrust.Transport(config)
	roundTripper.DisableKeepAlives = true
	defer roundTripper.CloseIdleConnections()
	client := &http.Client{Timeout: f.timeout, Transport: roundTripper, CheckRedirect: refuseRedirect}
	if _, err := nodeclient.Describe(ctx, client, baseURL); err != nil {
		return nil, err
	}
	return observed.ConfirmRenewal(pin)
}

// ---------------------------------------------------------------------------
// Views
// ---------------------------------------------------------------------------

// NodeView is what the administration page shows. It includes the SIM's
// operator and whether it is ready, never its identifiers.
type NodeView struct {
	Resources       *proto.SystemResources `json:"resources,omitempty"`
	DeviceID        string                 `json:"node_id"`
	DisplayName     string                 `json:"display_name"`
	Platform        string                 `json:"platform"`
	Model           string                 `json:"model,omitempty"`
	Fingerprint     string                 `json:"fingerprint"`
	PairState       string                 `json:"pair_state"`
	Online          bool                   `json:"online"`
	Revoked         bool                   `json:"revoked"`
	Compatible      bool                   `json:"compatible"`
	Incompatible    string                 `json:"incompatible,omitempty"`
	Transport       string                 `json:"transport"`
	USBEndpoint     string                 `json:"usb_endpoint,omitempty"`
	WiFiEndpoint    string                 `json:"wifi_endpoint,omitempty"`
	AgentVersion    string                 `json:"agent_version,omitempty"`
	LastSeenAt      time.Time              `json:"last_seen_at,omitzero"`
	LastError       string                 `json:"last_error,omitempty"`
	ModemState      string                 `json:"modem_state,omitempty"`
	SIMState        string                 `json:"sim_state,omitempty"`
	Operator        string                 `json:"operator,omitempty"`
	Registration    string                 `json:"registration,omitempty"`
	SignalBars      int                    `json:"signal_bars"`
	SMSCapable      bool                   `json:"sms_capable"`
	VoiceCapable    bool                   `json:"voice_capable"`
	VoLTE           proto.Tristate         `json:"volte"`
	VoiceControl    bool                   `json:"voice_control"`
	VoiceAudio      bool                   `json:"voice_audio"`
	MessagesOnStick int                    `json:"messages_on_device"`
	LineEndpointID  string                 `json:"line_endpoint_id"`
	// Identity is what was established about the Node's identity keys;
	// LinkSecurity is the key agreement of the gateway's session with it. They
	// are separate facts and neither implies the other.
	Identity        IdentityView     `json:"identity"`
	LinkSecurity    LinkSecurityView `json:"link_security"`
	IdentityRefused bool             `json:"identity_refused"`
	// Trust is how far the device may be used; see internal/trustengine.
	Trust TrustView `json:"trust"`
}

// viewOf builds the administration view, including the security facts that
// depend on the fleet's configured level.
func (f *Fleet) viewOf(live *liveNode) NodeView {
	view := live.view(f.identity.CoreID)
	live.mu.Lock()
	view.Identity = live.identityView(f)
	view.LinkSecurity = linkSecurityView(f.level, live.client.ObservedLink(), live.state.LinkSecurity,
		live.state.LinkSecurityKnown && live.state.Online)
	if !live.state.Online {
		// An observation from a session that is no longer up describes nothing.
		view.LinkSecurity = linkSecurityView(f.level, nodeclient.ObservedLink{}, proto.SecurityCapability{}, false)
	}
	view.IdentityRefused = live.state.IdentityRefused
	live.mu.Unlock()
	view.Trust = f.trustView(live.record.ID)
	return view
}

func (l *liveNode) view(coreID string) NodeView {
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.state
	view := NodeView{
		DeviceID:       l.record.ID,
		DisplayName:    l.record.DisplayName,
		Platform:       l.record.Platform,
		Model:          state.Model,
		Fingerprint:    Fingerprint(l.pin.PublicKey),
		PairState:      l.record.PairState,
		Online:         state.Online,
		Revoked:        state.Revoked,
		Compatible:     state.Compatible,
		Incompatible:   state.Incompatible,
		Transport:      string(state.ConnectionType),
		USBEndpoint:    l.record.USBEndpoint,
		WiFiEndpoint:   l.record.WiFiEndpoint,
		AgentVersion:   state.AgentVersion,
		LastSeenAt:     state.LastSeen,
		LastError:      state.LastError,
		SMSCapable:     state.Capabilities.SMS,
		VoiceCapable:   state.Capabilities.VoiceControl && state.Capabilities.VoiceAudio,
		VoLTE:          normalizeTristate(state.Capabilities.VoLTE),
		VoiceControl:   state.Capabilities.VoiceControl,
		VoiceAudio:     state.Capabilities.VoiceAudio,
		LineEndpointID: lineEndpointID(l.record.ID),
	}
	if view.LastSeenAt.IsZero() {
		view.LastSeenAt = l.record.LastSeenAt
	}
	if !state.Polled {
		view.LastError = ""
	}
	if state.StatusKnown {
		if state.Online {
			view.Resources = state.Status.Resources
		}
		view.ModemState = string(state.Status.State)
		view.SIMState = string(state.Status.SIM.State)
		view.Operator = firstNonEmpty(state.Status.Network.OperatorName, state.Status.SIM.OperatorName)
		view.Registration = string(state.Status.Network.Registration)
		view.SignalBars = state.Status.Signal.Bars
	}
	if state.MessagesKnown {
		view.MessagesOnStick = len(state.Messages)
	}
	_ = coreID
	return view
}

// Nodes lists every paired Node with its live state.
func (f *Fleet) Nodes() []NodeView {
	f.mu.Lock()
	nodes := make([]*liveNode, 0, len(f.nodes))
	for _, live := range f.nodes {
		nodes = append(nodes, live)
	}
	f.mu.Unlock()
	views := make([]NodeView, 0, len(nodes))
	for _, live := range nodes {
		views = append(views, f.viewOf(live))
	}
	sort.Slice(views, func(i, j int) bool { return views[i].DeviceID < views[j].DeviceID })
	return views
}

// Refresh polls one Node now.
func (f *Fleet) Refresh(ctx context.Context, deviceID string) (NodeView, error) {
	live := f.lookup(deviceID)
	if live == nil {
		return NodeView{}, ErrUnknownNode
	}
	before := f.contributionRevision()
	f.pollOne(ctx, live)
	if f.contributionRevision() != before {
		f.notify()
	}
	return f.viewOf(live), nil
}

func (f *Fleet) lookup(deviceID string) *liveNode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nodes[strings.TrimSpace(deviceID)]
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// Fingerprint is the value shown to the administrator for comparison with the
// device (Windows client, label, or the Node's own status page): the first 16
// bytes of SHA-256 over the device public key, in groups of four hex digits.
func Fingerprint(key ed25519.PublicKey) string {
	digest := sha256.Sum256(key)
	encoded := strings.ToUpper(hex.EncodeToString(digest[:16]))
	groups := make([]string, 0, 8)
	for index := 0; index < len(encoded); index += 4 {
		groups = append(groups, encoded[index:index+4])
	}
	return strings.Join(groups, " ")
}

func normalizeFingerprint(value string) string {
	value = strings.ToUpper(value)
	var builder strings.Builder
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'A' && r <= 'F') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return errors.New("node requests must not redirect")
}

func tcpReachable(ctx context.Context, baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	host := parsed.Host
	if parsed.Port() == "" {
		host = net.JoinHostPort(parsed.Hostname(), "443")
	}
	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func isTLSIdentityError(err error) bool {
	var verification *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	if errors.As(err, &verification) || errors.As(err, &unknown) ||
		errors.As(err, &invalid) || errors.As(err, &hostname) ||
		errors.Is(err, nodetrust.ErrNotPinned) || errors.Is(err, proto.ErrCertificateBinding) {
		return true
	}
	// The transport selector reports probe failures as text, so the typed
	// error does not survive; recognise crypto/tls's own wording.
	message := err.Error()
	return strings.Contains(message, "failed to verify certificate") ||
		strings.Contains(message, nodetrust.ErrNotPinned.Error())
}

const pinRejectedDiagnosis = "device certificate is not the pinned key"

// publicError keeps diagnostics useful without leaking addresses or bodies.
func publicError(err error) string {
	if err == nil {
		return ""
	}
	var operation *proto.OperationError
	if errors.As(err, &operation) {
		if operation.Message != "" {
			return string(operation.Code) + ": " + operation.Message
		}
		return string(operation.Code)
	}
	if isTLSIdentityError(err) {
		return pinRejectedDiagnosis
	}
	if strings.Contains(err.Error(), "no usable endpoint") {
		return "device is not reachable over USB or Wi-Fi"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timed out"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	message := err.Error()
	if len(message) > 160 {
		message = message[:160]
	}
	return message
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// MarshalJSON keeps the fingerprint grouping stable in API output.
func (c Candidate) MarshalJSON() ([]byte, error) {
	type plain Candidate
	return json.Marshal(plain(c))
}

// SetWiFiEndpoint records a Wi-Fi address for a paired Node by hand, for a
// network where the Node's own report of its address is not usable (for
// example behind a VLAN the Node cannot see). The address carries no trust:
// every connection to it is still pinned to the device key.
func (f *Fleet) SetWiFiEndpoint(ctx context.Context, deviceID, baseURL string) error {
	live := f.lookup(deviceID)
	if live == nil {
		return ErrUnknownNode
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL != "" {
		parsed, err := url.Parse(baseURL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.Path != "" {
			return errors.New("the Wi-Fi address must look like https://host:port")
		}
	}
	live.mu.Lock()
	live.record.WiFiEndpoint = baseURL
	record := live.record
	live.mu.Unlock()
	_, selector := live.handles()
	selector.SetEndpoints(endpointsFor(record))
	return f.repository.SaveNasSimHubNode(ctx, record)
}

// normalizeTristate reports a capability the Node has not stated yet (before
// the first poll, or from an agent that predates the field) as unknown rather
// than as an empty string the page could misread.
func normalizeTristate(value proto.Tristate) proto.Tristate {
	switch value {
	case proto.TriYes, proto.TriNo:
		return value
	default:
		return proto.TriUnknown
	}
}
