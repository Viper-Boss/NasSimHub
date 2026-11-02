package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/airsimgw"
)

// AirSIM iPhone app compatibility (internal/airsimgw). All optional.
//
//	MODEMDECK_AIRSIM=off                 do not start the AirSIM listeners
//	MODEMDECK_AIRSIM_CONTROL_PORT=7592   signed control requests
//	MODEMDECK_AIRSIM_PCM_PORT=7593       call audio
//	MODEMDECK_AIRSIM_PAIRING_PORT=7594   pairing, answered only while an
//	                                     administrator holds a window open
//
// The listeners run inside the api container; nothing reaches them until
// docker-compose.airsim.yml publishes the same ports on the host, and nothing
// is answered until an administrator turns the feature on in Settings. A
// port is the same number inside and outside: the app is told the port to
// use, so a mapping to a different host port would advertise the wrong one.
func airsimConfigFromEnvironment(logger *slog.Logger, dataDirectory string, secrets airsimgw.SecretBox) (*airsimgw.Gateway, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MODEMDECK_AIRSIM"))) {
	case "off", "false", "0", "disabled":
		return nil, nil
	}
	ports := map[string]int{}
	for name, spec := range map[string]struct {
		value    string
		fallback int
	}{
		"control": {os.Getenv("MODEMDECK_AIRSIM_CONTROL_PORT"), airsimgw.DefaultControlPort},
		"pcm":     {os.Getenv("MODEMDECK_AIRSIM_PCM_PORT"), airsimgw.DefaultPCMPort},
		"pairing": {os.Getenv("MODEMDECK_AIRSIM_PAIRING_PORT"), airsimgw.DefaultPairingPort},
	} {
		port, err := parseAirSIMPort(spec.value, spec.fallback)
		if err != nil {
			return nil, fmt.Errorf("AirSIM %s port: %w", name, err)
		}
		ports[name] = port
	}
	if ports["control"] == ports["pcm"] || ports["control"] == ports["pairing"] || ports["pcm"] == ports["pairing"] {
		return nil, fmt.Errorf("AirSIM control, PCM and pairing ports must differ")
	}
	return airsimgw.New(airsimgw.Config{
		ControlAddress: ":" + strconv.Itoa(ports["control"]),
		PCMAddress:     ":" + strconv.Itoa(ports["pcm"]),
		PairingAddress: ":" + strconv.Itoa(ports["pairing"]),
		StatePath:      filepath.Join(dataDirectory, "homesim", "airsim.json"),
		Secrets:        secrets,
		Logger:         logger.With("component", "airsim"),
	})
}

func parseAirSIMPort(value string, fallback int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	port, err := strconv.Atoi(value)
	// The app refuses ports below 1024 (VoWLANEndpoint).
	if err != nil || port < 1024 || port > 65535 {
		return 0, fmt.Errorf("%q is not a port between 1024 and 65535", value)
	}
	return port, nil
}

// runAirSIM serves the gateway until ctx ends. A port that cannot be bound
// is logged and shown on the settings page; it does not stop the API.
func runAirSIM(ctx context.Context, gateway *airsimgw.Gateway, logger *slog.Logger) {
	if gateway == nil {
		return
	}
	if err := gateway.Serve(ctx); err != nil {
		logger.Warn("AirSIM gateway stopped", "component", "airsim", "error", err)
	}
}
