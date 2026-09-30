package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNasSimHubFirstContactBindsDocumentToTLSKey(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(public)
	id := fmt.Sprintf("NSH-410-%X", hash[:3])
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: id},
		DNSNames:     []string{strings.ToLower(id) + ".node.nassimhub.local"},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		BasicConstraintsValid: true, IsCA: true,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	advertised := base64.StdEncoding.EncodeToString(public)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/node" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"device_id": id, "platform": "msm8916", "public_key": advertised,
		})
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{certDER}, PrivateKey: private}}}
	server.StartTLS()
	defer server.Close()
	got, err := identifyNasSimHubCandidate(context.Background(), server.URL)
	if err != nil || got != id {
		t.Fatalf("verified identity = %q, %v", got, err)
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	advertised = base64.StdEncoding.EncodeToString(other)
	if _, err := identifyNasSimHubCandidate(context.Background(), server.URL); err == nil {
		t.Fatal("discovery document from another key was trusted")
	}
	advertised = base64.StdEncoding.EncodeToString(public)
	id = "NSH-410-FFFFFF"
	if _, err := identifyNasSimHubCandidate(context.Background(), server.URL); err == nil {
		t.Fatal("forged device id was trusted")
	}
}

func TestNasSimHubLiveFirstContact(t *testing.T) {
	baseURL := os.Getenv("NSH_LIVE_NODE_URL")
	if baseURL == "" {
		t.Skip("set NSH_LIVE_NODE_URL for a read-only hardware test")
	}
	id, err := identifyNasSimHubCandidate(context.Background(), baseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("verified device identity: %s", id)
}
