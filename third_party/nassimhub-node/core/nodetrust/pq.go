package nodetrust

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"github.com/human-agent65535/nassimhub-node/proto"
)

// Post-quantum key agreement, applied to the configurations in nodetrust.go.
//
// The existing three configurations are unchanged in what they VERIFY - the
// pinned public key, the identity binding, the certificate's self-signature.
// This file only decides which key agreement groups are offered, which is a
// property of the session rather than of identity. That separation is the whole
// reason post-quantum could be added to a frozen trust model at all.
//
// # Why the policy is enforced by omission
//
// A REQUIRED policy lists only post-quantum groups. A peer that cannot do one
// then produces a TLS handshake failure inside crypto/tls, before any callback
// of ours runs. There is deliberately no code here that looks at a completed
// classical handshake and decides whether to allow it - such code would be the
// thing an attacker attacks, and its absence is what makes "cannot be silently
// downgraded" a structural claim rather than a careful one.
//
// Verified against Go 1.24.7 by running it: a PQ-only client and a
// classical-only server fail to handshake, in both directions.

// Security is the post-quantum policy for a connection.
type Security struct {
	Profile proto.PQProfile
	Policy  proto.PQPolicy
}

// DefaultSecurity is what a Core uses unless an operator chose otherwise:
// post-quantum preferred, standard profile.
//
// Preferred rather than required, because a Node running older firmware must
// still be reachable - and the UI is required to show plainly when a connection
// ended up classical, which is what stops "preferred" from meaning "absent".
func DefaultSecurity() Security {
	return Security{Profile: proto.PQStandard, Policy: proto.PQPreferred}
}

// Validate checks a security setting and reports whether this build can satisfy
// it.
//
// The second check matters on an older toolchain. Go before 1.25 has no
// SecP384r1MLKEM1024, so a PQ_EXTREME + REQUIRED policy cannot be met: the
// handshake would fail locally with "no supported elliptic curves for ECDHE",
// which is safe but incomprehensible. Saying so here turns it into a sentence
// an operator can act on.
func (s Security) Validate() error {
	if err := proto.ValidatePQProfile(s.Profile); err != nil {
		return err
	}
	if err := proto.ValidatePQPolicy(s.Policy); err != nil {
		return err
	}
	if s.Policy != proto.PQRequired {
		return nil
	}
	for _, group := range proto.PQGroups(s.Profile, s.Policy) {
		if proto.BuildSupportsGroup(group) {
			return nil
		}
	}
	return fmt.Errorf("%w: this build cannot offer any group the %s profile requires; "+
		"SecP384r1MLKEM1024 needs Go 1.25 or later",
		proto.ErrPQUnavailable, s.Profile)
}

// curves returns the groups to offer, filtered to what this build implements.
//
// Filtering is what keeps a PREFERRED policy working on an older toolchain: an
// unimplemented group in the list makes crypto/tls refuse the whole connection
// locally, so offering only what can actually be performed is the difference
// between "falls back" and "never connects".
//
// Under REQUIRED the filter is still applied, and if it empties the list the
// configuration is refused by Validate above rather than silently becoming a
// classical handshake.
func (s Security) curves() []tls.CurveID {
	groups := proto.PQGroups(s.Profile, s.Policy)
	out := make([]tls.CurveID, 0, len(groups))
	for _, group := range groups {
		if proto.BuildSupportsGroup(group) {
			out = append(out, tls.CurveID(group))
		}
	}
	return out
}

// apply sets the key agreement preferences on a configuration.
func (s Security) apply(config *tls.Config) *tls.Config {
	if config == nil {
		return nil
	}
	config.CurvePreferences = s.curves()
	return config
}

// Observe reports what a completed handshake actually negotiated.
//
// On Go 1.25 and later this reads the group from the connection state. On
// earlier toolchains the field does not exist, so pq_active is reported as
// unknown - an honest answer, and one the UI is built to display. Reporting
// "yes" because post-quantum was requested would be reporting an intention as
// a fact, which is how a security indicator stops meaning anything.
func Observe(state tls.ConnectionState, security Security) proto.SecurityCapability {
	capability := proto.SecurityCapability{
		PQSupported: proto.BuildSupportsGroup(proto.GroupX25519MLKEM768),
		PQActive:    proto.TriUnknown,
		PQProfile:   security.Profile,
		PQPolicy:    security.Policy,
		TLSVersion:  tlsVersionName(state.Version),
		Observable:  proto.GroupObservable,
	}
	group, known := proto.NegotiatedGroup(state)
	if !known {
		return capability
	}
	capability.PQGroup = proto.GroupName(group)
	if proto.IsPQGroup(group) {
		capability.PQActive = proto.TriYes
	} else {
		capability.PQActive = proto.TriNo
	}
	return capability
}

func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case 0:
		return ""
	default:
		return fmt.Sprintf("TLS 0x%04x", version)
	}
}

// ---------------------------------------------------------------------------
// The three configurations, with a security policy
// ---------------------------------------------------------------------------

// SecureClientConfig is ClientConfig with a post-quantum policy applied.
func SecureClientConfig(pin Pin, certificate *x509.Certificate, security Security) (*tls.Config, error) {
	if err := security.Validate(); err != nil {
		return nil, err
	}
	config, err := ClientConfig(pin, certificate)
	if err != nil {
		return nil, err
	}
	return security.apply(config), nil
}

// SecureFirstContactConfig is FirstContactConfig with a policy applied.
//
// The policy applies at first contact too, and that is worth saying: a device
// adopted over a classical handshake would be adopted with a session key that a
// future attacker could recover from a recording made today. If an operator has
// asked for REQUIRED, adoption must fail rather than quietly happen anyway.
func SecureFirstContactConfig(observed *Observation, security Security) (*tls.Config, error) {
	if err := security.Validate(); err != nil {
		return nil, err
	}
	return security.apply(FirstContactConfig(observed)), nil
}

// SecureRepinConfig is RepinConfig with a policy applied.
func SecureRepinConfig(pin Pin, observed *Observation, security Security) (*tls.Config, error) {
	if err := security.Validate(); err != nil {
		return nil, err
	}
	config, err := RepinConfig(pin, observed)
	if err != nil {
		return nil, err
	}
	return security.apply(config), nil
}
