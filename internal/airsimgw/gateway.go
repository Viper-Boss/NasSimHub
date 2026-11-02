// Package airsimgw lets the AirSIM iPhone app (github.com/kai-wu-cortex/AirSIM)
// use a NasSimHub line as if it were the Android phone the app was written
// for. It serves the app's three local ("VoWLAN") endpoints, unchanged:
//
//   - pairing: GET /pair/v1/info and POST /pair/v1/claim, open only while an
//     administrator holds a pairing window open in Settings;
//   - control: HMAC-signed HTTP requests, translated into NasSimHub commands;
//   - PCM: one full-duplex 8 kHz audio stream bridged into the call media.
//
// Wire formats live in internal/airsimproto. See docs/AIRSIM-COMPAT.md.
package airsimgw

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/airsimproto"
)

// Default ports. 7592/7593 are what the AirSIM Android gateway uses, so a
// phone that already knows them needs nothing new; 7594 is the pairing port.
const (
	DefaultControlPort = 7592
	DefaultPCMPort     = 7593
	DefaultPairingPort = 7594
)

var (
	ErrDisabled      = errors.New("airsim: gateway is disabled")
	ErrNoLine        = errors.New("airsim: no line is selected")
	ErrNotListening  = errors.New("airsim: gateway listeners are not running")
	ErrBackendNotSet = errors.New("airsim: backend is not configured")
)

type Config struct {
	// Listen addresses; empty disables that listener.
	ControlAddress string
	PCMAddress     string
	PairingAddress string
	// Ports the iPhone must use, as published on the host. They default to
	// the listen ports and differ only behind a port mapping.
	ControlPort int
	PCMPort     int
	PairingPort int

	StatePath string
	Secrets   SecretBox
	Logger    *slog.Logger
	Now       func() time.Time
	// AllowPeer decides which client addresses may connect. The default
	// accepts loopback, RFC 1918, link-local, ULA and 100.64.0.0/10
	// (Tailscale) addresses, like the Android gateway.
	AllowPeer func(netip.Addr) bool
}

type Gateway struct {
	config  Config
	logger  *slog.Logger
	now     func() time.Time
	store   *store
	replay  *airsimproto.ReplayCache
	backend Backend

	mu         sync.Mutex
	window     *pairingWindow
	lastPaired *Pairing
	owned      map[string]string // pairing ID -> call ID it controls
	muted      map[string]bool
	watches    map[string]*eventWatch
	pcmPairing string
	listening  bool
	listenErr  error
}

type pairingWindow struct {
	session *airsimproto.PairingSession
	userID  string
}

func New(config Config) (*Gateway, error) {
	if config.StatePath == "" {
		return nil, errors.New("airsim: state path is required")
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.AllowPeer == nil {
		config.AllowPeer = PrivatePeer
	}
	config.ControlPort = defaultPort(config.ControlPort, config.ControlAddress, DefaultControlPort)
	config.PCMPort = defaultPort(config.PCMPort, config.PCMAddress, DefaultPCMPort)
	config.PairingPort = defaultPort(config.PairingPort, config.PairingAddress, DefaultPairingPort)
	state, err := openStore(config.StatePath, config.Secrets)
	if err != nil {
		return nil, err
	}
	return &Gateway{
		config:  config,
		logger:  config.Logger,
		now:     config.Now,
		store:   state,
		replay:  airsimproto.NewReplayCache(),
		owned:   map[string]string{},
		muted:   map[string]bool{},
		watches: map[string]*eventWatch{},
	}, nil
}

func defaultPort(explicit int, address string, fallback int) int {
	if explicit > 0 {
		return explicit
	}
	if _, port, err := net.SplitHostPort(address); err == nil {
		if parsed, err := netip.ParseAddrPort("0.0.0.0:" + port); err == nil && parsed.Port() > 0 {
			return int(parsed.Port())
		}
	}
	return fallback
}

// SetBackend connects the gateway to NasSimHub. It must be called once,
// before Serve.
func (g *Gateway) SetBackend(backend Backend) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.backend = backend
}

func (g *Gateway) currentBackend() Backend {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.backend
}

// PrivatePeer accepts the address ranges a phone on the same Wi-Fi, hotspot
// or tailnet uses, and nothing routable on the Internet.
func PrivatePeer(address netip.Addr) bool {
	address = address.Unmap()
	if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() {
		return true
	}
	return netip.MustParsePrefix("100.64.0.0/10").Contains(address)
}

func (g *Gateway) peerAllowed(remote string) (netip.Addr, bool) {
	addrPort, err := netip.ParseAddrPort(remote)
	if err != nil {
		return netip.Addr{}, false
	}
	return addrPort.Addr().Unmap(), g.config.AllowPeer(addrPort.Addr())
}

// Serve runs the listeners until ctx ends.
func (g *Gateway) Serve(ctx context.Context) error {
	if g.currentBackend() == nil {
		return ErrBackendNotSet
	}
	type listener struct {
		name    string
		address string
		serve   func(net.Listener) error
		close   func()
	}
	var listeners []listener
	newHTTP := func(handler http.Handler) *http.Server {
		return &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			// The event long-poll holds a response for up to 30 s.
			WriteTimeout:   45 * time.Second,
			IdleTimeout:    30 * time.Second,
			MaxHeaderBytes: 16 << 10,
			ErrorLog:       slog.NewLogLogger(g.logger.Handler(), slog.LevelDebug),
		}
	}
	if g.config.PairingAddress != "" {
		server := newHTTP(http.HandlerFunc(g.servePairing))
		server.SetKeepAlivesEnabled(false)
		listeners = append(listeners, listener{"pairing", g.config.PairingAddress, server.Serve, func() { _ = server.Close() }})
	}
	if g.config.ControlAddress != "" {
		server := newHTTP(http.HandlerFunc(g.serveControl))
		listeners = append(listeners, listener{"control", g.config.ControlAddress, server.Serve, func() { _ = server.Close() }})
	}
	if g.config.PCMAddress != "" {
		// Closed with the other bound listeners below.
		listeners = append(listeners, listener{"pcm", g.config.PCMAddress, func(l net.Listener) error {
			return g.servePCM(ctx, l)
		}, func() {}})
	}
	if len(listeners) == 0 {
		return nil
	}
	bound := make([]net.Listener, 0, len(listeners))
	for _, item := range listeners {
		l, err := net.Listen("tcp", item.address)
		if err != nil {
			for _, open := range bound {
				_ = open.Close()
			}
			g.mu.Lock()
			g.listenErr = fmt.Errorf("listen %s %s: %w", item.name, item.address, err)
			g.mu.Unlock()
			return g.listenErr
		}
		bound = append(bound, l)
	}
	g.mu.Lock()
	g.listening = true
	g.listenErr = nil
	g.mu.Unlock()
	g.logger.Info("AirSIM gateway listening", "component", "airsim",
		"pairing", g.config.PairingAddress, "control", g.config.ControlAddress, "pcm", g.config.PCMAddress)

	errs := make(chan error, len(listeners))
	for index, item := range listeners {
		go func(item listener, l net.Listener) {
			err := item.serve(l)
			if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
				err = nil
			}
			errs <- err
		}(item, bound[index])
	}
	go g.keepCalls(ctx)
	<-ctx.Done()
	for _, item := range listeners {
		item.close()
	}
	for _, l := range bound {
		_ = l.Close()
	}
	var first error
	for range listeners {
		if err := <-errs; err != nil && first == nil {
			first = err
		}
	}
	g.mu.Lock()
	g.listening = false
	g.mu.Unlock()
	return first
}

// Status is what the settings page shows.
type Status struct {
	Enabled     bool          `json:"enabled"`
	LineID      string        `json:"line_id"`
	AgentID     string        `json:"agent_id"`
	Listening   bool          `json:"listening"`
	ListenError string        `json:"listen_error,omitempty"`
	ControlPort int           `json:"control_port"`
	PCMPort     int           `json:"pcm_port"`
	PairingPort int           `json:"pairing_port"`
	Pairing     *WindowStatus `json:"pairing,omitempty"`
	LastPaired  *Pairing      `json:"last_paired,omitempty"`
	Devices     []Pairing     `json:"devices"`
}

type WindowStatus struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (g *Gateway) Status() Status {
	enabled, lineID := g.store.settings()
	devices := g.store.list()
	g.mu.Lock()
	defer g.mu.Unlock()
	status := Status{
		Enabled:     enabled,
		LineID:      lineID,
		AgentID:     g.store.agentID(),
		Listening:   g.listening,
		ControlPort: g.config.ControlPort,
		PCMPort:     g.config.PCMPort,
		PairingPort: g.config.PairingPort,
		LastPaired:  g.lastPaired,
		Devices:     devices,
	}
	if g.listenErr != nil {
		status.ListenError = g.listenErr.Error()
	}
	if window := g.openWindowLocked(); window != nil {
		status.Pairing = &WindowStatus{Code: window.session.Code, ExpiresAt: window.session.ExpiresAt}
	}
	return status
}

// Configure turns the gateway on or off and selects the line it exposes.
// Turning it off closes any pairing window; paired iPhones are kept.
func (g *Gateway) Configure(enabled bool, lineID string) error {
	if enabled && lineID == "" {
		return ErrNoLine
	}
	if err := g.store.setSettings(enabled, lineID); err != nil {
		return err
	}
	if !enabled {
		g.mu.Lock()
		g.window = nil
		g.mu.Unlock()
	}
	return nil
}

// StartPairing opens a 120 s pairing window for the given administrator.
// A window that is already open is replaced.
func (g *Gateway) StartPairing(userID string) (WindowStatus, error) {
	enabled, lineID := g.store.settings()
	if !enabled {
		return WindowStatus{}, ErrDisabled
	}
	if lineID == "" {
		return WindowStatus{}, ErrNoLine
	}
	if g.config.PairingAddress == "" {
		return WindowStatus{}, ErrNotListening
	}
	session, err := airsimproto.NewPairingSession(g.now())
	if err != nil {
		return WindowStatus{}, err
	}
	g.mu.Lock()
	g.window = &pairingWindow{session: session, userID: userID}
	g.lastPaired = nil
	g.mu.Unlock()
	return WindowStatus{Code: session.Code, ExpiresAt: session.ExpiresAt}, nil
}

func (g *Gateway) CancelPairing() {
	g.mu.Lock()
	g.window = nil
	g.mu.Unlock()
}

func (g *Gateway) openWindowLocked() *pairingWindow {
	if g.window == nil || g.now().After(g.window.session.ExpiresAt) {
		return nil
	}
	return g.window
}

// Revoke forgets one paired iPhone; its signed requests fail from now on.
func (g *Gateway) Revoke(id string) error {
	if err := g.store.remove(id); err != nil {
		return err
	}
	g.forget(id)
	return nil
}

// RevokeUser forgets every iPhone an account paired, for when the account
// is deleted, disabled or loses the administrator role.
func (g *Gateway) RevokeUser(userID string) {
	for _, pairing := range g.store.list() {
		if pairing.UserID == userID {
			g.forget(pairing.ID)
		}
	}
	if _, err := g.store.removeUser(userID); err != nil {
		g.logger.Warn("revoke AirSIM pairings", "component", "airsim", "error", err)
	}
}

func (g *Gateway) forget(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.owned, id)
	delete(g.muted, id)
	delete(g.watches, id)
}

func (g *Gateway) identity(pairing Pairing, peer string) (Identity, error) {
	enabled, lineID := g.store.settings()
	if !enabled {
		return Identity{}, ErrDisabled
	}
	if lineID == "" {
		return Identity{}, ErrNoLine
	}
	return Identity{PairingID: pairing.ID, UserID: pairing.UserID, LineID: lineID, Peer: peer}, nil
}

func (g *Gateway) setOwned(pairingID, callID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if callID == "" {
		delete(g.owned, pairingID)
		return
	}
	g.owned[pairingID] = callID
}

func (g *Gateway) ownedCall(pairingID string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.owned[pairingID]
}

func (g *Gateway) setMuted(pairingID string, muted bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.muted[pairingID] = muted
}

func (g *Gateway) isMuted(pairingID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.muted[pairingID]
}

// keepCalls renews call ownership for every call an iPhone controls, as long
// as that iPhone is still talking to the gateway. A browser renews its own
// lease; the AirSIM app knows nothing about leases, so the gateway does it on
// its behalf and stops when the phone goes quiet, which lets the lease lapse.
func (g *Gateway) keepCalls(ctx context.Context) {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		backend := g.currentBackend()
		pairings := g.store.list()
		now := g.now()
		for _, pairing := range pairings {
			callID := g.ownedCall(pairing.ID)
			if callID == "" {
				continue
			}
			if now.Sub(pairing.LastSeenAt) > 45*time.Second && !g.pcmActiveFor(pairing.ID) {
				continue
			}
			identity, err := g.identity(pairing, pairing.LastPeer)
			if err != nil {
				continue
			}
			renewCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err = backend.KeepCall(renewCtx, identity, callID)
			cancel()
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) || errors.Is(err, ErrForbidden) {
				g.setOwned(pairing.ID, "")
			}
		}
	}
}
