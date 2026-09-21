package license

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"
	"time"
)

// The golden vector.
//
// THIS TEST EXISTS IN TWO REPOSITORIES. Its twin is in the issuing console
// (nassimhub-license, internal/cert/certificate_test.go), which has its own
// implementation of this certificate format because it is a separate module
// and cannot import this package. Two implementations of one signed encoding
// is a standing risk: the day they disagree by one byte, every certificate the
// console issues stops verifying here, and the symptom reaches us as "I paid
// and it is still locked" rather than as a failing build.
//
// So both sides pin the same fixed key, the same fixed certificates, the same
// canonical bytes and the same signatures. A change to the encoding on either
// side fails that side's copy of this test immediately. Neither copy may be
// "fixed" by updating the expected values: if these numbers need to change,
// the format has changed, and that means a new CurrentVersion and a gateway
// that still accepts the old one.
//
// The key below is a test key with a seed of 0x00..0x1f. It is not secret and
// must never be used to issue anything.

const goldenSeedHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

// goldenPublicKeyHex is the public half of that seed, written out rather than
// derived so that a change in how Go derives it would also be caught.
const goldenPublicKeyHex = "03a107bff3ce10be1d70dd18e74bc09967e4d6309ba50d5f1ddc8664125531b8"

type goldenCase struct {
	name        string
	certificate Certificate
	canonical   string
	signature   string
}

// goldenCases covers both scopes, features given out of order (the encoding
// sorts them, so a signature must not depend on the order they were typed in),
// a note containing spaces and punctuation, and both an expiring and a
// perpetual certificate.
var goldenCases = []goldenCase{
	{
		name: "machine scope, three features, expiring",
		certificate: Certificate{
			Version:   1,
			ID:        "golden-machine-0001",
			Scope:     ScopeMachine,
			MachineID: "GOLDEN-MACHINE-ID",
			Features:  []string{"net.wireguard", "beta.unlocked", "call.dial"},
			IssuedAt:  1750000000,
			ExpiresAt: 1790000000,
			Note:      "golden vector; do not edit",
		},
		canonical: "v=1\n" +
			"id=golden-machine-0001\n" +
			"scope=machine\n" +
			"machine_id=GOLDEN-MACHINE-ID\n" +
			"module_sn=\n" +
			"features=beta.unlocked,call.dial,net.wireguard\n" +
			"issued_at=1750000000\n" +
			"expires_at=1790000000\n" +
			"note=golden vector; do not edit\n",
		signature: "iK1vxnaiLds6UGCrzvLq8TBbPyqKtgzOEinKMTwgkSNaFIyUeFnWJV38XI+o80psq3N7peATyCAKUExeUXtUCg==",
	},
	{
		name: "module scope, one feature, perpetual",
		certificate: Certificate{
			Version:  1,
			ID:       "golden-module-0001",
			Scope:    ScopeModule,
			ModuleSN: "860000000000001",
			Features: []string{"beta.unlocked"},
			IssuedAt: 1750000000,
		},
		canonical: "v=1\n" +
			"id=golden-module-0001\n" +
			"scope=module\n" +
			"machine_id=\n" +
			"module_sn=860000000000001\n" +
			"features=beta.unlocked\n" +
			"issued_at=1750000000\n" +
			"expires_at=0\n" +
			"note=\n",
		signature: "mTfQCJrEkrqkrxjfdzYb0Ab4bcAPbRtc1pbP2R3FqiSpmgVQmw5pil4RoINGhfZH+q921EptepubNTvmdLDaAQ==",
	},
}

func goldenKeys(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	seed, err := hex.DecodeString(goldenSeedHex)
	if err != nil {
		t.Fatalf("golden seed is not hex: %v", err)
	}
	private := ed25519.NewKeyFromSeed(seed)
	public, err := hex.DecodeString(goldenPublicKeyHex)
	if err != nil {
		t.Fatalf("golden public key is not hex: %v", err)
	}
	derived, ok := private.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("private key has no public half")
	}
	if hex.EncodeToString(derived) != goldenPublicKeyHex {
		t.Fatalf("key derivation changed: seed %s now yields public key %s, pinned %s",
			goldenSeedHex, hex.EncodeToString(derived), goldenPublicKeyHex)
	}
	return private, ed25519.PublicKey(public)
}

// TestGoldenCanonicalBytes is the one that says WHERE a drift happened. A test
// that only compared signatures would report "signature mismatch" and leave
// whoever is reading it to work out which field moved.
func TestGoldenCanonicalBytes(t *testing.T) {
	for _, test := range goldenCases {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := test.certificate.canonical()
			if err != nil {
				t.Fatalf("canonical: %v", err)
			}
			if string(encoded) != test.canonical {
				t.Fatalf("canonical encoding drifted.\n got: %q\nwant: %q", string(encoded), test.canonical)
			}
		})
	}
}

func TestGoldenSignatures(t *testing.T) {
	private, _ := goldenKeys(t)
	for _, test := range goldenCases {
		t.Run(test.name, func(t *testing.T) {
			signed, err := Sign(test.certificate, private)
			if err != nil {
				t.Fatalf("sign: %v", err)
			}
			if signed.Signature != test.signature {
				t.Fatalf("signature drifted.\n got: %s\nwant: %s", signed.Signature, test.signature)
			}
		})
	}
}

// TestGoldenVerifies checks the direction that actually matters in the field:
// bytes produced elsewhere, by the console, verifying here. The signatures are
// used as literals rather than re-signed, so this passes only if this build
// accepts a certificate it did not itself create.
func TestGoldenVerifies(t *testing.T) {
	_, public := goldenKeys(t)
	now := time.Unix(1760000000, 0)
	for _, test := range goldenCases {
		t.Run(test.name, func(t *testing.T) {
			signed := Signed{Certificate: test.certificate, Signature: test.signature}
			if err := Verify(signed, public, now); err != nil {
				t.Fatalf("a certificate issued by the console does not verify here: %v", err)
			}
		})
	}
}

// TestGoldenRejectsTamper is the claim the whole scheme rests on, so it is
// asserted rather than assumed: every field is covered by the signature, and
// changing any one of them invalidates it.
func TestGoldenRejectsTamper(t *testing.T) {
	_, public := goldenKeys(t)
	now := time.Unix(1760000000, 0)
	base := goldenCases[0]

	tampered := map[string]func(*Certificate){
		"machine id swapped":  func(c *Certificate) { c.MachineID = "SOMEONE-ELSES-MACHINE" },
		"feature added":       func(c *Certificate) { c.Features = append(c.Features, "sms.send") },
		"expiry extended":     func(c *Certificate) { c.ExpiresAt = 1990000000 },
		"expiry removed":      func(c *Certificate) { c.ExpiresAt = 0 },
		"issue date moved":    func(c *Certificate) { c.IssuedAt = 1750000001 },
		"id renamed":          func(c *Certificate) { c.ID = "golden-machine-0002" },
		"note edited":         func(c *Certificate) { c.Note = "golden vector, do not edit" },
		"feature substituted": func(c *Certificate) { c.Features = []string{"net.wireguard", "beta.unlocked", "call.answer"} },
	}
	for name, mutate := range tampered {
		t.Run(name, func(t *testing.T) {
			altered := base.certificate
			altered.Features = append([]string(nil), base.certificate.Features...)
			mutate(&altered)
			signed := Signed{Certificate: altered, Signature: base.signature}
			if err := Verify(signed, public, now); err == nil {
				t.Fatal("a tampered certificate verified")
			}
		})
	}
}

// TestGoldenRejectsForeignKey covers the other half: a correctly formed
// certificate signed by somebody else's key. This is what a customer who
// generates their own key pair and signs themselves a certificate produces,
// and it must be refused rather than merely unrecognised.
func TestGoldenRejectsForeignKey(t *testing.T) {
	_, public := goldenKeys(t)
	foreignSeed := make([]byte, ed25519.SeedSize)
	for i := range foreignSeed {
		foreignSeed[i] = 0xAA
	}
	foreign := ed25519.NewKeyFromSeed(foreignSeed)

	signed, err := Sign(goldenCases[0].certificate, foreign)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := Verify(signed, public, time.Unix(1760000000, 0)); err == nil {
		t.Fatal("a certificate signed by a foreign key verified")
	}
}
