package license

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, func(Certificate) Signed) {
	t.Helper()
	public, private := issuer(t)
	store := NewStore(t.TempDir(), base64.StdEncoding.EncodeToString(public))
	sign := func(certificate Certificate) Signed {
		signed, err := Sign(certificate, private)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	return store, sign
}

func machineCertificate(machineID string, features ...string) Certificate {
	return Certificate{
		Version: CurrentVersion, ID: "c_machine", Scope: ScopeMachine,
		MachineID: machineID, Features: features, IssuedAt: time.Now().Unix(),
	}
}

func moduleCertificate(serial string, features ...string) Certificate {
	return Certificate{
		Version: CurrentVersion, ID: "c_module_" + serial, Scope: ScopeModule,
		ModuleSN: serial, Features: features, IssuedAt: time.Now().Unix(),
	}
}

// A build with no issuer key must lock every paid feature rather than refusing
// to start. Taking the free features down with the paid ones would turn a
// packaging mistake into a dead gateway.
func TestABuildWithNoIssuerKeyLocksPaidFeaturesAndKeepsFreeOnes(t *testing.T) {
	store := NewStore(t.TempDir(), "")
	if store.Configured() {
		t.Fatal("a store with no key reported itself configured")
	}
	entitlements := store.Entitlements("machine-1", []string{"SN1"}, time.Now())
	if entitlements.Check(FeatureWireGuard).Allowed {
		t.Fatal("WireGuard was allowed with no issuer key")
	}
	if entitlements.Check(FeatureCallDial).Allowed {
		t.Fatal("outbound calling was allowed with no issuer key")
	}
	// Receiving must keep working: it is what makes the product useful at all.
	if !entitlements.Check(FeatureSMSForward).Allowed {
		t.Fatal("SMS forwarding was locked by a missing issuer key")
	}
	if !entitlements.Check(FeatureCallAnswer).Allowed {
		t.Fatal("answering calls was locked by a missing issuer key")
	}
}

// A certificate that cannot be verified must be refused at the door, not
// stored. A stored bad certificate is a file that looks like a licence, reports
// as rejected forever, and gives a paying customer something to point at.
func TestAnUnverifiableCertificateIsNotStored(t *testing.T) {
	store, sign := newTestStore(t)
	signed := sign(machineCertificate("machine-1", string(FeatureWireGuard)))
	signed.Certificate.MachineID = "machine-2" // signature no longer matches

	if err := store.Save(signed, time.Now()); err == nil {
		t.Fatal("a tampered certificate was accepted")
	}
	if _, err := os.Stat(store.machinePath()); err == nil {
		t.Fatal("a tampered certificate was written to disk")
	}
}

func TestAMachineCertificateUnlocksTheMachineFeature(t *testing.T) {
	store, sign := newTestStore(t)
	if err := store.Save(sign(machineCertificate("machine-1", string(FeatureWireGuard))), time.Now()); err != nil {
		t.Fatal(err)
	}
	entitlements := store.Entitlements("machine-1", nil, time.Now())
	decision := entitlements.Check(FeatureWireGuard)
	if !decision.Allowed || !decision.Licensed {
		t.Fatalf("decision = %+v", decision)
	}
	// And not on another machine, or the binding means nothing.
	elsewhere := store.Entitlements("machine-2", nil, time.Now().Add(time.Minute))
	if elsewhere.Check(FeatureWireGuard).Allowed {
		t.Fatal("a machine certificate worked on a different machine")
	}
}

// The point of storing module certificates separately: the licence follows the
// module. Moving the modem to another gateway is a file copy.
func TestAModuleCertificateOnlyCountsWhileItsModuleIsPresent(t *testing.T) {
	store, sign := newTestStore(t)
	if err := store.Save(sign(moduleCertificate("SN-AAA", string(FeatureCallDial))), time.Now()); err != nil {
		t.Fatal(err)
	}
	present := store.Entitlements("machine-1", []string{"SN-AAA"}, time.Now())
	if !present.Check(FeatureCallDial).Allowed {
		t.Fatal("outbound calling was locked with its module attached")
	}
	// Module pulled out. The cache must not keep answering yes.
	absent := store.Entitlements("machine-1", []string{"SN-BBB"}, time.Now())
	if absent.Check(FeatureCallDial).Allowed {
		t.Fatal("outbound calling stayed unlocked after the module was removed")
	}
}

// The cache is on the path of every dial, so it has to be invalidated by the
// two things that change the answer: a new certificate, and the module set.
func TestANewCertificateTakesEffectWithoutARestart(t *testing.T) {
	store, sign := newTestStore(t)
	now := time.Now()
	if store.Entitlements("machine-1", nil, now).Check(FeatureWireGuard).Allowed {
		t.Fatal("WireGuard was allowed before any certificate existed")
	}
	// Saving must drop the cache, or a customer who has just paid waits for a
	// TTL with no way to know why.
	if err := store.Save(sign(machineCertificate("machine-1", string(FeatureWireGuard))), now); err != nil {
		t.Fatal(err)
	}
	if !store.Entitlements("machine-1", nil, now).Check(FeatureWireGuard).Allowed {
		t.Fatal("a newly saved certificate did not take effect")
	}
}

func TestAModuleSerialCannotEscapeItsDirectory(t *testing.T) {
	store, sign := newTestStore(t)
	// The serial reaches this from a certificate that arrived over the network.
	// Verify() already refuses separators, so this is the second line of
	// defence rather than the only one -- which is why it is tested.
	for _, serial := range []string{"../../etc/passwd", "..", "/absolute", "a/b"} {
		signed := sign(moduleCertificate(serial, string(FeatureCallDial)))
		_ = store.Save(signed, time.Now())
		path := store.modulePath(serial)
		resolved, err := filepath.Abs(path)
		if err != nil {
			t.Fatal(err)
		}
		root, err := filepath.Abs(filepath.Join(store.root, moduleDirectory))
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Dir(resolved) != root {
			t.Fatalf("serial %q resolved outside the module directory: %s", serial, resolved)
		}
	}
}

// The activation fingerprint is written once. Rewriting it every boot would
// destroy exactly what it is for: a record of the machine AS IT WAS ACTIVATED,
// which is the only way to tell a repaired board from a different one.
func TestTheActivationFingerprintIsNeverOverwritten(t *testing.T) {
	store, _ := newTestStore(t)
	first := Fingerprint{
		MachineID: "aaaa1111", PrimarySource: "emmc-cid-mmcblk0",
		Version: DerivationVersion,
		Sources: map[string]string{"emmc-cid-mmcblk0": "original"},
	}
	if err := store.SaveFingerprint(first); err != nil {
		t.Fatal(err)
	}
	later := Fingerprint{
		MachineID: "bbbb2222", PrimarySource: "emmc-cid-mmcblk0",
		Version: DerivationVersion,
		Sources: map[string]string{"emmc-cid-mmcblk0": "replaced"},
	}
	if err := store.SaveFingerprint(later); err != nil {
		t.Fatal(err)
	}
	stored, found := store.StoredFingerprint()
	if !found {
		t.Fatal("the fingerprint was not readable back")
	}
	if stored.MachineID != "aaaa1111" || stored.Sources["emmc-cid-mmcblk0"] != "original" {
		t.Fatalf("the activation fingerprint was overwritten: %+v", stored)
	}
}

// The free allowance and the fraud guard are different counters. This is the
// test that stops them being merged later: the fraud guard is lifted by typing
// "unlock" on the dial pad, and if that lifted the free allowance the paywall
// would be bypassed by a word typed into the dial pad.
func TestTheFreeAllowanceHasNoUnlockOfAnyKind(t *testing.T) {
	allowance := NewUsageCounter(t.TempDir())
	entitlements := Entitlements{granted: map[Scope]map[Feature]bool{}}
	decision := entitlements.Check(FeatureSMSSend)
	if decision.Limit == nil || decision.Limit.Count != 3 {
		t.Fatalf("the free SMS allowance is %+v; expected 3 per day", decision.Limit)
	}

	for attempt := 1; attempt <= 3; attempt++ {
		if !allowance.Consume(FeatureSMSSend, decision) {
			t.Fatalf("send %d of the free allowance was refused", attempt)
		}
	}
	if allowance.Consume(FeatureSMSSend, decision) {
		t.Fatal("a fourth free send was allowed")
	}
	if remaining := allowance.Remaining(FeatureSMSSend, decision); remaining != 0 {
		t.Fatalf("remaining = %d after exhausting the allowance", remaining)
	}
}

func TestALicensedModuleLiftsTheAllowanceEntirely(t *testing.T) {
	store, sign := newTestStore(t)
	if err := store.Save(sign(moduleCertificate("SN-AAA", string(FeatureSMSSend))), time.Now()); err != nil {
		t.Fatal(err)
	}
	entitlements := store.Entitlements("machine-1", []string{"SN-AAA"}, time.Now())
	decision := entitlements.Check(FeatureSMSSend)
	if !decision.Allowed || decision.Limit != nil {
		t.Fatalf("a licensed module still has a limit: %+v", decision)
	}
	// Unlimited must read as unlimited, not as zero remaining.
	allowance := NewUsageCounter(t.TempDir())
	if remaining := allowance.Remaining(FeatureSMSSend, decision); remaining != -1 {
		t.Fatalf("unlimited reported %d remaining; -1 means unlimited", remaining)
	}
	for attempt := 0; attempt < 50; attempt++ {
		if !allowance.Consume(FeatureSMSSend, decision) {
			t.Fatalf("a licensed send was refused after %d", attempt)
		}
	}
}

// The allowance is consumed before the action, so a failed send has to be
// refundable. Charging a day's allowance for a message that never left is a
// small unfairness that gets remembered.
func TestAFailedSendCanBeRefunded(t *testing.T) {
	allowance := NewUsageCounter(t.TempDir())
	entitlements := Entitlements{granted: map[Scope]map[Feature]bool{}}
	decision := entitlements.Check(FeatureSMSSend)

	if !allowance.Consume(FeatureSMSSend, decision) {
		t.Fatal("the first send was refused")
	}
	if used := allowance.Used(FeatureSMSSend); used != 1 {
		t.Fatalf("used = %d", used)
	}
	allowance.Refund(FeatureSMSSend)
	if used := allowance.Used(FeatureSMSSend); used != 0 {
		t.Fatalf("used = %d after a refund", used)
	}
	// A refund must not go negative, or a caller that refunds twice grants
	// itself extra sends.
	allowance.Refund(FeatureSMSSend)
	if used := allowance.Used(FeatureSMSSend); used != 0 {
		t.Fatalf("a refund below zero produced used = %d", used)
	}
}

// Retrying at the boundary must not get through, and the count must stay
// truthful while it is refused.
//
// This test replaced one that asserted refused attempts were COUNTED, on the
// theory that it stopped boundary retries. It does not: once the count reaches
// the limit every further attempt is refused anyway. The old version passed
// with the behaviour either way, which is how the bad reasoning survived --
// found by sabotaging Consume and watching the test not fail.
func TestRetryingAtTheBoundaryIsRefusedAndNotCounted(t *testing.T) {
	allowance := NewUsageCounter(t.TempDir())
	entitlements := Entitlements{granted: map[Scope]map[Feature]bool{}}
	decision := entitlements.Check(FeatureSMSSend)
	for attempt := 0; attempt < 3; attempt++ {
		allowance.Consume(FeatureSMSSend, decision)
	}
	for attempt := 0; attempt < 10; attempt++ {
		if allowance.Consume(FeatureSMSSend, decision) {
			t.Fatal("retrying at the boundary got through")
		}
	}
	// 3 of 3, not 13 of 3. A Used() that counts refusals cannot be shown to
	// anybody and makes Refund guess which uses were real.
	if used := allowance.Used(FeatureSMSSend); used != 3 {
		t.Fatalf("used = %d after 10 refused retries; want 3", used)
	}
}

func TestTheAllowanceResetsOnANewDay(t *testing.T) {
	directory := t.TempDir()
	allowance := NewUsageCounter(directory)
	base := time.Date(2026, 3, 1, 23, 0, 0, 0, time.UTC)
	allowance.now = func() time.Time { return base }
	entitlements := Entitlements{granted: map[Scope]map[Feature]bool{}}
	decision := entitlements.Check(FeatureSMSSend)
	for attempt := 0; attempt < 3; attempt++ {
		allowance.Consume(FeatureSMSSend, decision)
	}
	if allowance.Consume(FeatureSMSSend, decision) {
		t.Fatal("the allowance did not run out")
	}

	// A fresh process on the next day reads the same file.
	tomorrow := NewUsageCounter(directory)
	tomorrow.now = func() time.Time { return base.Add(2 * time.Hour) }
	if !tomorrow.Consume(FeatureSMSSend, decision) {
		t.Fatal("the allowance did not reset overnight")
	}
}
