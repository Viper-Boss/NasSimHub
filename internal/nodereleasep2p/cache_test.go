package nodereleasep2p

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/human-agent65535/nassimhub-node/proto"
)

// twoReleases sets up two seeders holding releases for two different
// platforms, both of which the fetcher accepts -- the only way two downloads
// can be wanted at once, since for one platform only the newest is fetched.
func twoReleases(t *testing.T, options ...gatewayOption) (*network, *clock, *gateway, *scriptedSeeder, *scriptedSeeder, []byte, []byte) {
	t.Helper()
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	first, second := artifactBytes(7, testArtifact), artifactBytes(8, testArtifact)
	options = append([]gatewayOption{withOptions(func(o *Options) {
		o.AllowedTargets = []Target{{Platform: string(proto.PlatformMock)}}
	})}, options...)
	fetcher := newGateway(t, net, shared, "fetcher", signer, options...)
	stick := newScriptedSeeder(t, net, "stick", "r-1.1.0", signer.release("r-1.1.0", "1.1.0", first), first)
	mock := newScriptedSeeder(t, net, "mock", "m-2.0.0", signer.release("m-2.0.0", "2.0.0", second,
		func(m *proto.OTAManifest) { m.Platform = proto.PlatformMock }), second)
	return net, shared, fetcher, stick, mock, first, second
}

func holdChunks(net *network) {
	net.filter = func(_ packet, message Message) bool { return message.Type != TypeChunk }
}

func TestASecondDownloadIsRefusedCleanlyWhenTheCacheHasNoRoom(t *testing.T) {
	net, shared, fetcher, stick, mock, first, second := twoReleases(t, withOptions(func(o *Options) {
		o.MaxDownloads = 2
		o.CacheCapBytes = testArtifact + testArtifact/2 // room for one and a half
	}))
	holdChunks(net)
	offerOnce(net, fetcher, stick)
	offerOnce(net, fetcher, mock)

	status := fetcher.status()
	if status.Refused.CacheFull != 1 || status.Refused.Busy != 0 {
		t.Fatalf("the second download should be refused as cache full: %+v", status.Refused)
	}
	if status.CacheBytes != testArtifact || status.CacheBytes > status.CacheCapBytes || len(status.Downloads) != 1 {
		t.Fatalf("the cache must hold only the first download: %+v", status)
	}
	if entries := fetcher.cacheEntries(); len(entries) != 1 || entries[0] != "r-1.1.0" {
		t.Fatalf("a refusal for lack of room must not leave a partial download: %v", entries)
	}
	if mock.chunks.Load() != 0 || status.Peers.BackedOffTotal != 0 {
		t.Fatalf("no payload may be asked for, and the peer is not to blame for this gateway's cache: %+v", status)
	}

	// The first finishes; then there is room and the second is fetched.
	net.filter = nil
	drive(net, shared, 31*time.Second, 3, fetcher)
	requireImported(t, fetcher, "r-1.1.0", first)
	offerOnce(net, fetcher, mock)
	if got := fetcher.stored("m-2.0.0"); string(got) != string(second) {
		t.Fatal("once there was room the second release should have been fetched")
	}
}

func TestDownloadsAreLimitedInNumberAsWellAsInBytes(t *testing.T) {
	net, _, fetcher, stick, mock, _, _ := twoReleases(t) // default: one at a time, 96 MiB
	holdChunks(net)
	offerOnce(net, fetcher, stick)
	offerOnce(net, fetcher, mock)
	status := fetcher.status()
	if status.Refused.Busy != 1 || status.Refused.CacheFull != 0 || len(status.Downloads) != 1 {
		t.Fatalf("with one download allowed the second should be refused as busy: %+v", status)
	}
	if status.MaxDownloads != DefaultMaxDownloads || status.CacheCapBytes != DefaultCacheCapBytes {
		t.Fatalf("defaults are not what is documented: %+v", status)
	}

	// Two allowed and room for both: both run.
	net, _, fetcher, stick, mock, _, _ = twoReleases(t, withOptions(func(o *Options) { o.MaxDownloads = 2 }))
	holdChunks(net)
	offerOnce(net, fetcher, stick)
	offerOnce(net, fetcher, mock)
	if status := fetcher.status(); len(status.Downloads) != 2 || status.CacheBytes != 2*testArtifact {
		t.Fatalf("two downloads should be running: %+v", status)
	}
}

func TestAnArtifactLargerThanTheWholeCacheIsRefused(t *testing.T) {
	net, _, _, seeder, fetcher, _, _ := pairOfGateways(t, withOptions(func(o *Options) { o.CacheCapBytes = testArtifact - 1 }))
	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)
	requireRefusedBeforeAnyPayload(t, net, fetcher, func(r Refused) int64 { return r.CacheFull }, 1)
	requireNothingStored(t, fetcher)
}

func TestACacheOfZeroDisablesDownloadingAndSaysSo(t *testing.T) {
	net, _, _, seeder, fetcher, raw, artifact := pairOfGateways(t, withOptions(func(o *Options) { o.CacheCapBytes = 0 }))
	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)
	status := fetcher.status()
	if status.Enabled || status.Reason != DisabledCache || status.CacheCapBytes != 0 {
		t.Fatalf("a cache of zero should disable downloading and say why: %+v", status)
	}
	if sent := net.sentOfType(fetcher.address, ""); sent != 0 {
		t.Fatalf("a gateway that does not download must not ask anybody, sent %d", sent)
	}
	requireNothingStored(t, fetcher)
	// It can still hand out what an administrator uploaded.
	fetcher.hold(raw, artifact)
	if !fetcher.status().Seeding {
		t.Fatal("a cache of zero disables downloading, not seeding")
	}
	other := newGateway(t, net, fetcher.clock, "other", newPublisher(t, 1))
	other.engine.Ask([]string{fetcher.address})
	settle(net, fetcher, other)
	requireImported(t, other, "r-1.1.0", artifact)
}

func TestWithoutNodeReleaseKeysNothingIsFetchedOrServed(t *testing.T) {
	net, shared, _, seeder, fetcher, raw, artifact := pairOfGateways(t, withoutKeys())
	status := fetcher.status()
	if status.Enabled || status.Reason != DisabledNoReleaseKeys || status.Seeding || status.SeedingReason != DisabledNoReleaseKeys {
		t.Fatalf("without release keys the exchange must be off, both ways, and say why: %+v", status)
	}
	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)
	if sent := net.sentOfType(fetcher.address, ""); sent != 0 {
		t.Fatalf("an unverifiable manifest must never cause a download; %d messages were sent", sent)
	}

	// Even an offer it is handed is not looked at.
	liar := newScriptedSeeder(t, net, "liar", "r-1.1.0", raw, artifact)
	nonce := "0123456789abcdef"
	fetcher.engine.mu.Lock()
	fetcher.engine.queries[nonce] = &query{peer: liar.address, expires: shared.Now().Add(time.Minute), remaining: 2}
	fetcher.engine.mu.Unlock()
	data, _ := json.Marshal(liar.offer(nonce))
	fetcher.engine.Handle(liar.address, data)
	settle(net, fetcher)
	if status := fetcher.status(); len(status.Downloads) != 0 || net.sentOfType(fetcher.address, "") != 0 {
		t.Fatalf("an offer reached a gateway with no keys and something happened: %+v", status)
	}
	requireNothingStored(t, fetcher)

	// A release an administrator uploaded there is carried unverified, and
	// so is not handed to other gateways.
	fetcher.hold(raw, artifact)
	probe := newProbe(net, "probe")
	probe.send(fetcher.address, Message{Type: TypeQuery, Nonce: "n1"})
	net.pump()
	if len(probe.received) != 0 || fetcher.status().Served.Queries != 0 {
		t.Fatalf("a gateway that verified nothing must serve nothing, answered %+v", probe.received)
	}

	// A download left by an earlier configuration is not kept around.
	stale := filepath.Join(fetcher.cacheDir, "r-0.9.0")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, partFileName), make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	fetcher.restart()
	if entries := fetcher.cacheEntries(); len(entries) != 0 {
		t.Fatalf("a cache that cannot be used must not keep %v", entries)
	}
}

func TestADownloadThatStopsProgressingIsAbandonedAndItsCacheFreed(t *testing.T) {
	net, shared, _, seeder, fetcher, _, _ := pairOfGateways(t)
	delivered := 0
	net.filter = func(_ packet, message Message) bool {
		if message.Type == TypeChunk {
			delivered++
			if delivered == 4 {
				net.down[seeder.address] = true
			}
		}
		return true
	}
	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)
	if status := fetcher.status(); status.CacheBytes != testArtifact {
		t.Fatalf("expected a download holding its reservation: %+v", status)
	}

	drive(net, shared, time.Hour, 23, seeder, fetcher)
	if entry, ok := fetcher.download("r-1.1.0"); !ok || entry.State != stateWaiting {
		t.Fatalf("after 23 hours it should still be waiting: %+v", entry)
	}
	drive(net, shared, time.Hour, 2, seeder, fetcher)
	status := fetcher.status()
	if status.CacheBytes != 0 || len(fetcher.cacheEntries()) != 0 || status.Transfers.Abandoned != 1 {
		t.Fatalf("after a day without progress the download should be gone: %+v, cache %v", status, fetcher.cacheEntries())
	}
	if len(status.Downloads) != 1 || status.Downloads[0].State != stateAbandoned {
		t.Fatalf("what became of it should be reported: %+v", status.Downloads)
	}
	requireNothingStored(t, fetcher)
}

func TestAStalledDownloadMakesWayForAnotherButOneBeingImportedNeverDoes(t *testing.T) {
	t.Run("a stalled download is evicted for a new one", func(t *testing.T) {
		net, shared, fetcher, stick, mock, _, second := twoReleases(t)
		holdChunks(net)
		offerOnce(net, fetcher, stick)

		// Not yet stalled: the newcomer is refused.
		shared.advance(29 * time.Minute)
		offerOnce(net, fetcher, mock)
		if status := fetcher.status(); status.Refused.Busy != 1 || status.Transfers.Evicted != 0 {
			t.Fatalf("a download that is only 29 minutes quiet is not stalled: %+v", status)
		}
		// Stalled: it is evicted and the newcomer takes its place.
		shared.advance(2 * time.Minute)
		net.filter = nil
		offerOnce(net, fetcher, mock)
		status := fetcher.status()
		if status.Transfers.Evicted != 1 || string(fetcher.stored("m-2.0.0")) != string(second) {
			t.Fatalf("the stalled download should have made way: %+v", status)
		}
		if _, err := os.Stat(filepath.Join(fetcher.cacheDir, "r-1.1.0")); !os.IsNotExist(err) {
			t.Fatal("the evicted download's files are still in the cache")
		}
	})

	t.Run("a download being imported is not evicted", func(t *testing.T) {
		net, shared, fetcher, stick, mock, first, _ := twoReleases(t)
		entered, release := make(chan struct{}), make(chan struct{})
		fetcher.engine.hooks.beforeImport = func() error {
			close(entered)
			<-release
			return nil
		}
		fetcher.engine.Ask([]string{stick.address})
		net.pump() // every chunk arrives; the download is ready to verify
		done := make(chan struct{})
		go func() {
			defer close(done)
			fetcher.engine.Tick() // verifies, then blocks in the hook, mid-import
		}()
		<-entered

		// However long ago its last chunk was, it is not a candidate.
		shared.advance(48 * time.Hour)
		offerOnce(net, fetcher, mock)
		status := fetcher.status()
		if status.Refused.Busy != 1 || status.Transfers.Evicted != 0 || status.Transfers.Abandoned != 0 {
			t.Fatalf("a download being imported must be left alone: %+v", status)
		}
		if entry, _ := fetcher.download("r-1.1.0"); entry.State != stateVerifying && entry.State != stateImporting {
			t.Fatalf("the download should be reported as being verified or imported: %+v", entry)
		}
		close(release)
		<-done
		requireImported(t, fetcher, "r-1.1.0", first)
	})
}

func TestAFloodOfOffersIsBounded(t *testing.T) {
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	artifact := artifactBytes(7, testArtifact)
	fetcher := newGateway(t, net, shared, "fetcher", signer, withOptions(func(o *Options) { o.MaxOffers = 4 }))
	forger := newPublisher(t, 99)

	// 1. Offers nobody asked for are dropped unread: no verification, no
	//    refusal counted, nothing tracked.
	for index := 0; index < 500; index++ {
		id := fmt.Sprintf("r-1.1.%d", index)
		liar := &scriptedSeeder{t: t, net: net, address: "flood", releaseID: id,
			manifest: forger.release(id, "1.1.0", artifact), tableOf: artifact, serve: artifact, chunkSize: testChunk}
		data, _ := json.Marshal(liar.offer(fmt.Sprintf("guess-%d", index)))
		fetcher.engine.Handle("flood", data)
	}
	status := fetcher.status()
	if status.Offers.Unsolicited != 500 || status.Offers.Received != 0 || status.Refused != (Refused{}) || status.Offers.Tracked != 0 {
		t.Fatalf("unsolicited offers must be dropped unread: %+v", status)
	}

	// 2. A peer that was asked may answer with two offers; the rest of what
	//    it sends is unsolicited too.
	chatty := endpointFunc(func(from string, data []byte) bool {
		message, _ := decodeMessage(data)
		if message.Type != TypeQuery {
			return true
		}
		for index := 0; index < 10; index++ {
			id := fmt.Sprintf("c-1.1.%d", index)
			liar := &scriptedSeeder{t: t, net: net, address: "chatty", releaseID: id,
				manifest: forger.release(id, "1.1.0", artifact), tableOf: artifact, serve: artifact, chunkSize: testChunk}
			reply, _ := json.Marshal(liar.offer(message.Nonce))
			_ = net.senderFor("chatty")(from, reply)
		}
		return true
	})
	net.attach("chatty", chatty)
	fetcher.engine.Ask([]string{"chatty"})
	settle(net, fetcher)
	status = fetcher.status()
	if status.Offers.Received != 1 || status.Refused.Signature != 1 {
		// The first forgery rests the peer, which also withdraws the query.
		t.Fatalf("one forged offer should be read and the peer rested: %+v", status)
	}
	if status.Offers.Unsolicited != 509 {
		t.Fatalf("the other nine offers should have been dropped unread: %+v", status.Offers)
	}

	// 3. Refusals are remembered for a bounded number of offers.
	for index := 0; index < 10; index++ {
		id := fmt.Sprintf("o-0.9.%d", index)
		// Genuine, and too old to want: remembered so it is not verified again.
		older := newScriptedSeeder(t, net, fmt.Sprintf("old-%d", index), id, signer.release(id, "0.9.0", artifact), artifact)
		offerOnce(net, fetcher, older)
	}
	status = fetcher.status()
	// Eleven refusals so far (the forgery above and these ten), four kept.
	if status.Refused.Downgrade != 10 || status.Offers.Tracked != 4 || status.Offers.Max != 4 || status.Offers.Dropped != 7 {
		t.Fatalf("remembered refusals must stay within the bound: %+v", status)
	}

	// 4. Queries outstanding at once are bounded, so asking a whole
	//    directory cannot open the door to that many answers.
	peers := make([]string, 300)
	for index := range peers {
		peers[index] = fmt.Sprintf("directory-%d", index)
	}
	shared.advance(time.Minute) // the queries above have run out
	before := net.sentOfType(fetcher.address, TypeQuery)
	fetcher.engine.Ask(peers)
	net.pump()
	if sent := net.sentOfType(fetcher.address, TypeQuery) - before; sent != 64 {
		t.Fatalf("at most 64 queries may be outstanding, %d were sent", sent)
	}

	// 5. The table of peers that are being rested is bounded as well.
	fetcher.engine.mu.Lock()
	for index := 0; index < 1000; index++ {
		fetcher.engine.strikeLocked(fmt.Sprintf("forger-%d", index), shared.Now())
	}
	tracked := len(fetcher.engine.peers)
	fetcher.engine.mu.Unlock()
	if tracked > maxPeerRecords {
		t.Fatalf("%d peers are tracked; the bound is %d", tracked, maxPeerRecords)
	}
	requireNothingStored(t, fetcher)
}
