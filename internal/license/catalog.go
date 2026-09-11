package license

import (
	"crypto/ed25519"
	"fmt"
	"sort"
	"time"
)

// The feature policy lives in one table.
//
// This is the single place that decides what costs money. It is a table rather
// than conditions spread through the code because the policy is expected to
// change -- what is free today may not be tomorrow -- and a policy scattered
// across call sites cannot be changed without hunting for every one of them,
// which is how a feature ends up half-gated.
//
// Nothing outside this package should ask "does a certificate exist". It should
// ask Entitlements.Check(feature), and act on the answer.

// Feature names a gated capability. They are strings rather than an enum so a
// certificate issued today stays readable after the catalog changes.
type Feature string

const (
	// Free, and intended to stay free: receiving is what makes the product
	// useful at all, and a gateway that cannot receive is not worth running.
	FeatureSMSForward Feature = "sms.forward"
	FeatureCallAnswer Feature = "call.answer"

	// Free up to a daily allowance, unlimited with a module certificate.
	FeatureSMSSend Feature = "sms.send"

	// Paid. Outbound calling is billed by the carrier and is the feature most
	// worth paying for.
	FeatureCallDial Feature = "call.dial"

	// Paid, and machine-scoped rather than module-scoped: tunnels belong to
	// the gateway, not to whichever SIM happens to be in it.
	FeatureWireGuard Feature = "net.wireguard"
)

// Period is the window a free allowance resets over.
type Period string

const (
	// PeriodDay resets at midnight UTC.
	PeriodDay Period = "day"
)

// Allowance is what an unlicensed installation may do.
//
// A boolean would not have been enough: sending SMS is free for the first few
// each day and unlimited once licensed, so the answer to "may I" is a quantity,
// not a yes or no. Modelling that here rather than at the call site keeps the
// policy in one place even for the features that are only partly free.
type Allowance struct {
	// Unlimited means no cap.
	Unlimited bool
	// Count is how many times per Period, when not Unlimited. Zero with
	// Unlimited false means the feature is entirely paid.
	Count  int
	Period Period
}

var (
	free = Allowance{Unlimited: true}
	paid = Allowance{}
	// guarded is free as far as this catalogue is concerned: the feature is not
	// metered here, and its daily limit is the anti-fraud guard at the call
	// site. Spelled as its own name rather than reusing free so the table says
	// which of the two it means -- "no licence needed" against "no limit at
	// all" -- without the reader having to go and look.
	guarded   = Allowance{Unlimited: true}
	unlimited = Allowance{Unlimited: true}
)

// rule is one row of the policy.
type rule struct {
	// Free is what an installation with no certificate may do.
	Free Allowance
	// Licensed is what a certificate grants. Only consulted when Requires is
	// satisfied.
	Licensed Allowance
	// Requires lists every scope that must be licensed. A feature naming both
	// scopes needs a machine certificate AND a module certificate; expressing
	// it this way means a new combination is a new row, not a new certificate
	// type.
	Requires []Scope
}

// catalog is the policy. Editing this table is how the policy changes.
var catalog = map[Feature]rule{
	FeatureSMSForward: {Free: free, Licensed: free},
	FeatureCallAnswer: {Free: free, Licensed: free},
	// The two outbound features are FREE here, and that is not an oversight.
	//
	// Their limit is the anti-fraud guard -- five distinct numbers a day for
	// calls, three for messages, both lifted by typing "unlock" on the dial pad
	// -- and it lives at the call site in internal/httpapi/call_guard.go
	// because it is about abuse, not about money. A licence lifts that guard,
	// so what a certificate buys is "no daily limit", and an unlicensed
	// installation is fully functional within the limit rather than refused.
	//
	// Expressing it this way means Check() never refuses either feature, so no
	// call site can accidentally render a paywall for them: the only thing an
	// operator over the limit can be shown is the anti-fraud notice. Licensed
	// is still declared, because Decision.Licensed is what the call sites read
	// to decide whether the guard applies at all.
	FeatureSMSSend: {
		Free:     guarded,
		Licensed: unlimited,
		Requires: []Scope{ScopeModule},
	},
	FeatureCallDial: {
		Free:     guarded,
		Licensed: unlimited,
		Requires: []Scope{ScopeModule},
	},
	FeatureWireGuard: {
		Free:     paid,
		Licensed: unlimited,
		Requires: []Scope{ScopeMachine},
	},
}

// Features lists every catalog entry, sorted, for the settings page.
func Features() []Feature {
	names := make([]Feature, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return names
}

// Requirements reports which scopes a feature needs, for explaining to the
// operator what they would have to buy.
func Requirements(feature Feature) ([]Scope, bool) {
	row, ok := catalog[feature]
	if !ok {
		return nil, false
	}
	return append([]Scope(nil), row.Requires...), true
}

// Decision is the answer to "may this installation use this feature".
type Decision struct {
	// Allowed is false only when the feature is entirely unavailable. A
	// feature with a free allowance is Allowed with a Limit; the caller
	// enforces the count.
	Allowed bool
	// Limit is nil when unlimited.
	Limit *Allowance
	// Licensed says whether a certificate is what permits this, so the UI can
	// distinguish "free tier" from "paid, and you have paid".
	Licensed bool
	// Missing lists the scopes that would have to be licensed to lift the
	// limit. It is what the UI turns into "this needs a module licence"
	// instead of greying a button out with no explanation.
	Missing []Scope
	// Reason is a short machine-readable cause when not Allowed.
	Reason string
}

// Entitlements is the evaluated licence state for this installation.
type Entitlements struct {
	// granted maps a scope to the features licensed within it.
	granted map[Scope]map[Feature]bool
	// certs are the certificates that were accepted, for display.
	certs []Certificate
	// rejected explains certificates that did not apply, so the settings page
	// can say why rather than silently ignoring a file the customer paid for.
	rejected []Rejection
}

// Rejection is a certificate that was present but did not count.
type Rejection struct {
	Certificate Certificate
	Reason      string
}

// Identity is what the certificates are checked against.
type Identity struct {
	MachineID string
	// ModuleSNs are the serial numbers currently attached. A module
	// certificate counts only while its module is present, which is what makes
	// a licence follow the module rather than the box.
	ModuleSNs []string
}

// Evaluate turns certificates into entitlements.
//
// A certificate that fails for any reason is recorded in rejected rather than
// dropped. A customer who paid and sees nothing unlocked needs to be told
// which of expired, wrong machine, or bad signature applies; without that the
// only available answer is "it doesn't work".
func Evaluate(
	certificates []Signed,
	publicKey ed25519.PublicKey,
	identity Identity,
	now time.Time,
) Entitlements {
	result := Entitlements{granted: map[Scope]map[Feature]bool{}}
	modules := make(map[string]bool, len(identity.ModuleSNs))
	for _, serial := range identity.ModuleSNs {
		modules[serial] = true
	}

	for _, signed := range certificates {
		if err := Verify(signed, publicKey, now); err != nil {
			result.rejected = append(result.rejected, Rejection{
				Certificate: signed.Certificate,
				Reason:      err.Error(),
			})
			continue
		}
		certificate := signed.Certificate
		switch certificate.Scope {
		case ScopeMachine:
			if identity.MachineID == "" || certificate.MachineID != identity.MachineID {
				result.rejected = append(result.rejected, Rejection{
					Certificate: certificate,
					Reason:      "issued for a different machine",
				})
				continue
			}
		case ScopeModule:
			if !modules[certificate.ModuleSN] {
				result.rejected = append(result.rejected, Rejection{
					Certificate: certificate,
					Reason:      "the module this covers is not attached",
				})
				continue
			}
		}
		if result.granted[certificate.Scope] == nil {
			result.granted[certificate.Scope] = map[Feature]bool{}
		}
		for _, name := range certificate.Features {
			result.granted[certificate.Scope][Feature(name)] = true
		}
		result.certs = append(result.certs, certificate)
	}
	return result
}

// Check decides whether a feature may be used.
func (e Entitlements) Check(feature Feature) Decision {
	row, ok := catalog[feature]
	if !ok {
		// An unknown feature is refused rather than allowed. A typo in a call
		// site must not silently unlock something.
		return Decision{Reason: "unknown feature"}
	}

	var missing []Scope
	for _, scope := range row.Requires {
		if !e.granted[scope][feature] {
			missing = append(missing, scope)
		}
	}

	if len(missing) == 0 && len(row.Requires) > 0 {
		return decisionFor(row.Licensed, true, nil)
	}
	return decisionFor(row.Free, false, missing)
}

func decisionFor(allowance Allowance, licensed bool, missing []Scope) Decision {
	switch {
	case allowance.Unlimited:
		return Decision{Allowed: true, Licensed: licensed, Missing: missing}
	case allowance.Count > 0:
		limit := allowance
		return Decision{Allowed: true, Limit: &limit, Licensed: licensed, Missing: missing}
	default:
		return Decision{Licensed: licensed, Missing: missing, Reason: "requires a licence"}
	}
}

// Certificates returns the accepted certificates for display.
func (e Entitlements) Certificates() []Certificate {
	return append([]Certificate(nil), e.certs...)
}

// Rejected returns certificates that were present but did not apply.
func (e Entitlements) Rejected() []Rejection {
	return append([]Rejection(nil), e.rejected...)
}

// String renders a decision for logs and error messages.
func (d Decision) String() string {
	switch {
	case !d.Allowed:
		return fmt.Sprintf("denied (%s)", d.Reason)
	case d.Limit != nil:
		return fmt.Sprintf("allowed, %d per %s", d.Limit.Count, d.Limit.Period)
	case d.Licensed:
		return "allowed (licensed)"
	default:
		return "allowed (free)"
	}
}
