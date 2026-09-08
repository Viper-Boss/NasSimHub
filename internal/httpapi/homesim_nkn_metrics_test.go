package httpapi

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNKNMetricsDeduplicateAndPersist(t *testing.T) {
	api, err := New(&fakeRepository{}, Options{disableAuthentication: true, ContactBackupDirectory: filepath.Join(t.TempDir(), "backups", "contacts")})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	count := 2
	result := nknNetworkPeersResponse{Active24h: &count, FetchedAt: now, Peers: []nknNetworkPeer{{Address: "a." + strings.Repeat("ab", 32)}, {Address: "b." + strings.Repeat("ab", 32)}}}
	if err := api.recordNKNObservations(&result); err != nil {
		t.Fatal(err)
	}
	if *result.Active24h != 1 || *result.ActiveMonth != 1 || *result.RegisteredTotal != 1 {
		t.Fatal("identity aliases counted twice")
	}
	result.FetchedAt = now.Add(31 * 24 * time.Hour)
	result.Peers = []nknNetworkPeer{{Address: "c." + strings.Repeat("cd", 32)}}
	if err := api.recordNKNObservations(&result); err != nil {
		t.Fatal(err)
	}
	if *result.ActiveMonth != 1 || *result.RegisteredTotal != 2 || !result.CoverageStartedAt.Equal(now) {
		t.Fatal("history lost or monthly window incorrect")
	}
}
