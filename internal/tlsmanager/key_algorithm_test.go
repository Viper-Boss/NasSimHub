package tlsmanager

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"testing"
)

func TestTheServedKeyIsNamed(t *testing.T) {
	t.Parallel()
	clock := newTestClock()
	config := testConfig(t.TempDir(), clock)
	manager, err := Open(config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if got := manager.Status().KeyAlgorithm; got != "ECDSA P-256" {
		t.Fatalf("automatic certificate key = %q, want ECDSA P-256", got)
	}

	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	edKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		key  any
		want string
	}{
		{&p384.PublicKey, "ECDSA P-384"},
		{&rsaKey.PublicKey, "RSA-2048"},
		{edKey, "Ed25519"},
		// Something it does not know is named as nothing, not guessed.
		{"not a key", ""},
		{&rsa.PublicKey{}, ""},
	}
	for _, c := range cases {
		if got := keyAlgorithm(c.key); got != c.want {
			t.Errorf("keyAlgorithm(%T) = %q, want %q", c.key, got, c.want)
		}
	}
}
