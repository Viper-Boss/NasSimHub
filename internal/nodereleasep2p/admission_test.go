package nodereleasep2p

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/nassimhub-node/proto"
)

// proposalFor builds the offer an honest peer would make for a manifest and
// its artifact.
func proposalFor(raw, artifact []byte, totalSize int64) offer {
	if totalSize == 0 {
		totalSize = int64(len(artifact))
	}
	count := int((totalSize + testChunk - 1) / testChunk)
	var manifest proto.OTAManifest
	releaseID := ""
	// Read the id the way a peer would fill in the envelope; nothing here is
	// trusted by the code under test.
	if signed, err := decodeSignedForTest(raw); err == nil {
		manifest = signed
		releaseID = manifest.ReleaseID
	}
	return offer{
		releaseID: releaseID, raw: raw, chunkSize: testChunk, chunkCount: count, totalSize: totalSize,
		tableSHA: sha256.Sum256(artifact), tableOK: true,
	}
}

// The order of the checks is part of the contract: signature before anything
// is read out of the manifest, target and version before the cache is asked
// for room. Each case below breaks one check AND everything after it that it
// can, and must be refused for the first.
func TestAdmissionRefusesInTheDocumentedOrder(t *testing.T) {
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	forger := newPublisher(t, 99) // same key id, another key
	artifact := artifactBytes(7, testArtifact)
	held := artifactBytes(8, 1000)

	// The gateway already stores 1.2.0 and its cache is too small for
	// anything: so "not newer" and "no room" are both available to every case.
	fetcher := newGateway(t, net, shared, "fetcher", signer, withOptions(func(o *Options) { o.CacheCapBytes = 1024 }))
	fetcher.hold(signer.release("r-1.2.0", "1.2.0", held), held)

	wrongPlatform := func(m *proto.OTAManifest) { m.Platform = proto.PlatformMock }
	wrongSchema := func(m *proto.OTAManifest) { m.SchemaVersion = 99 }
	hugeArtifact := func(m *proto.OTAManifest) { m.Artifacts[0].Size = proto.OTAMaxArtifactBytes + 1 }
	padded := func(m *proto.OTAManifest) { m.Notes = strings.Repeat("x", proto.OTAMaxManifestBytes) }

	cases := []struct {
		name string
		raw  []byte
		size int64
		want Reason
	}{
		{"an oversize manifest is refused for its size even though it is genuinely signed",
			signer.release("r-1.1.0", "1.1.0", artifact, padded, wrongPlatform), 0, ReasonManifestSize},
		{"an oversize forgery is refused for its size, before its signature is looked at",
			forger.release("r-1.1.0", "1.1.0", artifact, padded), 0, ReasonManifestSize},
		{"a forgery is refused for its signature, whatever else is wrong with it",
			forger.release("r-1.1.0", "1.1.0", artifact, wrongSchema, wrongPlatform, hugeArtifact), 0, ReasonSignature},
		{"another schema is refused before the platform is considered",
			signer.release("r-1.1.0", "1.1.0", artifact, wrongSchema, wrongPlatform, hugeArtifact), 0, ReasonSchema},
		{"another platform is refused before the version is considered",
			signer.release("r-1.1.0", "1.1.0", artifact, wrongPlatform, hugeArtifact), 0, ReasonPlatform},
		{"an older version is refused before the artifact is considered",
			signer.release("r-1.1.0", "1.1.0", artifact, hugeArtifact), proto.OTAMaxArtifactBytes + 1, ReasonDowngrade},
		{"an oversize artifact is refused before the cache is asked for room",
			signer.release("r-1.3.0", "1.3.0", artifact, hugeArtifact), proto.OTAMaxArtifactBytes + 1, ReasonArtifact},
		{"a release that passes everything else is refused when the cache has no room",
			signer.release("r-1.3.0", "1.3.0", artifact), 0, ReasonCacheFull},
	}
	for _, test := range cases {
		fetcher.engine.mu.Lock()
		admitted, refused := fetcher.engine.admitLocked(proposalFor(test.raw, artifact, test.size))
		fetcher.engine.mu.Unlock()
		if admitted != nil || refused == nil {
			t.Errorf("%s: it was admitted", test.name)
			continue
		}
		if refused.reason != test.want {
			t.Errorf("%s: refused as %q (%s), want %q", test.name, refused.reason, refused.detail, test.want)
		}
	}

	// And with room, the last one is admitted: the cases above were refused
	// for the reasons given and not because nothing is ever let through.
	roomy := newGateway(t, net, shared, "roomy", signer)
	roomy.hold(signer.release("r-1.2.0", "1.2.0", held), held)
	roomy.engine.mu.Lock()
	admitted, refused := roomy.engine.admitLocked(proposalFor(signer.release("r-1.3.0", "1.3.0", artifact), artifact, 0))
	roomy.engine.mu.Unlock()
	if admitted == nil || refused != nil {
		t.Fatalf("a newer, genuine release for a paired device was refused: %+v", refused)
	}
	want := []string{"manifest_size", "signature", "schema", "target", "version", "artifact", "cache"}
	for index, step := range admissionOrder {
		if index >= len(want) || step.name != want[index] {
			t.Fatalf("admission runs %q at step %d; the documented order is %v", step.name, index, want)
		}
	}
	if len(admissionOrder) != len(want) {
		t.Fatalf("admission has %d steps, the documented order has %d", len(admissionOrder), len(want))
	}
}

// offerOnce makes the fetcher ask one scripted peer and lets the exchange run.
func offerOnce(net *network, fetcher *gateway, seeder *scriptedSeeder) {
	fetcher.engine.Ask([]string{seeder.address})
	settle(net, fetcher)
}

// requireRefusedBeforeAnyPayload checks that an offer was turned away at the
// door: counted under its reason, with no table or chunk ever asked for and
// nothing on disk.
func requireRefusedBeforeAnyPayload(t *testing.T, net *network, fetcher *gateway, count func(Refused) int64, want int64) {
	t.Helper()
	status := fetcher.status()
	if got := count(status.Refused); got != want {
		t.Fatalf("expected %d refusals of this kind, counted %d: %+v", want, got, status.Refused)
	}
	if asked := net.sentOfType(fetcher.address, TypeTableGet) + net.sentOfType(fetcher.address, TypeChunkGet); asked != 0 {
		t.Fatalf("a refused offer must not cause a single payload request; %d were sent", asked)
	}
	if len(status.Downloads) != 0 || status.CacheBytes != 0 || len(fetcher.cacheEntries()) != 0 {
		t.Fatalf("a refused offer must leave no download behind: %+v, cache %v", status.Downloads, fetcher.cacheEntries())
	}
}

func TestForgedManifestsAreRefusedBeforeAnyPayloadAndCostThePeer(t *testing.T) {
	artifact := artifactBytes(7, testArtifact)
	signerSeed := byte(1)
	cases := map[string]func(t *testing.T, signer *publisher) []byte{
		"signed with another key under the trusted key id": func(t *testing.T, _ *publisher) []byte {
			return newPublisher(t, 99).release("r-1.1.0", "1.1.0", artifact)
		},
		"signed under a key id this gateway does not have": func(t *testing.T, _ *publisher) []byte {
			stranger := newPublisher(t, 99)
			stranger.keyID = "somebody-else-2026"
			return stranger.release("r-1.1.0", "1.1.0", artifact)
		},
		"genuinely signed, then altered": func(t *testing.T, signer *publisher) []byte {
			raw := signer.release("r-1.1.0", "1.1.0", artifact)
			altered := bytes.Replace(raw, []byte("1.1.0"), []byte("9.9.0"), 1)
			if bytes.Equal(raw, altered) {
				t.Fatal("the test did not alter the manifest")
			}
			return altered
		},
		"not a manifest at all": func(*testing.T, *publisher) []byte {
			return []byte(`{"hello":"world"}`)
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			net, shared, signer := newNetwork(t), newClock(), newPublisher(t, signerSeed)
			fetcher := newGateway(t, net, shared, "fetcher", signer)
			liar := newScriptedSeeder(t, net, "liar", "r-1.1.0", build(t, signer), artifact)

			offerOnce(net, fetcher, liar)
			requireRefusedBeforeAnyPayload(t, net, fetcher, func(r Refused) int64 { return r.Signature }, 1)
			requireNothingStored(t, fetcher)
			if peers := fetcher.status().Peers; peers.BackedOff != 1 || peers.BackedOffTotal != 1 {
				t.Fatalf("the peer that offered a forgery should be backed off: %+v", peers)
			}

			// While it is backed off it is not even asked.
			queries := net.sentOfType(fetcher.address, TypeQuery)
			offerOnce(net, fetcher, liar)
			if again := net.sentOfType(fetcher.address, TypeQuery); again != queries {
				t.Fatal("a peer that is backed off was asked again")
			}
			// After its rest it is asked again, refused again, and rested for
			// four times as long.
			shared.advance(61 * time.Minute)
			offerOnce(net, fetcher, liar)
			if refused := fetcher.status().Refused.Signature; refused != 2 {
				t.Fatalf("the second forgery should be refused too, counted %d", refused)
			}
			shared.advance(61 * time.Minute)
			queries = net.sentOfType(fetcher.address, TypeQuery)
			offerOnce(net, fetcher, liar)
			if again := net.sentOfType(fetcher.address, TypeQuery); again != queries {
				t.Fatal("a second strike must rest the peer for longer than the first")
			}
		})
	}
}

func TestOversizeManifestsAndArtifactsAreRefusedBeforeAnyPayload(t *testing.T) {
	artifact := artifactBytes(7, testArtifact)

	t.Run("manifest over the 32 KiB bound", func(t *testing.T) {
		net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
		fetcher := newGateway(t, net, shared, "fetcher", signer)
		// Genuinely signed, so only its size can be what refuses it.
		raw := signer.release("r-1.1.0", "1.1.0", artifact, func(m *proto.OTAManifest) {
			m.Notes = strings.Repeat("n", proto.OTAMaxManifestBytes)
		})
		offerOnce(net, fetcher, newScriptedSeeder(t, net, "big", "r-1.1.0", raw, artifact))
		requireRefusedBeforeAnyPayload(t, net, fetcher, func(r Refused) int64 { return r.ManifestSize }, 1)
		if fetcher.status().Refused.Signature != 0 {
			t.Fatal("an oversize manifest must be refused before its signature is checked")
		}
	})

	t.Run("artifact over the 32 MiB bound", func(t *testing.T) {
		net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
		fetcher := newGateway(t, net, shared, "fetcher", signer)
		raw := signer.release("r-1.1.0", "1.1.0", artifact, func(m *proto.OTAManifest) {
			m.Artifacts[0].Size = proto.OTAMaxArtifactBytes + 1
		})
		big := newScriptedSeeder(t, net, "big", "r-1.1.0", raw, artifact)
		big.totalSize, big.chunkSize = proto.OTAMaxArtifactBytes+1, MaxChunkSize
		offerOnce(net, fetcher, big)
		requireRefusedBeforeAnyPayload(t, net, fetcher, func(r Refused) int64 { return r.Artifact }, 1)
	})

	t.Run("an offer whose transfer does not describe the signed artifact", func(t *testing.T) {
		net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
		fetcher := newGateway(t, net, shared, "fetcher", signer)
		raw := signer.release("r-1.1.0", "1.1.0", artifact)
		for index, edit := range []func(*scriptedSeeder){
			func(s *scriptedSeeder) { s.totalSize = int64(len(artifact)) + 1 }, // another size than signed for
			func(s *scriptedSeeder) { s.chunkSize = MinChunkSize / 2 },         // chunks smaller than allowed
			func(s *scriptedSeeder) { s.chunkSize = MaxChunkSize * 2 },         // chunks larger than a message
		} {
			liar := newScriptedSeeder(t, net, "liar-"+string(rune('a'+index)), "r-1.1.0", raw, artifact)
			edit(liar)
			offerOnce(net, fetcher, liar)
			requireRefusedBeforeAnyPayload(t, net, fetcher, func(r Refused) int64 { return r.Artifact }, int64(index+1))
		}
	})
}

func TestDowngradesAndSameVersionOffersAreRefusedBeforeAnyPayload(t *testing.T) {
	artifact := artifactBytes(7, testArtifact)
	held := artifactBytes(8, 5000)

	offering := func(t *testing.T, id, version string, edits ...func(*proto.OTAManifest)) (*network, *gateway) {
		net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
		fetcher := newGateway(t, net, shared, "fetcher", signer,
			withNodes(PairedNode{Platform: "msm8916", AgentVersion: "1.2.4"}, PairedNode{Platform: "msm8916", AgentVersion: ""}))
		fetcher.hold(signer.release("r-1.2.0", "1.2.0", held), held)
		offerOnce(net, fetcher, newScriptedSeeder(t, net, "peer", id, signer.release(id, version, artifact, edits...), artifact))
		return net, fetcher
	}
	requireOnlyTheHeldRelease := func(t *testing.T, fetcher *gateway) {
		t.Helper()
		if releases := fetcher.store.Releases(); len(releases) != 1 || releases[0].ReleaseID != "r-1.2.0" {
			t.Fatalf("the store should hold only what it held before: %+v", releases)
		}
	}
	downgrade := func(r Refused) int64 { return r.Downgrade }

	t.Run("older than a stored release", func(t *testing.T) {
		net, fetcher := offering(t, "r-1.1.0", "1.1.0")
		requireRefusedBeforeAnyPayload(t, net, fetcher, downgrade, 1)
		requireOnlyTheHeldRelease(t, fetcher)
	})
	t.Run("the same version as a stored release, under another release id", func(t *testing.T) {
		net, fetcher := offering(t, "r-1.2.0-rebuild", "1.2.0")
		requireRefusedBeforeAnyPayload(t, net, fetcher, downgrade, 1)
		requireOnlyTheHeldRelease(t, fetcher)
	})
	t.Run("newer than stored but older than a paired device runs", func(t *testing.T) {
		net, fetcher := offering(t, "r-1.2.3", "1.2.3")
		requireRefusedBeforeAnyPayload(t, net, fetcher, downgrade, 1)
	})
	t.Run("a pre-release that would sort as its release", func(t *testing.T) {
		net, fetcher := offering(t, "r-1.5.0-rc1", "1.5.0-rc1")
		requireRefusedBeforeAnyPayload(t, net, fetcher, downgrade, 1)
	})
	t.Run("another manifest under a release id that is already stored", func(t *testing.T) {
		net, fetcher := offering(t, "r-1.2.0", "1.9.0")
		requireRefusedBeforeAnyPayload(t, net, fetcher, downgrade, 1)
		requireOnlyTheHeldRelease(t, fetcher)
	})
	t.Run("exactly what a paired device runs is not a downgrade", func(t *testing.T) {
		net, fetcher := offering(t, "r-1.2.4", "1.2.4")
		requireImported(t, fetcher, "r-1.2.4", artifact)
		if refused := fetcher.status().Refused.Downgrade; refused != 0 {
			t.Fatalf("refused %d times", refused)
		}
		_ = net
	})
	t.Run("a release already held is simply not fetched, and nobody is blamed", func(t *testing.T) {
		net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
		raw := signer.release("r-1.2.0", "1.2.0", artifact)
		seeder := newGateway(t, net, shared, "seeder", signer)
		seeder.hold(raw, artifact)
		fetcher := newGateway(t, net, shared, "fetcher", signer)
		fetcher.hold(raw, artifact)
		fetcher.engine.Ask([]string{seeder.address})
		settle(net, seeder, fetcher)
		status := fetcher.status()
		if status.Offers.AlreadyHeld != 1 || status.Refused != (Refused{}) || status.Peers.BackedOffTotal != 0 {
			t.Fatalf("an offer of what is already here is not a refusal: %+v", status)
		}
		if asked := net.sentOfType(fetcher.address, TypeTableGet); asked != 0 {
			t.Fatal("a release already held was fetched again")
		}
	})
}

func TestReleasesForOtherDevicesAreRefusedBeforeAnyPayload(t *testing.T) {
	artifact := artifactBytes(7, testArtifact)
	mock := func(m *proto.OTAManifest) { m.Platform = proto.PlatformMock }
	amd64 := func(m *proto.OTAManifest) { m.Artifacts[0].Arch = "amd64" }
	noArch := func(m *proto.OTAManifest) { m.Artifacts[0].Arch = "" }
	beta := func(m *proto.OTAManifest) { m.Channel = proto.OTAChannelBeta }

	run := func(t *testing.T, edit func(*proto.OTAManifest), options ...gatewayOption) (*network, *gateway) {
		net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
		fetcher := newGateway(t, net, shared, "fetcher", signer, options...)
		offerOnce(net, fetcher, newScriptedSeeder(t, net, "peer", "r-1.1.0", signer.release("r-1.1.0", "1.1.0", artifact, edit), artifact))
		return net, fetcher
	}
	platform := func(r Refused) int64 { return r.Platform }

	t.Run("a platform no paired device has", func(t *testing.T) {
		net, fetcher := run(t, mock)
		requireRefusedBeforeAnyPayload(t, net, fetcher, platform, 1)
	})
	t.Run("the right platform for another architecture", func(t *testing.T) {
		net, fetcher := run(t, amd64)
		requireRefusedBeforeAnyPayload(t, net, fetcher, platform, 1)
	})
	t.Run("the right platform with no architecture stated", func(t *testing.T) {
		net, fetcher := run(t, noArch)
		requireRefusedBeforeAnyPayload(t, net, fetcher, platform, 1)
	})
	t.Run("no paired device and no allowlist accepts nothing", func(t *testing.T) {
		net, fetcher := run(t, func(*proto.OTAManifest) {}, withNodes())
		requireRefusedBeforeAnyPayload(t, net, fetcher, platform, 1)
		if targets := fetcher.status().Targets; len(targets) != 0 {
			t.Fatalf("no targets expected, status lists %v", targets)
		}
	})
	t.Run("a revoked device does not count", func(t *testing.T) {
		net, fetcher := run(t, func(*proto.OTAManifest) {}, withNodes(PairedNode{Platform: "msm8916", Revoked: true}))
		requireRefusedBeforeAnyPayload(t, net, fetcher, platform, 1)
	})
	t.Run("the operator's allowlist admits a platform without a paired device", func(t *testing.T) {
		targets, dropped := ParseTargets("mock, MSM8916/arm64 ;; not-ok!,../x a/b/c")
		if len(targets) != 2 || len(dropped) != 3 || targets[1] != (Target{Platform: "msm8916", Arch: "arm64"}) {
			t.Fatalf("allowlist parsed as %v, dropped %v", targets, dropped)
		}
		_, fetcher := run(t, mock, withNodes(), withOptions(func(o *Options) { o.AllowedTargets = targets }))
		requireImported(t, fetcher, "r-1.1.0", artifact)
	})
	t.Run("a channel other than stable", func(t *testing.T) {
		net, fetcher := run(t, beta)
		requireRefusedBeforeAnyPayload(t, net, fetcher, func(r Refused) int64 { return r.Channel }, 1)
	})
	t.Run("another protocol generation", func(t *testing.T) {
		net, fetcher := run(t, func(m *proto.OTAManifest) { m.ProtocolMajor = proto.ProtocolMajor + 1 })
		requireRefusedBeforeAnyPayload(t, net, fetcher, func(r Refused) int64 { return r.Schema }, 1)
	})
}

// A peer that honestly holds an older release as well as a newer one is
// refused the older and still used for the newer.
func TestAnHonestPeerHoldingAnOlderReleaseIsStillUsedForTheNewerOne(t *testing.T) {
	net, shared, signer := newNetwork(t), newClock(), newPublisher(t, 1)
	newer, older := artifactBytes(7, testArtifact), artifactBytes(8, testArtifact)
	seeder := newGateway(t, net, shared, "seeder", signer)
	seeder.hold(signer.release("r-1.1.0", "1.1.0", newer), newer)
	seeder.hold(signer.release("r-1.0.5", "1.0.5", older), older)
	fetcher := newGateway(t, net, shared, "fetcher", signer, withNodes(PairedNode{Platform: "msm8916", AgentVersion: "1.0.9"}))

	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)

	requireImported(t, fetcher, "r-1.1.0", newer)
	status := fetcher.status()
	if status.Refused.Downgrade != 1 || status.Peers.BackedOffTotal != 0 {
		t.Fatalf("the older release should be refused without resting the peer: %+v", status)
	}
	if _, err := fetcher.store.Release("r-1.0.5"); err == nil {
		t.Fatal("the release older than the paired device runs was imported")
	}
	// The refused offer is remembered: asked again, it is refused again
	// without being fetched.
	shared.advance(2 * time.Minute)
	fetcher.engine.Ask([]string{seeder.address})
	settle(net, seeder, fetcher)
	if again := fetcher.status(); again.Refused.Downgrade != 2 || again.Offers.Tracked != 1 {
		t.Fatalf("the remembered refusal should be counted again: %+v", again)
	}
}
