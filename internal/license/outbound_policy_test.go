package license

import (
	"testing"
	"time"
)

// The outbound policy, pinned.
//
// Calling out and texting out are FREE. Their only limit is the anti-fraud
// guard in internal/httpapi/call_guard.go -- five distinct numbers a day for
// calls, three for messages, both lifted by typing "unlock" on the dial pad --
// and a licence lifts that guard. So a certificate buys "no daily limit", not
// "the feature works at all".
//
// This file exists because an earlier version had it the other way round:
// FeatureCallDial was entirely paid, so every call without a certificate was
// refused and the operator was shown a purchase prompt. That is not what this
// feature is, and it locked dialling on the owner's own gateway. These tests
// are what stops it coming back, since the mistake is a one-word edit to the
// catalogue and nothing else in the system would complain.

func unlicensed() Entitlements {
	return Entitlements{granted: map[Scope]map[Feature]bool{}}
}

// Check must never refuse either outbound feature. This is the load-bearing
// assertion: a call site cannot render a paywall for a decision that is always
// allowed, so the policy itself is what keeps purchase copy off those paths
// rather than the discipline of whoever edits them next.
func TestOutboundFeaturesAreNeverRefused(t *testing.T) {
	for _, feature := range []Feature{FeatureCallDial, FeatureSMSSend} {
		decision := unlicensed().Check(feature)
		if !decision.Allowed {
			t.Fatalf(
				"%s is refused without a certificate; the call site then has a refusal "+
					"to render and the operator is shown a paywall for it",
				feature,
			)
		}
		if decision.Limit != nil {
			t.Fatalf(
				"%s carries a metered limit of %+v; the daily limit is the anti-fraud "+
					"guard, and a second counter here is one the dial-pad unlock cannot lift",
				feature,
				decision.Limit,
			)
		}
	}
}

// Receiving is free and stays free, licensed or not. It is the reason the
// gateway is useful at all.
func TestInboundFeaturesAreFreeEitherWay(t *testing.T) {
	for _, feature := range []Feature{FeatureCallAnswer, FeatureSMSForward} {
		if decision := unlicensed().Check(feature); !decision.Allowed {
			t.Fatalf("%s is not free", feature)
		}
	}
}

// And the licence has to be observable, or nothing can lift the guard. The
// call sites read Decision.Licensed and nothing else.
func TestALicenceShowsUpAsLicensedRatherThanAsAllowed(t *testing.T) {
	store, sign := newTestStore(t)
	if err := store.Save(
		sign(moduleCertificate("SN-AAA", string(FeatureCallDial))),
		time.Now(),
	); err != nil {
		t.Fatal(err)
	}
	entitlements := store.Entitlements("machine-1", []string{"SN-AAA"}, time.Now())

	licensed := entitlements.Check(FeatureCallDial)
	if !licensed.Licensed {
		t.Fatal(
			"a certificate does not report as licensed, so the guard would still " +
				"apply to a customer who has paid",
		)
	}
	// Unlicensed must be allowed AND not licensed: allowed so nothing refuses
	// it, not licensed so the guard applies.
	free := unlicensed().Check(FeatureCallDial)
	if !free.Allowed || free.Licensed {
		t.Fatalf("an unlicensed line reads as %+v; want allowed and not licensed", free)
	}
}

// A module certificate covers only its own module, so the guard returns as soon
// as that module is gone. Without this a licence would follow the box.
func TestTheLicenceOnlyLiftsTheGuardWhileItsModuleIsPresent(t *testing.T) {
	store, sign := newTestStore(t)
	if err := store.Save(
		sign(moduleCertificate("SN-AAA", string(FeatureCallDial))),
		time.Now(),
	); err != nil {
		t.Fatal(err)
	}
	absent := store.Entitlements("machine-1", []string{"SN-BBB"}, time.Now())
	decision := absent.Check(FeatureCallDial)
	if decision.Licensed {
		t.Fatal("another module's certificate lifted the guard")
	}
	if !decision.Allowed {
		t.Fatal("a line with no certificate was refused instead of guarded")
	}
}
