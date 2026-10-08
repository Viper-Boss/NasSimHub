package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/pqsig"
)

func dualFixture(t *testing.T) (Manifest, ed25519.PrivateKey, string, pqsig.Signer, string) {
	t.Helper()
	if !pqsig.Available() {
		t.Skip("this toolchain has no crypto/mldsa; build with " + pqsig.RequiredToolchain)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := pqsig.NewSeed()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := pqsig.NewSigner(pqsig.MLDSA87, seed)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("ab", 32)
	var images []Image
	for _, component := range Components() {
		images = append(images, Image{Name: component, Repository: Repositories[component], Tag: "v1.20.0", Digest: digest})
	}
	manifest := Manifest{SchemaVersion: SchemaVersion, ReleaseVersion: "v1.20.0", CreatedAt: 1790000000, Images: images}
	return manifest, private, base64.StdEncoding.EncodeToString(public), signer, base64.StdEncoding.EncodeToString(signer.PublicKey())
}

func TestADualSignedManifestNeedsBothSignatures(t *testing.T) {
	manifest, private, edKey, signer, pqKey := dualFixture(t)
	signed, err := SignDual(manifest, private, signer)
	if err != nil {
		t.Fatal(err)
	}
	if verified, err := VerifyDual(signed, edKey, pqKey); err != nil || verified.ReleaseVersion != "v1.20.0" {
		t.Fatalf("a correctly dual-signed manifest verifies: %v", err)
	}
	// The classical half is byte-for-byte what a classical gateway accepts.
	if _, err := Verify(Signed{Manifest: signed.Manifest, Signature: signed.Signature}, edKey); err != nil {
		t.Fatalf("the Ed25519 half must stand on its own: %v", err)
	}

	otherSeed, _ := pqsig.NewSeed()
	other, _ := pqsig.NewSigner(pqsig.MLDSA87, otherSeed)
	_, otherPrivate, _ := ed25519.GenerateKey(rand.Reader)
	for name, mutate := range map[string]func(*SignedDual){
		"post-quantum signature removed": func(s *SignedDual) { s.PQSignature = "" },
		"post-quantum signature altered": func(s *SignedDual) { s.PQSignature = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 4627)) },
		"post-quantum by another key":    func(s *SignedDual) { o, _ := SignDual(manifest, private, other); s.PQSignature = o.PQSignature },
		"classical signature removed":    func(s *SignedDual) { s.Signature = "" },
		"classical by another key":       func(s *SignedDual) { o, _ := Sign(manifest, otherPrivate); s.Signature = o.Signature },
		"algorithm renamed":              func(s *SignedDual) { s.PQAlgorithm = "ml-dsa-65" },
		"algorithm emptied":              func(s *SignedDual) { s.PQAlgorithm = "" },
		"manifest changed after signing": func(s *SignedDual) { s.Manifest.MinimumVersion = "v1.0.0" },
		"digest changed after signing":   func(s *SignedDual) { s.Manifest.Images[0].Digest = "sha256:" + strings.Repeat("cd", 32) },
		"signatures of different content": func(s *SignedDual) {
			changed := manifest
			changed.CreatedAt++
			o, _ := SignDual(changed, private, signer)
			s.PQSignature = o.PQSignature // PQ signs another manifest than Ed25519 does
		},
	} {
		tampered := signed
		tampered.Manifest.Images = append([]Image(nil), signed.Manifest.Images...)
		mutate(&tampered)
		if _, err := VerifyDual(tampered, edKey, pqKey); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	// A post-quantum signature over the bare canonical bytes (without the
	// domain line) must not verify: the domain is part of what is signed.
	canonical, _ := manifest.canonical()
	raw, _ := signer.Sign(canonical)
	undomained := signed
	undomained.PQSignature = base64.StdEncoding.EncodeToString(raw)
	if _, err := VerifyDual(undomained, edKey, pqKey); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a signature outside the post-quantum domain verified: %v", err)
	}
}

func TestABuildWithAPostQuantumKeyNeverAcceptsAClassicalManifest(t *testing.T) {
	manifest, private, edKey, signer, pqKey := dualFixture(t)
	classical, err := Sign(manifest, private)
	if err != nil {
		t.Fatal(err)
	}
	classicalJSON, _ := json.Marshal(classical)
	dual, err := SignDual(manifest, private, signer)
	if err != nil {
		t.Fatal(err)
	}
	dualJSON, _ := json.Marshal(dual)

	// With a post-quantum key: dual accepted, classical refused - no fallback.
	if _, err := verifyPublished(dualJSON, edKey, pqKey); err != nil {
		t.Fatalf("dual-required build, dual manifest: %v", err)
	}
	if _, err := verifyPublished(classicalJSON, edKey, pqKey); err == nil {
		t.Fatal("a dual-required build accepted a classical manifest")
	}
	// Without one: classical accepted; a dual document is not silently read as
	// classical either (unknown fields are refused), so the two never blur.
	if _, err := verifyPublished(classicalJSON, edKey, ""); err != nil {
		t.Fatalf("classical build, classical manifest: %v", err)
	}
	if _, err := verifyPublished(dualJSON, edKey, ""); !errors.Is(err, ErrMalformed) {
		t.Fatalf("a classical build must not half-read a dual manifest: %v", err)
	}
	if policyFor("") != PolicyClassical || policyFor(pqKey) != PolicyDualRequired {
		t.Fatal("the policy follows the compiled-in key")
	}
	// This tree ships without a post-quantum release key, and says so.
	if PQSigningPublicKey != "" || PQSignaturePolicy() != PolicyClassical {
		t.Fatal("update this test together with the key: the tree now claims a post-quantum release key")
	}
	// An oversized document is refused before it is parsed.
	if _, err := VerifyDualJSON(bytes.NewReader(bytes.Repeat([]byte(" "), MaxDualManifestBytes+10)), edKey, pqKey); !errors.Is(err, ErrMalformed) {
		t.Fatalf("oversized: %v", err)
	}
}

func TestSignDualRefusesToEmitAHalfSignedManifest(t *testing.T) {
	manifest, private, _, _, _ := dualFixture(t)
	if _, err := SignDual(manifest, private, nil); err == nil {
		t.Fatal("no post-quantum signer must be an error, not a classical manifest")
	}
	seed, _ := pqsig.NewSeed()
	weaker, _ := pqsig.NewSigner("ml-dsa-65", seed)
	if _, err := SignDual(manifest, private, weaker); err == nil {
		t.Fatal("releases are signed with ML-DSA-87 only")
	}
}
