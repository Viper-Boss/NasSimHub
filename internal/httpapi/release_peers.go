package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	nkn "github.com/nknorg/nkn-sdk-go"

	"github.com/human-agent65535/modemdeck/internal/release"
	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

// Releases over the peer networks.
//
// The operator decided on 2026-09-25 that updates are to be decentralised, in
// two steps. This is the first: the SIGNED MANIFEST -- "which release is
// newest, and which image digests make it up" -- travels between gateways over
// NKN and the DHT, as two more sources beside the HTTPS mirrors. The images
// themselves still come from the registry; moving them is the second step.
//
// Nothing a peer says is trusted, and that is what makes it safe to ask
// strangers. A manifest is believed because it verifies against the key
// compiled into this build, and it then competes with every mirror's under
// the same "highest verified version wins" rule. A hostile peer can fail to
// answer, or answer with an older genuine release that loses; it cannot put
// anything the publisher did not sign in front of the updater.
//
// Every gateway shares by default (the operator's call): one that reached a
// mirror hands the manifest on to one that could not. Sharing is a toggle,
// and answers are rate-limited per asker and overall.

const (
	releaseQueryType  = "homesim.release.query.v1"
	releaseAnswerType = "homesim.release.manifest.v1"

	// releaseRoundInterval is how often the background round asks. A release
	// lands a few times a year; asking every two hours finds it the same day
	// at a cost of a dozen tiny messages.
	releaseRoundInterval = 2 * time.Hour
	releaseFirstRound    = 2 * time.Minute
	// releaseRoundWindow is how long a background round listens for answers.
	// An operator pressing "check again" waits less (releaseRefreshWindow):
	// a gateway that is online answers within a second or two, and the page
	// should not hang for the ones that are not.
	releaseRoundWindow   = 10 * time.Second
	releaseRefreshWindow = 4 * time.Second
	// releaseQueryPeers bounds how many gateways one round asks.
	releaseQueryPeers = 12
	// releaseDirectoryTTL is how long a fetched directory listing is reused.
	releaseDirectoryTTL = 30 * time.Minute
	// releaseShareTTL is how long the document on offer is reused before the
	// channel is asked again.
	releaseShareTTL = 10 * time.Minute
	// Answer limits: one asker at most once per releaseAnswerInterval, and
	// releaseAnswersPerMinute answers overall.
	releaseAnswerInterval   = 2 * time.Minute
	releaseAnswersPerMinute = 30
	// releaseMessageLimit refuses an NKN message too big to be a release
	// query or answer before it is decoded: a manifest at its size limit,
	// base64-encoded, plus the envelope.
	releaseMessageLimit = release.MaxManifestBytes*4/3 + 1024
	releaseSharingFile  = "release-sharing.json"
)

// ReleaseRelay is the release channel as the peer networks reach it: the
// updater's over HTTP when there is one (updaterclient), this process's own
// otherwise.
type ReleaseRelay interface {
	OfferRelease(ctx context.Context, kind string, signed []byte, peers int) error
	SignedRelease(ctx context.Context) ([]byte, error)
}

// dhtReleaseService is the part of the agent that shares releases over the
// DHT. Asserted, not required, so an agent client without it -- or a test
// double -- simply leaves the DHT out of the rounds.
type dhtReleaseService interface {
	SetDHTRelease(context.Context, []byte) error
	FetchDHTReleases(context.Context) ([][]byte, error)
}

type releaseMessage struct {
	Type         string `json:"type"`
	Nonce        string `json:"nonce"`
	SignedBase64 string `json:"signed_base64,omitempty"`
}

// releaseLink is this gateway's NKN identity for one round: its own address,
// and a way to send.
type releaseLink struct {
	self string
	send func(addresses []string, data []byte) error
}

type releaseAnswer struct {
	from   string
	signed []byte
}

type releaseSharing struct {
	Enabled  bool  `json:"enabled"`
	Revision int64 `json:"revision"`
}

type releasePeers struct {
	api *API

	// Injected so the rounds can be exercised without a network, and with a
	// key a test can sign with.
	publicKey string
	link      func() (releaseLink, error)
	directory func(self string) ([]string, error)
	now       func() time.Time

	roundMu sync.Mutex // one round at a time

	mu        sync.Mutex
	pending   map[string]chan releaseAnswer
	listing   []string
	listedAt  time.Time
	shared    []byte
	sharedAt  time.Time
	pushed    []byte // what the agent was last given
	answered  map[string]time.Time
	minute    time.Time
	answers   int
	settingMu sync.Mutex
}

func newReleasePeers(api *API) *releasePeers {
	peers := &releasePeers{
		api:       api,
		publicKey: release.SigningPublicKey,
		now:       time.Now,
		pending:   make(map[string]chan releaseAnswer),
		answered:  make(map[string]time.Time),
	}
	peers.link = peers.nknLink
	peers.directory = func(self string) ([]string, error) {
		listing, err := fetchNKNNetworkPeers(self)
		if err != nil {
			return nil, err
		}
		addresses := make([]string, 0, len(listing.Peers))
		for _, peer := range listing.Peers {
			if !peer.IsSelf {
				addresses = append(addresses, peer.Address)
			}
		}
		return addresses, nil
	}
	return peers
}

func (peers *releasePeers) nknLink() (releaseLink, error) {
	seed, err := peers.api.managedNKNSeed()
	if err != nil {
		return releaseLink{}, err
	}
	entry, err := peers.api.homeSIMNKNClient(seed)
	if err != nil {
		return releaseLink{}, err
	}
	return releaseLink{
		self: entry.address,
		send: func(addresses []string, data []byte) error {
			_, err := entry.client.Send(
				nkn.NewStringArray(addresses...),
				data,
				&nkn.MessageConfig{NoReply: true, MaxHoldingSeconds: 60},
			)
			return err
		},
	}, nil
}

// Start runs the background rounds until ctx ends.
func (peers *releasePeers) Start(ctx context.Context) {
	go func() {
		timer := time.NewTimer(releaseFirstRound)
		defer timer.Stop()
		shareTicker := time.NewTicker(releaseShareTTL)
		defer shareTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-shareTicker.C:
				// Keep what this gateway shares in step with what the
				// channel knows, including releases it found on a mirror.
				peers.refreshShared(ctx)
			case <-timer.C:
				peers.Round(ctx, releaseRoundWindow)
				timer.Reset(releaseRoundInterval)
			}
		}
	}()
}

// Round asks both peer networks, hands what they said to the channel, and
// refreshes what this gateway shares. window bounds how long NKN answers are
// awaited.
func (peers *releasePeers) Round(ctx context.Context, window time.Duration) {
	relay := peers.api.releaseRelay
	if relay == nil {
		return
	}
	// One round at a time, and a second caller does not queue: the round
	// already running will hand its answers to the channel in a moment, and
	// an operator's "check again" should not wait out a background round.
	if !peers.roundMu.TryLock() {
		return
	}
	defer peers.roundMu.Unlock()
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		peers.nknRound(ctx, relay, window)
	}()
	go func() {
		defer wait.Done()
		peers.dhtRound(ctx, relay)
	}()
	wait.Wait()
	peers.refreshShared(ctx)
}

func (peers *releasePeers) offer(ctx context.Context, relay ReleaseRelay, kind string, best []byte, count int) {
	offerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := relay.OfferRelease(offerCtx, kind, best, count); err != nil {
		peers.api.logger.Warn("release channel refused a peer offer", "component", "updates", "source", kind, "error", err)
	}
}

func (peers *releasePeers) nknRound(ctx context.Context, relay ReleaseRelay, window time.Duration) {
	// Connecting to NKN, and reading the directory, can each take far longer
	// than an operator waits on a button; both are cut off by ctx. Whatever
	// they eventually produce is simply not used by this round.
	type prepared struct {
		link    releaseLink
		targets []string
		err     error
	}
	ready := make(chan prepared, 1)
	go func() {
		link, err := peers.link()
		if err != nil {
			ready <- prepared{err: err}
			return
		}
		ready <- prepared{link: link, targets: peers.targets(link.self)}
	}()
	var setup prepared
	select {
	case setup = <-ready:
	case <-ctx.Done():
		return
	}
	if setup.err != nil {
		// NKN switched off, or no identity yet: this source has nothing to
		// report, which the card shows as "not taking part" rather than as a
		// network that was asked and stayed silent.
		return
	}
	link, targets := setup.link, setup.targets
	if len(targets) == 0 {
		peers.offer(ctx, relay, updatecheck.SourceKindNKN, nil, 0)
		return
	}
	nonce, err := randomNonce()
	if err != nil {
		return
	}
	answers := make(chan releaseAnswer, len(targets))
	peers.mu.Lock()
	peers.pending[nonce] = answers
	peers.mu.Unlock()
	defer func() {
		peers.mu.Lock()
		delete(peers.pending, nonce)
		peers.mu.Unlock()
	}()
	query, _ := json.Marshal(releaseMessage{Type: releaseQueryType, Nonce: nonce})
	if err := link.send(targets, query); err != nil {
		peers.offer(ctx, relay, updatecheck.SourceKindNKN, nil, 0)
		return
	}
	asked := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		asked[target] = struct{}{}
	}
	heard := make(map[string][]byte, len(targets))
	timer := time.NewTimer(window)
	defer timer.Stop()
collect:
	for len(heard) < len(targets) {
		select {
		case <-ctx.Done():
			break collect
		case <-timer.C:
			break collect
		case answer := <-answers:
			// Only gateways this round asked, and each once: an answer from
			// anyone else, or a second one, is not a second opinion.
			if _, ok := asked[answer.from]; !ok {
				continue
			}
			if _, seen := heard[answer.from]; seen {
				continue
			}
			heard[answer.from] = answer.signed
		}
	}
	documents := make([][]byte, 0, len(heard))
	for _, signed := range heard {
		documents = append(documents, signed)
	}
	best, count := bestRelease(documents, peers.publicKey)
	peers.offer(ctx, relay, updatecheck.SourceKindNKN, best, count)
}

func (peers *releasePeers) dhtRound(ctx context.Context, relay ReleaseRelay) {
	service, ok := peers.api.dhtService.(dhtReleaseService)
	if !ok {
		return
	}
	peers.api.dhtMu.Lock()
	settings, err := peers.api.loadDHTSettings()
	peers.api.dhtMu.Unlock()
	if err != nil || !settings.Enabled {
		return
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	documents, err := service.FetchDHTReleases(fetchCtx)
	if err != nil {
		peers.offer(ctx, relay, updatecheck.SourceKindDHT, nil, 0)
		return
	}
	best, count := bestRelease(documents, peers.publicKey)
	peers.offer(ctx, relay, updatecheck.SourceKindDHT, best, count)
}

// bestRelease picks the highest verified release among what peers sent, and
// counts how many sent that same version. Unverifiable documents are dropped
// here so they cannot even be counted; the channel verifies the winner again.
func bestRelease(documents [][]byte, publicKey string) ([]byte, int) {
	var (
		best     []byte
		manifest release.Manifest
		count    int
	)
	for _, signed := range documents {
		candidate, err := release.VerifyJSON(bytes.NewReader(signed), publicKey)
		if err != nil {
			continue
		}
		if best == nil {
			best, manifest, count = signed, candidate, 1
			continue
		}
		order, ok := updatecheck.CompareVersions(manifest.ReleaseVersion, candidate.ReleaseVersion)
		switch {
		case !ok:
			continue
		case order < 0, order == 0 && candidate.CreatedAt > manifest.CreatedAt:
			best, manifest, count = signed, candidate, 1
		case order == 0:
			count++
		}
	}
	return best, count
}

// targets is who one round asks: this gateway's paired peers first -- they
// are the ones most likely online and in reach -- then a random sample of the
// public directory of installations.
func (peers *releasePeers) targets(self string) []string {
	seen := map[string]struct{}{}
	if self != "" {
		seen[strings.ToLower(self)] = struct{}{}
	}
	var targets []string
	add := func(address string) {
		key := strings.ToLower(strings.TrimSpace(address))
		if key == "" || len(targets) >= releaseQueryPeers {
			return
		}
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		targets = append(targets, strings.TrimSpace(address))
	}
	peers.api.wireGuardMu.Lock()
	wireGuard, err := peers.api.loadWireGuardSettings()
	peers.api.wireGuardMu.Unlock()
	if err == nil {
		for _, peer := range wireGuard.Peers {
			add(peer.NKNAddress)
		}
	}
	for _, address := range shuffled(peers.directoryListing(self)) {
		add(address)
	}
	return targets
}

func (peers *releasePeers) directoryListing(self string) []string {
	peers.mu.Lock()
	if peers.listing != nil && peers.now().Sub(peers.listedAt) < releaseDirectoryTTL {
		listing := peers.listing
		peers.mu.Unlock()
		return listing
	}
	peers.mu.Unlock()
	listing, err := peers.directory(self)
	if err != nil {
		return nil
	}
	peers.mu.Lock()
	peers.listing, peers.listedAt = listing, peers.now()
	peers.mu.Unlock()
	return listing
}

// receiveReleaseMessage claims NKN release queries and answers.
func (api *API) receiveReleaseMessage(client *homeSIMNKNClient, message *nkn.Message) bool {
	if message == nil || len(message.Data) > releaseMessageLimit {
		return false
	}
	var decoded releaseMessage
	if json.Unmarshal(message.Data, &decoded) != nil {
		return false
	}
	switch decoded.Type {
	case releaseQueryType:
		api.releasePeers.answer(message.Src, decoded.Nonce, func(data []byte) error {
			_, err := client.client.Send(
				nkn.NewStringArray(message.Src),
				data,
				&nkn.MessageConfig{NoReply: true, MaxHoldingSeconds: 60},
			)
			return err
		})
		return true
	case releaseAnswerType:
		api.releasePeers.deliver(message.Src, decoded)
		return true
	}
	return false
}

// answer replies to one query with the document this gateway shares, if it
// shares, has one, and the asker is within its limits.
func (peers *releasePeers) answer(from, nonce string, reply func([]byte) error) {
	if from == "" || nonce == "" || len(nonce) > 64 || !peers.sharingEnabled() {
		return
	}
	if !peers.admit(from) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	signed := peers.sharedDocument(ctx)
	if len(signed) == 0 {
		return
	}
	data, err := json.Marshal(releaseMessage{
		Type:         releaseAnswerType,
		Nonce:        nonce,
		SignedBase64: base64.StdEncoding.EncodeToString(signed),
	})
	if err != nil {
		return
	}
	_ = reply(data)
}

// admit applies the answer limits: each asker once per interval, and a cap
// per minute overall.
func (peers *releasePeers) admit(from string) bool {
	peers.mu.Lock()
	defer peers.mu.Unlock()
	now := peers.now()
	key := strings.ToLower(from)
	if last, ok := peers.answered[key]; ok && now.Sub(last) < releaseAnswerInterval {
		return false
	}
	if now.Sub(peers.minute) >= time.Minute {
		peers.minute, peers.answers = now, 0
	}
	if peers.answers >= releaseAnswersPerMinute {
		return false
	}
	for other, at := range peers.answered {
		if now.Sub(at) >= releaseAnswerInterval {
			delete(peers.answered, other)
		}
	}
	peers.answered[key] = now
	peers.answers++
	return true
}

func (peers *releasePeers) deliver(from string, message releaseMessage) {
	signed, err := base64.StdEncoding.DecodeString(message.SignedBase64)
	if err != nil || len(signed) == 0 || len(signed) > release.MaxManifestBytes {
		return
	}
	peers.mu.Lock()
	answers, ok := peers.pending[message.Nonce]
	peers.mu.Unlock()
	if !ok {
		// Not a round this gateway is running: an unsolicited "answer" is
		// dropped, however well signed.
		return
	}
	select {
	case answers <- releaseAnswer{from: from, signed: signed}:
	default:
	}
}

// sharedDocument is the verified signed document on offer, re-read from the
// channel when the copy is older than releaseShareTTL.
func (peers *releasePeers) sharedDocument(ctx context.Context) []byte {
	peers.mu.Lock()
	if peers.shared != nil && peers.now().Sub(peers.sharedAt) < releaseShareTTL {
		shared := peers.shared
		peers.mu.Unlock()
		return shared
	}
	peers.mu.Unlock()
	peers.refreshShared(ctx)
	peers.mu.Lock()
	defer peers.mu.Unlock()
	return peers.shared
}

// refreshShared re-reads the document from the channel and hands it to the
// agent for the DHT -- or, with sharing off, takes it back from the agent.
func (peers *releasePeers) refreshShared(ctx context.Context) {
	relay := peers.api.releaseRelay
	if relay == nil {
		return
	}
	var signed []byte
	if peers.sharingEnabled() {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		document, err := relay.SignedRelease(readCtx)
		cancel()
		if err != nil {
			return
		}
		signed = document
	}
	peers.mu.Lock()
	peers.shared, peers.sharedAt = signed, peers.now()
	changed := !bytes.Equal(peers.pushed, signed)
	peers.mu.Unlock()
	if !changed {
		return
	}
	service, ok := peers.api.dhtService.(dhtReleaseService)
	if !ok {
		return
	}
	pushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := service.SetDHTRelease(pushCtx, signed); err == nil {
		peers.mu.Lock()
		peers.pushed = append([]byte(nil), signed...)
		peers.mu.Unlock()
	}
}

func (peers *releasePeers) sharingPath() (string, bool) {
	dir, ok := peers.api.homeSIMDataDirectory()
	if !ok {
		return "", false
	}
	return filepath.Join(dir, releaseSharingFile), true
}

// sharing is on unless the operator switched it off: the operator's call on
// 2026-09-25, because a network where nobody shares by default is a network
// where only the publisher's own gateway ever answers.
func (peers *releasePeers) loadSharing() (releaseSharing, error) {
	settings := releaseSharing{Enabled: true, Revision: 1}
	path, ok := peers.sharingPath()
	if !ok {
		return settings, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return releaseSharing{Enabled: true, Revision: 1}, fmt.Errorf("corrupt %s: %w", releaseSharingFile, err)
	}
	return settings, nil
}

func (peers *releasePeers) sharingEnabled() bool {
	peers.settingMu.Lock()
	defer peers.settingMu.Unlock()
	settings, err := peers.loadSharing()
	return err == nil && settings.Enabled
}

// releaseSharingHandler answers GET/PUT /api/v1/updates/sharing.
func (api *API) releaseSharingHandler(response http.ResponseWriter, request *http.Request) {
	peers := api.releasePeers
	switch request.Method {
	case http.MethodGet:
		peers.settingMu.Lock()
		settings, err := peers.loadSharing()
		peers.settingMu.Unlock()
		if err != nil {
			writeError(response, http.StatusServiceUnavailable, "release_sharing_unavailable", "Release sharing settings could not be read", "")
			return
		}
		writeJSON(response, http.StatusOK, settings)
	case http.MethodPut:
		if !api.requireAdmin(response, request) {
			return
		}
		var input struct {
			Enabled          bool  `json:"enabled"`
			ExpectedRevision int64 `json:"expected_revision"`
		}
		if !decodeJSONBody(response, request, &input) {
			return
		}
		peers.settingMu.Lock()
		settings, err := peers.loadSharing()
		if err == nil && input.ExpectedRevision != settings.Revision {
			peers.settingMu.Unlock()
			writeError(response, http.StatusConflict, "revision_conflict", "The setting changed elsewhere; reload and try again", "")
			return
		}
		next := releaseSharing{Enabled: input.Enabled, Revision: settings.Revision + 1}
		path, ok := peers.sharingPath()
		if !ok {
			peers.settingMu.Unlock()
			writeError(response, http.StatusServiceUnavailable, "release_sharing_unavailable", "Server storage is unavailable", "")
			return
		}
		if err := writeHomeSIMJSON(path, next); err != nil {
			peers.settingMu.Unlock()
			writeError(response, http.StatusServiceUnavailable, "release_sharing_unavailable", "The setting could not be saved", "")
			return
		}
		peers.settingMu.Unlock()
		// Take effect now: stop (or resume) offering on the DHT, and stop
		// answering over NKN from the next query.
		peers.refreshShared(context.WithoutCancel(request.Context()))
		writeJSON(response, http.StatusOK, next)
	default:
		response.Header().Set("Allow", "GET, PUT")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "Only GET and PUT are supported", "")
	}
}

func randomNonce() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// shuffled returns a random permutation, so every round asks a different
// sample of the directory rather than always the same first dozen.
func shuffled(values []string) []string {
	out := append([]string(nil), values...)
	for i := len(out) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return out
		}
		out[i], out[j.Int64()] = out[j.Int64()], out[i]
	}
	return out
}
