package airsimproto

import (
	"crypto/ecdh"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// The expected values below were produced by AirSIM's own Android gateway
// code (VoWLANAuth.java, compiled unchanged, and PairingCrypto.deriveKey
// copied verbatim) and, for the sealed registration, by an independent
// Python implementation of the iOS app's CryptoKit calls. They pin the wire
// format: if one of these tests fails, an unmodified AirSIM app will no
// longer talk to the gateway.

func testSecret() []byte {
	secret := make([]byte, 32)
	for index := range secret {
		secret[index] = byte(index + 1)
	}
	return secret
}

func TestCanonicalAndSignatureMatchTheAndroidGateway(t *testing.T) {
	canonical := Canonical("post", "/api/calls/dial?x=1", []byte(`{"number":"10086"}`), 1793600000, "nonce-abc-123")
	want := "POST\n/api/calls/dial?x=1\na8ef8485bac143ea16241105a3d9aa2b048b678a89abb8fabb15399ed38b249a\n1793600000\nnonce-abc-123"
	if canonical != want {
		t.Fatalf("canonical = %q, want %q", canonical, want)
	}
	if got := Sign(testSecret(), canonical); got != "T79O-CqSig21t8LKhZtM0CUmwNO_HNaiqd7z909XuEs" {
		t.Fatalf("signature = %s", got)
	}
	if !VerifySignature(testSecret(), "T79O-CqSig21t8LKhZtM0CUmwNO_HNaiqd7z909XuEs", canonical) {
		t.Fatal("a correct signature was rejected")
	}
	if VerifySignature(testSecret(), "T79O-CqSig21t8LKhZtM0CUmwNO_HNaiqd7z909XuEt", canonical) {
		t.Fatal("a wrong signature was accepted")
	}
	if VerifySignature(testSecret()[:31], Sign(testSecret()[:31], canonical), canonical) {
		t.Fatal("a secret shorter than 32 bytes was accepted")
	}
}

func TestProofsMatchTheAndroidGateway(t *testing.T) {
	secret := testSecret()
	for name, test := range map[string]struct{ got, want string }{
		"health": {HealthProof(secret, "nonce-abc-123", "nassimhub-test"), "jPBg_ulZx15c32LUNugJESI6JKT1bKa7O3eLIyj65NQ"},
		"dial":   {DialProof(secret, "nonce-abc-123", "op-1", "call-9", "call-9"), "v8qE7N6W6VEKFbdZp7g3ndFflglUaggzq1etzOH9l7Y"},
		"direct": {DirectRouteProof(secret, "sess-1", "clientkey", "nassimhub-test", 7591, 7592), "ZGg0kPk-QS3DUzwREpYuG4UjObwUqFmNDsh55hhKGas"},
	} {
		if test.got != test.want {
			t.Errorf("%s proof = %s, want %s", name, test.got, test.want)
		}
	}
}

func TestDeriveKeyMatchesTheAndroidGateway(t *testing.T) {
	// deriveKey takes the X25519 output; compare the HKDF step directly.
	shared := make([]byte, 32)
	for index := range shared {
		shared[index] = byte(255 - index)
	}
	key, err := hkdfForTest(shared, "11111111-2222-4333-8444-555555555555", "042917")
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(key) != "10ba379a7ee998d9a60de0ecff1465a5866905e57122865ee516af0f64b170be" {
		t.Fatalf("derived key = %x", key)
	}
}

func TestOpenSealedRegistrationFromTheApp(t *testing.T) {
	serverPrivate, err := ecdh.X25519().NewPrivateKey(testSecret())
	if err != nil {
		t.Fatal(err)
	}
	if got := Base64URL(serverPrivate.PublicKey().Bytes()); got != "B6N8vBQgk8i3VdwbEOhstCY3StFqqFPtC9_AsrhtHHw" {
		t.Fatalf("server public key = %s", got)
	}
	clientPublic, _ := DecodeBase64URL("WGmv9FBUlzLLqu1eXfmzCm2jHLDldCutWtShp2jxpns")
	sealed, _ := DecodeBase64URL("ZGVmZ2hpamtsbW5vfhj6oAs_3IeZg1DpRoTfp760rc8JM3B4SO3EIzTzNhoPCoUBpyoGQwN8fIHkcc8noCG22E9PYl0Oi1YRzBF7fpq1rMmEhyNGKwNwOMLMvLfOw8veZE7Ol0TzgytrDcgmrQ")
	session := "11111111-2222-4333-8444-555555555555"
	plaintext, err := OpenSealedRegistration(serverPrivate, clientPublic, sealed, session, "042917")
	if err != nil {
		t.Fatalf("open = %v", err)
	}
	if !strings.Contains(string(plaintext), `"vowlan_secret":"AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA"`) {
		t.Fatalf("plaintext = %s", plaintext)
	}
	if _, err := OpenSealedRegistration(serverPrivate, clientPublic, sealed, session, "042918"); err != ErrAuthentication {
		t.Fatalf("wrong code error = %v, want ErrAuthentication", err)
	}
	if _, err := OpenSealedRegistration(serverPrivate, clientPublic, sealed, "other-session", "042917"); err != ErrAuthentication {
		t.Fatalf("wrong session error = %v, want ErrAuthentication", err)
	}
}

func TestPairingSessionOpensOnceAndExpires(t *testing.T) {
	now := time.Unix(1793600000, 0)
	session, err := NewPairingSession(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Code) != 6 || len(session.PublicKey()) != 32 || len(session.ID) != 36 {
		t.Fatalf("session = %+v", session)
	}
	client, _ := ecdh.X25519().GenerateKey(nil)
	sealed, err := SealRegistration(client, session.PublicKey(), []byte(`{"x":1}`), session.ID, session.Code, nil)
	if err != nil {
		t.Fatal(err)
	}
	wrongCode := "000000"
	if session.Code == wrongCode {
		wrongCode = "000001"
	}
	bad, _ := SealRegistration(client, session.PublicKey(), []byte(`{"x":1}`), session.ID, wrongCode, nil)
	if _, err := session.Open(client.PublicKey().Bytes(), bad, now); err == nil {
		t.Fatal("a claim sealed with the wrong code was opened")
	}
	if _, err := session.Open(client.PublicKey().Bytes(), sealed, now.Add(PairingLifetime+time.Second)); err != ErrPairingExpired {
		t.Fatalf("expired open error = %v", err)
	}
	if plaintext, err := session.Open(client.PublicKey().Bytes(), sealed, now); err != nil || string(plaintext) != `{"x":1}` {
		t.Fatalf("open = %q, %v", plaintext, err)
	}
	if _, err := session.Open(client.PublicKey().Bytes(), sealed, now); err != ErrPairingUsed {
		t.Fatalf("second open error = %v, want ErrPairingUsed", err)
	}
}

func TestReplayCacheAndTimestamps(t *testing.T) {
	cache := NewReplayCache()
	now := time.Unix(1793600000, 0)
	if !cache.Accept("nonce-1234", now) || cache.Accept("nonce-1234", now.Add(time.Second)) {
		t.Fatal("a nonce was not single-use")
	}
	if cache.Accept("short", now) {
		t.Fatal("a too-short nonce was accepted")
	}
	if !cache.Accept("nonce-1234", now.Add(2*ClockSkew+time.Second)) {
		t.Fatal("an expired nonce stayed blocked forever")
	}
	if !FreshTimestamp(now.Unix()-30, now) || FreshTimestamp(now.Unix()-31, now) || FreshTimestamp(now.Unix()+31, now) {
		t.Fatal("clock skew window is not ±30 s")
	}
}

func TestPCMPreface(t *testing.T) {
	signature := Sign(testSecret(), Canonical("PCM", "/v1/pcm", nil, 1793600000, "nonce-abc-123"))
	preface, err := ParsePCMPreface("AIRSIMVWL1 1793600000 nonce-abc-123 " + signature + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySignature(testSecret(), preface.Signature, PCMCanonical(preface)) {
		t.Fatal("PCM preface signature did not verify")
	}
	for _, bad := range []string{
		"AIRSIMVWL1 1793600000 nonce-abc-123 " + signature,
		"AIRSIMVWL2 1793600000 nonce-abc-123 " + signature + "\n",
		"AIRSIMVWL1 x nonce-abc-123 " + signature + "\n",
		"AIRSIMVWL1 1793600000 n " + signature + "\n",
	} {
		if _, err := ParsePCMPreface(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func hkdfForTest(shared []byte, sessionID, code string) ([]byte, error) {
	return hkdfKey(shared, sessionID, code)
}
