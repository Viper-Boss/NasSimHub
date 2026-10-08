package nodereleasep2p

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedingGateway is a gateway holding one complete, verified release, and a
// bare peer to talk to it with.
func seedingGateway(t *testing.T, options ...gatewayOption) (*network, *clock, *publisher, *gateway, *probe, []byte, []byte) {
	t.Helper()
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	seeder := newGateway(t, net, shared, "seeder", signer, options...)
	artifact := artifactBytes(7, testArtifact)
	raw := signer.release("r-1.1.0", "1.1.0", artifact)
	seeder.hold(raw, artifact)
	return net, shared, signer, seeder, newProbe(net, "probe"), raw, artifact
}

// ask sends one message and returns what came back.
func (p *probe) ask(to string, message Message) []Message {
	p.send(to, message)
	p.net.pump()
	return p.take()
}

func ofType(messages []Message, messageType string) []Message {
	var matched []Message
	for _, message := range messages {
		if message.Type == messageType {
			matched = append(matched, message)
		}
	}
	return matched
}

func TestOnlyCompleteReleasesThisGatewayVerifiedAreOffered(t *testing.T) {
	net, shared, signer, seeder, peer, raw, artifact := seedingGateway(t)
	// A manifest whose artifact was never uploaded.
	incomplete := artifactBytes(9, testArtifact)
	if _, err := seeder.store.ImportManifest(signer.release("r-1.9.0", "1.9.0", incomplete)); err != nil {
		t.Fatal(err)
	}

	answers := peer.ask(seeder.address, Message{Type: TypeQuery, Nonce: "n-1"})
	if len(answers) != 1 || answers[0].Type != TypeOffer || answers[0].ReleaseID != "r-1.1.0" || answers[0].Nonce != "n-1" {
		t.Fatalf("exactly the complete release should be offered: %+v", answers)
	}
	offered := answers[0]
	manifest, err := base64.StdEncoding.DecodeString(offered.ManifestBase64)
	if err != nil || !bytes.Equal(manifest, raw) {
		t.Fatal("the manifest must be offered byte for byte as it was signed")
	}
	if offered.TotalSize != int64(len(artifact)) || offered.ChunkSize != testChunk || offered.ChunkCount != 13 {
		t.Fatalf("the offer does not describe the artifact: %+v", offered)
	}
	// The table it then serves hashes to what it advertised, and describes
	// the artifact.
	tables := ofType(peer.ask(seeder.address, Message{Type: TypeTableGet, ReleaseID: "r-1.1.0", Transfer: offered.Transfer}), TypeTable)
	if len(tables) != 1 {
		t.Fatal("the chunk table was not served")
	}
	table, _ := base64.StdEncoding.DecodeString(tables[0].TableBase64)
	digest := tableDigest(offered.TotalSize, offered.ChunkSize, offered.ChunkCount, table)
	if hex.EncodeToString(digest[:]) != offered.TableSHA256 || offered.Transfer != TransferKey("r-1.1.0", sha256.Sum256(raw), digest) {
		t.Fatal("the served table is not the advertised one")
	}
	for index := 0; index < 13; index++ {
		chunks := ofType(peer.ask(seeder.address, Message{Type: TypeChunkGet, ReleaseID: "r-1.1.0", Transfer: offered.Transfer, Index: index}), TypeChunk)
		if len(chunks) != 1 || chunks[0].Index != index || chunks[0].Transfer != offered.Transfer {
			t.Fatalf("chunk %d was not served under its transfer: %+v", index, chunks)
		}
		data, _ := base64.StdEncoding.DecodeString(chunks[0].DataBase64)
		sum := sha256.Sum256(data)
		if !bytes.Equal(sum[:], table[index*32:(index+1)*32]) || !bytes.Equal(data, artifact[index*testChunk:min((index+1)*testChunk, len(artifact))]) {
			t.Fatalf("chunk %d is not the artifact's", index)
		}
	}
	// The incomplete release is not served even when asked for by name.
	for _, messageType := range []string{TypeTableGet, TypeChunkGet} {
		if got := peer.ask(seeder.address, Message{Type: messageType, ReleaseID: "r-1.9.0", Transfer: offered.Transfer}); len(ofType(got, TypeTable))+len(ofType(got, TypeChunk)) != 0 {
			t.Fatalf("an incomplete release was served: %+v", got)
		}
	}

	// With more than two complete releases, the two newest are offered.
	for _, version := range []string{"1.3.0", "1.2.0"} {
		extra := artifactBytes(11, 3000)
		seeder.hold(signer.release("r-"+version, version, extra), extra)
	}
	shared.advance(2 * time.Minute)
	answers = peer.ask(seeder.address, Message{Type: TypeQuery, Nonce: "n-2"})
	if len(answers) != 2 || answers[0].ReleaseID != "r-1.3.0" || answers[1].ReleaseID != "r-1.2.0" {
		t.Fatalf("the two newest complete releases should be offered, newest first: %+v", answers)
	}
	_ = net
}

func TestRequestsThatNameAnythingButAServedTransferGetNoPayload(t *testing.T) {
	_, _, _, seeder, peer, _, _ := seedingGateway(t)
	offered := peer.ask(seeder.address, Message{Type: TypeQuery, Nonce: "n-1"})[0]

	// Something that looks exactly like a release, just outside the store.
	// If a release id could ever be used as a path, this is what it would
	// reach.
	outside := filepath.Join(filepath.Dir(seeder.storeDir), "secret")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.json", "artifact", "imported.json"} {
		source := filepath.Join(seeder.storeDir, "r-1.1.0", name)
		content, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outside, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ids := []string{
		"", ".", "..", "../secret", "../secret/", "..%2fsecret", "r-1.1.0/../../secret", "/etc/passwd",
		filepath.Join(filepath.Dir(seeder.storeDir), "secret"), "r-1.1.0/", "./r-1.1.0", "r-1.1.0\x00", "current", "previous",
		strings.Repeat("a", 65), "r 1.1.0", "r-1.1.0\n", "..\\secret", "R-9.9.9",
	}
	for _, id := range ids {
		for _, messageType := range []string{TypeTableGet, TypeChunkGet} {
			got := peer.ask(seeder.address, Message{Type: messageType, ReleaseID: id, Transfer: offered.Transfer})
			if len(ofType(got, TypeTable))+len(ofType(got, TypeChunk)) != 0 {
				t.Fatalf("release id %q got a payload back: %+v", id, got)
			}
		}
	}
	// The right release under another transfer key, and chunk numbers that
	// do not exist.
	other := strings.Repeat("ab", 32)
	requests := []Message{
		{Type: TypeChunkGet, ReleaseID: "r-1.1.0", Transfer: other},
		{Type: TypeTableGet, ReleaseID: "r-1.1.0", Transfer: other},
		{Type: TypeChunkGet, ReleaseID: "r-1.1.0", Transfer: ""},
		{Type: TypeChunkGet, ReleaseID: "r-1.1.0", Transfer: offered.Transfer, Index: -1},
		{Type: TypeChunkGet, ReleaseID: "r-1.1.0", Transfer: offered.Transfer, Index: 13},
		{Type: TypeChunkGet, ReleaseID: "r-1.1.0", Transfer: offered.Transfer, Index: 1 << 40},
	}
	for _, request := range requests {
		got := peer.ask(seeder.address, request)
		if len(ofType(got, TypeTable))+len(ofType(got, TypeChunk)) != 0 {
			t.Fatalf("%+v got a payload back: %+v", request, got)
		}
	}
	served := seeder.status().Served
	if served.Chunks != 0 || served.Tables != 0 || served.Bytes != 0 {
		t.Fatalf("nothing should have been served: %+v", served)
	}
	if want := int64(2*len(ids) + len(requests)); served.RefusedInvalid != want {
		t.Fatalf("every one of the %d requests should be counted as invalid, counted %d", want, served.RefusedInvalid)
	}
	// What IS sent back says "unknown", carries no bytes, and is limited per
	// peer per minute however many requests there are.
	heard := 0
	for index := 0; index < 100; index++ {
		for _, answer := range peer.ask(seeder.address, Message{Type: TypeChunkGet, ReleaseID: "r-1.1.0", Transfer: other}) {
			if answer.Type != TypeRefuse || answer.Reason != refuseUnknown || answer.DataBase64 != "" {
				t.Fatalf("unexpected answer to a request for an unknown transfer: %+v", answer)
			}
			heard++
		}
	}
	if heard == 0 || heard > refusalsPerMinute {
		t.Fatalf("a peer should hear at most %d refusals a minute, heard %d", refusalsPerMinute, heard)
	}
}

func TestSeedingIsRateLimitedPerPeerAndOverall(t *testing.T) {
	limited := withOptions(func(o *Options) {
		o.Tuning.ServePeerBytesPerMinute = 3 * testChunk
		o.Tuning.ServeGlobalBytesPerMinute = 5 * testChunk
	})
	net, shared, _, seeder, first, _, _ := seedingGateway(t, limited)
	second, third := newProbe(net, "probe-2"), newProbe(net, "probe-3")
	offered := first.ask(seeder.address, Message{Type: TypeQuery, Nonce: "n-1"})[0]
	chunk := func(peer *probe, index int) []Message {
		return peer.ask(seeder.address, Message{Type: TypeChunkGet, ReleaseID: "r-1.1.0", Transfer: offered.Transfer, Index: index})
	}

	// One peer: three chunks a minute, then "busy" with a time to wait.
	for index := 0; index < 3; index++ {
		if got := chunk(first, index); len(ofType(got, TypeChunk)) != 1 {
			t.Fatalf("chunk %d should be within the limit: %+v", index, got)
		}
	}
	refused := chunk(first, 3)
	if len(refused) != 1 || refused[0].Type != TypeRefuse || refused[0].Reason != refuseBusy ||
		refused[0].RetryAfterSeconds < 1 || refused[0].RetryAfterSeconds > 60 {
		t.Fatalf("over its limit a peer should be told to wait: %+v", refused)
	}
	// Everybody together: five a minute. The second peer gets two.
	for index := 0; index < 2; index++ {
		if got := chunk(second, index); len(ofType(got, TypeChunk)) != 1 {
			t.Fatalf("the second peer's chunk %d should be within the overall limit: %+v", index, got)
		}
	}
	if got := chunk(second, 2); len(ofType(got, TypeChunk)) != 0 {
		t.Fatalf("the overall limit should have been reached: %+v", got)
	}
	if got := chunk(third, 0); len(ofType(got, TypeChunk)) != 0 {
		t.Fatalf("a third peer cannot get around the overall limit: %+v", got)
	}
	served := seeder.status().Served
	if served.Chunks != 5 || served.Bytes != 5*testChunk || served.RefusedRate != 3 {
		t.Fatalf("served counters do not match what was allowed: %+v", served)
	}
	// However often a peer over its limit asks, it is told so only so often.
	told := 0
	for index := 0; index < 50; index++ {
		told += len(chunk(first, 3))
	}
	if told != refusalsPerMinute-1 {
		t.Fatalf("a peer over its limit should hear at most %d refusals a minute, heard %d more", refusalsPerMinute, told)
	}
	// A minute later the budgets are fresh.
	shared.advance(61 * time.Second)
	if got := chunk(first, 3); len(ofType(got, TypeChunk)) != 1 {
		t.Fatalf("after the window the peer should be served again: %+v", got)
	}

	// Queries: one peer once a minute, thirty a minute overall.
	if got := first.ask(seeder.address, Message{Type: TypeQuery, Nonce: "n-2"}); len(got) != 1 {
		t.Fatalf("a query after the interval should be answered: %+v", got)
	}
	if got := first.ask(seeder.address, Message{Type: TypeQuery, Nonce: "n-3"}); len(got) != 0 {
		t.Fatalf("a second query inside the interval should not be answered: %+v", got)
	}
	shared.advance(61 * time.Second)
	answered := 0
	for index := 0; index < 40; index++ {
		asker := newProbe(net, fmt.Sprintf("asker-%d", index))
		answered += len(asker.ask(seeder.address, Message{Type: TypeQuery, Nonce: "n"}))
	}
	if answered != 30 {
		t.Fatalf("thirty queries a minute should be answered overall, %d were", answered)
	}
	// A nonce that could not be one of this protocol's is not echoed.
	shared.advance(61 * time.Second)
	if got := newProbe(net, "long").ask(seeder.address, Message{Type: TypeQuery, Nonce: strings.Repeat("n", 65)}); len(got) != 0 {
		t.Fatalf("an oversize nonce was answered: %+v", got)
	}
}

func TestAReleaseThatIsRemovedOrDamagedStopsBeingServed(t *testing.T) {
	for name, spoil := range map[string]func(t *testing.T, seeder *gateway){
		"removed from the store": func(t *testing.T, seeder *gateway) {
			if err := seeder.store.Remove("r-1.1.0"); err != nil {
				t.Fatal(err)
			}
		},
		"artifact overwritten on disk": func(t *testing.T, seeder *gateway) {
			if err := os.WriteFile(filepath.Join(seeder.storeDir, "r-1.1.0", "artifact"), artifactBytes(666, testArtifact), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, shared, _, seeder, peer, _, _ := seedingGateway(t)
			offered := peer.ask(seeder.address, Message{Type: TypeQuery, Nonce: "n-1"})[0]
			request := Message{Type: TypeChunkGet, ReleaseID: "r-1.1.0", Transfer: offered.Transfer, Index: 2}
			if got := peer.ask(seeder.address, request); len(ofType(got, TypeChunk)) != 1 {
				t.Fatalf("the release should be served to begin with: %+v", got)
			}
			spoil(t, seeder)
			// It is looked up in the store again at most a minute later.
			shared.advance(61 * time.Second)
			got := peer.ask(seeder.address, request)
			if len(ofType(got, TypeChunk)) != 0 || len(got) != 1 || got[0].Reason != refuseUnknown {
				t.Fatalf("a release that no longer checks out must not be served: %+v", got)
			}
			shared.advance(61 * time.Second)
			if got := peer.ask(seeder.address, Message{Type: TypeQuery, Nonce: "n-2"}); len(got) != 0 {
				t.Fatalf("and it must not be offered: %+v", got)
			}
		})
	}
}

func TestSeedingFollowsBothSwitches(t *testing.T) {
	_, shared, _, seeder, peer, _, _ := seedingGateway(t)
	offered := peer.ask(seeder.address, Message{Type: TypeQuery, Nonce: "n-1"})[0]
	request := Message{Type: TypeChunkGet, ReleaseID: "r-1.1.0", Transfer: offered.Transfer, Index: 0}

	seeder.sharing.Store(false)
	shared.advance(2 * time.Minute)
	if status := seeder.status(); status.Seeding || status.SeedingReason != DisabledSharingOff || !status.Enabled {
		t.Fatalf("with sharing off the gateway still downloads but does not seed: %+v", status)
	}
	if got := append(peer.ask(seeder.address, Message{Type: TypeQuery, Nonce: "n-2"}), peer.ask(seeder.address, request)...); len(got) != 0 {
		t.Fatalf("with sharing off nothing is answered: %+v", got)
	}

	seeder.sharing.Store(true)
	seeder.enabled.Store(false)
	if status := seeder.status(); status.Seeding || status.SeedingReason != DisabledSwitchedOff {
		t.Fatalf("with the exchange off nothing is seeded: %+v", status)
	}
	if got := append(peer.ask(seeder.address, Message{Type: TypeQuery, Nonce: "n-3"}), peer.ask(seeder.address, request)...); len(got) != 0 {
		t.Fatalf("with the exchange off nothing is answered: %+v", got)
	}

	seeder.enabled.Store(true)
	if got := peer.ask(seeder.address, request); len(ofType(got, TypeChunk)) != 1 {
		t.Fatalf("switched back on it serves again: %+v", got)
	}
}

func TestADownloadInProgressIsNeverServedFrom(t *testing.T) {
	net, _, _, seeder, fetcher, _, _ := pairOfGateways(t)
	holdChunks(net)
	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)
	if _, downloading := fetcher.download("r-1.1.0"); !downloading {
		t.Fatal("expected a download in progress")
	}
	peer := newProbe(net, "probe")
	if got := peer.ask(fetcher.address, Message{Type: TypeQuery, Nonce: "n-1"}); len(got) != 0 {
		t.Fatalf("a release that is not yet verified and imported must not be offered: %+v", got)
	}
}

// A fetcher facing a tightly limited seeder waits as it is told and still
// finishes.
func TestADownloadFromARateLimitedSeederFinishesAsTheBudgetAllows(t *testing.T) {
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	seeder := newGateway(t, net, shared, "seeder", signer, withOptions(func(o *Options) {
		o.Tuning.ServePeerBytesPerMinute = 4 * testChunk
	}))
	artifact := artifactBytes(7, testArtifact)
	seeder.hold(signer.release("r-1.1.0", "1.1.0", artifact), artifact)
	fetcher := newGateway(t, net, shared, "fetcher", signer)

	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)
	if entry, _ := fetcher.download("r-1.1.0"); entry.ReceivedChunks == 0 || entry.ReceivedChunks > 4 {
		t.Fatalf("the first minute's budget is four chunks: %+v", entry)
	}
	drive(net, shared, 20*time.Second, 30, seeder, fetcher)
	requireImported(t, fetcher, "r-1.1.0", artifact)
	if seeder.status().Served.RefusedRate == 0 {
		t.Fatal("the seeder's limit should have been reached along the way")
	}
	if rested := fetcher.status().Peers.BackedOffTotal; rested != 0 {
		t.Fatal("a seeder that says \"busy\" is not held to have done anything wrong")
	}
}
