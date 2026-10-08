package trustengine

import (
	"context"
	"errors"
	"math"
	"strings"
)

// What a device should enforce.
//
// The engine decides; since r128 a NasSimHub Node also enforces the decision
// itself, from a policy the gateway signs (see the Node protocol's
// trustpolicy.go). This file is the engine's part of that: stating the
// decision in the terms a policy uses, and handing out the policy generation
// numbers a device uses to refuse an older policy.
//
// Nothing here signs or sends. The fleet does both, with the Core identity key
// it already holds.

// DeviceActions are the actions a device can be told to refuse, in the order a
// policy lists them.
var DeviceActions = []Action{ActionDial, ActionDTMF, ActionForwardOTP, ActionSendSMS}

// DevicePolicy is the engine's current decision for one device.
type DevicePolicy struct {
	SubjectID string
	// State is the EFFECTIVE state: the record's, unless the record or the
	// journal cannot be believed, in which case it is at least RESTRICTED.
	State       State
	Enforcement Enforcement
	// Deny lists the actions that are not allowed, in DeviceActions order.
	Deny []Action
	// Reason is short and carries no identifier and no administrator's free
	// text: it is shown on the device's own page.
	Reason string
	// Generation is the last policy generation handed out, 0 if none.
	Generation uint64
}

// DevicePolicy reports what a device should enforce now. ok is false for a
// subject with no trust record: there is nothing to sign yet, and the
// gateway's own gate refuses everything outgoing for it.
func (e *Engine) DevicePolicy(subject string) (DevicePolicy, bool) {
	subject = strings.TrimSpace(subject)
	e.mu.Lock()
	defer e.mu.Unlock()
	record, known := e.records[subject]
	if !known {
		return DevicePolicy{}, false
	}
	state, reason := e.effectiveLocked(subject)
	policy := DevicePolicy{SubjectID: subject, State: state, Enforcement: e.enforcement, Generation: record.PolicyGeneration, Deny: []Action{}}
	for _, action := range DeviceActions {
		if !allowed(state, record.OTPLimited, action) {
			policy.Deny = append(policy.Deny, action)
		}
	}
	if reason == "" && record.OTPLimited {
		reason = "an unusual number of verification codes"
	}
	if reason == "" && state == Observation {
		reason = "observation period"
	}
	policy.Reason = deviceReason(reason)
	return policy, true
}

// deviceReason shortens a reason for the device. The engine's reasons are
// "<what happened>: <detail>", and the detail may quote an administrator or a
// device report; only the first part travels.
func deviceReason(reason string) string {
	if head, _, found := strings.Cut(reason, ":"); found {
		reason = head
	}
	reason = strings.TrimSpace(Sanitize(reason))
	const limit = 120
	if len(reason) > limit {
		reason = strings.ToValidUTF8(reason[:limit], "")
	}
	return reason
}

// ErrPolicyGenerationExhausted is returned when no higher generation exists.
var ErrPolicyGenerationExhausted = errors.New("the trust policy generation cannot be increased further")

// NextPolicyGeneration hands out the next policy generation for a subject and
// persists it in the trust record before returning, so a number is never used
// twice across a restart. The result is greater than every generation handed
// out before and greater than atLeast - which a caller sets to the generation
// a device reports holding when that is ahead of this record (a database
// restored from a backup): the device is right to refuse a lower number, and
// the authority that holds the signing key is the one that may move past it.
func (e *Engine) NextPolicyGeneration(ctx context.Context, subject string, atLeast uint64) (uint64, error) {
	subject = strings.TrimSpace(subject)
	e.mu.Lock()
	defer e.mu.Unlock()
	record, known := e.records[subject]
	if !known {
		return 0, ErrUnknownSubject
	}
	floor := max(record.PolicyGeneration, atLeast)
	// The column is a signed 64-bit integer. A device can only report a number
	// this large from a damaged file (no gateway counts that far), and such a
	// file has to be removed on the device by its owner: it is reported, not
	// worked around by wrapping.
	if floor >= math.MaxInt64 {
		return 0, ErrPolicyGenerationExhausted
	}
	next := *record
	next.PolicyGeneration = floor + 1
	if _, bad := e.damaged[subject]; bad {
		// A record that is not believed is not re-signed by this: only an
		// administrator's Restore vouches for it again. The number is still
		// written, so it cannot be handed out twice; the row keeps a signature
		// that does not verify and stays damaged.
		err := e.store.Commit(ctx, &next, nil, func(previous Head, _ []Event, _ *Record) (Head, error) { return previous, nil })
		if err != nil {
			return 0, err
		}
		record.PolicyGeneration = next.PolicyGeneration
		return next.PolicyGeneration, nil
	}
	if err := e.commitLocked(ctx, &next, nil); err != nil {
		return 0, err
	}
	return next.PolicyGeneration, nil
}

// SetOnChange registers a listener told, on its own goroutine, when a
// subject's state, verification-code limit or reason changed - that is, when
// the policy a device should enforce may differ from the one it holds.
func (e *Engine) SetOnChange(listener func(subject string)) {
	e.mu.Lock()
	e.onChange = listener
	e.mu.Unlock()
}
