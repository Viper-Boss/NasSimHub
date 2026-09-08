package httpapi

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/store"
)

func TestMobileEnrollmentEncryptedSingleUse(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	public := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	invitation := strings.Repeat("a", 43)
	digest := sha256.Sum256([]byte(invitation))
	for _, scenario := range []struct {
		name             string
		expired, allowed bool
		want             int
	}{
		{"success", false, true, 201}, {"expired", true, true, 401}, {"permission revoked", false, false, 403},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			repository := &fakeMobilePairingRepository{fakeRepository: &fakeRepository{}, pairing: store.IOSPairingStatus{Allowed: scenario.allowed}}
			api, err := New(repository, Options{disableAuthentication: true})
			if err != nil {
				t.Fatal(err)
			}
			expiry := time.Now().Add(time.Minute)
			if scenario.expired {
				expiry = time.Now().Add(-time.Minute)
			}
			api.mobileInvites = map[[32]byte]mobileInvite{digest: {UserID: "owner", Expires: expiry}}
			body, _ := json.Marshal(map[string]string{"invitation": invitation, "public_key": public})
			exchange := func() *httptest.ResponseRecorder {
				request := httptest.NewRequest("POST", "/api/v1/mobile/enrollment/exchange", bytes.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				api.exchangeMobileInvite(response, request)
				return response
			}
			response := exchange()
			if response.Code != scenario.want {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("credential response must not be cached")
			}
			if scenario.want != 201 {
				if repository.createdUserID != "" {
					t.Fatal("unauthorized credential issued")
				}
				return
			}
			var payload map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			ciphertext, err := base64.StdEncoding.DecodeString(payload["encrypted_token"])
			if err != nil {
				t.Fatal(err)
			}
			token, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, key, ciphertext, []byte("nassimhub.mobile.enrollment.v1"))
			if err != nil || len(token) == 0 {
				t.Fatalf("cannot decrypt: %v", err)
			}
			if strings.Contains(response.Body.String(), string(token)) {
				t.Fatal("plaintext credential leaked")
			}
			if repository.createdUserID != "owner" {
				t.Fatal("wrong credential owner")
			}
			if again := exchange(); again.Code != 401 {
				t.Fatalf("replay accepted: %d", again.Code)
			}
		})
	}
}
