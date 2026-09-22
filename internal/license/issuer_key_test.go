package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

// The compiled-in issuer key has to be a usable Ed25519 public key, and this
// test is the only thing that will ever tell anybody if it is not.
//
// NewStore accepts a malformed key by leaving the store's key unset:
//
//	decoded, err := base64.StdEncoding.DecodeString(...)
//	if err == nil && len(decoded) == ed25519.PublicKeySize {
//	        store.publicKey = ed25519.PublicKey(decoded)
//	}
//
// That is the right behaviour there -- a gateway must not refuse to boot and
// take the free features down over a licence key -- but it means a key that
// lost a character in transit produces NO error, NO log line, and a gateway
// where every paid feature is locked and the settings page reads "this build
// cannot verify certificates". For an unlicensed build that message is
// correct, so nothing looks wrong. Certificates issued against the real key
// would archive and deliver perfectly and be rejected by every customer.
//
// So the check belongs at build time, where a wrong paste is cheap. Below it
// is a compile-and-test failure; in production it was a silent outage that
// looked like a paywall.
func TestTheCompiledIssuerKeyIsUsable(t *testing.T) {
	if IssuerPublicKey == "" {
		t.Fatal("IssuerPublicKey is empty: this build can verify no certificate at all, so every paid feature is locked and nothing says why")
	}
	if IssuerPublicKey != strings.TrimSpace(IssuerPublicKey) {
		t.Errorf("IssuerPublicKey has surrounding whitespace, which is exactly what a copy-paste adds: %q", IssuerPublicKey)
	}

	decoded, err := base64.StdEncoding.DecodeString(IssuerPublicKey)
	if err != nil {
		t.Fatalf("IssuerPublicKey is not valid base64 (%v); a truncated or re-wrapped paste looks like this", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		t.Fatalf(
			"IssuerPublicKey decodes to %d bytes, not %d.\n"+
				"A 64-byte value means somebody pasted the PRIVATE key here, which would be a disclosure, not a typo.",
			len(decoded), ed25519.PublicKeySize,
		)
	}

	// Round-trip, so a value that decodes but is not canonical base64 (wrong
	// padding, a stray newline the decoder tolerated) still fails here rather
	// than differing from what the issuing console prints.
	if reEncoded := base64.StdEncoding.EncodeToString(decoded); reEncoded != IssuerPublicKey {
		t.Errorf(
			"IssuerPublicKey is not canonical base64: it re-encodes to %q.\n"+
				"Use the line the issuing console prints at start-up verbatim.",
			reEncoded,
		)
	}
}

// And the end the rest of the code actually depends on: a store built with the
// compiled key must report itself able to verify. This is what every paid
// feature's decision runs through, and what the settings page reads to tell
// "you have not bought anything" apart from "this build cannot check".
func TestAStoreBuiltWithTheCompiledKeyCanVerify(t *testing.T) {
	if store := NewStore(t.TempDir(), IssuerPublicKey); !store.Configured() {
		t.Fatal("a store built with the compiled-in issuer key reports it cannot verify certificates; every paid feature is locked in this build")
	}
}
