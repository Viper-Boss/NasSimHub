package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/clientreport"
	"github.com/human-agent65535/modemdeck/internal/license"
	"github.com/human-agent65535/modemdeck/internal/store"
)

// TestTheReportCarriesNoSubscriberData is the one test in this file that is
// about restraint rather than function.
//
// The report goes to the vendor, from a product that handles other people's
// messages and calls. store.Device carries CurrentICCID, SIMInserted and a
// whole SIM struct directly beside the Model and Firmware fields the report
// does use, so "just include the device" is always one convenient line away --
// and it would ship the customer's SIM account identifiers to us.
//
// The distinction being defended: an IMEI identifies the MODULE, is printed on
// its label, and is already disclosed by the identity reply. An ICCID or IMSI
// identifies the SUBSCRIBER'S ACCOUNT. They look equally like opaque numbers in
// a struct, which is exactly why this is mechanical and not a review habit.
func TestTheReportCarriesNoSubscriberData(t *testing.T) {
	// A repository whose device carries EVERY sensitive field populated.
	//
	// This matters more than it looks. The first version of this test used the
	// empty fixture, so the report contained no modules at all: adding ICCID to
	// the module struct, and adding the dialled numbers to the fraud block,
	// both passed it. `omitempty` on an empty value leaves nothing to find.
	// A privacy test that can only fail when the fixture happens to hold data
	// is not a privacy test, so the fixture now holds all of it.
	api, private, _ := populatedAPI(t)
	machineID := reportMachineID(t, api)

	request := signedReportRequest(t, machineID, private)
	report := api.buildClientReport(context.Background(), request, machineID)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	document := strings.ToLower(string(encoded))

	// Field names first: a struct copied in wholesale brings its JSON tags.
	for _, forbidden := range []string{
		"iccid", "imsi", "msisdn", "sim_inserted", "subscriber",
		"latitude", "longitude", "location",
		"contact", "message_body", "text", "call_log", "history",
		"password", "token", "secret", "private_key", "wallet",
		"phone_number", "lac", "cell_id", "operator", "roaming", "apn",
		// Key MATERIAL, not the word: "net.wireguard" is a feature name and
		// belongs in the entitlement list. Banning the bare word caught that
		// and would have pushed the next person to weaken the test rather
		// than the report.
		"wireguard_key", "preshared", "peer_key", "public_key",
	} {
		if strings.Contains(document, forbidden) {
			t.Errorf("the client report contains %q, which is not ours to send:\n%s",
				forbidden, encoded)
		}
	}

	// And the values the fixture supplies, in case a field is renamed.
	for _, forbidden := range fakeRepositorySensitiveValues() {
		if forbidden == "" {
			continue
		}
		if strings.Contains(document, strings.ToLower(forbidden)) {
			t.Errorf("the client report leaks the value %q", forbidden)
		}
	}

	// A report that leaked nothing because it contained nothing would pass
	// every check above, so the fields that ARE ours are asserted present.
	for _, expected := range []string{reportableIMEI, reportableFirmware} {
		if !strings.Contains(document, strings.ToLower(expected)) {
			t.Errorf("the report is missing %q, which it is supposed to carry; "+
				"this test would pass on an empty report without this check", expected)
		}
	}
}

// The fraud-guard block reports counts, never destinations.
//
// The guard's own state holds the distinct numbers dialled today, because that
// is how it counts them. How MANY an installation used is the accountability
// record the guard exists to produce; WHICH ones is a call log, and a call log
// is not what an issuer needs to decide whether to sell somebody a licence.
func TestTheFraudGuardReportsCountsNotNumbers(t *testing.T) {
	api, private, _ := licensedAPI(t)
	machineID := reportMachineID(t, api)

	guard, ok := api.callGuardStore()
	if !ok {
		t.Skip("no guard in this configuration")
	}
	const dialled = "+8613800138000"
	guard.Attempt(dialled)
	guard.Attempt("+8613800138001")

	request := signedReportRequest(t, machineID, private)
	report := api.buildClientReport(context.Background(), request, machineID)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), dialled) {
		t.Fatalf("the report leaked a dialled number:\n%s", encoded)
	}
	if report.FraudGuard.Call.Used != 2 {
		t.Errorf("used = %d, want 2", report.FraudGuard.Call.Used)
	}
	if report.FraudGuard.Call.Remaining != report.FraudGuard.Call.Limit-2 {
		t.Errorf("remaining = %d with limit %d",
			report.FraudGuard.Call.Remaining, report.FraudGuard.Call.Limit)
	}
	if report.FraudGuard.Triggered {
		t.Error("triggered with allowance left")
	}
}

// Reaching the limit is what "triggered" means, and an unlocked guard is not
// triggered -- it is deliberately lifted, which is a different fact.
func TestTriggeredMeansTheLimitWasReached(t *testing.T) {
	api, private, _ := licensedAPI(t)
	machineID := reportMachineID(t, api)
	guard, ok := api.callGuardStore()
	if !ok {
		t.Skip("no guard")
	}
	for index := 0; index < callGuardDailyLimit; index++ {
		guard.Attempt("+861380013800" + string(rune('0'+index)))
	}
	request := signedReportRequest(t, machineID, private)
	report := api.buildClientReport(context.Background(), request, machineID)
	if !report.FraudGuard.Triggered {
		t.Fatalf("the limit was reached and triggered is false: %+v", report.FraudGuard.Call)
	}

	guard.Unlock()
	report = api.buildClientReport(context.Background(), request, machineID)
	if report.FraudGuard.Triggered {
		t.Error("an unlocked guard reports as triggered")
	}
	if !report.FraudGuard.Call.Unlocked {
		t.Error("an unlocked guard does not report unlocked")
	}
	if report.FraudGuard.Call.Used != 0 {
		t.Errorf("an unlocked guard reports used = %d; Remaining() returns 0 when "+
			"unlocked, so a naive subtraction would claim the limit was fully spent",
			report.FraudGuard.Call.Used)
	}
}

// Every feature appears, licensed or not.
//
// A report listing only what was granted would make "never bought" and "this
// build no longer has that feature" identical from the console's side.
func TestEveryCatalogueFeatureIsReported(t *testing.T) {
	api, private, _ := licensedAPI(t)
	machineID := reportMachineID(t, api)
	report := api.buildClientReport(
		context.Background(), signedReportRequest(t, machineID, private), machineID)

	reported := map[string]bool{}
	for _, feature := range report.Entitlements.Features {
		reported[feature.Feature] = true
	}
	for _, expected := range []license.Feature{
		license.FeatureSMSForward,
		license.FeatureSMSSend,
		license.FeatureCallAnswer,
		license.FeatureCallDial,
		license.FeatureWireGuard,
	} {
		if !reported[string(expected)] {
			t.Errorf("%s is missing from the report", expected)
		}
	}
}

// The issuer key fingerprint answers "can this gateway verify anything at all"
// before the console signs a certificate that could never work.
func TestTheReportNamesTheIssuerKeyItCanVerifyWith(t *testing.T) {
	api, private, _ := licensedAPI(t)
	machineID := reportMachineID(t, api)
	report := api.buildClientReport(
		context.Background(), signedReportRequest(t, machineID, private), machineID)

	if !report.System.CanVerifyCertificates {
		t.Error("a build with an issuer key says it cannot verify")
	}
	if len(report.System.IssuerKeyFingerprint) != 8 {
		t.Errorf("fingerprint = %q, want 8 hex characters",
			report.System.IssuerKeyFingerprint)
	}

	// A build with no key must say so on both fields, because that state is
	// otherwise indistinguishable from an unpaid installation.
	if got := issuerKeyFingerprint(""); got != "" {
		t.Errorf("a build with no key reported fingerprint %q", got)
	}
	if got := issuerKeyFingerprint("not base64"); got != "" {
		t.Errorf("a malformed key reported fingerprint %q", got)
	}
}

// A rejected certificate must be reported WITH its reason. This is the field
// that turns "I paid and nothing happened" into an answer.
func TestRejectedCertificatesAreReportedWithReasons(t *testing.T) {
	api, private, directory := licensedAPI(t)
	machineID := reportMachineID(t, api)

	// A certificate for a different machine: present, signed correctly, and
	// inapplicable. The most common real mistake.
	expired, err := license.Sign(license.Certificate{
		Version:   license.CurrentVersion,
		ID:        "cert-elsewhere",
		Scope:     license.ScopeMachine,
		MachineID: "some-other-machine",
		Features:  []string{string(license.FeatureWireGuard)},
		IssuedAt:  time.Now().Add(-48 * time.Hour).Unix(),
		ExpiresAt: time.Now().Add(-24 * time.Hour).Unix(),
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	writeCertificate(t, directory, expired, "license/machine/certificate.json")

	report := api.buildClientReport(
		context.Background(), signedReportRequest(t, machineID, private), machineID)

	if len(report.Entitlements.Rejected) == 0 {
		t.Fatalf("a certificate that did not apply was not reported as rejected: %+v",
			report.Entitlements)
	}
	rejection := report.Entitlements.Rejected[0]
	if rejection.ID != "cert-elsewhere" {
		t.Errorf("rejection names %q", rejection.ID)
	}
	if strings.TrimSpace(rejection.Reason) == "" {
		t.Error("a rejection with no reason is no better than silence")
	}
}

// The reply must stay inside the size bound the sender enforces, with a
// realistic amount of content in it.
func TestTheReportFitsItsSizeBound(t *testing.T) {
	api, private, _ := licensedAPI(t)
	machineID := reportMachineID(t, api)
	encoded, err := json.Marshal(api.buildClientReport(
		context.Background(), signedReportRequest(t, machineID, private), machineID))
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > clientReportMaxReplyBytes {
		t.Fatalf("the report is %d bytes, over the %d-byte send bound",
			len(encoded), clientReportMaxReplyBytes)
	}
	if len(encoded) < 200 {
		t.Fatalf("the report is only %d bytes; it has stopped reporting anything:\n%s",
			len(encoded), encoded)
	}
}

// ---------------------------------------------------------------------------

// reportMachineID returns a synthetic identity.
//
// buildClientReport takes the machine ID as a parameter, so these tests do not
// need a real hardware fingerprint -- and must not depend on one. An earlier
// version read the true fingerprint and skipped when it was unreadable, which
// meant every test in this file silently skipped in a container: a skipped
// privacy test is no privacy test at all. The signature path that does need a
// real identity is covered in internal/clientreport.
func reportMachineID(t *testing.T, api *API) string {
	t.Helper()
	if api.licensing() == nil {
		t.Fatal("no licensing runtime")
	}
	return "test-machine-1"
}

func signedReportRequest(t *testing.T, machineID string, private []byte) clientreport.Request {
	t.Helper()
	request, err := clientreport.Sign(
		"nonce-for-tests", machineID, time.Now(), private)
	if err != nil {
		t.Fatalf("sign report request: %v", err)
	}
	return request
}

// fakeRepositorySensitiveValues lists the values the test repository holds that
// must never appear in a report. Kept beside the test so that adding a
// sensitive field to the fixture also extends the check.
func fakeRepositorySensitiveValues() []string {
	return []string{
		sensitiveICCID, sensitiveIMSI, sensitivePhone,
		sensitiveLAC, sensitiveCellID,
	}
}

const (
	sensitiveICCID  = "89860412345678901234"
	sensitiveIMSI   = "460001234567890"
	sensitivePhone  = "+8613800138999"
	sensitiveLAC    = "4A2B"
	sensitiveCellID = "00C1F3A9"
	// The two facts that ARE ours to report, asserted present so this test
	// cannot pass by reporting nothing at all.
	reportableIMEI     = "860000000000001"
	reportableFirmware = "QDC507-1.2.3"
)

// populatedAPI is licensedAPI with a module whose subscriber fields are all
// filled in, so a leak has something to leak.
func populatedAPI(t *testing.T) (*API, ed25519.PrivateKey, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	api, err := New(&fakeRepository{devices: []store.Device{{
		IMEI:         reportableIMEI,
		EndpointID:   "endpoint-1",
		Name:         "line-a",
		Model:        "QDC507",
		Firmware:     reportableFirmware,
		Present:      true,
		LastSeen:     "2026-09-22T10:00:00Z",
		CurrentICCID: sensitiveICCID,
		SIMInserted:  true,
		SIM: &store.SIMCard{
			ICCID:       sensitiveICCID,
			IMSI:        sensitiveIMSI,
			PhoneNumber: sensitivePhone,
			Operator:    "China Mobile",
			LAC:         sensitiveLAC,
			CellID:      sensitiveCellID,
			APN:         "cmnet",
			Roaming:     false,
		},
	}}}, Options{
		disableAuthentication:  true,
		ContactBackupDirectory: filepath.Join(root, "backups", "contacts"),
		IssuerPublicKey:        base64.StdEncoding.EncodeToString(public),
		Communications:         &fakeCommunications{},
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
