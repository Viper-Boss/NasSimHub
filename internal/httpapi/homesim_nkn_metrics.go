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
	if result.Active24h == nil {
		return nil
	}
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
	unique := map[string]bool{}
	for _, peer := range result.Peers {
		identity := nknDeviceIdentity(peer.Address)
		hash := sha256.Sum256([]byte(identity))
		key := hex.EncodeToString(hash[:])
		unique[key] = true
		if result.FetchedAt.After(history.LastSeen[key]) {
			history.LastSeen[key] = result.FetchedAt
		}
	}
	if len(history.LastSeen) > 100000 {
		return fmt.Errorf("statistics history limit reached")
	}
	month := 0
	for _, last := range history.LastSeen {
		if !last.Before(result.FetchedAt.Add(-30 * 24 * time.Hour)) {
			month++
		}
	}
	if err := writeHomeSIMJSON(path, history); err != nil {
		return err
	}
	daily, total := len(unique), len(history.LastSeen)
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
