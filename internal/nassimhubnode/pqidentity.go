package nassimhubnode

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/store"
	"github.com/human-agent65535/nassimhub-node/core/nodeclient"
	"github.com/human-agent65535/nassimhub-node/proto"
)

// Post-quantum identity for Nodes, on the gateway side.
//
// The Ed25519 pin made at pairing is unchanged and remains what identifies a
// device. This file adds a second, independent check on top of it: the Node
// proves, live, that it holds its identity keys by signing a transcript bound
// to a fresh nonce (proto/pqattest.go), and the gateway records the Node's
// ADDITIONAL ML-DSA key the first time a dual signature verifies.
//
// # What each security level does with that
//
//	STANDARD     Verify what the Node offers and report it. Nothing is
//	             demanded, and a Node whose post-quantum key later changes or
//	             disappears keeps working - classically - with the discrepancy
//	             shown. The gateway does not offer a key of its own, so an
//	             older gateway build restored over this one still works with
//	             every Node.
//	PQ           The Node's key is pinned and enforced: once seen it must be
//	             presented, unchanged and with a valid signature, or the Node
//	             is refused. The gateway offers its own key and the Node pins
//	             it the same way.
//	PQ_EXTREME   As PQ, and a Node with no post-quantum identity is refused.
//
// The split is deliberate. Enforcement that cannot be turned off would make an
// agent rollback - which removes the Node's ability to sign - look like a
// compromised device on a gateway whose owner never asked for post-quantum
// identity. Choosing PQ is the owner saying they want that refusal.
//
// These are separate facts from the TLS key agreement on the same connection
// (reported under link_security) and from update signatures. A dual identity
// does not make the session post-quantum, and the reverse.

// attestInterval bounds how long a verified identity is relied on without
// asking again. A change of boot id or a failed poll asks sooner.
const attestInterval = 10 * time.Minute

// PQIdentityRepository is the persistence for Node post-quantum pins.
// *store.Store implements it.
type PQIdentityRepository interface {
	NasSimHubNodePQPin(context.Context, string) (store.NasSimHubNodePQPin, error)
	PinNasSimHubNodePQ(context.Context, store.NasSimHubNodePQPin) (bool, error)
	ResetNasSimHubNodePQPin(context.Context, string) (bool, error)
}

// Identity outcomes as the administration page receives them.
const (
	IdentityOutcomeUnknown       = "unknown"
	IdentityOutcomeDual          = "dual"
	IdentityOutcomeClassicalOnly = "classical_only"
	IdentityOutcomeRefused       = "refused"
)

// Pin states.
const (
	PinStateNone      = "none"
	PinStatePinned    = "pinned"
	PinStateChanged   = "changed"
	PinStateWithdrawn = "withdrawn"
)

// IdentityView is what the gateway established about a Node's identity keys.
//
// Every field is an observation. In particular Outcome is "dual" only after
// both signatures over a fresh transcript verified in this process; a Node
// that merely publishes a post-quantum key is not reported as dual.
type IdentityView struct {
	// Outcome is unknown until the first check, then dual, classical_only or
	// refused.
	Outcome string `json:"outcome"`
	// Policy is what the configured security level demands of the Node.
	Policy string `json:"policy"`
	// Enforced says whether a pinned key is mandatory (PQ and PQ_EXTREME).
	Enforced bool `json:"enforced"`
	// Algorithm and Fingerprint describe the Node's post-quantum key, when one
	// is pinned or was just verified. The fingerprint is a digest for
	// comparison; the key itself is never returned.
	Algorithm   string `json:"algorithm,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// PinState is none, pinned, changed (the Node presents a different key)
	// or withdrawn (the Node presents none).
	PinState string    `json:"pin_state"`
	PinnedAt time.Time `json:"pinned_at,omitzero"`
	// AttestationSupported is false for an agent that predates the exchange.
	AttestationSupported bool `json:"attestation_supported"`
	// CoreKeyOffered says whether this gateway presents a post-quantum key of
	// its own; CoreKeyPinned whether the Node has pinned it.
	CoreKeyOffered bool `json:"core_key_offered"`
	CoreKeyPinned  bool `json:"core_key_pinned"`
	// Available and Backend describe this gateway build: whether it can verify
	// ML-DSA at all, and with what.
	Available bool   `json:"available"`
	Backend   string `json:"backend"`
	// Reason explains a refusal or a discrepancy in words.
	Reason    string    `json:"reason,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
}

// identityState is the live record behind IdentityView.
type identityState struct {
	checked      bool
	checkedAt    time.Time
	bootID       string
	outcome      string
	supported    bool
	algorithm    string
	fingerprint  string
	pinState     string
	pinnedAt     time.Time
	corePinned   bool
	reason       string
	refused      bool
	pinLoaded    bool
	pin          proto.PQIdentity
	forceRecheck bool
}

// PQFingerprint is the short digest shown for a post-quantum key: the first
// 16 bytes of SHA-256 over the raw key, in groups of four hex digits, the same
// shape as the Ed25519 fingerprint.
func PQFingerprint(identity proto.PQIdentity) string {
	raw, err := base64.StdEncoding.DecodeString(identity.PublicKey)
	if err != nil || len(raw) == 0 {
		return ""
	}
	digest := sha256.Sum256(raw)
	encoded := strings.ToUpper(hex.EncodeToString(digest[:16]))
	groups := make([]string, 0, 8)
	for index := 0; index < len(encoded); index += 4 {
		groups = append(groups, encoded[index:index+4])
	}
	return strings.Join(groups, " ")
}

func (f *Fleet) identityPolicy() proto.PQIdentityPolicy {
	return proto.IdentityRequirementFor(f.level)
}

// identityEnforced reports whether a pinned post-quantum key is mandatory.
func (f *Fleet) identityEnforced() bool {
	return f.level == proto.LevelPQ || f.level == proto.LevelPQExtreme
}

// loadPin reads the stored pin once per live Node.
func (f *Fleet) loadPin(ctx context.Context, live *liveNode) (proto.PQIdentity, time.Time, error) {
	live.mu.Lock()
	if live.identity.pinLoaded {
		pin, at := live.identity.pin, live.identity.pinnedAt
		live.mu.Unlock()
		return pin, at, nil
	}
	deviceID := live.record.ID
	live.mu.Unlock()
	if f.pins == nil {
		return proto.PQIdentity{}, time.Time{}, nil
	}
	stored, err := f.pins.NasSimHubNodePQPin(ctx, deviceID)
	pin := proto.PQIdentity{}
	var at time.Time
	switch {
	case err == nil:
		pin = proto.PQIdentity{
			Algorithm: proto.PQSignatureAlgorithm(stored.Algorithm),
			PublicKey: base64.StdEncoding.EncodeToString(stored.PublicKey),
		}
		at = stored.PinnedAt
	case errors.Is(err, store.ErrNasSimHubNodePQPinNotFound):
	default:
		return proto.PQIdentity{}, time.Time{}, err
	}
	live.mu.Lock()
	live.identity.pinLoaded = true
	live.identity.pin = pin
	live.identity.pinnedAt = at
	live.mu.Unlock()
	return pin, at, nil
}

// verifyIdentity runs the attestation when one is due and reports whether the
// Node must be refused, with the reason to show.
//
// It is called from the poll, after the Node has been confirmed paired with
// this gateway and compatible. A refusal stops the poll before anything is
// read from the device: a Node whose identity cannot be established to the
// configured standard contributes no line, no messages and no calls.
func (f *Fleet) verifyIdentity(ctx context.Context, live *liveNode, client *nodeclient.Client, bootID string) (bool, string) {
	now := f.now()
	live.mu.Lock()
	state := live.identity
	live.mu.Unlock()
	due := !state.checked || state.forceRecheck || state.bootID != bootID ||
		now.Sub(state.checkedAt) >= attestInterval || state.refused
	if !due {
		return false, ""
	}

	policy := f.identityPolicy()
	enforced := f.identityEnforced()
	pin, pinnedAt, err := f.loadPin(ctx, live)
	if err != nil {
		// The pin store could not be read. Under enforcement that is not a
		// reason to proceed as though nothing were pinned.
		if enforced {
			return f.recordIdentity(live, identityState{
				checked: true, checkedAt: now, bootID: bootID, outcome: IdentityOutcomeRefused,
				refused: true, reason: "the stored identity pin could not be read",
			})
		}
		pin = proto.PQIdentity{}
	}
	verifyAgainst := proto.PQIdentity{}
	if enforced {
		verifyAgainst = pin
	}

	result, err := client.AttestIdentity(ctx, live.pin.PublicKey, verifyAgainst, policy)
	if err != nil && needsCorePQ(err) && f.identity.PQ != nil {
		// The Node's own level demands a post-quantum identity from its owner
		// and this pairing predates the gateway having one. Presenting it is
		// what the Node asked for.
		if _, pairErr := client.Pair(ctx); pairErr == nil {
			result, err = client.AttestIdentity(ctx, live.pin.PublicKey, verifyAgainst, policy)
		}
	}
	if err == nil && !result.CorePQPinned && f.identity.PQ != nil {
		// A pairing made before this gateway offered a post-quantum key.
		// Pairing again as the same owner is idempotent on the Node and is how
		// it pins the key; the attestation is then repeated so the result
		// describes the upgraded pairing.
		if _, pairErr := client.Pair(ctx); pairErr == nil {
			result, err = client.AttestIdentity(ctx, live.pin.PublicKey, verifyAgainst, policy)
		} else {
			f.logger.Warn("node did not pin the gateway post-quantum key", "component", "nassimhub",
				"node_id", live.record.ID, "error", publicError(pairErr))
		}
	}

	next := identityState{
		checked: true, checkedAt: now, bootID: bootID, supported: true,
		pinLoaded: true, pin: pin, pinnedAt: pinnedAt,
		pinState: PinStateNone,
	}
	if pin.Present() {
		next.pinState = PinStatePinned
		next.algorithm = string(pin.Algorithm)
		next.fingerprint = PQFingerprint(pin)
	}

	switch {
	case err == nil:
		next.corePinned = result.CorePQPinned
		weak := proto.CheckPQAlgorithmForLevel(f.level, result.DevicePQ)
		switch {
		case result.Outcome == proto.IdentityDual && weak != nil:
			// Verified, but with a smaller parameter set than this level
			// accepts. Not pinned: the device must rotate to the larger one.
			next.outcome, next.refused = IdentityOutcomeRefused, true
			next.algorithm = string(result.DevicePQ.Algorithm)
			next.fingerprint = PQFingerprint(result.DevicePQ)
			next.reason = "the device's post-quantum key is " + string(result.DevicePQ.Algorithm) +
				"; this security level requires " + string(proto.PQAlgorithmFor(f.level))
		case result.Outcome == proto.IdentityDual:
			next.outcome = IdentityOutcomeDual
			if !pin.Present() {
				created, pinErr := f.pinDeviceKey(ctx, live, result.DevicePQ, now)
				switch {
				case pinErr != nil && enforced:
					next.outcome, next.refused = IdentityOutcomeRefused, true
					next.reason = "the device identity verified but could not be recorded"
				case pinErr != nil:
					next.reason = "the device identity verified but could not be recorded"
				default:
					next.pin, next.pinState = result.DevicePQ, PinStatePinned
					if created {
						next.pinnedAt = now
					}
					next.algorithm = string(result.DevicePQ.Algorithm)
					next.fingerprint = PQFingerprint(result.DevicePQ)
				}
			} else if !proto.SamePQIdentity(pin, result.DevicePQ) {
				// Only reachable without enforcement: with it, the verifier
				// already refused. The session continues on the Ed25519 pin,
				// the pin is NOT moved, and the page says so.
				next.outcome = IdentityOutcomeClassicalOnly
				next.pinState = PinStateChanged
				next.reason = "the device presents a different post-quantum key from the one recorded"
			}
		default:
			next.outcome = IdentityOutcomeClassicalOnly
			if pin.Present() {
				next.pinState = PinStateWithdrawn
				next.reason = "the device no longer presents the post-quantum key recorded for it"
			}
		}
	case errors.Is(err, nodeclient.ErrAttestationUnsupported):
		// An agent that predates the exchange. It is an old device, not a
		// failed proof - unless the configured standard needs the proof.
		next.supported = false
		next.outcome = IdentityOutcomeClassicalOnly
		switch {
		case policy == proto.PQIdentityRequired:
			next.outcome, next.refused = IdentityOutcomeRefused, true
			next.reason = "the device software cannot prove a post-quantum identity, which this security level requires"
		case enforced && pin.Present():
			next.outcome, next.refused = IdentityOutcomeRefused, true
			next.pinState = PinStateWithdrawn
			next.reason = "the device no longer proves the post-quantum key recorded for it"
		case pin.Present():
			next.pinState = PinStateWithdrawn
			next.reason = "the device no longer proves the post-quantum key recorded for it"
		}
	case isIdentityRefusal(err):
		next.outcome, next.refused = IdentityOutcomeRefused, true
		next.reason = identityRefusalReason(err)
		switch {
		case errors.Is(err, proto.ErrPQIdentityChanged):
			next.pinState = PinStateChanged
		case errors.Is(err, proto.ErrPQIdentityWithdrawn):
			next.pinState = PinStateWithdrawn
		}
	default:
		// The exchange did not complete (link dropped, timeout, session not
		// obtained). Nothing was established either way. Keep what was known,
		// ask again on the next poll, and refuse meanwhile only where the
		// configured standard forbids using an unverified device.
		live.mu.Lock()
		previous := live.identity
		previous.forceRecheck = true
		previous.pinLoaded, previous.pin, previous.pinnedAt = true, pin, pinnedAt
		live.identity = previous
		live.mu.Unlock()
		if needsCorePQ(err) {
			return true, "the device requires a post-quantum identity from this gateway; set the security level to PQ"
		}
		if code := proto.CodeOf(err); code == proto.ErrorUnauthenticated || code == proto.ErrorPermissionDenied {
			// The device answered and declined this gateway's credentials. That
			// is not a link problem and will not fix itself: nothing can be
			// read from it, so it is reported as refused rather than as an
			// online device with every reading failing.
			return f.recordIdentity(live, identityState{
				checked: true, checkedAt: now, bootID: bootID, supported: true,
				outcome: IdentityOutcomeRefused, refused: true,
				pinLoaded: true, pin: pin, pinnedAt: pinnedAt, pinState: previous.pinState,
				algorithm: previous.algorithm, fingerprint: previous.fingerprint,
				reason: "the device does not accept this gateway's credentials: " + publicError(err),
			})
		}
		if (policy == proto.PQIdentityRequired || (enforced && pin.Present())) && !previous.checked {
			return true, "identity not yet verified: " + publicError(err)
		}
		return previous.refused, previous.reason
	}
	return f.recordIdentity(live, next)
}

func (f *Fleet) recordIdentity(live *liveNode, next identityState) (bool, string) {
	live.mu.Lock()
	previous := live.identity
	if !next.pinLoaded {
		next.pinLoaded, next.pin, next.pinnedAt = previous.pinLoaded, previous.pin, previous.pinnedAt
	}
	live.identity = next
	deviceID := live.record.ID
	live.mu.Unlock()
	if previous.outcome != next.outcome || previous.pinState != next.pinState || previous.refused != next.refused {
		attributes := []any{"component", "nassimhub", "node_id", deviceID,
			"outcome", next.outcome, "pin_state", next.pinState}
		if next.reason != "" {
			attributes = append(attributes, "reason", next.reason)
		}
		if next.refused || next.pinState == PinStateChanged || next.pinState == PinStateWithdrawn {
			f.logger.Warn("node identity check", attributes...)
		} else {
			f.logger.Info("node identity check", attributes...)
		}
		f.emitIdentityEvent(deviceID, previous, next)
	}
	return next.refused, next.reason
}

// pinDeviceKey records a Node's post-quantum key, write-once.
func (f *Fleet) pinDeviceKey(ctx context.Context, live *liveNode, identity proto.PQIdentity, at time.Time) (bool, error) {
	if f.pins == nil {
		return false, nil
	}
	raw, err := base64.StdEncoding.DecodeString(identity.PublicKey)
	if err != nil || len(raw) == 0 {
		return false, errors.New("device post-quantum key is not readable")
	}
	created, err := f.pins.PinNasSimHubNodePQ(ctx, store.NasSimHubNodePQPin{
		NodeID: live.record.ID, Algorithm: string(identity.Algorithm), PublicKey: raw, PinnedAt: at,
	})
	if err != nil {
		return false, err
	}
	if !created {
		// Another poll pinned first. Whatever is stored is the pin; make sure
		// it is the key that was just verified before relying on it.
		stored, readErr := f.pins.NasSimHubNodePQPin(ctx, live.record.ID)
		if readErr != nil {
			return false, readErr
		}
		if stored.Algorithm != string(identity.Algorithm) || base64.StdEncoding.EncodeToString(stored.PublicKey) != identity.PublicKey {
			return false, errors.New("a different post-quantum key is already recorded for this device")
		}
	}
	return created, nil
}

func isIdentityRefusal(err error) bool {
	for _, target := range []error{
		proto.ErrPQIdentityChanged, proto.ErrPQIdentityWithdrawn, proto.ErrPQIdentityInvalid,
		proto.ErrPQIdentityRequired, proto.ErrPQIdentityMissing, proto.ErrClassicalIdentityInvalid,
		proto.ErrAttestationMismatch, proto.ErrPQIdentityUnavailable,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

func identityRefusalReason(err error) string {
	switch {
	case errors.Is(err, proto.ErrPQIdentityChanged):
		return "the device presents a different post-quantum key from the one recorded; accept the new key only if you replaced it deliberately"
	case errors.Is(err, proto.ErrPQIdentityWithdrawn):
		return "the device no longer presents the post-quantum key recorded for it"
	case errors.Is(err, proto.ErrAttestationMismatch):
		return "the device's identity proof does not describe this gateway and this device"
	case errors.Is(err, proto.ErrClassicalIdentityInvalid):
		return "the device's identity signature did not verify"
	case errors.Is(err, proto.ErrPQIdentityUnavailable):
		return "this gateway build cannot verify post-quantum signatures (" + proto.GoVersionForMLDSA + " or newer is required)"
	case errors.Is(err, proto.ErrPQIdentityRequired), errors.Is(err, proto.ErrPQIdentityMissing):
		return "the device has no post-quantum identity, which this security level requires"
	default:
		return "the device's post-quantum signature did not verify"
	}
}

// needsCorePQ recognises a Node that refuses classical requests because its own
// level demands a post-quantum identity from the gateway.
func needsCorePQ(err error) bool {
	operation, ok := proto.AsOperationError(err)
	return ok && operation.Code == proto.ErrorFailedPrecondition &&
		strings.Contains(operation.Message, "post-quantum identity is required")
}

func (l *liveNode) identityView(f *Fleet) IdentityView {
	// Caller holds l.mu.
	state := l.identity
	view := IdentityView{
		Outcome:              IdentityOutcomeUnknown,
		Policy:               string(f.identityPolicy()),
		Enforced:             f.identityEnforced(),
		PinState:             PinStateNone,
		AttestationSupported: state.supported,
		CoreKeyOffered:       f.identity.PQ != nil,
		CoreKeyPinned:        state.corePinned,
		Available:            proto.PQIdentityAvailable(),
		Backend:              proto.PQBackendName(),
		Algorithm:            state.algorithm,
		Fingerprint:          state.fingerprint,
		Reason:               state.reason,
		CheckedAt:            state.checkedAt,
		PinnedAt:             state.pinnedAt,
	}
	if state.checked {
		view.Outcome = state.outcome
	}
	if state.pinState != "" {
		view.PinState = state.pinState
	}
	return view
}

// ResetPQPin forgets the post-quantum key recorded for a Node, so the key it
// presents next is verified and recorded in its place.
//
// This is the administrator accepting a deliberate rotation - a reinstalled
// agent, a replaced key file. It does not touch the Ed25519 pin: the device is
// still the device that was adopted, proven by the same key as before. The
// Node is polled immediately so the result is visible.
func (f *Fleet) ResetPQPin(ctx context.Context, deviceID string) (NodeView, error) {
	live := f.lookup(deviceID)
	if live == nil {
		return NodeView{}, ErrUnknownNode
	}
	if f.pins != nil {
		if _, err := f.pins.ResetNasSimHubNodePQPin(ctx, live.record.ID); err != nil {
			return NodeView{}, err
		}
	}
	live.mu.Lock()
	previous := live.identity
	live.identity = identityState{pinLoaded: true, forceRecheck: true}
	live.mu.Unlock()
	f.logger.Warn("node post-quantum pin reset by an administrator", "component", "nassimhub", "node_id", live.record.ID)
	f.emitIdentityEvent(live.record.ID, previous, identityState{outcome: "pin_reset", pinState: PinStateNone})
	return f.Refresh(ctx, deviceID)
}

// IdentityEvent is a change in what the gateway established about a Node's
// identity, for the security event log.
type IdentityEvent struct {
	NodeID   string
	Outcome  string
	PinState string
	Refused  bool
	Reason   string
	Previous string
}

// OnIdentityEvent registers a receiver for identity changes. It is called on
// the polling goroutine and must not block.
func (f *Fleet) OnIdentityEvent(callback func(IdentityEvent)) {
	f.mu.Lock()
	f.onIdentity = callback
	f.mu.Unlock()
}

func (f *Fleet) emitIdentityEvent(deviceID string, previous, next identityState) {
	f.mu.Lock()
	callback := f.onIdentity
	f.mu.Unlock()
	if callback == nil {
		return
	}
	callback(IdentityEvent{
		NodeID: deviceID, Outcome: next.outcome, PinState: next.pinState,
		Refused: next.refused, Reason: next.reason, Previous: previous.outcome,
	})
}

// LinkSecurityView is the key agreement of the gateway's TLS session with a
// Node: a property of the session, separate from identity.
//
// It carries two observations of the same connection and keeps them apart: what
// this gateway saw on its own side of the handshake, and what the Node says it
// saw. They should agree. Reporting only the Node's would be taking the
// device's word for the thing being checked.
type LinkSecurityView struct {
	TLSVersion string `json:"tls_version,omitempty"`
	// Group is the negotiated key agreement group as this gateway observed
	// it. Empty when unknown.
	Group string `json:"group,omitempty"`
	// PostQuantum is yes, no or unknown - what was negotiated, not what was
	// asked for.
	PostQuantum string `json:"post_quantum"`
	// NodeGroup is the group the Node reports for the same connection.
	NodeGroup string `json:"node_group,omitempty"`
	// RequestedLevel is the gateway's configured level, and RequiredGroup the
	// one group that level insists on (empty at STANDARD).
	RequestedLevel string `json:"requested_level"`
	RequiredGroup  string `json:"required_group,omitempty"`
	// MeetsLevel is whether the observed session satisfies the level. It is
	// false while nothing has been observed.
	MeetsLevel bool `json:"meets_level"`
	// NodeProfile and NodePolicy are what the Node itself was configured to
	// accept.
	NodeProfile string `json:"node_profile,omitempty"`
	NodePolicy  string `json:"node_policy,omitempty"`
	Known       bool   `json:"known"`
}

func tlsVersionLabel(version uint16) string {
	switch version {
	case 0x0304:
		return "TLS 1.3"
	case 0x0303:
		return "TLS 1.2"
	default:
		return ""
	}
}

func linkSecurityView(
	level proto.SecurityLevel,
	observed nodeclient.ObservedLink,
	reported proto.SecurityCapability,
	reportedKnown bool,
) LinkSecurityView {
	view := LinkSecurityView{
		PostQuantum:    string(proto.TriUnknown),
		RequestedLevel: string(level),
	}
	required, hasRequired := proto.RequiredGroupFor(level)
	if hasRequired {
		view.RequiredGroup = proto.GroupName(required)
	}
	if reportedKnown {
		view.NodeGroup = reported.PQGroup
		view.NodeProfile = string(reported.PQProfile)
		view.NodePolicy = string(reported.PQPolicy)
	}
	if !observed.Known {
		return view
	}
	view.Known = true
	view.TLSVersion = tlsVersionLabel(observed.TLSVersion)
	if !observed.GroupKnown {
		// This toolchain cannot read the group back. The level is still
		// enforced by what was offered; it just cannot be shown.
		return view
	}
	view.Group = proto.GroupName(observed.Group)
	if proto.IsPQGroup(observed.Group) {
		view.PostQuantum = string(proto.TriYes)
	} else {
		view.PostQuantum = string(proto.TriNo)
	}
	view.MeetsLevel = !hasRequired || observed.Group == required
	return view
}

// linkBelowLevel reports a session whose key agreement is not the one the
// level requires.
//
// The level is enforced by what the gateway offers: a peer that cannot do the
// required group never completes the handshake. This is the second look, on
// the completed session, and it should never find anything. It exists because
// "should never" was once wrong for PQ_EXTREME without anyone being told.
func (f *Fleet) linkBelowLevel(client *nodeclient.Client) string {
	required, hasRequired := proto.RequiredGroupFor(f.level)
	if !hasRequired {
		return ""
	}
	observed := client.ObservedLink()
	if !observed.Known || !observed.GroupKnown || observed.Group == required {
		return ""
	}
	return "the session negotiated " + proto.GroupName(observed.Group) + "; " +
		string(f.level) + " requires " + proto.GroupName(required)
}
