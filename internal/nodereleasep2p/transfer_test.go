package nodereleasep2p

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/rand"
	"testing"
	"time"

	"github.com/human-agent65535/nassimhub-node/proto"
)

func TestAReleaseHeldByOnePeerArrivesWholeAndIsImportedThroughTheStore(t *testing.T) {
	net, _, _, seeder, fetcher, _, artifact := pairOfGateways(t)

	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)

	requireImported(t, fetcher, "r-1.1.0", artifact)
	status := fetcher.status()
	if !status.Enabled || status.Reason != "" {
		t.Fatalf("the exchange should report itself enabled: %+v", status)
	}
	if status.Transfers.Completed != 1 || status.Transfers.ChunksReceived != 13 ||
		status.Transfers.BytesReceived != int64(len(artifact)) {
		t.Fatalf("transfer counters do not describe one 13-chunk download: %+v", status.Transfers)
	}
	if len(status.Downloads) != 1 || status.Downloads[0].State != stateImported ||
		status.Downloads[0].ReceivedChunks != 13 || status.Downloads[0].ChunkCount != 13 ||
		status.Downloads[0].ReceivedBytes != int64(len(artifact)) {
		t.Fatalf("the finished download should be listed as imported: %+v", status.Downloads)
	}
	served := seeder.status().Served
	if served.Queries != 1 || served.Offers != 1 || served.Tables != 1 || served.Chunks != 13 {
		t.Fatalf("the seeder's counters do not describe what it served: %+v", served)
	}
	// Exactly one request per chunk: nothing was asked for twice.
	if asked := net.sentOfType(fetcher.address, TypeChunkGet); asked != 13 {
		t.Fatalf("13 chunks should take 13 requests, took %d", asked)
	}
	// The gateway that fetched it now seeds it to a third, under the
	// identical transfer key: two honest gateways always describe one
	// release the same way.
	third := newGateway(t, net, fetcher.clock, "third", newPublisher(t, 1))
	third.engine.Ask([]string{fetcher.address})
	settle(net, fetcher, third)
	requireImported(t, third, "r-1.1.0", artifact)
	keys := map[string]bool{}
	for _, sent := range net.delivered {
		if message := sent.message(t); message.Type == TypeOffer {
			keys[message.Transfer] = true
		}
	}
	if len(keys) != 1 {
		t.Fatalf("the two seeders offered the same release under %d different transfer keys", len(keys))
	}
}

func TestNothingIsFetchedUntilTheSwitchIsOnAndItPausesWhenSwitchedOff(t *testing.T) {
	net, _, _, seeder, fetcher, _, artifact := pairOfGateways(t)
	fetcher.enabled.Store(false)

	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)
	if sent := net.sentOfType(fetcher.address, ""); sent != 0 {
		t.Fatalf("a gateway that is switched off must not send anything, sent %d", sent)
	}
	if status := fetcher.status(); status.Enabled || status.Reason != DisabledSwitchedOff {
		t.Fatalf("status should say switched_off: %+v", status)
	}
	requireNothingStored(t, fetcher)

	// Switched on, then off again part way through.
	fetcher.enabled.Store(true)
	chunks := 0
	net.filter = func(_ packet, message Message) bool {
		if message.Type == TypeChunk {
			chunks++
			if chunks == 5 {
				fetcher.enabled.Store(false)
			}
		}
		return true
	}
	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)
	entry, ok := fetcher.download("r-1.1.0")
	if !ok || entry.State != statePaused || entry.ReceivedChunks >= 13 {
		t.Fatalf("a download switched off part way should be paused and incomplete: %+v", entry)
	}
	received := entry.ReceivedChunks
	requireNothingStored(t, fetcher)

	net.filter = nil
	fetcher.enabled.Store(true)
	// The requests whose answers were ignored time out and are asked again.
	drive(net, fetcher.clock, time.Minute, 3, seeder, fetcher)
	requireImported(t, fetcher, "r-1.1.0", artifact)
	if total := fetcher.status().Transfers.ChunksReceived; total != 13 {
		t.Fatalf("resuming after the switch must keep the %d chunks it had; received %d in all", received, total)
	}
}

func TestChunksArrivingTwiceAndInAnyOrderAssembleTheSameFile(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		net, _, _, seeder, fetcher, _, artifact := pairOfGateways(t)
		net.duplicate = true
		net.shuffle = rand.New(rand.NewSource(seed))

		fetcher.engine.Ask([]string{seeder.address})
		settle(net, seeder, fetcher)

		requireImported(t, fetcher, "r-1.1.0", artifact)
		transfers := fetcher.status().Transfers
		if transfers.ChunksReceived != 13 {
			t.Fatalf("each chunk is counted once however often it arrives: %+v", transfers)
		}
		if transfers.DuplicateChunks == 0 {
			t.Fatalf("every chunk was delivered twice and none was noticed: %+v", transfers)
		}
	}
}

func TestADownloadDrawsFromSeveralPeersOnlyWhenTheyAgreeOnEveryByte(t *testing.T) {
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	artifact := artifactBytes(7, testArtifact)
	raw := signer.release("r-1.1.0", "1.1.0", artifact)
	first := newGateway(t, net, shared, "seeder-1", signer)
	second := newGateway(t, net, shared, "seeder-2", signer)
	first.hold(raw, artifact)
	second.hold(raw, artifact)
	fetcher := newGateway(t, net, shared, "fetcher", signer)

	fetcher.engine.Ask([]string{first.address, second.address})
	settle(net, first, second, fetcher)

	requireImported(t, fetcher, "r-1.1.0", artifact)
	one, two := first.status().Served.Chunks, second.status().Served.Chunks
	if one == 0 || two == 0 || one+two != 13 {
		t.Fatalf("two peers with the identical transfer should share the 13 chunks, served %d and %d", one, two)
	}
}

func TestAPeerThatGoesQuietIsRotatedOutAndTheOthersFinishTheDownload(t *testing.T) {
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	artifact := artifactBytes(7, testArtifact)
	raw := signer.release("r-1.1.0", "1.1.0", artifact)
	first := newGateway(t, net, shared, "seeder-1", signer)
	second := newGateway(t, net, shared, "seeder-2", signer)
	first.hold(raw, artifact)
	second.hold(raw, artifact)
	fetcher := newGateway(t, net, shared, "fetcher", signer)

	// seeder-1 answers the query and the table request, then disappears.
	net.filter = func(p packet, message Message) bool {
		if p.to == first.address && message.Type == TypeChunkGet {
			net.down[first.address] = true
			return false
		}
		return true
	}
	fetcher.engine.Ask([]string{first.address, second.address})
	settle(net, first, second, fetcher)
	if entry, _ := fetcher.download("r-1.1.0"); entry.State != stateDownloading || entry.ReceivedChunks == 0 || entry.ReceivedChunks == 13 {
		t.Fatalf("with one peer gone the download should be part way and waiting on it: %+v", entry)
	}

	// Its requests time out; they go to the peer that is still there.
	drive(net, shared, 31*time.Second, 2, first, second, fetcher)
	requireImported(t, fetcher, "r-1.1.0", artifact)
	if timeouts := fetcher.status().Transfers.RequestTimeouts; timeouts == 0 {
		t.Fatal("the unanswered requests should have been counted as timeouts")
	}
	if served := first.status().Served.Chunks; served != 0 {
		t.Fatalf("the peer that went quiet served %d chunks", served)
	}
}

func TestADownloadSurvivesTheOnlyPeerDisconnectingAndComingBack(t *testing.T) {
	net, shared, _, seeder, fetcher, _, artifact := pairOfGateways(t)
	delivered := 0
	net.filter = func(_ packet, message Message) bool {
		if message.Type == TypeChunk {
			delivered++
			if delivered == 6 {
				net.down[seeder.address] = true
			}
		}
		return true
	}
	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)

	// Unanswered again and again: rested with a growing pause, and after
	// MaxPeerFailures in a row no longer asked at all.
	for round := 0; round < 12; round++ {
		shared.advance(2 * time.Minute)
		settle(net, seeder, fetcher)
	}
	entry, ok := fetcher.download("r-1.1.0")
	if !ok || entry.State != stateWaiting || entry.Peers != 0 {
		t.Fatalf("with its only peer gone the download should be waiting for peers: %+v", entry)
	}
	if !fetcher.engine.WantsPeers() {
		t.Fatal("a download with nobody to fetch from should ask for peers")
	}
	asked := net.sentOfType(fetcher.address, TypeChunkGet)
	shared.advance(10 * time.Minute)
	settle(net, seeder, fetcher)
	if again := net.sentOfType(fetcher.address, TypeChunkGet); again != asked {
		t.Fatalf("retries must be bounded: %d more requests went to a peer that was dropped", again-asked)
	}

	// The peer is back. Asking it again re-attaches it and the download
	// carries on from the chunks it has.
	net.filter = nil
	net.setDown(seeder.address, false)
	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)
	requireImported(t, fetcher, "r-1.1.0", artifact)
	if total := fetcher.status().Transfers.ChunksReceived; total != 13 {
		t.Fatalf("chunks already held must not be fetched again, received %d", total)
	}
}

func TestATransportThatCannotSendIsCountedAndRecoveredFrom(t *testing.T) {
	net, shared, _, seeder, fetcher, _, artifact := pairOfGateways(t)

	// Not connected when the round starts: the query never leaves.
	net.sendFails[fetcher.address] = true
	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)
	if failed := fetcher.status().Transfers.SendErrors; failed != 1 {
		t.Fatalf("one query failed to send; counted %d", failed)
	}
	requireNothingStored(t, fetcher)

	// Connected again, then cut off part way through a download.
	net.sendFails[fetcher.address] = false
	chunks := 0
	net.filter = func(_ packet, message Message) bool {
		if message.Type == TypeChunk {
			chunks++
			if chunks == 5 {
				net.sendFails[fetcher.address] = true
			}
		}
		return true
	}
	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)
	if entry, _ := fetcher.download("r-1.1.0"); entry.ReceivedChunks == 0 || entry.ReceivedChunks == 13 {
		t.Fatalf("the download should be part way: %+v", entry)
	}
	before := fetcher.status().Transfers.SendErrors
	drive(net, shared, time.Minute, 2, seeder, fetcher)
	if after := fetcher.status().Transfers.SendErrors; after <= before {
		t.Fatal("requests that could not be sent should be counted")
	}

	// Reconnected. Nothing is re-sent on its own; the requests are simply
	// asked again when their time is up.
	net.filter = nil
	net.sendFails[fetcher.address] = false
	drive(net, shared, time.Minute, 4, seeder, fetcher)
	requireImported(t, fetcher, "r-1.1.0", artifact)
}

// Mix-ups: a chunk is only ever written under the one transfer key it was
// asked for under.
func TestChunksOfAnotherReleaseTableOrManifestAreNeverMixedIn(t *testing.T) {
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	artifact := artifactBytes(7, testArtifact)
	other := artifactBytes(8, testArtifact)
	raw := signer.release("r-1.1.0", "1.1.0", artifact)
	honest := newScriptedSeeder(t, net, "honest", "r-1.1.0", raw, artifact)
	fetcher := newGateway(t, net, shared, "fetcher", signer)

	// Stop after the table so the download is open and empty.
	net.filter = func(_ packet, message Message) bool { return message.Type != TypeChunkGet }
	fetcher.engine.Ask([]string{honest.address})
	settle(net, fetcher)
	entry, ok := fetcher.download("r-1.1.0")
	if !ok || entry.ReceivedChunks != 0 {
		t.Fatalf("expected an open, empty download: %+v", entry)
	}

	inject := func(from string, message Message) {
		t.Helper()
		data, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		fetcher.engine.Handle(from, data)
	}
	chunk := func(source []byte, index int) string {
		end := (index + 1) * testChunk
		if end > len(source) {
			end = len(source)
		}
		return base64.StdEncoding.EncodeToString(source[index*testChunk : end])
	}

	// 1. A chunk of another release, correctly labelled as such.
	otherRaw := signer.release("r-9.9.9", "9.9.9", other)
	otherSeeder := newScriptedSeeder(t, net, "other", "r-9.9.9", otherRaw, other)
	inject(honest.address, Message{Type: TypeChunk, ReleaseID: "r-9.9.9", Transfer: otherSeeder.key(), Index: 0, DataBase64: chunk(other, 0)})
	// 2. The right release id, but the transfer key of another signing of it.
	resigned := signer.release("r-1.1.0", "1.1.0", artifact, func(m *proto.OTAManifest) { m.Notes = "signed again" })
	resignedSeeder := newScriptedSeeder(t, net, "resigned", "r-1.1.0", resigned, artifact)
	inject(honest.address, Message{Type: TypeChunk, ReleaseID: "r-1.1.0", Transfer: resignedSeeder.key(), Index: 0, DataBase64: chunk(artifact, 0)})
	// 3. The right manifest under another table (another chunk size).
	regrouped := newScriptedSeeder(t, net, "regrouped", "r-1.1.0", raw, artifact)
	regrouped.chunkSize = 2 * testChunk
	inject(honest.address, Message{Type: TypeChunk, ReleaseID: "r-1.1.0", Transfer: regrouped.key(), Index: 0, DataBase64: chunk(artifact, 0)})
	// 4. A table under another key.
	inject(honest.address, Message{Type: TypeTable, ReleaseID: "r-1.1.0", Transfer: regrouped.key(),
		TableBase64: base64.StdEncoding.EncodeToString(regrouped.table())})

	status := fetcher.status()
	if status.Transfers.Mixups != 4 {
		t.Fatalf("four messages named another transfer; %d were counted as mix-ups", status.Transfers.Mixups)
	}
	if entry, _ := fetcher.download("r-1.1.0"); entry.ReceivedChunks != 0 {
		t.Fatalf("a chunk tagged with another transfer was written: %+v", entry)
	}

	// 5. The right key and the right bytes, from a peer that never offered
	//    this transfer: not written either.
	inject("stranger", Message{Type: TypeChunk, ReleaseID: "r-1.1.0", Transfer: honest.key(), Index: 0, DataBase64: chunk(artifact, 0)})
	if entry, _ := fetcher.download("r-1.1.0"); entry.ReceivedChunks != 0 {
		t.Fatalf("a chunk from a peer that did not offer the transfer was written: %+v", entry)
	}

	// 6. Peers offering the same release under another manifest or another
	//    table are not added to the download.
	fetcher.engine.Ask([]string{resignedSeeder.address, regrouped.address})
	settle(net, fetcher)
	status = fetcher.status()
	if status.Refused.Mixed != 2 {
		t.Fatalf("two offers were for another transfer of the same release; refused.mixed = %d", status.Refused.Mixed)
	}
	if entry, _ := fetcher.download("r-1.1.0"); entry.Peers != 1 {
		t.Fatalf("only the peer with the identical transfer may serve the download: %+v", entry)
	}
	if resignedSeeder.chunks.Load()+regrouped.chunks.Load() != 0 {
		t.Fatal("a chunk was requested from a peer whose transfer differs")
	}

	// And the download still completes from the one peer that matches.
	net.filter = nil
	drive(net, shared, 31*time.Second, 3, fetcher)
	requireImported(t, fetcher, "r-1.1.0", artifact)
	if !bytes.Equal(fetcher.stored("r-1.1.0"), artifact) {
		t.Fatal("the imported artifact is not the published one")
	}
}

func TestTheStatusDocumentHasTheShapeTheWebUIIsToldAbout(t *testing.T) {
	net, _, _, seeder, fetcher, _, _ := pairOfGateways(t)
	net.filter = func(_ packet, message Message) bool { return message.Type != TypeChunk }
	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)

	encoded, err := json.Marshal(fetcher.status())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"enabled", "seeding", "targets", "cache_bytes", "cache_cap_bytes", "max_downloads",
		"downloads", "refused", "offers", "transfers", "served", "peers", "counters_since"} {
		if _, present := document[key]; !present {
			t.Errorf("status is missing %q: %s", key, encoded)
		}
	}
	refused, _ := document["refused"].(map[string]any)
	for _, key := range []string{"signature", "platform", "downgrade", "cache_full", "table_mismatch", "hash_mismatch",
		"manifest_size", "schema", "channel", "artifact", "store_full", "busy", "mixed", "chunk_mismatch"} {
		if _, present := refused[key]; !present {
			t.Errorf("refused is missing %q: %s", key, encoded)
		}
	}
	downloads, _ := document["downloads"].([]any)
	if len(downloads) != 1 {
		t.Fatalf("expected one download in progress: %s", encoded)
	}
	entry, _ := downloads[0].(map[string]any)
	for _, key := range []string{"release_id", "version", "platform", "received_chunks", "chunk_count", "peers", "state"} {
		if _, present := entry[key]; !present {
			t.Errorf("a download is missing %q: %s", key, encoded)
		}
	}
	// Peers are a count. No address of any peer appears anywhere.
	if bytes.Contains(encoded, []byte(seeder.address)) {
		t.Fatalf("the status document names a peer: %s", encoded)
	}
	if document["cache_bytes"].(float64) != float64(testArtifact) || document["targets"].([]any)[0] != "msm8916/arm64" {
		t.Fatalf("cache or targets are not reported as expected: %s", encoded)
	}
}
