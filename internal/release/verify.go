package release

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/license"
)

// SigningPublicKey is the key release manifests are verified against.
//
// The same key as the licence issuer's, by explicit decision: one key pair to
// generate, back up and keep offline instead of two. Referenced rather than
// copied so the two cannot drift apart into a state where one of them is a
// key nobody holds any more.
//
// What that choice costs, recorded here so it is not rediscovered the hard
// way: a leak of the private half is a leak for BOTH purposes at once -- the
// holder could sign licences AND push a release to every gateway. The two
// purposes are otherwise unrelated, and separate keys would have contained
// either compromise to one of them.
//
// What it does NOT cost is cross-protocol replay, which is the failure people
// actually hit when reusing a key. That is closed structurally by the domain
// line -- see Domain in canonical.go.
const SigningPublicKey = license.IssuerPublicKey

// Verify checks the envelope and returns the manifest it vouches for.
//
// The manifest is returned ONLY on success. A caller that cannot get at an
// unverified manifest cannot accidentally act on one, which is the mistake this
// signature is here to prevent -- there is deliberately no "parse now, verify
// later" entry point in this package.
//
// Fail-closed throughout: every path out of here that is not a verified
// signature is an error, and no error means "install it anyway".
func Verify(signed Signed, publicKeyBase64 string) (Manifest, error) {
	publicKey, err := decodeKey(publicKeyBase64)
	if err != nil {
		return Manifest{}, err
	}

	// Canonical() validates, so a manifest that is malformed or of an
	// unsupported schema is rejected before any cryptography. Ordering matters
	// only for the quality of the error: signature-checking a manifest this
	// build cannot represent would report "bad signature" for what is really
	// "too new".
	message, err := signed.Manifest.canonical()
	if err != nil {
		return Manifest{}, err
	}

	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signed.Signature))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return Manifest{}, fmt.Errorf("%w: signature is not %d bytes",
			ErrBadSignature, ed25519.SignatureSize)
	}
	if !ed25519.Verify(publicKey, message, signature) {
		return Manifest{}, ErrBadSignature
	}
	return signed.Manifest, nil
}

// VerifyJSON reads an envelope and verifies it.
//
// Size-bounded, because the reader is a network connection under somebody
// else's control: a peer offering an endless manifest must not be able to
// exhaust memory on a gateway that is also handling calls.
//
// DisallowUnknownFields is on. A field this build does not know is not
// something to skip past quietly -- it means the document was produced for a
// different verifier, and the safe reading of "I do not understand part of
// this" is to refuse rather than to verify the part that is legible.
func VerifyJSON(reader io.Reader, publicKeyBase64 string) (Manifest, error) {
	var signed Signed
	decoder := json.NewDecoder(io.LimitReader(reader, maxManifestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&signed); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return Verify(signed, publicKeyBase64)
}

// Sign produces an envelope. It lives here, beside Verify, so the signing tool
// and the gateway cannot drift apart in their idea of what is signed.
//
// The gateway never calls this: it holds no private key, and there is nothing
// in the shipped binary that could supply one. It is exported for the issuing
// console and for tests, which need to produce manifests that genuinely verify
// rather than fixtures asserting against themselves.
func Sign(manifest Manifest, privateKey ed25519.PrivateKey) (Signed, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return Signed{}, fmt.Errorf("%w: signing key is not %d bytes",
			ErrMalformed, ed25519.PrivateKeySize)
	}
	message, err := manifest.canonical()
	if err != nil {
		return Signed{}, err
	}
	signed := Signed{
		Manifest:  manifest,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, message)),
	}

	// Verified before being returned, against the public half of the very key
	// that just signed it. A signer that emits something it cannot itself
	// verify has a bug that would otherwise surface on a customer's gateway,
	// where nobody can debug it.
	public, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return Signed{}, fmt.Errorf("%w: signing key has no public half", ErrMalformed)
	}
	if _, err := Verify(signed, base64.StdEncoding.EncodeToString(public)); err != nil {
		return Signed{}, fmt.Errorf("release: refusing to emit a manifest that does not verify: %w", err)
	}
	return signed, nil
}

func decodeKey(publicKeyBase64 string) (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKeyBase64))
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		// Distinct from ErrBadSignature: nothing was checked, so reporting a
		// signature failure would be a lie, and reporting it as the manifest's
		// fault would send somebody to debug the wrong artefact.
		return nil, fmt.Errorf("%w: verification key is not a %d-byte Ed25519 public key",
			ErrMalformed, ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(decoded), nil
}
