package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"github.com/human-agent65535/modemdeck/internal/license"
	"github.com/human-agent65535/modemdeck/internal/trustengine"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIssuerTrustApprovalSignedGrantMatrix(t *testing.T) {
	for _, variant := range []string{"valid", "expired", "other-machine", "other-feature", "forged", "module-only"} {
		t.Run(variant, func(t *testing.T) {
			api, key, directory := licensedAPI(t)
			trustAPI, engine, _ := newTrustAPI(t)
			api.nodeFleet = trustAPI.nodeFleet
			runtime := api.licensing()
			runtime.once.Do(func() { runtime.machine = "test-machine" })
			now := time.Now()
			cert := license.Certificate{Version: license.CurrentVersion, ID: "c_test_trust_admin", Scope: license.ScopeMachine, MachineID: "test-machine", Features: []string{string(license.FeatureTrustAdmin)}, IssuedAt: now.Add(-time.Hour).Unix(), ExpiresAt: now.Add(time.Hour).Unix()}
			switch variant {
			case "expired":
				cert.ExpiresAt = now.Add(-time.Minute).Unix()
			case "other-machine":
				cert.MachineID = "different-machine"
			case "other-feature":
				cert.Features = []string{string(license.FeatureWireGuard)}
			case "forged":
				_, key, _ = ed25519.GenerateKey(rand.Reader)
			case "module-only":
				cert.Scope = license.ScopeModule
				cert.ModuleSN = "module-test"
				cert.MachineID = ""
			}
			signed, err := license.Sign(cert, key)
			if err != nil {
				t.Fatal(err)
			}
			writeCertificate(t, directory, signed, filepath.Join("license", "machine", "certificate.json"))
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/v1/security/trust/approve", strings.NewReader(`{"device_id":"NSH-410-AAAAAA","reason":"verified household hardware"}`))
			req.Header.Set("Content-Type", "application/json")
			api.securityTrustApprove(w, req)
			if variant == "valid" {
				if w.Code != 200 {
					t.Fatal("valid grant rejected", w.Code, w.Body.String())
				}
				if err := engine.Require(context.Background(), "NSH-410-AAAAAA", trustengine.ActionDial); err != nil {
					t.Fatal(err)
				}
			} else if w.Code != 403 {
				t.Fatal("invalid grant accepted", w.Code, w.Body.String())
			}
		})
	}
}
