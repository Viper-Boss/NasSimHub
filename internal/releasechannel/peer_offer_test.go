package releasechannel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/release"
	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

func sourceOf(t *testing.T, result updatecheck.Result, kind string) updatecheck.Source {
	t.Helper()
	for _, source := range result.Sources {
		if source.Kind == kind {
			return source
		}
	}
	t.Fatalf("no %s source in %+v", kind, result.Sources)
	return updatecheck.Source{}
}

func TestAPeerOfferCompetesUnderTheSameRuleAsTheMirrors(t *testing.T) {
	key, public := testKey(t)
	mirror := serve(t, signedDocument(t, key, "v1.2.0", ""), http.StatusOK, nil)
	channel := newChannel(public, "v1.1.0", mirror.URL)

	if got := channel.Check(context.Background()); got.LatestVersion != "v1.2.0" {
		t.Fatalf("latest = %q before any peer spoke", got.LatestVersion)
	}
	newer := signedDocument(t, key, "v1.3.0", "")
	source, err := channel.Offer(updatecheck.SourceKindNKN, newer, 3)
	if err != nil {
		t.Fatal(err)
	}
	if source.State != updatecheck.SourceVerified || source.Version != "v1.3.0" || source.Peers != 3 {
		t.Fatalf("offer source = %+v", source)
	}
	// The cached answer changes at once: a peer telling us about a release is
	// not something to sit on for the six hours a mirror result is cached.
	result := channel.Check(context.Background())
	if result.LatestVersion != "v1.3.0" || result.Status != updatecheck.StatusUpdateAvailable {
		t.Fatalf("result after the offer = %+v", result)
	}
	if nkn := sourceOf(t, result, updatecheck.SourceKindNKN); nkn.Peers != 3 {
		t.Fatalf("nkn source = %+v", nkn)
	}
	if mirrorSource := sourceOf(t, result, updatecheck.SourceKindMirror); mirrorSource.Version != "v1.2.0" {
		t.Fatalf("mirror source = %+v", mirrorSource)
	}
	// The install plan comes from that same verified document, and what this
	// gateway shares is those exact bytes.
	if _, err := channel.Latest("v1.3.0"); err != nil {
		t.Fatalf("Latest(v1.3.0) = %v", err)
	}
	if !bytes.Equal(channel.Signed(), newer) {
		t.Fatal("the shared document is not the one that won")
	}
}

func TestAPeerCannotHoldAGatewayBack(t *testing.T) {
	key, public := testKey(t)
	mirror := serve(t, signedDocument(t, key, "v1.3.0", ""), http.StatusOK, nil)
	channel := newChannel(public, "v1.1.0", mirror.URL)
	channel.Check(context.Background())
	if _, err := channel.Offer(updatecheck.SourceKindDHT, signedDocument(t, key, "v1.2.0", ""), 5); err != nil {
		t.Fatal(err)
	}
	result := channel.Check(context.Background())
	if result.LatestVersion != "v1.3.0" {
		t.Fatalf("an older genuine release from peers won: %+v", result)
	}
	// Reported as what it is, not hidden.
	if dht := sourceOf(t, result, updatecheck.SourceKindDHT); dht.Version != "v1.2.0" || dht.State != updatecheck.SourceVerified {
		t.Fatalf("dht source = %+v", dht)
	}
}

func TestAForgedPeerOfferIsRefused(t *testing.T) {
	key, public := testKey(t)
	mirror := serve(t, signedDocument(t, key, "v1.2.0", ""), http.StatusOK, nil)
	channel := newChannel(public, "v1.1.0", mirror.URL)
	channel.Check(context.Background())

	// Signed by somebody else, and claiming a higher version.
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 99
	forger := ed25519.NewKeyFromSeed(seed)
	forged := signedDocument(t, forger, "v9.9.9", "")
	source, err := channel.Offer(updatecheck.SourceKindNKN, forged, 50)
	if err != nil {
		t.Fatal(err)
	}
	if source.State != updatecheck.SourceInvalidSignature || source.Peers != 0 || source.Version != "" {
		t.Fatalf("forged offer = %+v", source)
	}
	result := channel.Check(context.Background())
	if result.LatestVersion != "v1.2.0" {
		t.Fatalf("a forged manifest from peers moved the answer: %+v", result)
	}
	if _, err := channel.Latest("v9.9.9"); err == nil {
		t.Fatal("a forged version can be installed")
	}
	if bytes.Equal(channel.Signed(), forged) {
		t.Fatal("this gateway would pass a forgery on")
	}

	// A document that is not one at all.
	junk, _ := channel.Offer(updatecheck.SourceKindDHT, []byte(`{"hello":"world"}`), 1)
	if junk.State != updatecheck.SourceMalformed {
		t.Fatalf("junk offer = %+v", junk)
	}
	oversized, _ := channel.Offer(updatecheck.SourceKindDHT, bytes.Repeat([]byte(" "), release.MaxManifestBytes+1), 1)
	if oversized.State != updatecheck.SourceMalformed {
		t.Fatalf("oversized offer = %+v", oversized)
	}
	// A genuine manifest with a tail: the manifest alone would verify, and
	// the whole thing would then be kept and passed on to other gateways --
	// tail included, and over the size every other gateway refuses.
	padded := append(signedDocument(t, key, "v1.4.0", ""), bytes.Repeat([]byte(" "), release.MaxManifestBytes)...)
	tail, _ := channel.Offer(updatecheck.SourceKindDHT, padded, 1)
	if tail.State != updatecheck.SourceMalformed {
		t.Fatalf("a manifest with an oversized tail = %+v", tail)
	}
}

func TestAPeerNetworkThatStayedSilentSaysSo(t *testing.T) {
	key, public := testKey(t)
	mirror := serve(t, signedDocument(t, key, "v1.2.0", ""), http.StatusOK, nil)
	channel := newChannel(public, "v1.2.0", mirror.URL)
	channel.Check(context.Background())
	if _, err := channel.Offer(updatecheck.SourceKindNKN, nil, 0); err != nil {
		t.Fatal(err)
	}
	nkn := sourceOf(t, channel.Check(context.Background()), updatecheck.SourceKindNKN)
	if nkn.State != updatecheck.SourceUnreachable || nkn.Version != "" || nkn.Peers != 0 {
		t.Fatalf("silent network = %+v", nkn)
	}
	if _, err := channel.Offer("gitee", nil, 0); err == nil {
		t.Fatal("an unknown peer source was accepted")
	}
}

func TestPeersAloneCanCarryARelease(t *testing.T) {
	key, public := testKey(t)
	// Every mirror unreachable -- the case the peer networks exist for.
	dead := serve(t, nil, http.StatusBadGateway, nil)
	channel := newChannel(public, "v1.1.0", dead.URL)
	// Offered before the first check: kept, and used when it runs.
	if _, err := channel.Offer(updatecheck.SourceKindNKN, signedDocument(t, key, "v1.2.0", ""), 1); err != nil {
		t.Fatal(err)
	}
	result := channel.Check(context.Background())
	if result.LatestVersion != "v1.2.0" || result.Status != updatecheck.StatusUpdateAvailable || result.ErrorCode != "" {
		t.Fatalf("result with only a peer = %+v", result)
	}
}

func TestAReSignedReleaseReplacesTheEarlierSignature(t *testing.T) {
	key, public := testKey(t)
	first := signedDocument(t, key, "v1.2.0", "")
	mirror := serve(t, first, http.StatusOK, nil)
	channel := newChannel(public, "v1.1.0", mirror.URL)
	channel.Check(context.Background())

	var signed release.Signed
	if err := json.Unmarshal(first, &signed); err != nil {
		t.Fatal(err)
	}
	later := signed.Manifest
	later.CreatedAt += 3600
	resigned, err := release.Sign(later, key)
	if err != nil {
		t.Fatal(err)
	}
	document, _ := json.Marshal(resigned)
	if _, err := channel.Offer(updatecheck.SourceKindNKN, document, 1); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(channel.Signed(), document) {
		t.Fatal("the later signature of the same release did not win")
	}
}
