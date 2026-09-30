package ota

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/updatecheck"
	"github.com/human-agent65535/modemdeck/internal/updaterclient"
)

type recordingRelay struct {
	mu     sync.Mutex
	kind   string
	signed []byte
	peers  int
	shared []byte
}

func (relay *recordingRelay) Offer(kind string, signed []byte, peers int) (updatecheck.Source, error) {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	relay.kind, relay.signed, relay.peers = kind, signed, peers
	return updatecheck.Source{Name: kind, Kind: kind, State: updatecheck.SourceVerified}, nil
}

func (relay *recordingRelay) Signed() []byte {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	return relay.shared
}

func relayController(t *testing.T) *Controller {
	t.Helper()
	controller, err := New(Options{
		Checker:    fakeChecker{result: updatecheck.Result{Status: updatecheck.StatusUpToDate}},
		Manifests:  fakeManifestLoader{},
		Runtime:    &fakeRuntime{},
		Operations: &memoryOperationStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return controller
}

// The api's side of this boundary is updaterclient; testing the two together
// is what shows a document survives the trip.
func relayServer(t *testing.T, relay ReleaseRelay) *updaterclient.Client {
	t.Helper()
	handler, err := NewHandler(relayController(t), "token")
	if err != nil {
		t.Fatal(err)
	}
	if relay != nil {
		handler.WithRelease(relay)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := updaterclient.New(updaterclient.Options{BaseURL: server.URL, Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// The document crosses the api -> updater boundary as the exact bytes a peer
// sent, whitespace and all: the updater verifies what the peer signed, not a
// re-encoding of it.
func TestAPeerOfferReachesTheUpdaterByteForByte(t *testing.T) {
	relay := &recordingRelay{}
	client := relayServer(t, relay)
	document := []byte("{\n  \"manifest\": {},\n  \"signature\": \"x\"\n}\n")
	if err := client.OfferRelease(context.Background(), updatecheck.SourceKindDHT, document, 4); err != nil {
		t.Fatal(err)
	}
	if relay.kind != updatecheck.SourceKindDHT || relay.peers != 4 || !bytes.Equal(relay.signed, document) {
		t.Fatalf("relay got kind %q, peers %d, %q", relay.kind, relay.peers, relay.signed)
	}
	// A silent network is offered as nothing, not as an empty document.
	if err := client.OfferRelease(context.Background(), updatecheck.SourceKindNKN, nil, 0); err != nil {
		t.Fatal(err)
	}
	if relay.signed != nil {
		t.Fatalf("a silent network arrived as %q", relay.signed)
	}
}

func TestTheSharedDocumentComesBackAsSigned(t *testing.T) {
	relay := &recordingRelay{}
	client := relayServer(t, relay)
	// Nothing verified yet: no document, and no error either.
	signed, err := client.SignedRelease(context.Background())
	if err != nil || signed != nil {
		t.Fatalf("SignedRelease with nothing = %q, %v", signed, err)
	}
	relay.shared = []byte(`{"manifest":{},"signature":"y"}`)
	signed, err = client.SignedRelease(context.Background())
	if err != nil || !bytes.Equal(signed, relay.shared) {
		t.Fatalf("SignedRelease = %q, %v", signed, err)
	}
}

func TestAnOlderUpdaterWithoutTheRoutesIsAnError(t *testing.T) {
	client := relayServer(t, nil)
	if err := client.OfferRelease(context.Background(), updatecheck.SourceKindNKN, []byte("{}"), 1); err == nil {
		t.Fatal("an updater that cannot take offers accepted one")
	}
	if _, err := client.SignedRelease(context.Background()); err == nil {
		t.Fatal("an updater that cannot share reported nothing to share")
	}
}

func TestTheRelayRoutesNeedTheToken(t *testing.T) {
	handler, _ := NewHandler(relayController(t), "token")
	handler.WithRelease(&recordingRelay{shared: []byte("{}")})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/release/signed", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated read answered %d", recorder.Code)
	}
}
