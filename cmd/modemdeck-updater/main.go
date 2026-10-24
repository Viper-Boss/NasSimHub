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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/human-agent65535/modemdeck/internal/ota"
	"github.com/human-agent65535/modemdeck/internal/releasechannel"
	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	built, err := buildController(logger)
	if err != nil {
		logger.Error("configure updater", "error", err)
		os.Exit(1)
	}
	if len(os.Args) > 1 && os.Args[1] == "worker" {
		if err := runWorker(ctx, built.controller, os.Args[2:]); err != nil {
			logger.Error("apply software update", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := serve(ctx, built, logger); err != nil {
		logger.Error("updater stopped", "error", err)
		os.Exit(1)
	}
}

// updater bundles what main assembles, so serve and the worker share one
// construction path and cannot drift into two configurations.
type updater struct {
	controller  *ota.Controller
	runtime     *ota.DockerRuntime
	downloader  *ota.Downloader
	channel     *releasechannel.Channel
	preferences ota.PreferenceStore
	token       string
}

// workerEnvironmentKeys are forwarded to the one-shot install worker. The
// worker rebuilds its configuration from its own environment, so every
// setting that shaped the plan the operator approved has to reach it.
var workerEnvironmentKeys = []string{
	"MODEMDECK_UPDATE_STRATEGY",
	"MODEMDECK_COMPOSE_PROJECT",
	"MODEMDECK_COMPOSE_FILES",
	"MODEMDECK_COMPOSE_PROFILES",
	"MODEMDECK_UPDATE_SLOTS",
	"MODEMDECK_UPDATE_SERVICES",
	"MODEMDECK_REGISTRY_MIRRORS",
	"MODEMDECK_RELEASE_CHANNEL_URLS",
	// The worker mounts the state volume where the server has it. Without
	// this it would look in the default path, find nothing, and write its
	// progress into a read-only root filesystem.
	"MODEMDECK_UPDATER_STATE_DIR",
}

func buildController(logger *slog.Logger) (*updater, error) {
	deploymentDir := environmentOrDefault("MODEMDECK_DEPLOYMENT_DIR", "/deployment")
	stateDir := environmentOrDefault("MODEMDECK_UPDATER_STATE_DIR", "/var/lib/modemdeck-updater")
	environment := readEnvironment(filepath.Join(deploymentDir, ".env"))
	cloudflared := firstNonempty(
		os.Getenv("MODEMDECK_CLOUDFLARE_ENABLED"),
		environment["MODEMDECK_CLOUDFLARE_ENABLED"],
	) == "true"
	selfImage := firstNonempty(
		os.Getenv(ota.WorkerSelfImageEnvironment),
		os.Getenv("MODEMDECK_UPDATER_SELF_IMAGE"),
		environment["MODEMDECK_UPDATER_SELF_IMAGE"],
	)

	workerEnvironment := map[string]string{}
	for _, key := range workerEnvironmentKeys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			workerEnvironment[key] = value
		}
	}
	slots, err := parseAssignments(os.Getenv("MODEMDECK_UPDATE_SLOTS"))
	if err != nil {
		return nil, fmt.Errorf("MODEMDECK_UPDATE_SLOTS: %w", err)
	}
	services, err := parseAssignments(os.Getenv("MODEMDECK_UPDATE_SERVICES"))
	if err != nil {
		return nil, fmt.Errorf("MODEMDECK_UPDATE_SERVICES: %w", err)
	}

	runtime, err := ota.NewDockerRuntime(ota.DockerRuntimeOptions{
		DeploymentDir:     deploymentDir,
		HostDeploymentDir: environmentOrDefault("MODEMDECK_HOST_DEPLOYMENT_DIR", deploymentDir),
		StateDir:          stateDir,
		StateVolume: firstNonempty(
			os.Getenv("MODEMDECK_UPDATER_STATE_VOLUME"),
			environment["MODEMDECK_UPDATER_STATE_VOLUME"],
			"modemdeck-updater-state",
		),
		SelfImage:         selfImage,
		Cloudflared:       cloudflared,
		Strategy:          ota.UpdateStrategy(strings.TrimSpace(os.Getenv("MODEMDECK_UPDATE_STRATEGY"))),
		ComposeProject:    os.Getenv("MODEMDECK_COMPOSE_PROJECT"),
		ComposeFiles:      splitList(os.Getenv("MODEMDECK_COMPOSE_FILES")),
		ComposeProfiles:   splitList(os.Getenv("MODEMDECK_COMPOSE_PROFILES")),
		Slots:             slots,
		Services:          services,
		Mirrors:           splitList(os.Getenv("MODEMDECK_REGISTRY_MIRRORS")),
		WorkerEnvironment: workerEnvironment,
	})
	if err != nil {
		return nil, err
	}

	installedVersionPath := filepath.Join(stateDir, ota.InstalledVersionFilename)
	// Deliberately NOT an environment override. A version in the updater's
	// environment would sit in its Compose definition, so every release that
	// changed it would also recreate the updater -- the one service a release
	// must leave alone (deploy/tests/static-test.sh). What the updater installs
	// it records in installed-version; before the first install, the version
	// compiled in is the honest answer.
	fallbackVersion := firstNonempty(version, "dev")
	channelURLs, dropped := releasechannel.ParseURLs(os.Getenv("MODEMDECK_RELEASE_CHANNEL_URLS"))
	if len(dropped) > 0 {
		logger.Warn("ignoring release channel entries that are not http(s) URLs", "dropped", dropped)
	}
	// The signed release channel. Mirrors are untrusted by construction: a
	// manifest is believed because it verifies against the compiled-in key,
	// and the highest verified version across all of them wins, so one
	// honest reachable mirror defeats any number of stale ones.
	channel := releasechannel.New(releasechannel.Options{
		URLs:           channelURLs,
		CurrentVersion: currentVersionFunc(installedVersionPath, fallbackVersion),
	})

	downloads := ota.NewFileDownloadStore(stateDir)
	preferences := ota.NewFilePreferenceStore(stateDir)
	controller, err := ota.New(ota.Options{
		Checker:     channel,
		Manifests:   ota.ChannelManifests{Channel: channel},
		Runtime:     runtime,
		Operations:  ota.NewFileOperationStore(stateDir),
		Downloads:   downloads,
		Preferences: preferences,
	})
	if err != nil {
		return nil, err
	}
	downloader, err := ota.NewDownloader(ota.DownloaderOptions{
		Controller:   controller,
		Runtime:      runtime,
		Downloads:    downloads,
		Preferences:  preferences,
		Logger:       logger,
		InitialDelay: 3 * time.Minute,
	})
	if err != nil {
		return nil, err
	}

	token := ""
	if tokenFile := strings.TrimSpace(os.Getenv("MODEMDECK_UPDATER_TOKEN_FILE")); tokenFile != "" {
		contents, readErr := os.ReadFile(tokenFile)
		if readErr != nil {
			return nil, fmt.Errorf("read updater token: %w", readErr)
		}
		token = strings.TrimSpace(string(contents))
	}
	return &updater{
		controller:  controller,
		runtime:     runtime,
		downloader:  downloader,
		channel:     channel,
		preferences: preferences,
		token:       token,
	}, nil
}

// currentVersionFunc reads the installed version on every call. The updater
// replaces the running release underneath itself, so a version read once at
// start-up would go on offering the update it has just installed.
func currentVersionFunc(installedVersionPath, fallback string) func() string {
	return func() string {
		return firstNonempty(readTrimmedFile(installedVersionPath), fallback)
	}
}

// parseAssignments reads "api=name:tag,web=other:tag".
//
// Strict, unlike the mirror list: a slot is where an install writes, and a
// typo that silently dropped one would install three components of four.
func parseAssignments(value string) (map[string]string, error) {
	assignments := map[string]string{}
	for _, entry := range splitList(value) {
		key, assigned, ok := strings.Cut(entry, "=")
		key, assigned = strings.TrimSpace(key), strings.TrimSpace(assigned)
		if !ok || key == "" || assigned == "" {
			return nil, fmt.Errorf("%q is not component=value", entry)
		}
		if _, duplicate := assignments[key]; duplicate {
			return nil, fmt.Errorf("%s is assigned twice", key)
		}
		assignments[key] = assigned
	}
	return assignments, nil
}

func splitList(value string) []string {
	var items []string
	for _, field := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	}) {
		if field = strings.TrimSpace(field); field != "" {
			items = append(items, field)
		}
	}
	return items
}

func readTrimmedFile(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(contents))
}

func serve(
	ctx context.Context,
	built *updater,
	logger *slog.Logger,
) error {
	handler, err := ota.NewHandler(built.controller, built.token)
	if err != nil {
		return err
	}
	handler.WithDownloads(built.downloader, built.preferences).
		WithRunning(built.runtime).
		// The api hands over what the peer networks answered, and fetches
		// the signed document to share; see internal/httpapi/release_peers.go.
		WithRelease(built.channel)
	// Background downloads live with the server, not the worker: the worker
	// is a one-shot install and exits when it is done.
	go built.downloader.Run(ctx)
	server := &http.Server{
		// Loopback by default: a bare-metal updater serves one local API.
		// Container deployments (docker-compose.ota.yml) explicitly set
		// 0.0.0.0 because the API reaches the updater over the Compose
		// network; keep that override when changing the default.
		Addr:              environmentOrDefault("MODEMDECK_UPDATER_LISTEN_ADDRESS", "127.0.0.1:8081"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errorsChannel := make(chan error, 1)
	go func() {
		logger.Info("NasSimHub updater started", "version", version, "address", server.Addr)
		errorsChannel <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	case err := <-errorsChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func runWorker(ctx context.Context, controller *ota.Controller, arguments []string) error {
	flags := flag.NewFlagSet("worker", flag.ContinueOnError)
	expectedVersion := flags.String("expected-version", "", "release version to apply")
	operationID := flags.String("operation-id", "", "update operation identifier")
	startedAt := flags.String("started-at", "", "update operation start time")
	confirmHardware := flags.Bool("confirm-hardware", false, "confirm Hardware interruption")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *expectedVersion == "" || *operationID == "" || *startedAt == "" {
		return errors.New("worker update identity is incomplete")
	}
	return controller.RunWorker(ctx, ota.WorkerRequest{
		ExpectedVersion: *expectedVersion,
		ConfirmHardware: *confirmHardware,
		Operation: updatecheck.Operation{
			ID:            *operationID,
			State:         updatecheck.OperationRunning,
			TargetVersion: *expectedVersion,
			StartedAt:     *startedAt,
		},
	})
}

func readEnvironment(path string) map[string]string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(contents), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || strings.HasPrefix(key, "#") {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values
}

func environmentOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
