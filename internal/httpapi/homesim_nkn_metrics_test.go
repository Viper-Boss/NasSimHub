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

func newMetricsAPI(t *testing.T) *API {
	t.Helper()
	api, err := New(&fakeRepository{}, Options{
		disableAuthentication:  true,
		ContactBackupDirectory: filepath.Join(t.TempDir(), "backups", "contacts"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return api
}

func peerAt(seed string) nknNetworkPeer {
	return nknNetworkPeer{Address: "node." + strings.Repeat(seed, 32)}
}

// The daily figure has to mean the same thing as the monthly one over a shorter
// window. It used to be the size of the newest sample, so an identity seen two
// hours ago but absent from this poll was missing from "24h active".
func TestNKNMetricsDailyCountsTheFullDayNotTheLatestSample(t *testing.T) {
	api := newMetricsAPI(t)
	start := time.Now().UTC()

	first := nknNetworkPeersResponse{FetchedAt: start, Peers: []nknNetworkPeer{peerAt("ab"), peerAt("cd")}}
	if err := api.recordNKNObservations(&first); err != nil {
		t.Fatal(err)
	}
	if *first.Active24h != 2 {
		t.Fatalf("daily = %d; want 2", *first.Active24h)
	}

	// Two hours later only one of them is still subscribed.
	second := nknNetworkPeersResponse{FetchedAt: start.Add(2 * time.Hour), Peers: []nknNetworkPeer{peerAt("ab")}}
	if err := api.recordNKNObservations(&second); err != nil {
		t.Fatal(err)
	}
	if *second.Active24h != 2 {
		t.Fatalf("daily = %d; want 2 — both were seen inside the 24h window", *second.Active24h)
	}

	// A day later the one that stopped reporting drops out of the daily window
	// but is still inside the monthly one.
	third := nknNetworkPeersResponse{FetchedAt: start.Add(26 * time.Hour), Peers: []nknNetworkPeer{peerAt("ab")}}
	if err := api.recordNKNObservations(&third); err != nil {
		t.Fatal(err)
	}
	if *third.Active24h != 1 {
		t.Fatalf("daily = %d; want 1", *third.Active24h)
	}
	if *third.ActiveMonth != 2 {
		t.Fatalf("monthly = %d; want 2", *third.ActiveMonth)
	}
}

// A truncated peer page leaves Active24h nil upstream; that used to abort the
// whole recording, so a busy topic froze every counter.
func TestNKNMetricsRecordsTruncatedPages(t *testing.T) {
	api := newMetricsAPI(t)
	now := time.Now().UTC()

	result := nknNetworkPeersResponse{FetchedAt: now, Peers: []nknNetworkPeer{peerAt("ab")}}
	if err := api.recordNKNObservations(&result); err != nil {
		t.Fatal(err)
	}
	if result.Active24h == nil || *result.Active24h != 1 || *result.RegisteredTotal != 1 {
		t.Fatal("a truncated page must still be recorded and counted")
	}
	if result.StatisticsScope != "local_observed_identities" {
		t.Fatalf("scope = %q", result.StatisticsScope)
	}
}

// Identities that stop appearing eventually leave the history, so the file
// cannot grow until the hard cap freezes the statistics for good.
func TestNKNMetricsPrunesIdentitiesPastRetention(t *testing.T) {
	api := newMetricsAPI(t)
	start := time.Now().UTC()

	first := nknNetworkPeersResponse{FetchedAt: start, Peers: []nknNetworkPeer{peerAt("ab"), peerAt("cd")}}
	if err := api.recordNKNObservations(&first); err != nil {
		t.Fatal(err)
	}

	later := nknNetworkPeersResponse{
		FetchedAt: start.Add(nknObservationRetention + 24*time.Hour),
		Peers:     []nknNetworkPeer{peerAt("ab")},
	}
	if err := api.recordNKNObservations(&later); err != nil {
		t.Fatal(err)
	}
	if *later.RegisteredTotal != 1 {
		t.Fatalf("registered total = %d; want 1 after the stale identity aged out", *later.RegisteredTotal)
	}
}
