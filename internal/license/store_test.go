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
	// Outbound calling is free and guarded, so a missing issuer key leaves it
	// working within the guard's daily limit rather than refused -- but
	// unlicensed, so the guard still applies.
	dial := entitlements.Check(FeatureCallDial)
	if !dial.Allowed {
		t.Fatal("outbound calling was refused because of a missing issuer key")
	}
	if dial.Licensed {
		t.Fatal("outbound calling reported as licensed with no issuer key")
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
	if !present.Check(FeatureCallDial).Licensed {
		t.Fatal("the guard was not lifted with the licensed module attached")
	}
	// Module pulled out. The cache must not keep answering yes.
	absent := store.Entitlements("machine-1", []string{"SN-BBB"}, time.Now())
	if absent.Check(FeatureCallDial).Licensed {
		t.Fatal("the guard stayed lifted after the licensed module was removed")
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
