package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// identifyNasSimHubCandidate verifies the TLS key, self-signature, and the
// public identity document together. This proves which key answered; it does
// not authorize adoption of that key as this NAS's Node.
func identifyNasSimHubCandidate(ctx context.Context, baseURL string) (string, error) {
	var certificate *x509.Certificate
	transport := &http.Transport{
		Proxy:               nil,
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 2 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			// First contact has no trusted root. The callback below replaces
			// ordinary CA validation with a mandatory self-signature check.
			InsecureSkipVerify: true, //nolint:gosec
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(raw) != 1 {
					return errors.New("node must present one certificate")
				}
				leaf, err := x509.ParseCertificate(raw[0])
				if err != nil {
					return err
				}
				if _, ok := leaf.PublicKey.(ed25519.PublicKey); !ok {
					return errors.New("node certificate must use Ed25519")
				}
				if err := leaf.CheckSignatureFrom(leaf); err != nil {
					return err
				}
				now := time.Now()
				if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
					return errors.New("node certificate is outside its validity period")
				}
				certificate = leaf
				return nil
			},
		},
	}
	client := &http.Client{
		Timeout:   3 * time.Second,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("node discovery must not redirect")
		},
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/node", nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("node identity returned %d", response.StatusCode)
	}
	var document struct {
		DeviceID  string `json:"device_id"`
		Platform  string `json:"platform"`
		PublicKey string `json:"public_key"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil {
		return "", err
	}
	if len(body) > 8192 {
		return "", errors.New("node identity exceeds size limit")
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return "", err
	}
	if document.Platform != "msm8916" || !strings.HasPrefix(document.DeviceID, "NSH-410-") {
		return "", errors.New("not an MSM8916 NasSimHub Node")
	}
	key, err := base64.StdEncoding.DecodeString(document.PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return "", errors.New("node identity has invalid public key")
	}
	digest := sha256.Sum256(key)
	expectedID := fmt.Sprintf("NSH-410-%X", digest[:3])
	if document.DeviceID != expectedID || certificate == nil || certificate.Subject.CommonName != expectedID {
		return "", errors.New("node id is not bound to its certificate")
	}
	certKey := certificate.PublicKey.(ed25519.PublicKey)
	if !certKey.Equal(ed25519.PublicKey(key)) {
		return "", errors.New("node document key differs from TLS key")
	}
	if err := certificate.VerifyHostname(strings.ToLower(expectedID) + ".node.nassimhub.local"); err != nil {
		return "", err
	}
	return expectedID, nil
}
