package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/human-agent65535/modemdeck/agent/internal/controllease"
	"github.com/human-agent65535/modemdeck/agent/internal/deviceconfig"
	"github.com/human-agent65535/modemdeck/agent/internal/dht"
	"github.com/human-agent65535/modemdeck/agent/internal/diagnostics"
	"github.com/human-agent65535/modemdeck/agent/internal/domain"
	"github.com/human-agent65535/modemdeck/agent/internal/httpapi"
	"github.com/human-agent65535/modemdeck/agent/internal/media"
	"github.com/human-agent65535/modemdeck/agent/internal/modemmanager"
	"github.com/human-agent65535/modemdeck/agent/internal/networking"
	"github.com/human-agent65535/modemdeck/agent/internal/qdc507voice"
	"github.com/human-agent65535/modemdeck/agent/internal/safetywatchdog"
	"github.com/human-agent65535/modemdeck/agent/internal/unixsocket"
	"github.com/human-agent65535/modemdeck/agent/internal/volte"
	"github.com/human-agent65535/modemdeck/agent/internal/wireguard"
)

var version = "dev"

func main() {
	logBuffer := diagnostics.NewLogBuffer(diagnostics.DefaultLogCapacity)
	logger := slog.New(logBuffer.Handler(slog.NewJSONHandler(os.Stdout, nil)))
	slog.SetDefault(logger)
	if err := run(logBuffer); err != nil {
		slog.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}

func run(diagnosticLogs diagnostics.LogSource) error {
	socketPath := flag.String("socket", "/run/modemdeck/agent.sock", "absolute unix socket path")
	socketMode := flag.String("socket-mode", "0660", "unix socket permission mode in octal")
	socketUID := flag.Int("socket-uid", -1, "unix socket owner uid; -1 keeps the process uid")
	socketGID := flag.Int("socket-gid", -1, "unix socket owner gid; -1 keeps the process gid")
	mediaBindingsFile := flag.String(
		"media-bindings-file",
		os.Getenv("MODEMDECK_MEDIA_BINDINGS_FILE"),
		"absolute path to explicit host audio-port bindings JSON",
	)
	bearerStateFile := flag.String(
		"bearer-state-file",
		envOrDefault("MODEMDECK_BEARER_STATE_FILE", "/run/modemdeck/bearers.json"),
		"persistent file recording only ModemDeck-owned ModemManager bearers",
	)
	networkStateFile := flag.String(
		"network-state-file",
		envOrDefault("MODEMDECK_NETWORK_STATE_FILE", "/run/modemdeck/network.json"),
		"persistent file recording only ModemDeck-owned kernel network state",
	)
	radioStateFile := flag.String(
		"radio-state-file",
		envOrDefault("MODEMDECK_RADIO_STATE_FILE", "/run/modemdeck/radio-state.json"),
		"persistent file recording user-disabled modem radios",
	)
	trafficStateFile := flag.String(
		"traffic-state-file",
		os.Getenv("MODEMDECK_TRAFFIC_STATE_FILE"),
		"persistent file accumulating 4G traffic counters across interface resets",
	)
	qdc507VoiceRuntimeDirectory := flag.String(
		"qdc507-voice-runtime-dir",
		os.Getenv("MODEMDECK_QDC507_VOICE_RUNTIME_DIR"),
		"absolute directory containing the reviewed QDC507 module voice runtime",
	)
	watchdogHeartbeatFile := flag.String(
		"watchdog-heartbeat-file",
		os.Getenv("MODEMDECK_AGENT_HEARTBEAT_FILE"),
		"absolute heartbeat path for the independent call watchdog",
	)
	flag.Parse()

	mode, err := parseSocketMode(*socketMode)
	if err != nil {
		return err
	}

	dataPlane, err := networking.NewDataPlane(networking.DataPlaneOptions{
		StateFile: *networkStateFile,
	})
	if err != nil {
		return fmt.Errorf("create cellular data plane: %w", err)
	}
	qdc507VoiceRuntime, err := qdc507voice.New(qdc507voice.Options{
		RuntimeDirectory: *qdc507VoiceRuntimeDirectory,
	})
	if err != nil {
		return fmt.Errorf("create QDC507 module voice runtime: %w", err)
	}
	provider, err := modemmanager.OpenSystemBusWithOptions(modemmanager.Options{
		DataPlane:          dataPlane,
		BearerStateFile:    *bearerStateFile,
		RadioStateFile:     *radioStateFile,
		TrafficStateFile:   *trafficStateFile,
		QDC507VoiceRuntime: qdc507VoiceRuntime,
		ModuleShell:        qdc507VoiceRuntime,
	})
	if err != nil {
		return err
	}
	defer provider.Close()
	controlLease, err := controllease.New(provider, controllease.Options{
		Report: func(err error) {
			slog.Error("enforce application control lease", "error", err)
		},
	})
	if err != nil {
		return fmt.Errorf("create application control lease: %w", err)
	}
	recoveryContext, cancelRecovery := context.WithTimeout(
		context.Background(),
		15*time.Second,
	)
	if err := controlLease.Recover(recoveryContext); err != nil {
		cancelRecovery()
		return fmt.Errorf("recover orphan modem calls: %w", err)
	}
	cancelRecovery()
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := controlLease.Shutdown(shutdownContext); err != nil {
			slog.Error("end calls during agent shutdown", "error", err)
		}
	}()
	reconcileContext, cancelReconcile := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	if err := provider.ReconcileDataPlane(reconcileContext); err != nil {
		cancelReconcile()
		return fmt.Errorf("reconcile cellular data plane: %w", err)
	}
	cancelReconcile()
	bindings, err := loadMediaBindings(*mediaBindingsFile)
	if err != nil {
		return err
	}
	backends, err := configureMediaBackends(bindings)
	if err != nil {
		return err
	}
	mediaManager, err := media.NewManager(
		media.CallSourceFunc(func(ctx context.Context, callID string) (media.Call, error) {
			snapshot, err := provider.Snapshot(ctx)
			if err != nil {
				return media.Call{}, media.NewError(
					media.ErrorBackendUnavailable,
					"resolve_media_call",
					"ModemManager call state is unavailable",
					err,
				)
			}
			for _, call := range snapshot.Calls {
				if call.ID != callID {
					continue
				}
				projected := media.Call{
					ID:             call.ID,
					State:          call.State,
					AudioPort:      call.AudioPort,
					MediaAvailable: call.MediaAvailable,
				}
				if call.AudioFormat != nil {
					projected.AudioFormat = &media.AdvertisedFormat{
						Encoding:   call.AudioFormat.Encoding,
						Resolution: call.AudioFormat.Resolution,
						Rate:       call.AudioFormat.Rate,
					}
				}
				return projected, nil
			}
			return media.Call{}, media.NewError(
				media.ErrorNotFound,
				"resolve_media_call",
				"call was not found",
				nil,
			)
		}),
		bindings,
		backends,
		media.Options{},
	)
	if err != nil {
		return err
	}
	defer mediaManager.Close()
	volteRegistry, err := volte.NewRegistry(volte.QuectelLTEStandardQCFGIMSProfile())
	if err != nil {
		return fmt.Errorf("create VoLTE profile registry: %w", err)
	}
	deviceConfigurations, err := deviceconfig.New(
		provider,
		volteRegistry,
		func(
			_ context.Context,
			lineID string,
			_ volte.Identity,
		) (volte.Transports, error) {
			return volte.Transports{AT: provider.ATTransport(lineID)}, nil
		},
	)
	if err != nil {
		return fmt.Errorf("create device configuration service: %w", err)
	}
	networkManager, err := networking.NewManager(provider)
	if err != nil {
		return fmt.Errorf("create network manager: %w", err)
	}
	defer func() {
		if err := networkManager.Close(); err != nil {
			slog.Error("close network manager", "error", err)
		}
	}()

	listener, err := unixsocket.Listen(unixsocket.Config{
		Path: *socketPath,
		Mode: mode,
		UID:  *socketUID,
		GID:  *socketGID,
	})
	if err != nil {
		return err
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var heartbeatFailures <-chan error
	if *watchdogHeartbeatFile != "" {
		if err := safetywatchdog.WriteHeartbeat(
			*watchdogHeartbeatFile,
			os.Getpid(),
			time.Now(),
		); err != nil {
			return fmt.Errorf("initialize call watchdog heartbeat: %w", err)
		}
		defer os.Remove(*watchdogHeartbeatFile)
		failures := make(chan error, 1)
		heartbeatFailures = failures
		go func() {
			if err := safetywatchdog.RunEmitter(
				ctx,
				*watchdogHeartbeatFile,
				os.Getpid(),
				time.Second,
				time.Now,
			); err != nil {
				failures <- err
			}
		}()
	}
	go runRadioReconciler(ctx, provider)
	go runMessageReconciler(ctx, provider)
	go runSMSReachabilityReconciler(ctx, provider)
	if qdc507VoiceRuntime.Enabled() {
		go runQDC507VoiceReconciler(ctx, provider, qdc507VoiceRuntime)
	}
	go controlLease.Run(ctx)
	go provider.RunATCallObserver(ctx)

	// The WireGuard key lives on persistent hardware state, not in /run: a key
	// regenerated on every restart would invalidate every peer that pinned the
	// public key.
	wireGuardManager := wireguard.New(
		envOrDefault("MODEMDECK_WIREGUARD_STATE_DIR", "/var/lib/ModemManager/wireguard"),
	)
	// The libp2p identity lives on persistent state for the same reason as the
	// WireGuard key: a PeerID regenerated on every restart orphans this node
	// from everyone who pinned it.
	dhtService := dht.New(
		envOrDefault("MODEMDECK_DHT_STATE_DIR", "/var/lib/ModemManager/dht"),
	)
	defer dhtService.Stop()

	server := &http.Server{
		Handler: httpapi.NewWithOptions(provider, version, httpapi.Options{
			Media:                mediaManager,
			ControlLease:         controlLease,
			DeviceConfigurations: deviceConfigurations,
			Network:              networkManager,
			NetworkSelection:     provider,
			DiagnosticLogs:       diagnosticLogs,
			WireGuard:            wireGuardManager,
			DHT:                  dhtService,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- server.Serve(listener)
	}()

	slog.Info("agent listening", "socket", *socketPath, "api_version", "v1", "version", version)

	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve unix socket: %w", err)
	case err := <-heartbeatFailures:
		return fmt.Errorf("renew call watchdog heartbeat: %w", err)
	case <-ctx.Done():
		callShutdownContext, callShutdownCancel := context.WithTimeout(
			context.Background(),
			5*time.Second,
		)
		if err := controlLease.Shutdown(callShutdownContext); err != nil {
			slog.Error("end calls before agent server shutdown", "error", err)
		}
		callShutdownCancel()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		if err := <-serveResult; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve unix socket: %w", err)
		}
		return nil
	}
}

type qdc507VoiceLineProvider interface {
	QDC507VoiceLines(context.Context) ([]domain.Line, error)
	EnsureQDC507VoiceUSB(context.Context, []domain.Line) (bool, error)
	ForceQDC507VoiceUSB(context.Context, []domain.Line) (bool, error)
	QDC507VoiceRuntimeChanged()
	SubscribeModemLifecycle(context.Context) (<-chan struct{}, error)
}

type qdc507VoiceReconciler interface {
	Reconcile(context.Context, []domain.Line) (bool, error)
}

func runQDC507VoiceReconciler(
	ctx context.Context,
	provider qdc507VoiceLineProvider,
	runtime qdc507VoiceReconciler,
) {
	const attemptTimeout = 3 * time.Minute
	// Self-healing: when the module's internal adbd wedges, the ordinary
	// Ensure short-circuits on the (still correct) USB descriptors and the
	// voice reconcile keeps failing with the same error forever. After three
	// consecutive reconcile failures, force one full QADBKEY re-authorization
	// plus controlled reboot — with a cooldown so a genuinely dead module
	// cannot be reboot-looped.
	const forceAfterFailures = 3
	const forceCooldown = 10 * time.Minute
	consecutiveFailures := 0
	lastForceAttempt := time.Time{}
	events, err := provider.SubscribeModemLifecycle(ctx)
	if err != nil {
		slog.Error("subscribe QDC507 modem lifecycle", "error", err)
		return
	}
	reconcile := func() {
		attemptContext, cancel := context.WithTimeout(ctx, attemptTimeout)
		defer cancel()
		lines, err := provider.QDC507VoiceLines(attemptContext)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				slog.Warn("read QDC507 modem inventory", "error", err)
			}
			return
		}
		usbChanged, provisionErr := provider.EnsureQDC507VoiceUSB(attemptContext, lines)
		if usbChanged {
			provider.QDC507VoiceRuntimeChanged()
		}
		if provisionErr != nil {
			if !errors.Is(provisionErr, context.Canceled) {
				slog.Warn("provision QDC507 ADB/UAC interfaces", "error", provisionErr)
			}
			return
		}
		changed, reconcileErr := runtime.Reconcile(attemptContext, lines)
		if changed {
			provider.QDC507VoiceRuntimeChanged()
		}
		if reconcileErr == nil || errors.Is(reconcileErr, context.Canceled) {
			consecutiveFailures = 0
			return
		}
		slog.Warn("reconcile QDC507 module voice route", "error", reconcileErr)
		consecutiveFailures++
		if consecutiveFailures < forceAfterFailures {
			return
		}
		if !lastForceAttempt.IsZero() && time.Since(lastForceAttempt) < forceCooldown {
			return
		}
		slog.Warn("QDC507 voice route keeps failing; forcing QADBKEY re-authorization and reboot",
			"consecutive_failures", consecutiveFailures)
		lastForceAttempt = time.Now()
		consecutiveFailures = 0
		forcedChanged, forceErr := provider.ForceQDC507VoiceUSB(attemptContext, lines)
		if forcedChanged {
			provider.QDC507VoiceRuntimeChanged()
		}
		if forceErr != nil && !errors.Is(forceErr, context.Canceled) {
			slog.Warn("force QDC507 ADB/UAC re-provisioning", "error", forceErr)
		}
	}

	reconcile()
	// A periodic tick is required in addition to modem lifecycle events: when
	// the module is stable but its adbd is wedged (the field failure this
	// guards against), no lifecycle events fire, and the consecutive-failure
	// counter behind the forced QADBKEY re-authorization would never reach
	// its threshold.
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, open := <-events:
			if !open {
				return
			}
			reconcile()
		case <-ticker.C:
			reconcile()
		}
	}
}

type radioReconciler interface {
	ReconcileRadioState(context.Context) error
	SubscribeRadioLifecycle(context.Context) (<-chan struct{}, error)
}

func runRadioReconciler(ctx context.Context, provider radioReconciler) {
	const attemptTimeout = 45 * time.Second
	events, err := provider.SubscribeRadioLifecycle(ctx)
	if err != nil {
		slog.Error("subscribe modem radio lifecycle", "error", err)
		return
	}
	reconcile := func() {
		attemptContext, cancel := context.WithTimeout(ctx, attemptTimeout)
		defer cancel()
		if err := provider.ReconcileRadioState(attemptContext); err != nil &&
			!errors.Is(err, context.Canceled) {
			slog.Warn("reconcile modem radio state", "error", err)
		}
	}

	reconcile()
	// Same lesson as the voice reconciler: a module reboot can leave the
	// radio in a settling state (initializing → disabled) where the one-shot
	// InterfacesAdded event fires too early and ReconcileRadioState skips it
	// as a no-op. Without a periodic pass, the modem then stays disabled
	// (no signal) until someone runs mmcli --enable by hand.
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, open := <-events:
			if !open {
				return
			}
			reconcile()
		case <-ticker.C:
			reconcile()
		}
	}
}

type messageReconciler interface {
	ReconcileStuckMessages(context.Context) error
}

// runMessageReconciler frees SMS slots ModemManager has stopped progressing.
//
// Like the voice and radio reconcilers above, this needs a periodic tick rather
// than lifecycle events: an SMS whose reception stalled stops producing
// ModemManager signals, so no event can ever trigger its cleanup, and an
// event-driven design would never observe it a second time. The interval also
// defines what "stuck" means — a message still in transit on the next pass has
// been stalled for a whole interval, far longer than a healthy concatenated
// reception takes.
func runMessageReconciler(ctx context.Context, provider messageReconciler) {
	const attemptTimeout = time.Minute
	reconcile := func() {
		attemptContext, cancel := context.WithTimeout(ctx, attemptTimeout)
		if err := provider.ReconcileStuckMessages(attemptContext); err != nil &&
			!errors.Is(err, context.Canceled) {
			slog.Warn("reconcile stuck SMS reception", "error", err)
		}
		cancel()
	}

	// The first pass only records what is already in transit; the pass after it
	// clears anything that never progressed.
	reconcile()
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

type smsReachabilityReconciler interface {
	ReconcileSMSReachability(context.Context) error
}

// runSMSReachabilityReconciler surfaces receptions ModemManager never reported.
//
// This one polls faster than the other reconcilers because it exists to make a
// message the user is waiting for visible within about a minute: ModemManager
// only re-reads the modem's message storage when the modem is enabled again, so
// the agent has to notice the divergence and cycle the radio itself instead of
// leaving the user to toggle airplane mode. Detection and recovery are both
// bounded (see ReconcileSMSReachability), so the cadence cannot turn into
// repeated radio cycles.
func runSMSReachabilityReconciler(ctx context.Context, provider smsReachabilityReconciler) {
	const attemptTimeout = 2 * time.Minute
	// A stuck message is usually a login code with a five minute lifetime, and a
	// radio cycle costs about two seconds, so this polls faster than the other
	// reconcilers: the message surfaces within about half a minute.
	const interval = 20 * time.Second
	reconcile := func() {
		attemptContext, cancel := context.WithTimeout(ctx, attemptTimeout)
		if err := provider.ReconcileSMSReachability(attemptContext); err != nil &&
			!errors.Is(err, context.Canceled) {
			slog.Warn("reconcile SMS reachability", "error", err)
		}
		cancel()
	}

	reconcile()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

func envOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func parseSocketMode(value string) (os.FileMode, error) {
	parsed, err := strconv.ParseUint(value, 8, 32)
	if err != nil || parsed > 0o777 {
		return 0, fmt.Errorf("invalid socket mode %q: expected octal permissions between 0000 and 0777", value)
	}
	return os.FileMode(parsed), nil
}
