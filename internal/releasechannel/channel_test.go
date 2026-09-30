package releasechannel

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/release"
	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

// Real signatures throughout, with a key made for the test. A channel test
// that stubbed verification would be testing a channel that trusts its
// mirrors, which is the one thing this one must not do.
func testKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 7)
	}
	private := ed25519.NewKeyFromSeed(seed)
	public := private.Public().(ed25519.PublicKey)
	return private, base64.StdEncoding.EncodeToString(public)
}

func digest(character string) string { return "sha256:" + strings.Repeat(character, 64) }

func signedDocument(t *testing.T, key ed25519.PrivateKey, version, minimum string) []byte {
	t.Helper()
	var images []release.Image
	for index, component := range release.Components() {
		images = append(images, release.Image{
			Name:       component,
			Repository: release.Repositories[component],
			Tag:        version,
			Digest:     digest(string("abcd"[index])),
		})
	}
	signed, err := release.Sign(release.Manifest{
		SchemaVersion:  release.SchemaVersion,
		ReleaseVersion: version,
		CreatedAt:      1_790_000_000,
		MinimumVersion: minimum,
		Images:         images,
	}, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	document, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return document
}

func serve(t *testing.T, body []byte, status int, hits *int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		writer.WriteHeader(status)
		_, _ = writer.Write(body)
	}))
	t.Cleanup(server.Close)
	return server
}

func newChannel(publicKey string, current string, urls ...string) *Channel {
	return New(Options{
		URLs:            urls,
		PublicKeyBase64: publicKey,
		CurrentVersion:  func() string { return current },
		Now:             func() time.Time { return time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC) },
	})
}

func TestTheHighestVerifiedVersionWinsOverAStaleMirror(t *testing.T) {
	key, public := testKey(t)
	stale := serve(t, signedDocument(t, key, "v1.2.0", ""), http.StatusOK, nil)
	fresh := serve(t, signedDocument(t, key, "v1.3.0", ""), http.StatusOK, nil)

	channel := newChannel(public, "v1.1.0", stale.URL, fresh.URL)
	result := channel.Check(context.Background())

	if result.Status != updatecheck.StatusUpdateAvailable || result.LatestVersion != "v1.3.0" {
		t.Fatalf("result = %+v; want v1.3.0 available", result)
	}
	if len(result.Sources) != 2 ||
		result.Sources[0].Version != "v1.2.0" || result.Sources[1].Version != "v1.3.0" {
		t.Fatalf("sources = %+v; want both reported in configured order", result.Sources)
	}
	manifest, err := channel.Latest("v1.3.0")
	if err != nil || manifest.ReleaseVersion != "v1.3.0" {
		t.Fatalf("Latest() = %+v, %v", manifest, err)
	}
}

// A mirror that serves a document signed by some other key is reported, not
// believed -- and does not prevent an honest mirror from being used.
func TestAForgedManifestIsReportedAndIgnored(t *testing.T) {
	key, public := testKey(t)
	_, otherPrivate, _ := ed25519.GenerateKey(nil)
	forged := serve(t, signedDocument(t, otherPrivate, "v9.9.9", ""), http.StatusOK, nil)
	honest := serve(t, signedDocument(t, key, "v1.3.0", ""), http.StatusOK, nil)

	result := newChannel(public, "v1.1.0", forged.URL, honest.URL).Check(context.Background())
	if result.LatestVersion != "v1.3.0" {
		t.Fatalf("latest = %q; a forged v9.9.9 must not win", result.LatestVersion)
	}
	if result.Sources[0].State != updatecheck.SourceInvalidSignature {
		t.Fatalf("forged source = %+v", result.Sources[0])
	}
}

func TestOnlyForgeriesMeansUnavailableAndSaysWhy(t *testing.T) {
	_, public := testKey(t)
	_, otherPrivate, _ := ed25519.GenerateKey(nil)
	forged := serve(t, signedDocument(t, otherPrivate, "v9.9.9", ""), http.StatusOK, nil)
	down := serve(t, nil, http.StatusNotFound, nil)

	result := newChannel(public, "v1.1.0", forged.URL, down.URL).Check(context.Background())
	if result.Status != updatecheck.StatusUnavailable || result.ErrorCode != "release_signature_invalid" {
		t.Fatalf("result = %+v", result)
	}
	if result.LatestVersion != "" {
		t.Fatal("an unverified version leaked into the result")
	}
	if _, err := newChannel(public, "v1.1.0", forged.URL).Latest("v9.9.9"); !errors.Is(err, ErrNoVerifiedRelease) {
		t.Fatalf("Latest() error = %v", err)
	}
}

func TestAReleaseWithAFloorAboveThisInstallationCannotBeInstalled(t *testing.T) {
	key, public := testKey(t)
	server := serve(t, signedDocument(t, key, "v2.0.0", "v1.5.0"), http.StatusOK, nil)
	channel := newChannel(public, "v1.1.0", server.URL)

	result := channel.Check(context.Background())
	if result.Status != updatecheck.StatusUpdateAvailable ||
		result.ErrorCode != "release_requires_newer_installation" {
		t.Fatalf("result = %+v", result)
	}
	if _, err := channel.Latest("v2.0.0"); !errors.Is(err, ErrBelowMinimum) {
		t.Fatalf("Latest() error = %v; want ErrBelowMinimum", err)
	}
}

func TestResultsAreCachedUntilARefreshIsAsked(t *testing.T) {
	key, public := testKey(t)
	var hits int32
	server := serve(t, signedDocument(t, key, "v1.3.0", ""), http.StatusOK, &hits)
	channel := newChannel(public, "v1.1.0", server.URL)

	channel.Check(context.Background())
	channel.Check(context.Background())
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("mirror asked %d times; want 1 (cached)", hits)
	}
	channel.Check(updatecheck.WithRefresh(context.Background()))
	if atomic.LoadInt32(&hits) != 2 {
		t.Fatalf("mirror asked %d times after refresh; want 2", hits)
	}
}

func TestSourcesNameTheHostOnly(t *testing.T) {
	if got := hostOf("https://user:secret@mirror.example.cn/x/stable.signed.json?token=abc"); got != "mirror.example.cn" {
		t.Fatalf("hostOf() = %q; credentials or path must never be shown", got)
	}
}

func TestParseURLsKeepsOnlyWebAddresses(t *testing.T) {
	kept, dropped := ParseURLs("https://a.example/x.json, http://192.168.1.2/s.json\nfile:///etc/passwd ;nonsense")
	if len(kept) != 2 || len(dropped) != 2 {
		t.Fatalf("kept = %q, dropped = %q", kept, dropped)
	}
}

// The compiled-in key is the default. A channel that fell back to "no key"
// would reject every release forever and look exactly like a network outage.
func TestTheDefaultKeyIsTheCompiledInOne(t *testing.T) {
	channel := New(Options{})
	if channel.publicKey != release.SigningPublicKey || channel.publicKey == "" {
		t.Fatalf("default key = %q", channel.publicKey)
	}
	if len(channel.urls) != len(DefaultURLs) {
		t.Fatalf("default urls = %q", channel.urls)
	}
}

// The cached answer is for the version that was running when it was asked.
// Right after an install the running version changes underneath the updater;
// a cache that ignored that would keep offering the release just installed
// for the rest of its six hours.
func TestTheCacheIsForTheVersionThatWasRunning(t *testing.T) {
	key, public := testKey(t)
	var hits int32
	server := serve(t, signedDocument(t, key, "v1.3.0", ""), http.StatusOK, &hits)
	current := "v1.1.0"
	channel := New(Options{
		URLs:            []string{server.URL},
		PublicKeyBase64: public,
		CurrentVersion:  func() string { return current },
	})
	if result := channel.Check(context.Background()); result.Status != updatecheck.StatusUpdateAvailable {
		t.Fatalf("before install = %+v", result)
	}
	current = "v1.3.0"
	if result := channel.Check(context.Background()); result.Status != updatecheck.StatusUpToDate {
		t.Fatalf("after install = %s; the just-installed release is still on offer", result.Status)
	}
}

// The dashboard shows which key this gateway verifies releases against, in the
// console's own form, so the operator can match the two by eye. It is reported
// whether or not any mirror answered: the key is compiled in, not fetched.
func TestTheSigningKeyIsReportedInTheConsolesForm(t *testing.T) {
	_, public := testKey(t)
	decoded, _ := base64.StdEncoding.DecodeString(public)
	want := hex.EncodeToString(decoded[:4])

	down := serve(t, nil, http.StatusNotFound, nil)
	if got := newChannel(public, "v1.1.0", down.URL).Check(context.Background()).SigningKey; got != want {
		t.Fatalf("signing key = %q with no mirror reachable; want %q", got, want)
	}
	if got := keyFingerprint("not a key"); got != "" {
		t.Fatalf("a malformed key produced a fingerprint %q", got)
	}
}
