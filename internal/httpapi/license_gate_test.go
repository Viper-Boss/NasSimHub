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
	"strconv"
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
		// A working message path, so the guard can be exercised over HTTP
		// rather than only through featureDecision. Without it the handler
		// answers 503 before it reaches any limit, and a test asserting "not
		// 200" would pass on the wrong refusal.
		Communications: &fakeCommunications{},
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

// Dialling out is never refused for want of a licence, and nothing on that
// path may offer to sell one.
//
// An earlier version had this backwards: outbound calling was entirely paid, so
// every call without a certificate came back 402 with "send us your licence ID
// and we will turn it on". That put a call paywall on the dial pad, and it
// locked dialling on the owner's own gateway. The feature is free within the
// anti-fraud guard's daily limit; a licence lifts that guard.
func TestDiallingIsNeverRefusedForWantOfALicence(t *testing.T) {
	api, _, _ := licensedAPI(t)

	decision := api.featureDecision(context.Background(), license.FeatureCallDial)
	if !decision.Allowed {
		t.Fatal(
			"outbound calling was refused with no certificate; the call site then has " +
				"a refusal to render and the dial pad shows a paywall",
		)
	}
	if decision.Licensed {
		t.Fatal("an installation with no certificate reported as licensed")
	}
	if decision.Limit != nil {
		t.Fatalf("dialling is metered by the catalogue: %+v", decision.Limit)
	}
}

// And the dial handler itself must contain no purchase path at all.
//
// A source check rather than a behavioural one, deliberately. The requirement
// is about what copy can EVER reach that screen, and the failure mode is
// somebody adding a licence gate back to this handler -- which no response
// assertion would catch unless it happened to exercise the exact state that
// triggers it. Reading the handler catches the re-introduction itself.
func TestTheDialHandlerOffersNoLicencePurchase(t *testing.T) {
	source, err := os.ReadFile("communications.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(source), "func (api *API) startCall(")
	if start < 0 {
		t.Fatal("startCall is gone; this test is guarding nothing")
	}
	body := string(source)[start:]
	if end := strings.Index(body, "\nfunc "); end > 0 {
		body = body[:end]
	}
	for _, forbidden := range []string{
		"licenseDenial",
		"StatusPaymentRequired",
		"license_required",
		"license_quota",
		"授权",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf(
				"the dial handler contains %q; outbound calling must never show a "+
					"licence refusal or a purchase prompt",
				forbidden,
			)
		}
	}
	// It must still be guarded, or this test would pass on a handler with no
	// limit at all.
	if !strings.Contains(body, "callGuardStore") {
		t.Fatal("the dial handler no longer applies the anti-fraud guard")
	}
	if !strings.Contains(body, "licensedFeature") {
		t.Fatal("the dial handler no longer lets a licence lift the guard")
	}
}

// WireGuard is machine-scoped, so its refusal must name the machine licence
// rather than the module one. Getting this backwards sends the customer to buy
// the wrong thing.
func TestWireGuardWithoutALicenceNamesTheMachineLicence(t *testing.T) {
	api, _, _ := licensedAPI(t)
	decision := api.featureDecision(context.Background(), license.FeatureWireGuard)
	if decision.Allowed {
		t.Fatal("WireGuard was allowed with no certificate")
	}
	message := api.licenseDenial(decision)
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
		decision := api.featureDecision(context.Background(), feature)
		if !decision.Allowed {
			t.Fatalf("%s was gated", feature)
		}
		if decision.Limit != nil {
			t.Fatalf("%s was metered: %+v", feature, decision.Limit)
		}
	}
}

// Sending SMS is free too, with the message guard as its only limit.
//
// This replaced a test for a metered free tier of three sends a day. That
// counter sat on top of the guard, counted MESSAGES rather than recipients, and
// could not be lifted by the dial-pad unlock -- so an operator who had
// unlocked the guard still hit a wall at three messages with no way past it.
func TestSendingSMSIsFreeAndGuardedRatherThanMetered(t *testing.T) {
	api, _, _ := licensedAPI(t)
	decision := api.featureDecision(context.Background(), license.FeatureSMSSend)
	if !decision.Allowed {
		t.Fatal("sending SMS was refused entirely")
	}
	if decision.Limit != nil {
		t.Fatalf(
			"sending is metered by the catalogue (%+v); the daily limit is the message "+
				"guard, and a counter here is one the dial-pad unlock cannot lift",
			decision.Limit,
		)
	}
	if decision.Licensed {
		t.Fatal("an installation with no certificate reported as licensed")
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

	if api.featureDecision(context.Background(), license.FeatureCallDial).Licensed {
		t.Fatal("a certificate for a module that is not attached lifted the dial guard")
	}
	if api.featureDecision(context.Background(), license.FeatureSMSSend).Licensed {
		t.Fatal("a certificate for a module that is not attached lifted the message guard")
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

	decision := api.featureDecision(context.Background(), license.FeatureWireGuard)
	if decision.Allowed {
		t.Fatal("an expired certificate still unlocked WireGuard")
	}
}

// A send past the guard answers 403 fraud_guard, not 402.
//
// The code matters to the client: 402 means "you have not paid", and this
// refusal is not that. It is an abuse limit the operator can lift themselves,
// so the response has to send them to the dial-pad unlock rather than to a
// purchase.
func TestASendPastTheGuardAnswersTheFraudGuard(t *testing.T) {
	api, _, _ := licensedAPI(t)
	guard, ok := api.messageGuardStore()
	if !ok {
		t.Fatal("the message guard is unavailable")
	}
	// Exhaust the distinct-recipient budget for today.
	for recipient := 0; recipient < messageGuardDailyLimit; recipient++ {
		if guard.Attempt("1380013800" + strconv.Itoa(recipient)) {
			t.Fatalf("recipient %d was refused inside the budget", recipient)
		}
	}

	body := strings.NewReader(`{"line_id":"line-1","to":"13900139000","content":"hi"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/messages", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, request)

	if recorder.Code == http.StatusOK {
		t.Fatalf("a send past the guard succeeded: %s", recorder.Body.String())
	}
	if recorder.Code == http.StatusPaymentRequired {
		t.Fatalf(
			"a send past the anti-fraud guard answered 402; that tells the client the "+
				"operator has not paid, when what happened is an abuse limit they can "+
				"lift themselves: %s",
			recorder.Body.String(),
		)
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "unlock") {
		t.Fatalf("the refusal does not tell the operator how to lift it: %s", recorder.Body.String())
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
		decision := api.featureDecision(context.Background(), feature)
		if !decision.Allowed {
			t.Fatalf("%v was gated although no issuer key is configured", feature)
		}
	}
}
