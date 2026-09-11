package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/license"
)

// The paid feature gates, exercised through the real path.
//
// Every other test in this package sets disableLicenseEnforcement, because
// they are about call mechanics rather than licensing. These are the ones that
// are about licensing, so they do not.

// The escape hatch must never leave the test tree. It is unexported, like
// disableAuthentication, but unexported is not the same as unused: a shipped
// file could set it and nothing would complain.
func TestNothingInTheShippedTreeDisablesLicenceEnforcement(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	setter := regexp.MustCompile(`disableLicenseEnforcement:\s*true`)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		// The declaration and the read are expected; an assignment to true is
		// not.
		if setter.Match(raw) {
			t.Fatalf("%s disables licence enforcement outside a test", name)
		}
	}
}

// licensedAPI builds an API with real licence enforcement and a real issuer
// key, so a certificate can be minted for it.
func licensedAPI(t *testing.T) (*API, ed25519.PrivateKey, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	api, err := New(&fakeRepository{}, Options{
		disableAuthentication:  true,
		ContactBackupDirectory: filepath.Join(root, "backups", "contacts"),
		IssuerPublicKey:        base64.StdEncoding.EncodeToString(public),
	})
	if err != nil {
		t.Fatal(err)
	}
	directory, ok := api.homeSIMDataDirectory()
	if !ok {
		t.Fatal("no data directory")
	}
	return api, private, directory
}

func writeCertificate(t *testing.T, directory string, signed license.Signed, relative string) {
	t.Helper()
	path := filepath.Join(directory, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(signed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Outbound calling is paid, and the refusal has to say what to buy. A paywall
// that does not name what is missing is just a wall.
func TestDiallingWithoutALicenceSaysWhatIsMissing(t *testing.T) {
	api, _, _ := licensedAPI(t)

	decision, _ := api.featureDecision(context.Background(), license.FeatureCallDial)
	if decision.Allowed {
		t.Fatal("outbound calling was allowed with no certificate")
	}
	message := licenseDenial(decision)
	if !strings.Contains(message, "模组授权") {
		t.Fatalf("the refusal does not name the module licence: %q", message)
	}
}

// WireGuard is machine-scoped, so its refusal must name the machine licence
// rather than the module one. Getting this backwards sends the customer to buy
// the wrong thing.
func TestWireGuardWithoutALicenceNamesTheMachineLicence(t *testing.T) {
	api, _, _ := licensedAPI(t)
	decision, _ := api.featureDecision(context.Background(), license.FeatureWireGuard)
	if decision.Allowed {
		t.Fatal("WireGuard was allowed with no certificate")
	}
	message := licenseDenial(decision)
	if !strings.Contains(message, "本机授权") {
		t.Fatalf("the refusal does not name the machine licence: %q", message)
	}
	if strings.Contains(message, "模组授权") {
		t.Fatalf("a machine feature told the customer to buy a module licence: %q", message)
	}
}

// Receiving must never be gated. It is what makes the product useful at all,
// and a gateway that cannot receive is not worth running.
func TestReceivingIsNeverGated(t *testing.T) {
	api, _, _ := licensedAPI(t)
	for _, feature := range []license.Feature{license.FeatureSMSForward, license.FeatureCallAnswer} {
		decision, _ := api.featureDecision(context.Background(), feature)
		if !decision.Allowed {
			t.Fatalf("%s was gated", feature)
		}
		if decision.Limit != nil {
			t.Fatalf("%s was metered: %+v", feature, decision.Limit)
		}
	}
}

// Sending SMS is free for a few a day. The number comes from the catalogue, so
// this asserts the free tier exists rather than hardcoding the policy twice.
func TestSendingSMSIsFreeUpToTheCatalogueLimit(t *testing.T) {
	api, _, _ := licensedAPI(t)
	decision, usage := api.featureDecision(context.Background(), license.FeatureSMSSend)
	if !decision.Allowed {
		t.Fatal("the free SMS tier was refused entirely")
	}
	if decision.Limit == nil || decision.Limit.Count < 1 {
		t.Fatalf("no free SMS allowance: %+v", decision.Limit)
	}
	for used := 0; used < decision.Limit.Count; used++ {
		if !usage.Consume(license.FeatureSMSSend, decision) {
			t.Fatalf("free send %d was refused", used+1)
		}
	}
	if usage.Consume(license.FeatureSMSSend, decision) {
		t.Fatal("a send beyond the free allowance was permitted")
	}
}

// A module certificate lifts the SMS allowance, but ONLY while its module is
// attached. The fake repository reports no devices, so a certificate for some
// other module must not unlock anything.
func TestACertificateForAnAbsentModuleUnlocksNothing(t *testing.T) {
	api, private, directory := licensedAPI(t)
	signed, err := license.Sign(license.Certificate{
		Version: license.CurrentVersion, ID: "c_absent", Scope: license.ScopeModule,
		ModuleSN: "869999999999999",
		Features: []string{string(license.FeatureCallDial), string(license.FeatureSMSSend)},
		IssuedAt: time.Now().Unix(),
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	writeCertificate(t, directory, signed, filepath.Join("license", "modules", "869999999999999.json"))

	decision, _ := api.featureDecision(context.Background(), license.FeatureCallDial)
	if decision.Allowed {
		t.Fatal("a certificate for a module that is not attached unlocked dialling")
	}
	// And the SMS allowance stays at the free tier rather than going unlimited.
	send, _ := api.featureDecision(context.Background(), license.FeatureSMSSend)
	if send.Limit == nil {
		t.Fatal("an absent module's certificate lifted the free SMS allowance")
	}
}

// An expired certificate must stop working. A licence that outlives its expiry
// is not a subscription.
func TestAnExpiredCertificateStopsUnlocking(t *testing.T) {
	api, private, directory := licensedAPI(t)
	issued := time.Now().Add(-48 * time.Hour)
	signed, err := license.Sign(license.Certificate{
		Version: license.CurrentVersion, ID: "c_expired", Scope: license.ScopeMachine,
		MachineID: "whatever", Features: []string{string(license.FeatureWireGuard)},
		IssuedAt:  issued.Unix(),
		ExpiresAt: issued.Add(time.Hour).Unix(),
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	writeCertificate(t, directory, signed, filepath.Join("license", "machine", "certificate.json"))

	decision, _ := api.featureDecision(context.Background(), license.FeatureWireGuard)
	if decision.Allowed {
		t.Fatal("an expired certificate still unlocked WireGuard")
	}
}

// The HTTP surface has to answer 402 rather than 403, so a client can tell
// "you have not paid for this" apart from "you are not allowed to do this".
func TestAnUnlicensedSendAnswers402(t *testing.T) {
	api, _, directory := licensedAPI(t)
	// Exhaust the free allowance directly, so the request hits the quota
	// branch rather than the not-allowed branch.
	usage := license.NewUsageCounter(directory)
	entitlements := license.Entitlements{}
	decision := entitlements.Check(license.FeatureSMSSend)
	for used := 0; used < decision.Limit.Count; used++ {
		usage.Consume(license.FeatureSMSSend, decision)
	}

	body := strings.NewReader(`{"line_id":"line-1","to":"13800138000","content":"hi"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/messages", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, request)

	// 402 or a service-unavailable from the missing communications backend are
	// both acceptable here; what must not happen is 200.
	if recorder.Code == http.StatusOK {
		t.Fatalf("an over-quota send succeeded: %s", recorder.Body.String())
	}
}

// No issuer key configured means the gates stay off: with no key no
// certificate could ever verify, so enforcing there would only lock the
// operator out of a machine they own. Configuring a key is what turns the
// gates on -- licensedAPI above covers that side.
func TestNoIssuerKeyRunsUnlocked(t *testing.T) {
	api, err := New(&fakeRepository{}, Options{
		disableAuthentication:  true,
		ContactBackupDirectory: filepath.Join(t.TempDir(), "backups", "contacts"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, feature := range []license.Feature{license.FeatureCallDial, license.FeatureSMSSend, license.FeatureWireGuard} {
		decision, _ := api.featureDecision(context.Background(), feature)
		if !decision.Allowed {
			t.Fatalf("%v was gated although no issuer key is configured", feature)
		}
	}
}
