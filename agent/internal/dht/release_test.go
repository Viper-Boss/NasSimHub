package dht

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// Two real hosts on loopback: the release protocol is a network protocol, and
// a test that faked the stream would not catch it being pruned at startup.
func connectedPair(t *testing.T) (*Service, *Service) {
	t.Helper()
	first := New(t.TempDir())
	second := New(t.TempDir())
	t.Cleanup(func() {
		first.Apply(context.Background(), Config{})
		second.Apply(context.Background(), Config{})
	})
	for _, service := range []*Service{first, second} {
		if status := service.Apply(context.Background(), enabled()); status.State == StateError {
			t.Fatalf("host did not start: %s", status.Error)
		}
	}
	second.mu.Lock()
	target := peer.AddrInfo{ID: second.host.ID(), Addrs: second.host.Addrs()}
	second.mu.Unlock()
	var loopback []string
	for _, address := range target.Addrs {
		if strings.Contains(address.String(), "/127.0.0.1/") {
			loopback = append(loopback, address.String()+"/p2p/"+target.ID.String())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if dialled, err := first.Dial(ctx, loopback); err != nil || dialled == 0 {
		t.Fatalf("dial: %d, %v", dialled, err)
	}
	return first, second
}

func TestAPeerHandsOverTheReleaseItShares(t *testing.T) {
	asking, sharing := connectedPair(t)
	document := []byte(`{"manifest":{"release_version":"v1.3.0"},"signature":"x"}`)
	if err := sharing.SetRelease(document); err != nil {
		t.Fatal(err)
	}
	releases, err := asking.FetchReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || !bytes.Equal(releases[0].Signed, document) {
		t.Fatalf("releases = %+v", releases)
	}
	sharing.mu.Lock()
	id := sharing.host.ID().String()
	sharing.mu.Unlock()
	if releases[0].Peer != id {
		t.Fatalf("answer attributed to %q, want %q", releases[0].Peer, id)
	}

	// Asked again at once: the same peer is not served twice inside the
	// interval, which reads as no answer.
	again, err := asking.FetchReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("served twice inside the interval: %+v", again)
	}
}

func TestAPeerSharingNothingGivesNothing(t *testing.T) {
	asking, _ := connectedPair(t)
	releases, err := asking.FetchReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Fatalf("a peer with nothing to share answered: %+v", releases)
	}
}

func TestOnlyAManifestSizedDocumentIsShared(t *testing.T) {
	service := New(t.TempDir())
	if err := service.SetRelease(bytes.Repeat([]byte("x"), maxReleaseBytes+1)); err != ErrReleaseTooLarge {
		t.Fatalf("SetRelease(oversized) = %v", err)
	}
	if err := service.SetRelease(nil); err != nil {
		t.Fatalf("clearing = %v", err)
	}
}

func TestTheReleaseProtocolSurvivesThePrune(t *testing.T) {
	service := New(t.TempDir())
	t.Cleanup(func() { service.Apply(context.Background(), Config{}) })
	if status := service.Apply(context.Background(), enabled()); status.State == StateError {
		t.Fatalf("host did not start: %s", status.Error)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	for _, id := range service.host.Mux().Protocols() {
		if string(id) == ReleaseProtocol {
			return
		}
	}
	t.Fatalf("%s is not offered", ReleaseProtocol)
}
