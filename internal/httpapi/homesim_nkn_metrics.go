package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type nknObservationHistory struct {
	StartedAt time.Time            `json:"started_at"`
	LastSeen  map[string]time.Time `json:"last_seen"`
}

const (
	// Daily and monthly are the same measurement over two windows: identities
	// this NAS has seen subscribed to the topic since the cutoff. Reporting the
	// daily figure as "peers in the newest sample" instead made it an
	// instantaneous count that swung with every poll and could not be compared
	// against the monthly number beside it.
	nknObservationDailyWindow   = 24 * time.Hour
	nknObservationMonthlyWindow = 30 * 24 * time.Hour
	// Identities that have not been seen for this long stop being counted as
	// "registered", which also keeps the file from growing without bound.
	nknObservationRetention     = 180 * 24 * time.Hour
	nknObservationMaxIdentities = 100000
)

func nknDeviceIdentity(address string) string {
	parts := strings.Split(address, ".")
	key := parts[len(parts)-1]
	if raw, err := hex.DecodeString(key); err == nil && len(raw) == 32 {
		return strings.ToLower(key)
	}
	return address
}

// Historical counts describe this NAS's observations, not an invented global
// all-time registry. Only hashed public identities are retained on disk.
func (api *API) recordNKNObservations(result *nknNetworkPeersResponse) error {
	// A truncated peer page leaves Active24h nil upstream, because an incomplete
	// page must not be published as a complete instantaneous total. The sightings
	// in it are still real, though — skipping the whole recording meant that on a
	// busy topic (>=1000 subscribers) the history silently stopped updating and
	// every counter froze.
	api.nknMetricsMu.Lock()
	defer api.nknMetricsMu.Unlock()
	dir, ok := api.homeSIMDataDirectory()
	if !ok {
		return fmt.Errorf("statistics storage unavailable")
	}
	path := filepath.Join(dir, "nkn-observations.json")
	history := nknObservationHistory{StartedAt: result.FetchedAt, LastSeen: map[string]time.Time{}}
	raw, err := os.ReadFile(path)
	if err == nil {
		if json.Unmarshal(raw, &history) != nil || history.StartedAt.IsZero() || history.LastSeen == nil {
			return fmt.Errorf("statistics history is invalid; existing file preserved")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	for _, peer := range result.Peers {
		identity := nknDeviceIdentity(peer.Address)
		hash := sha256.Sum256([]byte(identity))
		key := hex.EncodeToString(hash[:])
		if result.FetchedAt.After(history.LastSeen[key]) {
			history.LastSeen[key] = result.FetchedAt
		}
	}
	// Drop identities past the retention window before the cap is checked.
	// Erroring out at a fixed size instead froze every counter permanently once
	// the file filled up, with no way back short of deleting it by hand.
	for key, last := range history.LastSeen {
		if last.Before(result.FetchedAt.Add(-nknObservationRetention)) {
			delete(history.LastSeen, key)
		}
	}
	if len(history.LastSeen) > nknObservationMaxIdentities {
		return fmt.Errorf("statistics history limit reached")
	}
	dailyCutoff := result.FetchedAt.Add(-nknObservationDailyWindow)
	monthCutoff := result.FetchedAt.Add(-nknObservationMonthlyWindow)
	daily, month := 0, 0
	for _, last := range history.LastSeen {
		if !last.Before(dailyCutoff) {
			daily++
		}
		if !last.Before(monthCutoff) {
			month++
		}
	}
	if err := writeHomeSIMJSON(path, history); err != nil {
		return err
	}
	total := len(history.LastSeen)
	result.Active24h = &daily
	result.ActiveMonth = &month
	result.RegisteredTotal = &total
	result.CoverageStartedAt = history.StartedAt
	result.StatisticsScope = "local_observed_identities"
	return nil
}

func (api *API) StartHomeSIMNKNMetrics(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			// Sample when the network directory is enabled (this manager
			// mirrors the directory switch), not when the NKN forwarding
			// channel is on — the two features are independent.
			if settings, err := api.nknNetwork.loadSettings(); err == nil && settings.Enabled {
				if result, err := fetchNKNNetworkPeers(""); err == nil {
					_ = api.recordNKNObservations(&result)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
