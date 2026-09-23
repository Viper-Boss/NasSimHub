package clientreport

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/license"
	"github.com/human-agent65535/modemdeck/internal/release"
)

const (
	testMachine = "machine-1"
	testNonce   = "nonce-abcdef"
)

func testKeys(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey, string) {
	t.Helper()
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x07}, ed25519.SeedSize))
	public, ok := private.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("no public half")
	}
	return private, public, base64.StdEncoding.EncodeToString(public)
}

func TestAGenuineRequestVerifies(t *testing.T) {
	private, _, public := testKeys(t)
	now := time.Unix(1790000000, 0)
	request, err := Sign(testNonce, testMachine, now, private)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := Verify(request, testMachine, public, now.Add(2*time.Second)); err != nil {
		t.Fatalf("a request we just signed was refused: %v", err)
	}
}

// Nothing unsigned gets in. This is the property the whole package exists for:
// before it, the identity endpoint answered anyone, and extending that to
// versions and entitlements would have been a disclosure.
func TestAnUnsignedOrForgedRequestIsRefused(t *testing.T) {
	private, _, public := testKeys(t)
	now := time.Unix(1790000000, 0)
	genuine, err := Sign(testNonce, testMachine, now, private)
	if err != nil {
		t.Fatal(err)
	}

	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
	fromElsewhere, err := Sign(testNonce, testMachine, now, other)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]Request{
		"no signature at all": func() Request {
			r := genuine
			r.Signature = ""
			return r
		}(),
		"signed by another key": fromElsewhere,
		"not base64": func() Request {
			r := genuine
			r.Signature = "!!!!"
			return r
		}(),
		"right length, wrong bytes": func() Request {
			r := genuine
			r.Signature = base64.StdEncoding.EncodeToString(
				bytes.Repeat([]byte{1}, ed25519.SignatureSize))
			return r
		}(),
	}
	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Verify(request, testMachine, public, now); err == nil {
				t.Fatal("an unauthenticated request was accepted")
			}
		})
	}
}

// Every signed field must be covered. A signature over only some of them would
// let a captured request be retargeted or back-dated.
func TestAlteringAnySignedFieldFailsVerification(t *testing.T) {
	private, _, public := testKeys(t)
	now := time.Unix(1790000000, 0)
	genuine, err := Sign(testNonce, testMachine, now, private)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("nonce", func(t *testing.T) {
		altered := genuine
		altered.Nonce = "nonce-different"
		if err := Verify(altered, testMachine, public, now); err == nil {
			t.Fatal("the nonce is not covered by the signature")
		}
	})
	t.Run("issued at", func(t *testing.T) {
		altered := genuine
		altered.IssuedAt = now.Add(time.Minute).Unix()
		if err := Verify(altered, testMachine, public, now.Add(time.Minute)); err == nil {
			t.Fatal("the timestamp is not covered by the signature")
		}
	})
	t.Run("machine id", func(t *testing.T) {
		altered := genuine
		altered.MachineID = "machine-2"
		// Verified AS machine-2, so the binding check passes and only the
		// signature can catch this.
		if err := Verify(altered, "machine-2", public, now); err == nil {
			t.Fatal("the machine id is not covered by the signature")
		}
	})
}

// A request signed for one gateway must not work on another.
//
// This is what limits the damage of a captured request: it is worth one
// gateway for five minutes, rather than every gateway forever.
func TestARequestForAnotherGatewayIsRefused(t *testing.T) {
	private, _, public := testKeys(t)
	now := time.Unix(1790000000, 0)
	request, err := Sign(testNonce, "machine-1", now, private)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(request, "machine-2", public, now); !errors.Is(err, ErrWrongMachine) {
		t.Fatalf("a request for another gateway was answered: %v", err)
	}
}

// A gateway that cannot read its own identity cannot confirm the binding, so it
// must refuse rather than skip the check.
func TestAGatewayWithNoIdentityRefuses(t *testing.T) {
	private, _, public := testKeys(t)
	now := time.Unix(1790000000, 0)
	request, err := Sign(testNonce, testMachine, now, private)
	if err != nil {
		t.Fatal(err)
	}
	for _, machineID := range []string{"", "   "} {
		if err := Verify(request, machineID, public, now); !errors.Is(err, ErrWrongMachine) {
			t.Fatalf("a gateway with no identity answered: %v", err)
		}
	}
}

func TestAStaleOrFutureRequestIsRefused(t *testing.T) {
	private, _, public := testKeys(t)
	signedAt := time.Unix(1790000000, 0)
	request, err := Sign(testNonce, testMachine, signedAt, private)
	if err != nil {
		t.Fatal(err)
	}
	for name, now := range map[string]time.Time{
		"too old":       signedAt.Add(FreshnessWindow + time.Second),
		"too far ahead": signedAt.Add(-FreshnessWindow - time.Second),
	} {
		t.Run(name, func(t *testing.T) {
			if err := Verify(request, testMachine, public, now); !errors.Is(err, ErrStale) {
				t.Fatalf("a request outside the window was answered: %v", err)
			}
		})
	}
	// Just inside, both directions.
	for name, now := range map[string]time.Time{
		"at the old edge":   signedAt.Add(FreshnessWindow - time.Second),
		"at the ahead edge": signedAt.Add(-FreshnessWindow + time.Second),
	} {
		t.Run(name, func(t *testing.T) {
			if err := Verify(request, testMachine, public, now); err != nil {
				t.Fatalf("a request inside the window was refused: %v", err)
			}
		})
	}
}

func TestNoUsableKeyMeansNoAnswer(t *testing.T) {
	private, _, _ := testKeys(t)
	now := time.Unix(1790000000, 0)
	request, err := Sign(testNonce, testMachine, now, private)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "abc", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if err := Verify(request, testMachine, key, now); err == nil {
			t.Fatalf("a gateway with key %q answered", key)
		}
	}
}

// A value carrying a separator could be re-split by a reader, giving two
// different requests one signature.
func TestSeparatorsInFieldsAreRefused(t *testing.T) {
	private, _, _ := testKeys(t)
	now := time.Unix(1790000000, 0)
	for _, nonce := range []string{"nonce\nmachine_id=x", "nonce=abcdef"} {
		if _, err := Sign(nonce, testMachine, now, private); err == nil {
			t.Fatalf("a nonce containing a separator was signed: %q", nonce)
		}
	}
	if _, err := Sign(testNonce, "machine\nid", now, private); err == nil {
		t.Fatal("a machine id containing a newline was signed")
	}
}

// ---------------------------------------------------------------------------
// Three uses of one key.
// ---------------------------------------------------------------------------

// TestTheThreeSignatureDomainsAreMutuallyExclusive is the test that keeps one
// key safe across three protocols.
//
// The issuer key signs licence certificates, release manifests, and these
// requests. A signature is only ever a signature over bytes, so if any two of
// those encodings could produce the same byte string, a signature obtained for
// one purpose would be valid for another -- a customer's own certificate
// becoming a release manifest, or a report request becoming a certificate.
//
// The exclusion is structural: each encoding's FIRST LINE is unique to it, and
// each verifier requires its own. Asserted here for every pairing rather than
// trusted, because these three constants live in three packages and nothing
// else would notice if two of them converged.
func TestTheThreeSignatureDomainsAreMutuallyExclusive(t *testing.T) {
	private, publicKey, public := testKeys(t)
	now := time.Unix(1790000000, 0)

	// One signed object of each kind, all with the same key.
	request, err := Sign(testNonce, testMachine, now, private)
	if err != nil {
		t.Fatal(err)
	}
	manifest := release.Manifest{
		SchemaVersion:  release.SchemaVersion,
		ReleaseVersion: "v1.0.0",
		CreatedAt:      now.Unix(),
		Images: []release.Image{{
			Name: "api", Repository: release.RepositoryAPI,
			Tag: "v1.0.0", Digest: "sha256:" + strings.Repeat("a", 64),
		}},
	}
	signedManifest, err := release.Sign(manifest, private)
	if err != nil {
		t.Fatal(err)
	}
	certificate := license.Certificate{
		Version:   license.CurrentVersion,
		ID:        "cert-1",
		Scope:     license.ScopeMachine,
		MachineID: testMachine,
		Features:  []string{"call.dial"},
		IssuedAt:  now.Unix(),
	}
	signedCertificate, err := license.Sign(certificate, private)
	if err != nil {
		t.Fatal(err)
	}

	// Each verifier must reject the other two signatures.
	t.Run("a manifest signature is not a report request", func(t *testing.T) {
		borrowed := request
		borrowed.Signature = signedManifest.Signature
		if err := Verify(borrowed, testMachine, public, now); err == nil {
			t.Fatal("a release manifest signature authenticated a report request")
		}
	})
	t.Run("a certificate signature is not a report request", func(t *testing.T) {
		borrowed := request
		borrowed.Signature = signedCertificate.Signature
		if err := Verify(borrowed, testMachine, public, now); err == nil {
			t.Fatal("a licence certificate signature authenticated a report request")
		}
	})
	t.Run("a report signature is not a manifest", func(t *testing.T) {
		borrowed := signedManifest
		borrowed.Signature = request.Signature
		if _, err := release.Verify(borrowed, public); err == nil {
			t.Fatal("a report request signature verified a release manifest")
		}
	})
	t.Run("a report signature is not a certificate", func(t *testing.T) {
		borrowed := signedCertificate
		borrowed.Signature = request.Signature
		if err := license.Verify(borrowed, publicKey, now); err == nil {
			t.Fatal("a report request signature verified a licence certificate")
		}
	})

	// And the property all four rest on, asserted directly so a future change
	// to any encoding fails here with a readable reason.
	message, err := request.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(message, []byte(Domain+"\n")) {
		t.Fatalf("report request bytes do not start with their domain: %q", firstLine(message))
	}
	manifestBytes, err := manifest.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(manifestBytes, []byte(release.Domain+"\n")) {
		t.Fatalf("manifest bytes do not start with their domain: %q", firstLine(manifestBytes))
	}
	if Domain == release.Domain {
		t.Fatal("two protocols share one domain string")
	}
}

func firstLine(value []byte) string {
	if index := bytes.IndexByte(value, '\n'); index >= 0 {
		return string(value[:index])
	}
	return string(value)
}

// The signed bytes are pinned, so a refactor that changed the encoding cannot
// pass by recomputing both sides from the new form.
func TestTheCanonicalEncodingIsPinned(t *testing.T) {
	request := Request{
		Type:      Type,
		Version:   Version,
		Nonce:     testNonce,
		MachineID: testMachine,
		IssuedAt:  1790000000,
	}
	const expected = "nsh-domain=client-report-request-v1\n" +
		"nonce=nonce-abcdef\n" +
		"machine_id=machine-1\n" +
		"issued_at=1790000000\n"
	message, err := request.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if string(message) != expected {
		t.Fatalf("the canonical encoding changed.\n got:\n%s\nwant:\n%s", message, expected)
	}

	// And the signature over it, with the seed testKeys uses. The issuing
	// console carries this exact vector (internal/report in nassimhub-license):
	// it is the only thing that proves the console signs the bytes this
	// package verifies, since the two cannot import each other.
	private, _, _ := testKeys(t)
	signed, err := Sign(testNonce, testMachine, time.Unix(1790000000, 0), private)
	if err != nil {
		t.Fatal(err)
	}
	const pinnedSignature = "CuOsDpSDU6Iiwo4ves+emex1uBGQC0AJ7OH9dNoB4u3jTrS0xBvDd+FsknXMREnurz1mPSoTNSQ2mGPfohuvBA=="
	if signed.Signature != pinnedSignature {
		t.Fatalf("the signature over the pinned vector changed.\n got: %s\nwant: %s",
			signed.Signature, pinnedSignature)
	}
}
