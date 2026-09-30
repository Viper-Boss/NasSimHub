package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	nkn "github.com/nknorg/nkn-sdk-go"

	"github.com/human-agent65535/modemdeck/internal/release"
	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

// Real signatures with a test key, as in the channel's own tests: a peer test
// that stubbed verification would be testing a gateway that believes strangers.
func releaseTestKey(t *testing.T, first byte) (ed25519.PrivateKey, string) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = first + byte(index)
	}
	private := ed25519.NewKeyFromSeed(seed)
	return private, base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey))
}

func signedRelease(t *testing.T, key ed25519.PrivateKey, version string) []byte {
	t.Helper()
	var images []release.Image
	for index, component := range release.Components() {
		images = append(images, release.Image{
			Name:       component,
			Repository: release.Repositories[component],
			Tag:        version,
			Digest:     "sha256:" + strings.Repeat(string("abcd"[index]), 64),
		})
	}
	signed, err := release.Sign(release.Manifest{
		SchemaVersion:  release.SchemaVersion,
		ReleaseVersion: version,
		CreatedAt:      1_790_000_000,
		Images:         images,
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	document, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

type relayOffer struct {
	kind   string
	signed []byte
	peers  int
}

type fakeRelay struct {
	mu     sync.Mutex
	offers []relayOffer
	signed []byte
	err    error
}

func (relay *fakeRelay) OfferRelease(_ context.Context, kind string, signed []byte, peers int) error {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	relay.offers = append(relay.offers, relayOffer{kind: kind, signed: signed, peers: peers})
	return nil
}

func (relay *fakeRelay) SignedRelease(context.Context) ([]byte, error) {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	return relay.signed, relay.err
}

func (relay *fakeRelay) offered(kind string) []relayOffer {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	var matched []relayOffer
	for _, offer := range relay.offers {
		if offer.kind == kind {
			matched = append(matched, offer)
		}
	}
	return matched
}

type releaseDHT struct {
	fakeDHTService
	mu        sync.Mutex
	documents [][]byte
	fetchErr  error
	set       [][]byte
}

func (dht *releaseDHT) SetDHTRelease(_ context.Context, signed []byte) error {
	dht.mu.Lock()
	defer dht.mu.Unlock()
	dht.set = append(dht.set, append([]byte(nil), signed...))
	return nil
}

func (dht *releaseDHT) FetchDHTReleases(context.Context) ([][]byte, error) {
	return dht.documents, dht.fetchErr
}

func releaseAPI(t *testing.T, relay ReleaseRelay, dht DHTService) *API {
	t.Helper()
	root := t.TempDir()
	api, err := New(&fakeRepository{}, Options{
		disableAuthentication:     true,
		disableLicenseEnforcement: true,
		ContactBackupDirectory:    filepath.Join(root, "backups", "contacts"),
		ReleaseRelay:              relay,
		DHT:                       dht,
	})
	if err != nil {
		t.Fatal(err)
	}
	// No real directory in a test.
	api.releasePeers.directory = func(string) ([]string, error) { return nil, nil }
	return api
}

// fakeLink records what a round sends and lets the test answer it.
type fakeLink struct {
	mu      sync.Mutex
	sent    [][]string
	queries []releaseMessage
	onSend  func(targets []string, query releaseMessage)
}

func (link *fakeLink) releaseLink() (releaseLink, error) {
	return releaseLink{
		self: "modemdeck.self",
		send: func(addresses []string, data []byte) error {
			var query releaseMessage
			if err := json.Unmarshal(data, &query); err != nil {
				return err
			}
			link.mu.Lock()
			link.sent = append(link.sent, append([]string(nil), addresses...))
			link.queries = append(link.queries, query)
			link.mu.Unlock()
			if link.onSend != nil {
				go link.onSend(addresses, query)
			}
			return nil
		},
	}, nil
}

func answerFrom(from, nonce string, signed []byte) releaseMessage {
	return releaseMessage{Type: releaseAnswerType, Nonce: nonce, SignedBase64: base64.StdEncoding.EncodeToString(signed)}
}

func TestAnNKNRoundHandsTheBestVerifiedReleaseToTheChannel(t *testing.T) {
	key, public := releaseTestKey(t, 3)
	forger, _ := releaseTestKey(t, 90)
	relay := &fakeRelay{}
	api := releaseAPI(t, relay, nil)
	peers := api.releasePeers
	peers.publicKey = public
	peers.directory = func(string) ([]string, error) {
		return []string{"modemdeck.a", "modemdeck.b", "modemdeck.c", "modemdeck.self", "modemdeck.d"}, nil
	}
	newer := signedRelease(t, key, "v1.3.0")
	link := &fakeLink{onSend: func(_ []string, query releaseMessage) {
		// Somebody nobody asked, first -- before the asked gateways have all
		// answered and the round stops listening.
		peers.deliver("modemdeck.stranger", answerFrom("modemdeck.stranger", query.Nonce, newer))
		peers.deliver("modemdeck.a", answerFrom("modemdeck.a", query.Nonce, newer))
		peers.deliver("modemdeck.b", answerFrom("modemdeck.b", query.Nonce, newer))
		// The same gateway again is not a third opinion.
		peers.deliver("modemdeck.b", answerFrom("modemdeck.b", query.Nonce, newer))
		peers.deliver("modemdeck.c", answerFrom("modemdeck.c", query.Nonce, signedRelease(t, key, "v1.2.0")))
		// A forgery claiming a higher version, from a gateway that was asked.
		peers.deliver("modemdeck.d", answerFrom("modemdeck.d", query.Nonce, signedRelease(t, forger, "v9.0.0")))
	}}
	peers.link = link.releaseLink

	peers.Round(context.Background(), 2*time.Second)

	offers := relay.offered(updatecheck.SourceKindNKN)
	if len(offers) != 1 {
		t.Fatalf("offers = %+v", relay.offers)
	}
	if !bytes.Equal(offers[0].signed, newer) || offers[0].peers != 2 {
		t.Fatalf("offered %d answers of %q, want the v1.3.0 document from 2 gateways",
			offers[0].peers, offers[0].signed)
	}
	link.mu.Lock()
	defer link.mu.Unlock()
	for _, target := range link.sent[0] {
		if target == "modemdeck.self" {
			t.Fatal("the round asked this gateway itself")
		}
	}
}

func TestPairedGatewaysAreAskedFirstAndTheSampleIsBounded(t *testing.T) {
	api := releaseAPI(t, &fakeRelay{}, nil)
	dir, err := api.wireGuardDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := wireGuardSettings{
		Revision:   1,
		Address:    "10.77.0.1/24",
		ListenPort: 51820,
		Peers: []wireGuardPeer{{
			Name:       "home",
			PublicKey:  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)),
			AllowedIPs: []string{"10.77.0.2/32"},
			NKNAddress: "modemdeck.paired",
		}},
	}
	if !validWGSettings(settings) {
		t.Fatal("the test's WireGuard settings are not valid ones")
	}
	raw, _ := json.Marshal(settings)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var listing []string
	for index := 0; index < 40; index++ {
		listing = append(listing, fmt.Sprintf("modemdeck.%02d", index))
	}
	listing = append(listing, "modemdeck.paired", "MODEMDECK.SELF")
	api.releasePeers.directory = func(string) ([]string, error) { return listing, nil }

	targets := api.releasePeers.targets("modemdeck.self")
	if len(targets) != releaseQueryPeers {
		t.Fatalf("asked %d gateways, want %d", len(targets), releaseQueryPeers)
	}
	if targets[0] != "modemdeck.paired" {
		t.Fatalf("the paired gateway is not asked first: %v", targets)
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if seen[strings.ToLower(target)] || strings.EqualFold(target, "modemdeck.self") {
			t.Fatalf("targets = %v", targets)
		}
		seen[strings.ToLower(target)] = true
	}
}

func TestASilentOrUnreachableNetworkIsReportedNotSkipped(t *testing.T) {
	relay := &fakeRelay{}
	api := releaseAPI(t, relay, nil)
	// Nobody to ask at all.
	api.releasePeers.link = (&fakeLink{}).releaseLink
	api.releasePeers.Round(context.Background(), 50*time.Millisecond)
	if offers := relay.offered(updatecheck.SourceKindNKN); len(offers) != 1 || offers[0].signed != nil {
		t.Fatalf("offers with nobody to ask = %+v", offers)
	}

	// Asked, nobody answered.
	api.releasePeers.directory = func(string) ([]string, error) { return []string{"modemdeck.a"}, nil }
	api.releasePeers.listing = nil
	api.releasePeers.Round(context.Background(), 50*time.Millisecond)
	if offers := relay.offered(updatecheck.SourceKindNKN); len(offers) != 2 || offers[1].signed != nil {
		t.Fatalf("offers after silence = %+v", offers)
	}

	// NKN switched off: nothing to report, so nothing is claimed.
	relay2 := &fakeRelay{}
	off := releaseAPI(t, relay2, nil)
	off.releasePeers.link = func() (releaseLink, error) { return releaseLink{}, errors.New("NKN 网络已关闭") }
	off.releasePeers.Round(context.Background(), 50*time.Millisecond)
	if offers := relay2.offered(updatecheck.SourceKindNKN); len(offers) != 0 {
		t.Fatalf("offers with NKN off = %+v", offers)
	}
}

func TestADHTRoundIsVerifiedAndCountedLikeNKN(t *testing.T) {
	key, public := releaseTestKey(t, 3)
	forger, _ := releaseTestKey(t, 90)
	relay := &fakeRelay{}
	newer := signedRelease(t, key, "v1.3.0")
	dht := &releaseDHT{documents: [][]byte{
		newer,
		signedRelease(t, key, "v1.2.0"),
		signedRelease(t, forger, "v9.9.9"),
		[]byte("not a manifest"),
		newer,
	}}
	api := releaseAPI(t, relay, dht)
	api.releasePeers.publicKey = public
	api.releasePeers.link = func() (releaseLink, error) { return releaseLink{}, errors.New("off") }
	api.releasePeers.Round(context.Background(), 50*time.Millisecond)
	offers := relay.offered(updatecheck.SourceKindDHT)
	if len(offers) != 1 || !bytes.Equal(offers[0].signed, newer) || offers[0].peers != 2 {
		t.Fatalf("dht offers = %+v", offers)
	}

	dht.fetchErr = errors.New("node is not running")
	api.releasePeers.Round(context.Background(), 50*time.Millisecond)
	if offers := relay.offered(updatecheck.SourceKindDHT); len(offers) != 2 || offers[1].signed != nil {
		t.Fatalf("dht offers after a failed fetch = %+v", offers)
	}
}

func TestAQueryIsAnsweredWithTheSharedDocumentWithinLimits(t *testing.T) {
	key, _ := releaseTestKey(t, 3)
	shared := signedRelease(t, key, "v1.3.0")
	relay := &fakeRelay{signed: shared}
	api := releaseAPI(t, relay, nil)
	peers := api.releasePeers
	clock := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	peers.now = func() time.Time { return clock }

	var replies []releaseMessage
	reply := func(data []byte) error {
		var message releaseMessage
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatal(err)
		}
		replies = append(replies, message)
		return nil
	}
	peers.answer("modemdeck.asker", "n1", reply)
	if len(replies) != 1 || replies[0].Type != releaseAnswerType || replies[0].Nonce != "n1" {
		t.Fatalf("replies = %+v", replies)
	}
	if decoded, _ := base64.StdEncoding.DecodeString(replies[0].SignedBase64); !bytes.Equal(decoded, shared) {
		t.Fatal("the answer is not the shared document byte for byte")
	}
	// The same asker again, too soon.
	peers.answer("modemdeck.asker", "n2", reply)
	if len(replies) != 1 {
		t.Fatal("one asker was answered twice inside the interval")
	}
	clock = clock.Add(releaseAnswerInterval)
	peers.answer("modemdeck.asker", "n3", reply)
	if len(replies) != 2 {
		t.Fatal("the asker was not answered after the interval")
	}
	// Overall cap per minute.
	clock = clock.Add(time.Minute)
	for index := 0; index < releaseAnswersPerMinute+10; index++ {
		peers.answer(fmt.Sprintf("modemdeck.many%02d", index), "n", reply)
	}
	if got := len(replies) - 2; got != releaseAnswersPerMinute {
		t.Fatalf("answered %d gateways in one minute, cap is %d", got, releaseAnswersPerMinute)
	}
}

func TestNothingIsSharedWhenSharingIsOffOrThereIsNothingVerified(t *testing.T) {
	key, _ := releaseTestKey(t, 3)
	relay := &fakeRelay{signed: signedRelease(t, key, "v1.3.0")}
	dht := &releaseDHT{}
	api := releaseAPI(t, relay, dht)
	peers := api.releasePeers
	answered := 0
	reply := func([]byte) error { answered++; return nil }

	// On by default: the document goes to the agent for the DHT, once.
	peers.refreshShared(context.Background())
	peers.refreshShared(context.Background())
	if len(dht.set) != 1 || len(dht.set[0]) == 0 {
		t.Fatalf("agent was given %d documents", len(dht.set))
	}

	// Switched off through the API.
	put := func(enabled bool, revision int64) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"enabled":%t,"expected_revision":%d}`, enabled, revision)
		request := httptest.NewRequest(http.MethodPut, "/api/v1/updates/sharing", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		api.ServeHTTP(recorder, request)
		return recorder
	}
	if recorder := put(false, 1); recorder.Code != http.StatusOK {
		t.Fatalf("PUT sharing answered %d: %s", recorder.Code, recorder.Body.String())
	}
	if recorder := put(true, 1); recorder.Code != http.StatusConflict {
		t.Fatalf("a stale revision answered %d", recorder.Code)
	}
	// The agent stops offering at once, and NKN queries go unanswered.
	if last := dht.set[len(dht.set)-1]; len(last) != 0 {
		t.Fatal("the agent still offers the document with sharing off")
	}
	peers.answer("modemdeck.asker", "n", reply)
	if answered != 0 {
		t.Fatal("a query was answered with sharing off")
	}

	// On again, but nothing verified to share: silence, not an empty answer.
	if recorder := put(true, 2); recorder.Code != http.StatusOK {
		t.Fatalf("PUT sharing answered %d", recorder.Code)
	}
	relay.mu.Lock()
	relay.signed = nil
	relay.mu.Unlock()
	peers.mu.Lock()
	peers.shared = nil
	peers.sharedAt = time.Time{}
	peers.mu.Unlock()
	peers.answer("modemdeck.other", "n", reply)
	if answered != 0 {
		t.Fatal("a query was answered with nothing verified to share")
	}

	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/updates/sharing", nil))
	if !strings.Contains(recorder.Body.String(), `"enabled":true`) {
		t.Fatalf("GET sharing = %s", recorder.Body.String())
	}
}

func TestTheSwitchAloneStopsAnswers(t *testing.T) {
	// The document is still in hand -- read a minute ago -- when the switch
	// goes off. The switch has to be enough on its own.
	key, _ := releaseTestKey(t, 3)
	shared := signedRelease(t, key, "v1.3.0")
	api := releaseAPI(t, &fakeRelay{signed: shared}, nil)
	peers := api.releasePeers
	peers.mu.Lock()
	peers.shared, peers.sharedAt = shared, time.Now()
	peers.mu.Unlock()
	path, ok := peers.sharingPath()
	if !ok {
		t.Fatal("no storage")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeHomeSIMJSON(path, releaseSharing{Enabled: false, Revision: 2}); err != nil {
		t.Fatal(err)
	}
	answered := 0
	peers.answer("modemdeck.asker", "n", func([]byte) error { answered++; return nil })
	if answered != 0 {
		t.Fatal("a query was answered with sharing switched off")
	}
}

func TestSharingIsOnByDefault(t *testing.T) {
	api := releaseAPI(t, &fakeRelay{}, nil)
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/updates/sharing", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"enabled":true`) {
		t.Fatalf("GET sharing = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAnUnsolicitedAnswerIsDropped(t *testing.T) {
	key, _ := releaseTestKey(t, 3)
	relay := &fakeRelay{}
	api := releaseAPI(t, relay, nil)
	api.releasePeers.deliver("modemdeck.x", answerFrom("modemdeck.x", "no-such-round", signedRelease(t, key, "v1.3.0")))
	if len(relay.offers) != 0 {
		t.Fatal("an answer to no round reached the channel")
	}
}

func TestOnlyReleaseMessagesAreClaimed(t *testing.T) {
	api := releaseAPI(t, &fakeRelay{}, nil)
	claims := func(data []byte) bool {
		return api.receiveReleaseMessage(nil, &nkn.Message{Src: "modemdeck.x", Data: data})
	}
	if claims([]byte(`{"type":"homesim.chat.v1","nonce":"n"}`)) {
		t.Fatal("a chat message was claimed")
	}
	if claims([]byte("not json")) {
		t.Fatal("junk was claimed")
	}
	huge := append([]byte(`{"type":"`+releaseAnswerType+`","nonce":"n","signed_base64":"`), bytes.Repeat([]byte("A"), releaseMessageLimit)...)
	huge = append(huge, []byte(`"}`)...)
	if claims(huge) {
		t.Fatal("a message bigger than any manifest was decoded")
	}
	if !claims([]byte(`{"type":"` + releaseAnswerType + `","nonce":"n","signed_base64":""}`)) {
		t.Fatal("a release answer was not claimed")
	}
}

func TestManualRecheckAsksThePeersFirst(t *testing.T) {
	key, public := releaseTestKey(t, 3)
	relay := &fakeRelay{}
	api := releaseAPI(t, relay, nil)
	api.updateChecker = fakeUpdateCheckerFunc(func(context.Context) updatecheck.Result {
		// By the time the channel is asked, the peers' answer has reached it.
		if len(relay.offered(updatecheck.SourceKindNKN)) == 0 {
			t.Error("the channel was asked before the peers")
		}
		return updatecheck.Result{Status: updatecheck.StatusUpToDate}
	})
	peers := api.releasePeers
	peers.publicKey = public
	peers.directory = func(string) ([]string, error) { return []string{"modemdeck.a"}, nil }
	newer := signedRelease(t, key, "v1.3.0")
	peers.link = (&fakeLink{onSend: func(_ []string, query releaseMessage) {
		peers.deliver("modemdeck.a", answerFrom("modemdeck.a", query.Nonce, newer))
	}}).releaseLink

	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/updates/check?refresh=1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("check answered %d", recorder.Code)
	}
}

type fakeUpdateCheckerFunc func(context.Context) updatecheck.Result

func (check fakeUpdateCheckerFunc) Check(ctx context.Context) updatecheck.Result { return check(ctx) }

func TestTheDirectoryListsTheAddressThatAnswers(t *testing.T) {
	// The directory used to subscribe with HOMESIM_NKN_SEED only, so on every
	// install without it (all but one) it failed to join, and the address it
	// listed was not the one messages reach.
	t.Setenv("HOMESIM_NKN_SEED", "")
	seed := bytes.Repeat([]byte{7}, 32)
	manager := newNKNNetworkManager(nil)
	manager.seed = func() ([]byte, error) { return seed, nil }
	account, err := nkn.NewAccount(seed)
	if err != nil {
		t.Fatal(err)
	}
	want := nknNetworkIdentifier + "." + fmt.Sprintf("%x", account.PubKey())
	if got := manager.selfAddress(); got != want {
		t.Fatalf("directory address = %q, want the control client's %q", got, want)
	}
	api := releaseAPI(t, &fakeRelay{}, nil)
	if api.nknNetwork.seed == nil {
		t.Fatal("the API does not give the directory its identity")
	}
}

func TestAHungNKNConnectionDoesNotHoldTheRoundPastItsBudget(t *testing.T) {
	relay := &fakeRelay{}
	api := releaseAPI(t, relay, nil)
	release := make(chan struct{})
	defer close(release)
	api.releasePeers.link = func() (releaseLink, error) {
		<-release // connecting, for as long as NKN takes
		return releaseLink{}, errors.New("late")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	api.releasePeers.Round(ctx, time.Minute)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("the round waited %s on a connection that never came", elapsed)
	}
	if offers := relay.offered(updatecheck.SourceKindNKN); len(offers) != 0 {
		t.Fatalf("a round that never asked reported %+v", offers)
	}
}

func TestASecondRoundDoesNotQueueBehindTheFirst(t *testing.T) {
	api := releaseAPI(t, &fakeRelay{}, nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	api.releasePeers.link = func() (releaseLink, error) {
		close(entered)
		<-release
		return releaseLink{}, errors.New("off")
	}
	done := make(chan struct{})
	go func() {
		api.releasePeers.Round(context.Background(), time.Second)
		close(done)
	}()
	<-entered
	defer func() {
		close(release)
		<-done
	}()
	second := make(chan struct{})
	go func() {
		api.releasePeers.Round(context.Background(), time.Second)
		close(second)
	}()
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("the second round is waiting for the first")
	}
}
