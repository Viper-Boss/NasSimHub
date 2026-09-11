package httpapi

import (
	"context"
	"time"
)

// Bringing WireGuard and the DHT back up after a restart.
//
// Both persist an "enabled" flag and both have an idempotent apply path, and
// until this file existed neither was ever called except from a PUT on the
// settings endpoint. So a host reboot left a configured, enabled tunnel down
// until somebody opened the settings page and saved it again -- and a status
// poll would not revive it, because the GET path deliberately reports state
// without applying. The gateway would come back with its own UI cheerfully
// showing "not running" and no way to find out why from the outside.
//
// For the DHT it was worse than an inconvenience: the DHT is meant to be on by
// default, and "on by default" that does not survive a reboot is not on by
// default. A NAS that reboots at 3am and comes back without its second control
// path has lost exactly the redundancy the path was added for.
//
// NKN already did this correctly (StartHomeSIMNKNOptionalServices). This is
// the same shape for the other two, deliberately so: three services that
// restore themselves three different ways is how one of them quietly stops.

// tunnelRestoreDelay lets the agent socket come up first.
//
// Applying before the agent is listening fails, and while both apply paths are
// idempotent and the retry below would recover, a failed apply records an
// error on the status the UI reads. A short wait means the ordinary case is
// silent rather than briefly alarming.
const tunnelRestoreDelay = 3 * time.Second

// tunnelRestoreRetries and tunnelRestoreInterval bound the retry.
//
// This is a restore, not a supervisor: it gets the configured state applied
// once the agent is reachable and then stops. A permanent retry loop here
// would fight an operator who has deliberately taken the interface down by
// other means, and would hide a real failure by papering over it every few
// seconds.
const (
	tunnelRestoreRetries  = 5
	tunnelRestoreInterval = 10 * time.Second
)

// StartHomeSIMTunnelRestore reapplies enabled tunnel settings after a restart.
//
// It rewrites nothing: settings are read, and the revision the caller stored is
// passed through untouched, so a restore cannot be mistaken for a change by
// anything watching revisions.
func (api *API) StartHomeSIMTunnelRestore(ctx context.Context) {
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(tunnelRestoreDelay):
		}

		// Each service is restored independently. A DHT that cannot start must
		// not stop WireGuard from coming back, and the reverse -- they are two
		// separate promises to the operator.
		api.restore(ctx, "wireguard", func(ctx context.Context) bool {
			settings, err := api.loadWireGuardSettings()
			if err != nil || !settings.Enabled {
				return true
			}
			if api.wireGuardService == nil {
				// The agent does not offer the capability on this build. There
				// is nothing to retry.
				return true
			}
			status := api.wireGuardRuntimeStatus(ctx, settings, true)
			return status.Error == "" && status.State != "unavailable"
		})

		api.restore(ctx, "dht", func(ctx context.Context) bool {
			settings, err := api.loadDHTSettings()
			if err != nil || !settings.Enabled {
				return true
			}
			if api.dhtService == nil {
				return true
			}
			status := api.dhtRuntimeStatus(ctx, settings, true)
			// "isolated" is a success: the host is up and looking for peers,
			// which is the most a DHT can promise on a quiet network. Retrying
			// on it would restart a working node every ten seconds.
			return status.Error == "" && status.State != "unavailable"
		})
	}()
}

// restore runs attempt until it reports success or the retries run out.
func (api *API) restore(ctx context.Context, name string, attempt func(context.Context) bool) {
	for tries := 0; tries < tunnelRestoreRetries; tries++ {
		if ctx.Err() != nil {
			return
		}
		if attempt(ctx) {
			return
		}
		api.logger.Warn("tunnel restore attempt failed, retrying",
			"service", name, "attempt", tries+1, "of", tunnelRestoreRetries)
		select {
		case <-ctx.Done():
			return
		case <-time.After(tunnelRestoreInterval):
		}
	}
	// Said plainly, because the next person to read this line is trying to work
	// out why a tunnel they enabled is down.
	api.logger.Error("service did not come back up after a restart; it stays off until its settings are saved again",
		"service", name, "attempts", tunnelRestoreRetries)
}
