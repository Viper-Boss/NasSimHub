package release

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/license"
)

// testKey is the seed from the licence golden vector, reused so a reader can
// check one set of pinned values against both packages.
var testSeed = []byte{
	0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
	0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
	0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
}

func testKeys(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	private := ed25519.NewKeyFromSeed(testSeed)
	public, ok := private.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("no public half")
	}
	return private, base64.StdEncoding.EncodeToString(public)
}

func sampleManifest() Manifest {
	return Manifest{
		SchemaVersion:  SchemaVersion,
		ReleaseVersion: "v1.4.2",
		CreatedAt:      1790000000,
		MinimumVersion: "v1.0.0",
		Images: []Image{
			{Name: "api", Repository: "ghcr.io/viper-boss/nassimhub", Tag: "v1.4.2",
				Digest: "sha256:" + strings.Repeat("a", 64)},
			{Name: "web", Repository: "ghcr.io/viper-boss/nassimhub-web", Tag: "v1.4.2",
				Digest: "sha256:" + strings.Repeat("b", 64)},
		},
	}
}

// ---------------------------------------------------------------------------
// The four acceptance cases the specification names for this phase.
// ---------------------------------------------------------------------------

// 合法 Release = PASS
func TestAGenuineManifestVerifies(t *testing.T) {
	private, public := testKeys(t)
	signed, err := Sign(sampleManifest(), private)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	manifest, err := Verify(signed, public)
	if err != nil {
		t.Fatalf("a manifest we just signed did not verify: %v", err)
	}
	if manifest.ReleaseVersion != "v1.4.2" {
		t.Fatalf("wrong manifest came back: %q", manifest.ReleaseVersion)
	}
	image, ok := manifest.Image("api")
	if !ok {
		t.Fatal("api image missing")
	}
	// The pull reference must be by digest. A tag here would mean the whole
	// exercise pinned nothing.
	if !strings.Contains(image.Reference(), "@sha256:") {
		t.Fatalf("reference is not digest-pinned: %q", image.Reference())
	}
	if strings.Contains(image.Reference(), ":v1.4.2") {
		t.Fatalf("reference resolves by tag: %q", image.Reference())
	}
}

// 修改任意 1 byte = FAIL -- every field, one at a time, rather than one
// representative field. A signature that covered most of the manifest would
// pass a single-field test and still leave a way to change what gets installed.
func TestAlteringAnySignedFieldFailsVerification(t *testing.T) {
	private, public := testKeys(t)
	signed, err := Sign(sampleManifest(), private)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	alterations := map[string]func(*Manifest){
		"release version":  func(m *Manifest) { m.ReleaseVersion = "v1.4.3" },
		"created at":       func(m *Manifest) { m.CreatedAt = 1790000001 },
		"minimum version":  func(m *Manifest) { m.MinimumVersion = "v1.1.0" },
		"image digest":     func(m *Manifest) { m.Images[0].Digest = "sha256:" + strings.Repeat("c", 64) },
		"image repository": func(m *Manifest) { m.Images[0].Repository = "ghcr.io/attacker/modemdeck" },
		"image tag":        func(m *Manifest) { m.Images[0].Tag = "v9.9.9" },
		"image name":       func(m *Manifest) { m.Images[0].Name = "hardware" },
		"image removed":    func(m *Manifest) { m.Images = m.Images[:1] },
		"image added": func(m *Manifest) {
			m.Images = append(m.Images, Image{
				Name: "updater", Repository: "ghcr.io/attacker/x", Tag: "v1.4.2",
				Digest: "sha256:" + strings.Repeat("d", 64)})
		},
	}

	for name, alter := range alterations {
		t.Run(name, func(t *testing.T) {
			tampered := signed
			tampered.Manifest = sampleManifest()
			alter(&tampered.Manifest)
			if _, err := Verify(tampered, public); err == nil {
				t.Fatalf("altering the %s still verified; the signature does not cover it", name)
			}
		})
	}
}

// Reordering the image array must NOT change the outcome: the signature is over
// meaning, not over JSON array order, or publishing would be flaky in a way
// that teaches whoever hits it to distrust the check.
func TestReorderingImagesStillVerifies(t *testing.T) {
	private, public := testKeys(t)
	manifest := sampleManifest()
	signed, err := Sign(manifest, private)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	reordered := signed
	reordered.Manifest.Images = []Image{manifest.Images[1], manifest.Images[0]}
	if _, err := Verify(reordered, public); err != nil {
		t.Fatalf("reordering images broke verification: %v", err)
	}
}

// 伪造 signature = FAIL
func TestAForgedSignatureFails(t *testing.T) {
	_, public := testKeys(t)
	manifest := sampleManifest()

	// Signed by a different key: the shape of a real attacker's attempt.
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
	elsewhere, err := Sign(manifest, other)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := Verify(elsewhere, public); err == nil {
		t.Fatal("a manifest signed by another key verified")
	}

	for name, signature := range map[string]string{
		"empty":      "",
		"not base64": "!!!!",
		"wrong length": base64.StdEncoding.EncodeToString(
			bytes.Repeat([]byte{0x01}, ed25519.SignatureSize-1)),
		"right length, wrong bytes": base64.StdEncoding.EncodeToString(
			bytes.Repeat([]byte{0x01}, ed25519.SignatureSize)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Verify(Signed{Manifest: manifest, Signature: signature}, public); err == nil {
				t.Fatalf("a %s signature verified", name)
			}
		})
	}
}

// 修改 manifest = FAIL, at the JSON level, which is the form it actually
// travels in -- including the case where a field is added that this build does
// not know about.
func TestTamperedAndUnknownJSONIsRefused(t *testing.T) {
	private, public := testKeys(t)
	signed, err := Sign(sampleManifest(), private)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	encoded, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyJSON(bytes.NewReader(encoded), public); err != nil {
		t.Fatalf("the untouched envelope did not verify: %v", err)
	}

	for name, document := range map[string]string{
		"digest swapped": strings.Replace(string(encoded),
			strings.Repeat("a", 64), strings.Repeat("e", 64), 1),
		"signature stripped": strings.Replace(string(encoded), signed.Signature, "", 1),
		"unknown field":      strings.Replace(string(encoded), `"manifest"`, `"trust_me":true,"manifest"`, 1),
		"not json":           "{",
		"empty":              "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyJSON(strings.NewReader(document), public); err == nil {
				t.Fatalf("%s verified", name)
			}
		})
	}
}

// unsupported schema, reported as its own thing so the operator is told to
// update the gateway rather than to suspect tampering.
func TestAnUnsupportedSchemaIsNamedAsSuch(t *testing.T) {
	private, public := testKeys(t)
	manifest := sampleManifest()
	manifest.SchemaVersion = SchemaVersion + 1
	// Signing must refuse it too: the signer shares validate(), so it cannot
	// mint something the verifier would have to reject.
	if _, err := Sign(manifest, private); err == nil {
		t.Fatal("signed a manifest with an unsupported schema")
	}
	if _, err := Verify(Signed{Manifest: manifest, Signature: "x"}, public); !strings.Contains(
		err.Error(), "unsupported manifest schema") {
		t.Fatalf("an unsupported schema was reported as %v", err)
	}
}

// A manifest with an unpinned digest cannot exist, which is the invariant that
// made this package worth writing.
func TestAnImageWithoutADigestCannotBeSigned(t *testing.T) {
	private, _ := testKeys(t)
	manifest := sampleManifest()
	manifest.Images[0].Digest = ""
	if _, err := Sign(manifest, private); err == nil {
		t.Fatal("signed a manifest whose image had no digest; a tag would then decide what installs")
	}
}

func TestNoVerificationKeyMeansNoInstall(t *testing.T) {
	private, _ := testKeys(t)
	signed, err := Sign(sampleManifest(), private)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "abc", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := Verify(signed, key); err == nil {
			t.Fatalf("verification succeeded with key %q", key)
		}
	}
}

// ---------------------------------------------------------------------------
// One key, two purposes: the replay this arrangement has to exclude.
// ---------------------------------------------------------------------------

// TestTheTwoSignatureDomainsCannotBeConfused is the test that justifies signing
// licence certificates and release manifests with the same key.
//
// The risk is concrete. A signature is a signature over bytes; it carries no
// notion of what those bytes were for. So if the certificate encoding and the
// manifest encoding could ever produce the same byte string, then a customer
// holding a legitimately issued certificate would hold a valid release
// manifest signature -- and could install anything on any gateway.
//
// The exclusion is structural, not statistical: the manifest encoding always
// begins with the domain line, and the certificate encoding always begins with
// "v=". Both halves are checked below, and the certificate bytes are rebuilt
// here from the format's documented shape rather than by calling the
// (unexported) encoder -- if that reconstruction were wrong, the licence
// signature built from it would not verify, and this test would say so.
func TestTheTwoSignatureDomainsCannotBeConfused(t *testing.T) {
	private, public := testKeys(t)
	publicKey, _ := private.Public().(ed25519.PublicKey)

	certificate := license.Certificate{
		Version:   license.CurrentVersion,
		ID:        "cert-1",
		Scope:     license.ScopeMachine,
		MachineID: "machine-1",
		Features:  []string{"outbound.call"},
		IssuedAt:  1790000000,
		ExpiresAt: 0,
		Note:      "",
	}

	// Independent reconstruction of what the licence encoder produces.
	certificateBytes := []byte(strings.Join([]string{
		"v=1",
		"id=cert-1",
		"scope=machine",
		"machine_id=machine-1",
		"module_sn=",
		"features=outbound.call",
		"issued_at=1790000000",
		"expires_at=0",
		"note=",
	}, "\n") + "\n")

	// Proof the reconstruction is byte-exact: a signature over these bytes is
	// accepted by the real licence verifier.
	handSigned := license.Signed{
		Certificate: certificate,
		Signature:   base64.StdEncoding.EncodeToString(ed25519.Sign(private, certificateBytes)),
	}
	if err := license.Verify(handSigned, publicKey, time.Unix(1790000001, 0)); err != nil {
		t.Fatalf("the reconstructed certificate bytes are not what the licence encoder produces (%v); "+
			"this test cannot prove anything until they match", err)
	}

	// Direction 1: that genuine certificate signature must not verify as a
	// release manifest, for ANY manifest.
	if _, err := Verify(Signed{
		Manifest:  sampleManifest(),
		Signature: handSigned.Signature,
	}, public); err == nil {
		t.Fatal("a licence certificate's signature verified as a release manifest")
	}

	// Direction 2: a genuine manifest signature must not verify as a licence
	// certificate.
	manifestSigned, err := Sign(sampleManifest(), private)
	if err != nil {
		t.Fatal(err)
	}
	if err := license.Verify(license.Signed{
		Certificate: certificate,
		Signature:   manifestSigned.Signature,
	}, publicKey, time.Unix(1790000001, 0)); err == nil {
		t.Fatal("a release manifest's signature verified as a licence certificate")
	}

	// And the property both directions rest on, asserted directly so that a
	// future change to either encoding fails here with a readable reason
	// rather than silently removing the separation.
	message, err := sampleManifest().Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(message, []byte(Domain+"\n")) {
		t.Fatalf("manifest bytes do not begin with the domain line: %q", firstLine(message))
	}
	if !bytes.HasPrefix(certificateBytes, []byte("v=")) {
		t.Fatalf("certificate bytes no longer begin with \"v=\": %q", firstLine(certificateBytes))
	}
}

// ---------------------------------------------------------------------------
// Golden vector.
// ---------------------------------------------------------------------------

// TestTheCanonicalEncodingIsPinned freezes the signed bytes and the signature
// over them.
//
// Without this, "the encoding" is whatever canonical() currently returns, and a
// refactor that changed it would break every manifest already published while
// every test still passed -- the signature would be recomputed from the new
// bytes on both sides. The pinned values below are the contract, and the
// signing tool in the issuing console carries the same vector so the two
// implementations are held to one answer.
func TestTheCanonicalEncodingIsPinned(t *testing.T) {
	private, public := testKeys(t)

	const expectedCanonical = "nsh-domain=release-manifest-v1\n" +
		"schema=3\n" +
		"release=v1.4.2\n" +
		"created_at=1790000000\n" +
		"minimum_version=v1.0.0\n" +
		"image=api ghcr.io/viper-boss/nassimhub v1.4.2 sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n" +
		"image=web ghcr.io/viper-boss/nassimhub-web v1.4.2 sha256:" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n"

	message, err := sampleManifest().Canonical()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if string(message) != expectedCanonical {
		t.Fatalf("the canonical encoding changed.\n got:\n%s\nwant:\n%s", message, expectedCanonical)
	}

	signed, err := Sign(sampleManifest(), private)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Ed25519 is deterministic, so the signature over pinned bytes with a
	// pinned key is itself a constant.
	if got := signed.Signature; got != pinnedSignature {
		t.Fatalf("the signature over the pinned vector changed.\n got: %s\nwant: %s",
			got, pinnedSignature)
	}
	if _, err := Verify(signed, public); err != nil {
		t.Fatalf("the pinned vector does not verify: %v", err)
	}
}

// pinnedSignature is Ed25519 over expectedCanonical with the seed above.
const pinnedSignature = "7U9eexYhws+giUgoAGn4e+fBJnmOvsg79afPgk00IjztyWlqt+sFrAOVDUEQi+NkMrTvRsgiJknQDJHS3pPOAg=="

func firstLine(value []byte) string {
	if index := bytes.IndexByte(value, '\n'); index >= 0 {
		return string(value[:index])
	}
	return string(value)
}
