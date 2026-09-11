package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func issuer(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return public, private
}

func machineCert(machineID string, features ...string) Certificate {
	return Certificate{
		Version:   CurrentVersion,
		ID:        "c_test_machine",
		Scope:     ScopeMachine,
		MachineID: machineID,
		Features:  features,
		IssuedAt:  time.Now().Add(-time.Hour).Unix(),
	}
}

func moduleCert(serial string, features ...string) Certificate {
	return Certificate{
		Version:  CurrentVersion,
		ID:       "c_test_module",
		Scope:    ScopeModule,
		ModuleSN: serial,
		Features: features,
		IssuedAt: time.Now().Add(-time.Hour).Unix(),
	}
}

// --- signing ---------------------------------------------------------------

// The whole scheme rests on this: a certificate cannot be edited after it is
// signed. If any field could be changed without breaking the signature, a
// customer could grant themselves anything.
func TestEditingAnySignedFieldBreaksTheSignature(t *testing.T) {
	public, private := issuer(t)
	original := machineCert("aaaa1111", string(FeatureWireGuard))
	signed, err := Sign(original, private)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(signed, public, time.Now()); err != nil {
		t.Fatalf("a freshly signed certificate did not verify: %v", err)
	}

	edits := map[string]func(*Certificate){
		"machine id": func(c *Certificate) { c.MachineID = "bbbb2222" },
		"features":   func(c *Certificate) { c.Features = []string{string(FeatureCallDial)} },
		"expiry":     func(c *Certificate) { c.ExpiresAt = time.Now().Add(999 * time.Hour).Unix() },
		"issued at":  func(c *Certificate) { c.IssuedAt = original.IssuedAt - 5 },
		"id":         func(c *Certificate) { c.ID = "c_other" },
		"note":       func(c *Certificate) { c.Note = "upgraded" },
	}
	for name, edit := range edits {
		tampered := signed
		tampered.Certificate = original
		edit(&tampered.Certificate)
		if err := Verify(tampered, public, time.Now()); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("editing the %s was not caught: %v", name, err)
		}
	}
}

// A certificate signed by anyone else must not work, or the scheme grants
// nothing at all.
func TestACertificateFromAnotherIssuerIsRefused(t *testing.T) {
	ours, _ := issuer(t)
	_, theirs := issuer(t)
	signed, err := Sign(machineCert("aaaa1111", string(FeatureWireGuard)), theirs)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(signed, ours, time.Now()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a foreign certificate verified: %v", err)
	}
}

// With no public key configured nothing can be valid. The alternative -- a
// build that treats "unconfigured" as "allow" -- would ship with every paid
// feature unlocked.
func TestNoConfiguredKeyMeansNothingVerifies(t *testing.T) {
	_, private := issuer(t)
	signed, err := Sign(machineCert("aaaa1111", string(FeatureWireGuard)), private)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(signed, nil, time.Now()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("verification succeeded with no key: %v", err)
	}
}

// Both kinds of certificate exist on purpose: a subscription expires and is
// renewed, a one-off purchase does not.
func TestPerpetualAndExpiringCertificates(t *testing.T) {
	public, private := issuer(t)

	perpetual := machineCert("aaaa1111", string(FeatureWireGuard))
	signedPerpetual, _ := Sign(perpetual, private)
	if err := Verify(signedPerpetual, public, time.Now().Add(10*365*24*time.Hour)); err != nil {
		t.Fatalf("a perpetual certificate expired: %v", err)
	}
	if _, has := perpetual.Expiry(); has {
		t.Fatal("a perpetual certificate reported an expiry")
	}

	expiring := machineCert("aaaa1111", string(FeatureWireGuard))
	expiring.ExpiresAt = time.Now().Add(24 * time.Hour).Unix()
	signedExpiring, _ := Sign(expiring, private)
	if err := Verify(signedExpiring, public, time.Now()); err != nil {
		t.Fatalf("an unexpired certificate was refused: %v", err)
	}
	if err := Verify(signedExpiring, public, time.Now().Add(48*time.Hour)); !errors.Is(err, ErrExpired) {
		t.Fatalf("an expired certificate was accepted: %v", err)
	}
}

// A certificate naming both a machine and a module would be a third scope in
// disguise. "Needs both" is expressed as two certificates.
func TestACertificateBindsExactlyOneThing(t *testing.T) {
	_, private := issuer(t)
	both := machineCert("aaaa1111", string(FeatureWireGuard))
	both.ModuleSN = "SN123"
	if _, err := Sign(both, private); !errors.Is(err, ErrMalformed) {
		t.Fatalf("a certificate binding both was signed: %v", err)
	}
	neither := Certificate{
		Version: CurrentVersion, ID: "c", Scope: ScopeMachine,
		Features: []string{"x"}, IssuedAt: time.Now().Unix(),
	}
	if _, err := Sign(neither, private); !errors.Is(err, ErrMalformed) {
		t.Fatalf("a certificate binding nothing was signed: %v", err)
	}
}

// The canonical form is separated by newlines and commas, so a value
// containing one could be read back as a different certificate with the same
// signature.
func TestSeparatorsAreRefusedRatherThanEscaped(t *testing.T) {
	_, private := issuer(t)
	for _, bad := range []string{"a\nb", "a,b", "a=b", "a\rb"} {
		certificate := machineCert("aaaa1111", bad)
		if _, err := Sign(certificate, private); !errors.Is(err, ErrMalformed) {
			t.Fatalf("a feature name containing %q was accepted", bad)
		}
	}
}

// Feature order must not change the signature, or a certificate that survived
// a round trip through any map would stop verifying.
func TestFeatureOrderDoesNotAffectTheSignature(t *testing.T) {
	public, private := issuer(t)
	one := machineCert("aaaa1111", "b.feature", "a.feature")
	two := machineCert("aaaa1111", "a.feature", "b.feature")
	two.IssuedAt = one.IssuedAt

	signedOne, _ := Sign(one, private)
	shuffled := Signed{Certificate: two, Signature: signedOne.Signature}
	if err := Verify(shuffled, public, time.Now()); err != nil {
		t.Fatalf("reordering features broke the signature: %v", err)
	}
}

// --- policy ----------------------------------------------------------------

// The free tier has to work with no certificate at all, or a fresh install is
// useless before anyone has paid for anything.
func TestFreeFeaturesNeedNoCertificate(t *testing.T) {
	public, _ := issuer(t)
	entitlements := Evaluate(nil, public, Identity{MachineID: "aaaa1111"}, time.Now())

	for _, feature := range []Feature{FeatureSMSForward, FeatureCallAnswer} {
		decision := entitlements.Check(feature)
		if !decision.Allowed || decision.Limit != nil {
			t.Fatalf("%s should be free and unlimited, got %s", feature, decision)
		}
	}
}

// Sending is free, and a licence lifts the anti-fraud guard rather than
// unlocking the feature. So the observable difference a certificate makes is
// Licensed, not Allowed.
//
// This test used to assert a metered free tier of three sends a day. That was a
// second counter on top of the guard -- three MESSAGES, not three recipients,
// and one the dial-pad unlock could not lift. It has been removed; the guard is
// the only limit.
func TestSendingSMSIsFreeAndALicenceLiftsTheGuard(t *testing.T) {
	public, private := issuer(t)
	identity := Identity{MachineID: "aaaa1111", ModuleSNs: []string{"SN123"}}

	unlicensed := Evaluate(nil, public, identity, time.Now()).Check(FeatureSMSSend)
	if !unlicensed.Allowed {
		t.Fatal("sending SMS must be allowed without a licence")
	}
	if unlicensed.Limit != nil {
		t.Fatalf("sending carries a metered limit of %+v; the guard is the limit", unlicensed.Limit)
	}
	if unlicensed.Licensed {
		t.Fatal("the free tier must not report as licensed, or the guard would never apply")
	}

	signed, _ := Sign(moduleCert("SN123", string(FeatureSMSSend)), private)
	licensed := Evaluate([]Signed{signed}, public, identity, time.Now()).Check(FeatureSMSSend)
	if !licensed.Allowed || !licensed.Licensed {
		t.Fatalf("a module licence should lift the guard, got %s", licensed)
	}
}

// Entitlements are scoped differently: the SIM carries calling, the box carries
// tunnels. WireGuard is genuinely paid and is refused without a licence;
// dialling is free and guarded, so it is never refused and a licence only shows
// up as Licensed.
func TestPaidFeaturesAreScopedToWhatTheyBelongTo(t *testing.T) {
	public, private := issuer(t)
	identity := Identity{MachineID: "aaaa1111", ModuleSNs: []string{"SN123"}}

	none := Evaluate(nil, public, identity, time.Now())
	if none.Check(FeatureCallDial).Licensed {
		t.Fatal("dialling out reported as licensed with no licence")
	}
	if !none.Check(FeatureCallDial).Allowed {
		t.Fatal("dialling out was refused instead of guarded")
	}
	if none.Check(FeatureWireGuard).Allowed {
		t.Fatal("WireGuard was allowed with no licence")
	}

	// A module licence covers dialling but must not unlock tunnels.
	moduleOnly, _ := Sign(moduleCert("SN123", string(FeatureCallDial), string(FeatureWireGuard)), private)
	withModule := Evaluate([]Signed{moduleOnly}, public, identity, time.Now())
	if !withModule.Check(FeatureCallDial).Licensed {
		t.Fatal("a module licence did not cover dialling")
	}
	if withModule.Check(FeatureWireGuard).Allowed {
		t.Fatal("a module certificate unlocked a machine-scoped feature")
	}

	machineOnly, _ := Sign(machineCert("aaaa1111", string(FeatureWireGuard)), private)
	withMachine := Evaluate([]Signed{machineOnly}, public, identity, time.Now())
	if !withMachine.Check(FeatureWireGuard).Allowed {
		t.Fatal("a machine licence did not unlock WireGuard")
	}
}

// This is what stops a certificate being passed around: it verifies perfectly
// and still grants nothing, because it names someone else's hardware.
func TestACertificateForAnotherMachineGrantsNothing(t *testing.T) {
	public, private := issuer(t)
	signed, _ := Sign(machineCert("aaaa1111", string(FeatureWireGuard)), private)

	entitlements := Evaluate([]Signed{signed}, public, Identity{MachineID: "bbbb2222"}, time.Now())
	if entitlements.Check(FeatureWireGuard).Allowed {
		t.Fatal("a certificate for another machine unlocked a feature")
	}
	// And the operator has to be told why, or a paying customer sees nothing
	// happen with no explanation.
	rejected := entitlements.Rejected()
	if len(rejected) != 1 || rejected[0].Reason == "" {
		t.Fatalf("the rejection was not explained: %+v", rejected)
	}
}

// A module licence follows the module. With the module unplugged it grants
// nothing, and plugging it into another gateway carries the entitlement there.
func TestAModuleLicenceOnlyCountsWhileItsModuleIsAttached(t *testing.T) {
	public, private := issuer(t)
	signed, _ := Sign(moduleCert("SN123", string(FeatureCallDial)), private)

	absent := Evaluate([]Signed{signed}, public, Identity{MachineID: "aaaa1111"}, time.Now())
	if absent.Check(FeatureCallDial).Licensed {
		t.Fatal("a module licence applied with the module absent")
	}

	elsewhere := Evaluate([]Signed{signed}, public, Identity{
		MachineID: "totally-different-box",
		ModuleSNs: []string{"SN123"},
	}, time.Now())
	if !elsewhere.Check(FeatureCallDial).Licensed {
		t.Fatal("a module licence did not follow its module to another gateway")
	}
}

// An unknown feature name must be refused. A typo at a call site must not
// unlock something.
func TestUnknownFeaturesAreRefused(t *testing.T) {
	public, _ := issuer(t)
	entitlements := Evaluate(nil, public, Identity{}, time.Now())
	if entitlements.Check(Feature("does.not.exist")).Allowed {
		t.Fatal("an unknown feature was allowed")
	}
}

// A locked feature has to say what would unlock it, or the UI can only grey a
// button out and leave the customer guessing what to buy.
func TestALockedFeatureNamesWhatIsMissing(t *testing.T) {
	public, _ := issuer(t)
	entitlements := Evaluate(nil, public, Identity{MachineID: "aaaa1111"}, time.Now())

	decision := entitlements.Check(FeatureWireGuard)
	if len(decision.Missing) != 1 || decision.Missing[0] != ScopeMachine {
		t.Fatalf("WireGuard should name the machine scope, got %+v", decision.Missing)
	}
	if requirements, ok := Requirements(FeatureCallDial); !ok ||
		len(requirements) != 1 || requirements[0] != ScopeModule {
		t.Fatalf("dialling should require the module scope, got %+v", requirements)
	}
}

// --- machine identity ------------------------------------------------------

func writeSysfs(t *testing.T, root, path, value string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The point of the whole exercise: reinstalling the OS must not change the ID.
// Everything the installer touches is excluded, so a fixture that keeps only
// the hardware values has to produce the same answer.
func TestMachineIDSurvivesAReinstall(t *testing.T) {
	before := t.TempDir()
	writeSysfs(t, before, "sys/firmware/devicetree/base/serial-number", "c3d4e5f600112233\x00")
	writeSysfs(t, before, "sys/block/mmcblk0/device/cid", "150100444d3432)\n")
	writeSysfs(t, before, "sys/class/net/eth0/address", "aa:bb:cc:dd:ee:ff\n")
	writeSysfs(t, before, "sys/class/net/eth0/addr_assign_type", "0\n")
	writeSysfs(t, before, "etc/machine-id", "1111111111111111111111111111111a\n")

	first, err := CollectFingerprint(before)
	if err != nil {
		t.Fatal(err)
	}

	// After a reflash: same hardware, new /etc/machine-id, and the container
	// runtime has added interfaces.
	after := t.TempDir()
	writeSysfs(t, after, "sys/firmware/devicetree/base/serial-number", "c3d4e5f600112233\x00")
	writeSysfs(t, after, "sys/block/mmcblk0/device/cid", "150100444d3432)\n")
	writeSysfs(t, after, "sys/class/net/eth0/address", "aa:bb:cc:dd:ee:ff\n")
	writeSysfs(t, after, "sys/class/net/eth0/addr_assign_type", "0\n")
	writeSysfs(t, after, "etc/machine-id", "2222222222222222222222222222222b\n")
	writeSysfs(t, after, "sys/class/net/docker0/address", "02:42:99:88:77:66\n")
	writeSysfs(t, after, "sys/class/net/docker0/addr_assign_type", "3\n")

	second, err := CollectFingerprint(after)
	if err != nil {
		t.Fatal(err)
	}
	if first.MachineID != second.MachineID {
		t.Fatalf("the machine ID changed across a reinstall: %s -> %s", first.MachineID, second.MachineID)
	}
	if _, present := second.Sources["mac-docker0"]; present {
		t.Fatal("a generated interface entered the fingerprint")
	}
}

// Different hardware must produce a different ID, or a certificate would work
// on any machine and the binding would mean nothing.
func TestDifferentHardwareGivesADifferentID(t *testing.T) {
	one := t.TempDir()
	writeSysfs(t, one, "sys/firmware/devicetree/base/serial-number", "1111111111111111\x00")
	two := t.TempDir()
	writeSysfs(t, two, "sys/firmware/devicetree/base/serial-number", "2222222222222222\x00")

	first, err := CollectFingerprint(one)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CollectFingerprint(two)
	if err != nil {
		t.Fatal(err)
	}
	if first.MachineID == second.MachineID {
		t.Fatal("two different boards produced the same machine ID")
	}
}

// A board with no usable identifier must refuse rather than invent one. A
// generated identity would be stored, survive until the next reflash, and then
// silently take the licence with it.
func TestNoStableIdentifierIsAnErrorNotAGuess(t *testing.T) {
	root := t.TempDir()
	writeSysfs(t, root, "etc/machine-id", "3333333333333333333333333333333c\n")
	// A randomised interface is not an identity.
	writeSysfs(t, root, "sys/class/net/eth0/address", "aa:bb:cc:dd:ee:ff\n")
	writeSysfs(t, root, "sys/class/net/eth0/addr_assign_type", "1\n")

	if _, err := CollectFingerprint(root); err == nil {
		t.Fatal("an identity was produced with nothing stable to derive it from")
	}
}

// Vendors leave placeholders in these fields. Treating one as an identity
// would give every affected board the same machine ID.
func TestPlaceholderValuesAreNotIdentities(t *testing.T) {
	for _, placeholder := range []string{"unknown", "None", "0", "To Be Filled By O.E.M.", "00000000"} {
		root := t.TempDir()
		writeSysfs(t, root, "sys/class/dmi/id/product_uuid", placeholder+"\n")
		if _, err := CollectFingerprint(root); err == nil {
			t.Fatalf("the placeholder %q was accepted as an identity", placeholder)
		}
	}
}

// The real board, as measured. This fixture is the probe output rather than a
// plausible-looking invention, which is the whole point: the previous version
// of this file tested against four identifiers that do not exist on the
// hardware this software ships on.
func rk3566Board(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSysfs(t, root, "sys/firmware/devicetree/base/compatible",
		"onethingtech,onethingcloud-oec\x00rockchip,rk3566\x00")
	writeSysfs(t, root, "sys/block/mmcblk0/device/cid",
		"1501003847544634520627ba4dd32a00\n")
	// The on-board NIC generates its MAC every boot.
	writeSysfs(t, root, "sys/class/net/end0/address", "b6:b3:86:ae:ea:34\n")
	writeSysfs(t, root, "sys/class/net/end0/addr_assign_type", "3\n")
	// The 4G module's ECM interface. It reports "permanent" -- to the module.
	writeSysfs(t, root, "sys/class/net/enx82ae89b182c2/address", "82:ae:89:b1:82:c2\n")
	writeSysfs(t, root, "sys/class/net/enx82ae89b182c2/addr_assign_type", "0\n")
	writeSysfs(t, root, "etc/machine-id", "34804f20a9d84121bb0f481558d7747c\n")
	// Both found by the second probe and stable across a full reboot.
	writeSysfs(t, root, "proc/cpuinfo",
		"processor\t: 0\nmodel name\t: ARMv8\nSerial\t\t: e27d0ad781520080\n")
	writeSysfs(t, root, "sys/bus/nvmem/devices/rockchip-otp0/nvmem",
		otpBytes(t, "524b35660200fe211001544b574c3139000000000000000d130a1b3804080000"))
	return root
}

// otpBytes turns the probe's hex dump back into the raw bytes the kernel
// exposes, so the fixture is the measured region rather than a stand-in.
func otpBytes(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// On the hardware this actually ships on, the eMMC CID is the identity. Not a
// fallback -- the only candidate that exists.
func TestTheShippingBoardDerivesFromItsStorageChip(t *testing.T) {
	print, err := CollectFingerprint(rk3566Board(t))
	if err != nil {
		t.Fatalf("the shipping board could not be identified: %v", err)
	}
	if print.PrimarySource != "emmc-cid-mmcblk0" {
		t.Fatalf("identity came from %q, expected the eMMC CID", print.PrimarySource)
	}
}

// The trap the hardware probe found, and the reason this file was rewritten.
//
// The module's ECM interface reports addr_assign_type=0. Permanent to the
// module is not permanent to the machine: a machine licence derived from it
// would be bound to a removable modem and would break the first time the
// customer swapped or unplugged one. Deriving from it must be impossible, not
// merely unlikely.
func TestTheModulesOwnMACCanNeverBecomeTheMachineIdentity(t *testing.T) {
	withModule := rk3566Board(t)
	first, err := CollectFingerprint(withModule)
	if err != nil {
		t.Fatal(err)
	}

	// Same board, module unplugged: the interface is simply gone.
	withoutModule := t.TempDir()
	writeSysfs(t, withoutModule, "sys/firmware/devicetree/base/compatible",
		"onethingtech,onethingcloud-oec\x00rockchip,rk3566\x00")
	writeSysfs(t, withoutModule, "sys/block/mmcblk0/device/cid",
		"1501003847544634520627ba4dd32a00\n")
	writeSysfs(t, withoutModule, "sys/class/net/end0/address", "42:11:9c:02:aa:31\n")
	writeSysfs(t, withoutModule, "sys/class/net/end0/addr_assign_type", "3\n")
	writeSysfs(t, withoutModule, "etc/machine-id", "34804f20a9d84121bb0f481558d7747c\n")

	second, err := CollectFingerprint(withoutModule)
	if err != nil {
		t.Fatal(err)
	}
	if first.MachineID != second.MachineID {
		t.Fatalf("unplugging the module changed the machine ID: %s -> %s", first.MachineID, second.MachineID)
	}
	for name := range first.Sources {
		if strings.HasPrefix(name, "mac-enx") {
			t.Fatalf("the module's interface %q entered the fingerprint", name)
		}
	}
}

// A board whose only interface reports a permanent MAC still gets no identity
// from it. There is no MAC fallback on any board any more.
func TestAPermanentMACIsStillNotAnIdentity(t *testing.T) {
	root := t.TempDir()
	writeSysfs(t, root, "sys/class/net/eth0/address", "aa:bb:cc:dd:ee:ff\n")
	writeSysfs(t, root, "sys/class/net/eth0/addr_assign_type", "0\n")
	writeSysfs(t, root, "etc/machine-id", "3333333333333333333333333333333c\n")

	if _, err := CollectFingerprint(root); err == nil {
		t.Fatal("a machine identity was derived from a MAC address")
	}
}

// A randomised MAC differs on every boot. Counting it as evidence either way
// would be counting noise, and on the shipping board it is the ONLY on-board
// NIC -- so counting it would poison every comparison that board ever makes.
func TestARandomisedMACIsNotEvidence(t *testing.T) {
	first, err := CollectFingerprint(rk3566Board(t))
	if err != nil {
		t.Fatal(err)
	}
	rebooted := rk3566Board(t)
	writeSysfs(t, rebooted, "sys/class/net/end0/address", "3e:07:d1:55:b2:0c\n")
	second, err := CollectFingerprint(rebooted)
	if err != nil {
		t.Fatal(err)
	}
	drift := CompareFingerprint(first, second)
	if !drift.SameID || drift.Verdict != VerdictSame {
		t.Fatalf("a reboot looked like a change: %+v", drift)
	}
	if drift.Changed != 0 {
		t.Fatalf("the randomised MAC was counted as a changed source: %+v", drift)
	}
}

// Reflashing keeps the identity and moves /etc/machine-id. The customer needs
// to hear "nothing to do", not silence.
func TestReflashingIsRecognisedAsItself(t *testing.T) {
	before, err := CollectFingerprint(rk3566Board(t))
	if err != nil {
		t.Fatal(err)
	}
	after := rk3566Board(t)
	writeSysfs(t, after, "etc/machine-id", "ffffffffffffffffffffffffffffffff\n")
	reflashed, err := CollectFingerprint(after)
	if err != nil {
		t.Fatal(err)
	}
	drift := CompareFingerprint(before, reflashed)
	if !drift.SameID {
		t.Fatal("reflashing changed the machine ID")
	}
	if drift.Verdict != VerdictReinstalled {
		t.Fatalf("a reflash was reported as %q", drift.Verdict)
	}
}

// The repair case, now answerable.
//
// The first probe left this board with one identifier, which meant a replaced
// eMMC and a stranger's board produced indistinguishable fingerprints. The
// second probe found the SoC's serial, which does not live in the storage, so
// the two cases separate: CID changed, cpu-serial did not, therefore the chip
// was replaced and the board is the same one.
func TestAReplacedStorageChipIsNowToldApartFromAStranger(t *testing.T) {
	stored, err := CollectFingerprint(rk3566Board(t))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Sources["cpu-serial"] != "e27d0ad781520080" {
		t.Fatalf("the SoC serial was not collected: %+v", stored.Sources)
	}

	// Returned from repair with a new eMMC: new CID, and the reinstall that
	// forces regenerated /etc/machine-id. The SoC is untouched.
	repaired := rk3566Board(t)
	writeSysfs(t, repaired, "sys/block/mmcblk0/device/cid", "150100384754463452069999deadbeef\n")
	writeSysfs(t, repaired, "etc/machine-id", "aaaa1111aaaa1111aaaa1111aaaa1111\n")
	afterRepair, err := CollectFingerprint(repaired)
	if err != nil {
		t.Fatal(err)
	}
	repair := CompareFingerprint(stored, afterRepair)
	if repair.SameID {
		t.Fatal("a new storage chip kept the old identity")
	}
	if repair.Verdict != VerdictStorageReplaced {
		t.Fatalf("a repaired board was reported as %q", repair.Verdict)
	}
	if !repair.NeedsHumanDecision() {
		t.Fatal("a repaired board was not routed for a re-issue")
	}

	// A different board of the same model: different SoC, different storage.
	stranger := rk3566Board(t)
	writeSysfs(t, stranger, "sys/block/mmcblk0/device/cid", "9902001122334455667788990011aabb\n")
	writeSysfs(t, stranger, "proc/cpuinfo", "processor\t: 0\nSerial\t\t: 5b1c9e04aa730011\n")
	other, err := CollectFingerprint(stranger)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := CompareFingerprint(stored, other)
	if elsewhere.Verdict != VerdictDifferentMachine {
		t.Fatalf("a stranger's board was reported as %q", elsewhere.Verdict)
	}
	if elsewhere.NeedsHumanDecision() {
		t.Fatal("a board that positively is not this one was routed to support")
	}
}

// The OTP is recorded and must never carry a verdict.
//
// Its readable window is a family magic plus what reads as a batch marking, and
// with one board in hand there is no way to know which bytes vary per unit. A
// stranger's board from the same production batch would therefore present the
// SAME otp-region -- so if that counted as corroboration, the stranger would be
// reported as a repair. This is the model-string bug wearing a value that looks
// random, and the test is here because that disguise is the whole danger.
func TestTheOTPIsRecordedButNeverCarriesAVerdict(t *testing.T) {
	stored, err := CollectFingerprint(rk3566Board(t))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Sources["otp-region"] == "" {
		t.Fatal("the OTP region was not recorded; a support case loses the chip's factory bytes")
	}
	if identifies("otp-region") {
		t.Fatal("the OTP region counts as identifying; a batch marking must never corroborate")
	}

	// Same batch, different unit: identical OTP, different SoC serial and
	// storage. The verdict must follow the serial, not the batch.
	sameBatch := rk3566Board(t)
	writeSysfs(t, sameBatch, "sys/block/mmcblk0/device/cid", "9902001122334455667788990011aabb\n")
	writeSysfs(t, sameBatch, "proc/cpuinfo", "processor\t: 0\nSerial\t\t: 5b1c9e04aa730011\n")
	other, err := CollectFingerprint(sameBatch)
	if err != nil {
		t.Fatal(err)
	}
	if other.Sources["otp-region"] != stored.Sources["otp-region"] {
		t.Fatal("the fixture no longer shares an OTP region; this test proves nothing")
	}
	drift := CompareFingerprint(stored, other)
	if drift.Verdict != VerdictDifferentMachine {
		t.Fatalf("a same-batch stranger was reported as %q", drift.Verdict)
	}
}

// The SoC serial corroborates but must never become the identity.
//
// Getting corroboration wrong mislabels a support case, which a person sorts
// out. Getting derivation wrong means two boards sharing a value share a
// machine ID, and one customer's certificate silently unlocks another's
// gateway. So a board with a SoC serial and no burned-in unit identifier is
// refused, not quietly bound to the serial.
func TestTheSoCSerialNeverBecomesTheIdentity(t *testing.T) {
	root := t.TempDir()
	writeSysfs(t, root, "sys/firmware/devicetree/base/compatible",
		"onethingtech,onethingcloud-oec\x00rockchip,rk3566\x00")
	writeSysfs(t, root, "proc/cpuinfo", "processor\t: 0\nSerial\t\t: e27d0ad781520080\n")
	writeSysfs(t, root, "sys/bus/nvmem/devices/rockchip-otp0/nvmem",
		otpBytes(t, "524b35660200fe211001544b574c3139000000000000000d130a1b3804080000"))
	// No eMMC CID, no device-tree serial.
	if _, err := CollectFingerprint(root); err == nil {
		t.Fatal("a machine identity was derived from the SoC serial or the OTP")
	}
}

// The storage chip still derives the identity, and adding the two new sources
// must not have moved it -- otherwise every certificate issued before this
// change would stop matching.
func TestAddingTheNewSourcesDidNotMoveTheMachineID(t *testing.T) {
	withNew, err := CollectFingerprint(rk3566Board(t))
	if err != nil {
		t.Fatal(err)
	}
	// The same board as the first probe saw it: no cpuinfo, no OTP.
	asFirstProbeSawIt := t.TempDir()
	writeSysfs(t, asFirstProbeSawIt, "sys/firmware/devicetree/base/compatible",
		"onethingtech,onethingcloud-oec\x00rockchip,rk3566\x00")
	writeSysfs(t, asFirstProbeSawIt, "sys/block/mmcblk0/device/cid",
		"1501003847544634520627ba4dd32a00\n")
	writeSysfs(t, asFirstProbeSawIt, "etc/machine-id", "34804f20a9d84121bb0f481558d7747c\n")
	before, err := CollectFingerprint(asFirstProbeSawIt)
	if err != nil {
		t.Fatal(err)
	}
	if before.MachineID != withNew.MachineID {
		t.Fatalf(
			"recording new sources changed the machine ID (%s -> %s); every issued certificate would break",
			before.MachineID, withNew.MachineID,
		)
	}
	if withNew.PrimarySource != "emmc-cid-mmcblk0" {
		t.Fatalf("the identity moved to %q", withNew.PrimarySource)
	}
	if before.Version != withNew.Version {
		t.Fatal("the derivation version moved; every machine would be routed to support for nothing")
	}
}

// The module's ECM interface renames itself along with its MAC, because udev
// derives the name from the address. The second probe caught it coming back as
// enxea2d3ee794dd where it had been enx82ae89b182c2 -- while still claiming
// addr_assign_type=0. The exclusion has to survive the rename.
func TestTheRenamedModuleInterfaceIsStillExcluded(t *testing.T) {
	rebooted := t.TempDir()
	writeSysfs(t, rebooted, "sys/firmware/devicetree/base/compatible",
		"onethingtech,onethingcloud-oec\x00rockchip,rk3566\x00")
	writeSysfs(t, rebooted, "sys/block/mmcblk0/device/cid", "1501003847544634520627ba4dd32a00\n")
	writeSysfs(t, rebooted, "proc/cpuinfo", "processor\t: 0\nSerial\t\t: e27d0ad781520080\n")
	// The same interface, under the name the reboot gave it.
	writeSysfs(t, rebooted, "sys/class/net/enxea2d3ee794dd/address", "ea:2d:3e:e7:94:dd\n")
	writeSysfs(t, rebooted, "sys/class/net/enxea2d3ee794dd/addr_assign_type", "0\n")

	print, err := CollectFingerprint(rebooted)
	if err != nil {
		t.Fatal(err)
	}
	for name := range print.Sources {
		if strings.HasPrefix(name, "mac-enx") {
			t.Fatalf("the module's renamed interface %q entered the fingerprint", name)
		}
	}
	if print.PrimarySource != "emmc-cid-mmcblk0" {
		t.Fatalf("identity came from %q after the module renamed itself", print.PrimarySource)
	}
}

// A model string is shared by every unit of that model. If it corroborated,
// any customer holding the same hardware could present as any other.
func TestTheBoardModelIsNeverEvidenceOfWhichBoard(t *testing.T) {
	stored, err := CollectFingerprint(rk3566Board(t))
	if err != nil {
		t.Fatal(err)
	}
	stranger := t.TempDir()
	writeSysfs(t, stranger, "sys/firmware/devicetree/base/compatible",
		"onethingtech,onethingcloud-oec\x00rockchip,rk3566\x00")
	writeSysfs(t, stranger, "sys/block/mmcblk0/device/cid", "9902001122334455667788990011aabb\n")
	other, err := CollectFingerprint(stranger)
	if err != nil {
		t.Fatal(err)
	}
	drift := CompareFingerprint(stored, other)
	for _, name := range drift.Corroborating {
		if identifies(name) {
			continue
		}
		// Non-identifying sources may be listed for a human to read, but they
		// must not have moved the verdict.
		if drift.Verdict == VerdictStorageReplaced {
			t.Fatalf("%q carried the verdict; it names a model, not a board", name)
		}
	}
}

// A board that DOES carry a second identifying source can support the stronger
// claim. The logic is not hardcoded to one board's poverty -- it follows what
// each board can actually prove.
func TestABoardWithASecondIdentifierCanProveARepair(t *testing.T) {
	build := func(cid string) string {
		root := t.TempDir()
		// This board's vendor set the device-tree serial, so it survives a
		// storage swap and names this unit.
		writeSysfs(t, root, "sys/firmware/devicetree/base/serial-number", "c3d4e5f600112233\x00")
		writeSysfs(t, root, "sys/block/mmcblk0/device/cid", cid)
		return root
	}
	stored, err := CollectFingerprint(build("1501003847544634520627ba4dd32a00\n"))
	if err != nil {
		t.Fatal(err)
	}
	// The dt-serial derives the ID here, so a chip swap does not even change
	// the identity. Force the CID to be the source to exercise the branch.
	storedFromCID := Fingerprint{
		MachineID: "aaaa1111", PrimarySource: "emmc-cid-mmcblk0", Version: DerivationVersion,
		Sources: map[string]string{
			"dt-serial":        stored.Sources["dt-serial"],
			"emmc-cid-mmcblk0": stored.Sources["emmc-cid-mmcblk0"],
		},
	}
	repaired := Fingerprint{
		MachineID: "bbbb2222", PrimarySource: "emmc-cid-mmcblk0", Version: DerivationVersion,
		Sources: map[string]string{
			"dt-serial":        stored.Sources["dt-serial"],
			"emmc-cid-mmcblk0": "150100384754463452069999deadbeef",
		},
	}
	drift := CompareFingerprint(storedFromCID, repaired)
	if drift.Verdict != VerdictStorageReplaced {
		t.Fatalf("a board that can prove the repair reported %q", drift.Verdict)
	}
}

// An identifying source that is present and disagrees is the one case this
// software can call outright.
func TestADisagreeingIdentifierIsADifferentMachine(t *testing.T) {
	stored := Fingerprint{
		MachineID: "aaaa1111", PrimarySource: "emmc-cid-mmcblk0", Version: DerivationVersion,
		Sources: map[string]string{
			"dt-serial":        "c3d4e5f600112233",
			"emmc-cid-mmcblk0": "1501003847544634520627ba4dd32a00",
		},
	}
	other := Fingerprint{
		MachineID: "cccc3333", PrimarySource: "emmc-cid-mmcblk0", Version: DerivationVersion,
		Sources: map[string]string{
			"dt-serial":        "9999999999999999",
			"emmc-cid-mmcblk0": "9902001122334455667788990011aabb",
		},
	}
	drift := CompareFingerprint(stored, other)
	if drift.Verdict != VerdictDifferentMachine {
		t.Fatalf("a board whose serial disagrees reported %q", drift.Verdict)
	}
	if drift.NeedsHumanDecision() {
		t.Fatal("a board that positively is not this one was routed to support")
	}
}

// Nothing outside this package may grant anything on a comparison. SameID is
// the only field that decides, and a drift that "looks like" the same machine
// has no way to say so any more.
func TestAComparisonNeverAuthorisesAnything(t *testing.T) {
	stored, err := CollectFingerprint(rk3566Board(t))
	if err != nil {
		t.Fatal(err)
	}
	repaired := rk3566Board(t)
	writeSysfs(t, repaired, "sys/block/mmcblk0/device/cid", "150100384754463452069999deadbeef\n")
	afterRepair, err := CollectFingerprint(repaired)
	if err != nil {
		t.Fatal(err)
	}
	drift := CompareFingerprint(stored, afterRepair)
	// The strongest possible "same machine" signal, and it still does not
	// produce a matching ID -- which is what Evaluate checks.
	if drift.SameID {
		t.Fatal("a repaired board reported SameID")
	}
	certificate := Certificate{
		Version: CurrentVersion, ID: "c_repair", Scope: ScopeMachine,
		MachineID: stored.MachineID, Features: []string{string(FeatureWireGuard)},
		IssuedAt: time.Now().Unix(),
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := Sign(certificate, private)
	if err != nil {
		t.Fatal(err)
	}
	entitlements := Evaluate([]Signed{signed}, public, Identity{MachineID: afterRepair.MachineID}, time.Now())
	if entitlements.Check(FeatureWireGuard).Allowed {
		t.Fatal("the old certificate still unlocked the feature on the repaired board")
	}
}
