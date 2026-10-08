package nodereleasep2p

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The chunk table is the peer's own word. A peer that controls both the
// table and the bytes can make every chunk "verify" -- which is exactly why
// the table proves nothing and the signed manifest's hash decides.
func TestAGenuineManifestWithALyingTableEndsInNothingImportedAndThePeerBlamed(t *testing.T) {
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	artifact := artifactBytes(7, testArtifact)
	garbage := artifactBytes(666, testArtifact)
	raw := signer.release("r-1.1.0", "1.1.0", artifact) // genuinely signed, for the real artifact
	fetcher := newGateway(t, net, shared, "fetcher", signer)

	liar := newScriptedSeeder(t, net, "liar", "r-1.1.0", raw, garbage) // table AND bytes are of the garbage
	offerOnce(net, fetcher, liar)

	status := fetcher.status()
	if liar.chunks.Load() != 13 || status.Transfers.ChunksReceived != 13 {
		t.Fatalf("every lying chunk matches the lying table, so all 13 should have been taken: served %d, %+v",
			liar.chunks.Load(), status.Transfers)
	}
	if status.Refused.HashMismatch != 1 || status.Refused.ChunkMismatch != 0 {
		t.Fatalf("the whole-file hash is what must catch this: %+v", status.Refused)
	}
	requireNothingStored(t, fetcher)
	if entries := fetcher.cacheEntries(); len(entries) != 0 || status.CacheBytes != 0 {
		t.Fatalf("everything obtained under a false table must be discarded: %v, %d bytes", entries, status.CacheBytes)
	}
	if len(status.Downloads) != 1 || status.Downloads[0].State != stateFailed {
		t.Fatalf("the download should be reported as failed: %+v", status.Downloads)
	}
	if status.Peers.BackedOff != 1 || status.Transfers.Failed != 1 {
		t.Fatalf("the peer that vouched for the table should be backed off: %+v", status)
	}

	// Another peer vouching for the same table is refused on sight: no
	// payload is fetched under a table already proven false.
	accomplice := newScriptedSeeder(t, net, "accomplice", "r-1.1.0", raw, garbage)
	offerOnce(net, fetcher, accomplice)
	status = fetcher.status()
	if accomplice.chunks.Load() != 0 || status.Refused.TableMismatch != 1 || status.Peers.BackedOff != 2 {
		t.Fatalf("a table proven false must not be fetched under again: served %d, %+v", accomplice.chunks.Load(), status)
	}

	// The real release is still obtainable from an honest gateway.
	honest := newGateway(t, net, shared, "honest", signer)
	honest.hold(raw, artifact)
	fetcher.engine.Ask([]string{honest.address})
	settle(net, honest, fetcher)
	requireImported(t, fetcher, "r-1.1.0", artifact)
}

// The other way round: the table is the honest one, the bytes are not. Here
// the table earns its keep -- the bad chunk is dropped as it arrives and the
// peer that sent it is known.
func TestLyingChunksUnderAnHonestTableAreDroppedAsTheyArrive(t *testing.T) {
	for name, corrupt := range map[string]func([]byte) []byte{
		"other bytes of the right length": func(artifact []byte) []byte { return artifactBytes(666, len(artifact)) },
		"a short last chunk":              func(artifact []byte) []byte { return artifact[:len(artifact)-100] },
		"one flipped bit": func(artifact []byte) []byte {
			flipped := append([]byte(nil), artifact...)
			flipped[3*testChunk+17] ^= 0x01
			return flipped
		},
	} {
		t.Run(name, func(t *testing.T) {
			net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
			artifact := artifactBytes(7, testArtifact)
			raw := signer.release("r-1.1.0", "1.1.0", artifact)
			fetcher := newGateway(t, net, shared, "fetcher", signer)

			// The liar advertises the honest table -- so the identical
			// transfer key as the honest gateway -- and serves its own bytes.
			liar := newScriptedSeeder(t, net, "liar", "r-1.1.0", raw, artifact)
			liar.serve = corrupt(artifact)
			honest := newGateway(t, net, shared, "honest", signer)
			honest.hold(raw, artifact)

			// Alone first, so it is certainly the one asked for the chunk it
			// corrupts; then the honest gateway joins the same download.
			offerOnce(net, fetcher, liar)
			if entry, _ := fetcher.download("r-1.1.0"); entry.Peers != 0 || entry.ReceivedChunks == 13 {
				t.Fatalf("the liar should have been dropped at its bad chunk: %+v", entry)
			}
			fetcher.engine.Ask([]string{honest.address})
			settle(net, honest, fetcher)

			requireImported(t, fetcher, "r-1.1.0", artifact)
			status := fetcher.status()
			if status.Refused.ChunkMismatch != 1 || status.Refused.HashMismatch != 0 {
				t.Fatalf("one bad chunk should be caught by the table, and the whole file should then be right: %+v", status.Refused)
			}
			if status.Peers.BackedOff != 1 {
				t.Fatalf("exactly the peer that sent the bad chunk should be backed off: %+v", status.Peers)
			}
			if honest.status().Served.Chunks == 0 {
				t.Fatal("the honest peer should have finished the download")
			}
		})
	}
}

func TestALyingPeerAloneNeverGetsAByteIntoTheStore(t *testing.T) {
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	artifact := artifactBytes(7, testArtifact)
	raw := signer.release("r-1.1.0", "1.1.0", artifact)
	fetcher := newGateway(t, net, shared, "fetcher", signer)
	liar := newScriptedSeeder(t, net, "liar", "r-1.1.0", raw, artifact)
	liar.serve = artifactBytes(666, len(artifact))

	offerOnce(net, fetcher, liar)
	entry, ok := fetcher.download("r-1.1.0")
	if !ok || entry.ReceivedChunks != 0 || entry.Peers != 0 || entry.State != stateWaiting {
		t.Fatalf("a chunk that fails the table must not be written, and its sender must be dropped: %+v", entry)
	}
	part, err := os.ReadFile(filepath.Join(fetcher.cacheDir, "r-1.1.0", partFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range part {
		if value != 0 {
			t.Fatal("bytes from a refused chunk reached the part file")
		}
	}
	requireNothingStored(t, fetcher)
}

func TestATableThatIsNotTheOneAdvertisedIsRefused(t *testing.T) {
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	artifact := artifactBytes(7, testArtifact)
	raw := signer.release("r-1.1.0", "1.1.0", artifact)
	fetcher := newGateway(t, net, shared, "fetcher", signer)

	for index, sent := range [][]byte{
		newScriptedSeeder(t, net, "scratch", "r-1.1.0", raw, artifactBytes(666, testArtifact)).table(), // another table
		make([]byte, 13*32-1), // not a whole number of digests
		make([]byte, 14*32),   // more digests than chunks
	} {
		liar := newScriptedSeeder(t, net, "liar-"+string(rune('a'+index)), "r-1.1.0", raw, artifact)
		liar.sentTable = sent
		offerOnce(net, fetcher, liar)
		status := fetcher.status()
		if status.Refused.TableMismatch != int64(index+1) || liar.chunks.Load() != 0 {
			t.Fatalf("a table that does not hash to what was offered must be refused before any chunk: %+v", status.Refused)
		}
		if status.CacheBytes != 0 || len(fetcher.cacheEntries()) != 0 || status.Peers.BackedOff != index+1 {
			t.Fatalf("the refused transfer should be dropped and its peer backed off: %+v", status)
		}
	}
	requireNothingStored(t, fetcher)
}

func TestAPeerThatOffersAndThenNeverAnswersCostsABoundedWait(t *testing.T) {
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	artifact := artifactBytes(7, testArtifact)
	fetcher := newGateway(t, net, shared, "fetcher", signer)
	mute := newScriptedSeeder(t, net, "mute", "r-1.1.0", signer.release("r-1.1.0", "1.1.0", artifact), artifact)
	mute.silent = true

	offerOnce(net, fetcher, mute)
	if entry, _ := fetcher.download("r-1.1.0"); entry.State != stateFetchingTable {
		t.Fatalf("expected the download to be waiting for its table: %+v", entry)
	}
	drive(net, shared, time.Minute, 20, fetcher)
	status := fetcher.status()
	if status.CacheBytes != 0 || len(status.Downloads) != 1 || status.Downloads[0].State != stateFailed {
		t.Fatalf("a table that never comes must end the download and free its reservation: %+v", status)
	}
	if asked := net.sentOfType(fetcher.address, TypeTableGet); asked != 3 {
		t.Fatalf("the table should be asked for a bounded number of times (3), was asked for %d", asked)
	}
}
