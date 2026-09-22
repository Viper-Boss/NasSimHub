package ota

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/release"
	"github.com/human-agent65535/modemdeck/internal/updatecheck"
)

// An unsigned manifest must not be installable, whatever else is in order.
//
// This is the gate the whole exercise rests on, so it is asserted against the
// planner rather than against the loader: the loader is a wiring choice, and
// wiring the wrong one by mistake must cost update availability rather than
// integrity.
func TestAnUnverifiedManifestCannotProduceAnInstallPlan(t *testing.T) {
	manifest := testManifest(testDigest("d"))
	// Schema 2 is the old unsigned file: structurally valid, digests present,
	// and still refused because nothing vouched for it.
	manifest.SchemaVersion = 2

	runtime := &fakeRuntime{}
	controller := newTestController(t, runtime, manifest, testDigest("9"))

	result := controller.Check(context.Background())
	if result.ApplyAvailable {
		t.Fatal("an unsigned manifest produced an installable update")
	}
	if result.ErrorCode != "ota_manifest_unavailable" {
		t.Fatalf("unexpected error code %q", result.ErrorCode)
	}
	if _, err := controller.Apply(context.Background(), updatecheck.ApplyRequest{
		Version: "v2.0.0",
	}); err == nil {
		t.Fatal("Apply() accepted an unsigned manifest")
	}
}

// A first-party image with no pinned digest must not be installable either.
//
// Before this change an empty digest meant "resolve the tag from the registry
// at install time", so a re-pointed tag reached every gateway on its next
// update without the manifest changing. An empty digest now means refuse.
func TestAMissingDigestIsRefusedRatherThanResolved(t *testing.T) {
	for _, component := range []string{"api", "web", "hardware", "updater"} {
		t.Run(component, func(t *testing.T) {
			manifest := testManifest(testDigest("d"))
			switch component {
			case "api":
				manifest.Digests.API = ""
			case "web":
				manifest.Digests.Web = ""
			case "hardware":
				manifest.Digests.Hardware = ""
			case "updater":
				manifest.Digests.Updater = ""
			}
			runtime := &fakeRuntime{}
			controller := newTestController(t, runtime, manifest, testDigest("9"))
			// newTestController sets Digests.Hardware from its own argument, so
			// the hardware case is cleared again here.
			if component == "hardware" {
				manifest.Digests.Hardware = ""
				controller.manifests = fakeManifestLoader{manifest: manifest}
			}
			if controller.Check(context.Background()).ApplyAvailable {
				t.Fatalf("a manifest with no %s digest produced an installable update", component)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The loader.
// ---------------------------------------------------------------------------

type stubTransport struct {
	status int
	body   string
}

func (transport stubTransport) Do(*http.Request) (*http.Response, error) {
	status := transport.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       http.NoBody,
		Request:    nil,
	}, nil
}

type bodyTransport struct {
	body    string
	lastURL string
}

func (transport *bodyTransport) Do(request *http.Request) (*http.Response, error) {
	transport.lastURL = request.URL.String()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       readCloser{strings.NewReader(transport.body)},
	}, nil
}

type readCloser struct{ *strings.Reader }

func (readCloser) Close() error { return nil }

func signedDocument(t *testing.T, version string, private ed25519.PrivateKey) string {
	t.Helper()
	manifest := release.Manifest{
		SchemaVersion:  release.SchemaVersion,
		ReleaseVersion: version,
		CreatedAt:      1790000000,
		Images: []release.Image{
			{Name: "api", Repository: "ghcr.io/viper-boss/nassimhub",
				Tag: version, Digest: testDigest("a")},
			{Name: "web", Repository: "ghcr.io/viper-boss/nassimhub-web",
				Tag: version, Digest: testDigest("b")},
			{Name: "hardware", Repository: "ghcr.io/viper-boss/nassimhub-hardware",
				Tag: version, Digest: testDigest("9")},
			{Name: "updater", Repository: "ghcr.io/viper-boss/nassimhub-updater",
				Tag: version, Digest: testDigest("c")},
		},
	}
	signed, err := release.Sign(manifest, private)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	encoded, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestTheLoaderAcceptsOnlyAVerifiedManifest(t *testing.T) {
	private := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	public, _ := private.Public().(ed25519.PublicKey)
	publicBase64 := base64.StdEncoding.EncodeToString(public)

	document := signedDocument(t, "v2.0.0", private)

	t.Run("genuine", func(t *testing.T) {
		transport := &bodyTransport{body: document}
		loader := NewSignedManifestLoader("https://example.invalid/%s.json", publicBase64, transport)
		manifest, err := loader.Load(context.Background(), "v2.0.0")
		if err != nil {
			t.Fatalf("a genuine manifest was rejected: %v", err)
		}
		if !manifest.Signed() {
			t.Fatal("a verified manifest did not report itself signed")
		}
		if manifest.Digests.API != testDigest("a") {
			t.Fatalf("api digest not carried through: %q", manifest.Digests.API)
		}
		if transport.lastURL != "https://example.invalid/v2.0.0.json" {
			t.Fatalf("unexpected URL %q", transport.lastURL)
		}
	})

	t.Run("tampered", func(t *testing.T) {
		tampered := strings.Replace(document, strings.Repeat("a", 64), strings.Repeat("e", 64), 1)
		loader := NewSignedManifestLoader("https://example.invalid/%s.json", publicBase64,
			&bodyTransport{body: tampered})
		if _, err := loader.Load(context.Background(), "v2.0.0"); err == nil {
			t.Fatal("a tampered manifest loaded")
		}
	})

	t.Run("signed by another key", func(t *testing.T) {
		elsewhere := ed25519.NewKeyFromSeed([]byte(strings.Repeat("z", ed25519.SeedSize)))
		loader := NewSignedManifestLoader("https://example.invalid/%s.json", publicBase64,
			&bodyTransport{body: signedDocument(t, "v2.0.0", elsewhere)})
		if _, err := loader.Load(context.Background(), "v2.0.0"); err == nil {
			t.Fatal("a manifest signed by another key loaded")
		}
	})

	// A genuinely signed manifest for a DIFFERENT release must not be accepted
	// as this one. This is the replay a signature alone does not stop: serving
	// last month's properly signed manifest to pin a gateway to an old build.
	t.Run("genuine manifest for another version", func(t *testing.T) {
		loader := NewSignedManifestLoader("https://example.invalid/%s.json", publicBase64,
			&bodyTransport{body: signedDocument(t, "v1.0.0", private)})
		_, err := loader.Load(context.Background(), "v2.0.0")
		if err == nil {
			t.Fatal("a signed manifest for another version was accepted")
		}
		if !strings.Contains(err.Error(), "but v2.0.0 was requested") {
			t.Fatalf("unhelpful error for a version mismatch: %v", err)
		}
	})

	t.Run("http error", func(t *testing.T) {
		loader := NewSignedManifestLoader("https://example.invalid/%s.json", publicBase64,
			stubTransport{status: http.StatusNotFound})
		if _, err := loader.Load(context.Background(), "v2.0.0"); err == nil {
			t.Fatal("a 404 produced a manifest")
		}
	})

	t.Run("unstable version is never fetched", func(t *testing.T) {
		transport := &bodyTransport{body: document}
		loader := NewSignedManifestLoader("https://example.invalid/%s.json", publicBase64, transport)
		if _, err := loader.Load(context.Background(), "not-a-version"); err == nil {
			t.Fatal("an unstable version string was fetched")
		}
		if transport.lastURL != "" {
			t.Fatalf("a request went out for %q", transport.lastURL)
		}
	})
}

// A manifest missing a component must be refused outright rather than
// installing the components it does name: a gateway running a mixture of two
// releases is a configuration nobody tested.
func TestAManifestMissingAComponentIsRefused(t *testing.T) {
	private := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	public, _ := private.Public().(ed25519.PublicKey)

	manifest := release.Manifest{
		SchemaVersion:  release.SchemaVersion,
		ReleaseVersion: "v2.0.0",
		CreatedAt:      1790000000,
		Images: []release.Image{
			{Name: "api", Repository: "ghcr.io/viper-boss/nassimhub",
				Tag: "v2.0.0", Digest: testDigest("a")},
		},
	}
	signed, err := release.Sign(manifest, private)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(signed)
	loader := NewSignedManifestLoader("https://example.invalid/%s.json",
		base64.StdEncoding.EncodeToString(public), &bodyTransport{body: string(encoded)})
	_, err = loader.Load(context.Background(), "v2.0.0")
	if err == nil {
		t.Fatal("a manifest naming only one component was accepted")
	}
	if !strings.Contains(err.Error(), "names no") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

// The repository allow-list still applies behind the signature.
func TestASignedManifestCannotRedirectToAnotherRegistry(t *testing.T) {
	private := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	public, _ := private.Public().(ed25519.PublicKey)

	manifest := release.Manifest{
		SchemaVersion:  release.SchemaVersion,
		ReleaseVersion: "v2.0.0",
		CreatedAt:      1790000000,
		Images: []release.Image{
			{Name: "api", Repository: "docker.io/attacker/modemdeck",
				Tag: "v2.0.0", Digest: testDigest("a")},
			{Name: "web", Repository: "ghcr.io/viper-boss/nassimhub-web",
				Tag: "v2.0.0", Digest: testDigest("b")},
			{Name: "hardware", Repository: "ghcr.io/viper-boss/nassimhub-hardware",
				Tag: "v2.0.0", Digest: testDigest("9")},
			{Name: "updater", Repository: "ghcr.io/viper-boss/nassimhub-updater",
				Tag: "v2.0.0", Digest: testDigest("c")},
		},
	}
	signed, err := release.Sign(manifest, private)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(signed)
	loader := NewSignedManifestLoader("https://example.invalid/%s.json",
		base64.StdEncoding.EncodeToString(public), &bodyTransport{body: string(encoded)})
	if _, err := loader.Load(context.Background(), "v2.0.0"); err == nil {
		t.Fatal("a validly signed manifest redirected the api image to another registry")
	}
}
