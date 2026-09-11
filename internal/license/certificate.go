// Package license decides which paid features this installation may use.
//
// What this can and cannot do, stated up front because it shapes every choice
// below: certificates are Ed25519-signed, so nobody can FORGE one without the
// issuer's private key, and a certificate copied to another machine is inert
// because it names the machine or module it was issued for. What it cannot do
// is stop somebody editing the source and deleting the check. This product
// ships as source and containers; the customer runs the code. Licensing here
// raises the cost of casual copying. It is not DRM and must not be described
// as though it were.
//
// That ceiling is why nothing in this package is obfuscated, hidden or
// encrypted at rest. Those measures would buy no security against the only
// attacker who matters, while making every support call harder to diagnose.
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Scope is what a certificate binds to.
type Scope string

const (
	// ScopeMachine binds to one gateway, identified by its hardware.
	ScopeMachine Scope = "machine"
	// ScopeModule binds to one modem, identified by its serial number.
	ScopeModule Scope = "module"
)

// Certificate is the signed grant. Every field is public: it travels over NKN
// to the customer and is stored in plain sight on their disk.
type Certificate struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
	Scope   Scope  `json:"scope"`
	// MachineID is set for ScopeMachine, ModuleSN for ScopeModule. Exactly one
	// of them is set: a certificate that named both would be a third scope in
	// disguise, and the catalog already expresses "needs both" as two
	// certificates.
	MachineID string `json:"machine_id,omitempty"`
	ModuleSN  string `json:"module_sn,omitempty"`
	// Features are the catalog names this certificate grants.
	Features []string `json:"features"`
	IssuedAt int64    `json:"issued_at"`
	// ExpiresAt is 0 for a perpetual certificate. Both kinds exist on purpose:
	// a subscription is issued with an expiry and renewed, a one-off purchase
	// is issued without one.
	ExpiresAt int64 `json:"expires_at,omitempty"`
	// Note is free text for the issuer's own records. It is signed along with
	// everything else, so it cannot be edited after the fact.
	Note string `json:"note,omitempty"`
}

// Signed is a certificate with its signature.
type Signed struct {
	Certificate Certificate `json:"cert"`
	Signature   string      `json:"sig"`
}

const (
	// CurrentVersion is the certificate format this build issues and accepts.
	CurrentVersion = 1
	maxFeatures    = 64
	maxIDLength    = 128
	maxNoteLength  = 256
)

var (
	// ErrBadSignature means the certificate was altered or signed by someone
	// else. It is never a reason to fall back to granting the feature.
	ErrBadSignature = errors.New("license: signature does not verify")
	// ErrExpired means the certificate was valid and no longer is.
	ErrExpired = errors.New("license: certificate has expired")
	// ErrNotYetValid guards against a clock skewed far into the past.
	ErrNotYetValid = errors.New("license: certificate is not yet valid")
	// ErrMalformed covers anything structurally wrong.
	ErrMalformed = errors.New("license: certificate is malformed")
)

// canonical renders the certificate as the exact bytes that get signed.
//
// This is deliberately NOT json.Marshal. A signature must survive a Go
// version bump, a struct field being reordered, and being reimplemented in the
// signing tool; JSON gives no guarantee about any of that. An explicit
// line-oriented encoding can be re-derived from this function by reading it,
// and every field that exists is present in a fixed order whether or not it is
// empty, so adding a field later cannot silently change what an old
// certificate hashed to.
func (c Certificate) canonical() ([]byte, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	features := append([]string(nil), c.Features...)
	sort.Strings(features)

	var builder strings.Builder
	write := func(key, value string) {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(value)
		builder.WriteByte('\n')
	}
	write("v", strconv.Itoa(c.Version))
	write("id", c.ID)
	write("scope", string(c.Scope))
	write("machine_id", c.MachineID)
	write("module_sn", c.ModuleSN)
	write("features", strings.Join(features, ","))
	write("issued_at", strconv.FormatInt(c.IssuedAt, 10))
	write("expires_at", strconv.FormatInt(c.ExpiresAt, 10))
	write("note", c.Note)
	return []byte(builder.String()), nil
}

// validate rejects anything that could make the canonical form ambiguous.
//
// The separators in the canonical encoding are "\n" and ",", so a value
// containing either could be split differently by a reader than by the writer
// -- two different certificates with one signature between them. Refusing the
// characters outright is simpler to reason about than escaping them.
func (c Certificate) validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrMalformed, c.Version)
	}
	if c.ID == "" || len(c.ID) > maxIDLength {
		return fmt.Errorf("%w: certificate id", ErrMalformed)
	}
	switch c.Scope {
	case ScopeMachine:
		if c.MachineID == "" || c.ModuleSN != "" {
			return fmt.Errorf("%w: a machine certificate names exactly one machine", ErrMalformed)
		}
	case ScopeModule:
		if c.ModuleSN == "" || c.MachineID != "" {
			return fmt.Errorf("%w: a module certificate names exactly one module", ErrMalformed)
		}
	default:
		return fmt.Errorf("%w: unknown scope %q", ErrMalformed, c.Scope)
	}
	if len(c.MachineID) > maxIDLength || len(c.ModuleSN) > maxIDLength {
		return fmt.Errorf("%w: bound id is too long", ErrMalformed)
	}
	if len(c.Features) == 0 || len(c.Features) > maxFeatures {
		return fmt.Errorf("%w: feature list", ErrMalformed)
	}
	if len(c.Note) > maxNoteLength {
		return fmt.Errorf("%w: note is too long", ErrMalformed)
	}
	for _, field := range append([]string{c.ID, c.MachineID, c.ModuleSN, c.Note}, c.Features...) {
		if strings.ContainsAny(field, "\n\r,=") {
			return fmt.Errorf("%w: %q contains a separator character", ErrMalformed, field)
		}
	}
	if c.IssuedAt <= 0 {
		return fmt.Errorf("%w: issued_at", ErrMalformed)
	}
	if c.ExpiresAt != 0 && c.ExpiresAt <= c.IssuedAt {
		return fmt.Errorf("%w: expires_at is not after issued_at", ErrMalformed)
	}
	return nil
}

// Sign produces a signed certificate. Only the issuing tool calls this; the
// gateway has no private key and no reason to hold one.
func Sign(certificate Certificate, key ed25519.PrivateKey) (Signed, error) {
	message, err := certificate.canonical()
	if err != nil {
		return Signed{}, err
	}
	if len(key) != ed25519.PrivateKeySize {
		return Signed{}, fmt.Errorf("license: signing key is the wrong size")
	}
	signature := ed25519.Sign(key, message)
	return Signed{
		Certificate: certificate,
		Signature:   base64.StdEncoding.EncodeToString(signature),
	}, nil
}

// Verify checks the signature and the validity window.
//
// It says nothing about whether the certificate belongs to THIS machine; that
// is the catalog's job, and keeping the two separate means a certificate for
// another machine reports as "not yours" rather than "invalid", which is the
// difference between a useful support answer and a baffling one.
func Verify(signed Signed, publicKey ed25519.PublicKey, now time.Time) error {
	message, err := signed.Certificate.canonical()
	if err != nil {
		return err
	}
	if len(publicKey) != ed25519.PublicKeySize {
		// No key configured means no certificate can be valid, which locks
		// every paid feature. That is the right default: the alternative
		// would be shipping a build where everything is unlocked.
		return ErrBadSignature
	}
	signature, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil {
		return fmt.Errorf("%w: signature is not base64", ErrMalformed)
	}
	if !ed25519.Verify(publicKey, message, signature) {
		return ErrBadSignature
	}
	// A little tolerance for clock skew between the issuer and the gateway.
	// Without it a certificate issued seconds ago can be rejected by a machine
	// whose clock is a minute behind, which looks like a broken purchase.
	const skew = 10 * time.Minute
	moment := now.Unix()
	if moment+int64(skew.Seconds()) < signed.Certificate.IssuedAt {
		return ErrNotYetValid
	}
	if signed.Certificate.ExpiresAt != 0 && moment > signed.Certificate.ExpiresAt {
		return ErrExpired
	}
	return nil
}

// Expiry reports when the certificate lapses, and whether it lapses at all.
func (c Certificate) Expiry() (time.Time, bool) {
	if c.ExpiresAt == 0 {
		return time.Time{}, false
	}
	return time.Unix(c.ExpiresAt, 0).UTC(), true
}
