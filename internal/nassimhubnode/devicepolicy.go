package nassimhubnode

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/trustengine"
	"github.com/human-agent65535/nassimhub-node/core/nodeclient"
	"github.com/human-agent65535/nassimhub-node/proto"
)

// Device-local enforcement of the trust engine's decision.
//
// The engine decides and the gateway keeps its own gate (trust.go). Since
// protocol 1.4 a Node also enforces the decision itself: the gateway signs a
// policy with its identity key and the device refuses outgoing use on its own
// account - from a buggy gateway process, from a replayed request and from
// its own standalone page. This file builds, signs and delivers that policy,
// and reads back what the device says it is doing.
//
// Two facts are kept apart everywhere they are shown: what the GATEWAY decided
// (TrustView's state) and what the DEVICE reports it holds and enforces
// (TrustView.Device). Neither is inferred from the other.
//
// When a policy is sent:
//
//   - on every new session with the device, before the request that needed the
//     session goes out (nodeclient.OnSession), so nothing outgoing is forwarded
//     on a fresh session ahead of the policy;
//   - when the engine's decision for the device changes;
//   - when the last one signed is older than policyRefreshEvery;
//   - when the device reports something other than what was last signed, or
//     reports it stale.
//
// The device's clock is not part of any of this. A UFI003 has no
// battery-backed clock, so the device accepts a policy whatever its own date
// is and counts the validity on its monotonic clock from the moment it
// receives it; and nothing here compares a time the device reports with the
// gateway's. After a restart the device holds its policy as "stale_restart"
// until it hears it again, which is why the push on a new session sends the
// last signed document even when nothing about the decision changed: the same
// bytes arriving again are what makes it fresh. A new generation is signed
// only for a new decision or on the renewal timer.
//
// A push that fails is recorded for the device and tried again at a later
// poll. It never stops the poll, so it never stops receiving; and it does not
// by itself stop an outgoing request the gateway's own gate allows - the
// device then decides from the policy it holds, which is the point of it
// holding one.

const (
	// policyValidity is how long a signed policy's permissions last.
	policyValidity = 24 * time.Hour
	// policyRefreshEvery is how often a new one is signed for a device that
	// is otherwise unchanged, well inside policyValidity.
	policyRefreshEvery = 6 * time.Hour
	// policyRetryEvery spaces retries of a failed push.
	policyRetryEvery = 30 * time.Second
	// unsupportedRecheckEvery is how often a device without the endpoints is
	// asked again; a device that was updated is noticed sooner, by its boot.
	unsupportedRecheckEvery = 6 * time.Hour
)

// devicePolicyState is what the gateway last signed for one device and what
// the device last said. mu guards the fields and is never held across a
// request to the device.
type devicePolicyState struct {
	mu sync.Mutex
	// hooked is the client the session hook is registered on; the client is
	// replaced when a device's certificate is re-pinned.
	hooked *nodeclient.Client

	signed    proto.SignedTrustPolicy
	hasSigned bool
	signedAt  time.Time
	delivered bool
	// floor is a generation the device reported holding that is not below
	// ours: the next policy is numbered past it.
	floor       uint64
	lastAttempt time.Time
	lastError   string

	// What the device said, and when.
	device      proto.TrustStatus
	deviceKnown bool
	checkedAt   time.Time
	// unsupported: the device has no trust endpoints (an agent before 1.4).
	unsupported   bool
	unsupportedAt time.Time
	// probedBoot identifies the device process that was asked, so an updated
	// and restarted device is asked again at once.
	probedBoot string
}

type policySyncKey struct{}

// inPolicySync marks a context whose request IS a policy push, so the session
// hook that may run underneath it does not start another.
func inPolicySync(ctx context.Context) bool { return ctx.Value(policySyncKey{}) != nil }

func protoDeny(actions []trustengine.Action) []proto.TrustAction {
	deny := make([]proto.TrustAction, 0, len(actions))
	for _, action := range actions {
		deny = append(deny, proto.TrustAction(action))
	}
	return proto.NormalizeTrustDeny(deny)
}

func sameDecision(signed proto.TrustPolicy, desired trustengine.DevicePolicy) bool {
	return string(signed.State) == string(desired.State) && string(signed.Mode) == string(desired.Enforcement) &&
		signed.Reason == desired.Reason && slices.Equal(signed.Deny, protoDeny(desired.Deny))
}

// preparePolicy decides whether the device needs a policy now and, if so,
// returns the one to send, signing a new generation when the decision changed
// or the last one is due for renewal. force is set on a new session.
func (f *Fleet) preparePolicy(ctx context.Context, live *liveNode, force bool) (proto.SignedTrustPolicy, bool) {
	engine := f.trustEngine()
	if engine == nil {
		return proto.SignedTrustPolicy{}, false
	}
	desired, known := engine.DevicePolicy(live.record.ID)
	if !known {
		// No trust record yet (the first poll creates it). There is nothing to
		// sign, and the gateway's gate refuses everything outgoing meanwhile.
		return proto.SignedTrustPolicy{}, false
	}
	state := &live.policy
	state.mu.Lock()
	defer state.mu.Unlock()
	now := f.now()
	if state.unsupported && now.Sub(state.unsupportedAt) < unsupportedRecheckEvery {
		// An agent without the endpoints is not asked again on every session:
		// readDeviceTrust notices a device that was updated, by its boot.
		return proto.SignedTrustPolicy{}, false
	}
	fresh := state.hasSigned && sameDecision(state.signed.TrustPolicy, desired) &&
		now.Sub(state.signedAt) < policyRefreshEvery && !now.Before(state.signedAt) && state.floor < state.signed.Generation
	if !fresh {
		generation, err := engine.NextPolicyGeneration(ctx, live.record.ID, state.floor)
		if err != nil {
			state.lastError, state.lastAttempt = "the policy could not be numbered: "+publicError(err), now
			return proto.SignedTrustPolicy{}, false
		}
		signed, err := proto.SignTrustPolicy(proto.TrustPolicy{
			Version: proto.TrustPolicyVersion, DeviceID: live.record.ID, CoreID: f.identity.CoreID, Generation: generation,
			State: proto.TrustState(desired.State), Mode: proto.TrustMode(desired.Enforcement), Deny: protoDeny(desired.Deny),
			Reason: desired.Reason, IssuedAt: proto.FormatTrustTime(now), ExpiresAt: proto.FormatTrustTime(now.Add(policyValidity)),
		}, f.identity.PrivateKey, f.identity.PQ)
		if err != nil {
			state.lastError, state.lastAttempt = "the policy could not be signed: "+publicError(err), now
			return proto.SignedTrustPolicy{}, false
		}
		state.signed, state.hasSigned, state.signedAt, state.delivered = signed, true, now, false
		return signed, true
	}
	if force {
		return state.signed, true
	}
	if state.delivered {
		return proto.SignedTrustPolicy{}, false
	}
	if state.lastError != "" && now.Sub(state.lastAttempt) < policyRetryEvery && !now.Before(state.lastAttempt) {
		return proto.SignedTrustPolicy{}, false // a failed push is retried, not hammered
	}
	return state.signed, true
}

// syncDevicePolicy makes sure the device holds the current policy. It is safe
// to call from anywhere and often: when there is nothing to send it makes no
// request. It never returns an error - a failure is recorded for the device
// and shown with it.
func (f *Fleet) syncDevicePolicy(ctx context.Context, live *liveNode, force bool) {
	if f.trustEngine() == nil {
		return
	}
	client, _ := live.handles()
	f.hookSession(live, client)
	// Two rounds at most: the second only after the device reported a
	// generation ahead of ours and a new one was signed past it.
	for round := 0; round < 2; round++ {
		signed, send := f.preparePolicy(ctx, live, force)
		if !send {
			return
		}
		if again := f.pushPolicy(context.WithValue(ctx, policySyncKey{}, true), live, client, signed); !again {
			return
		}
		force = false
	}
}

// hookSession registers the session hook on the device's current client.
func (f *Fleet) hookSession(live *liveNode, client *nodeclient.Client) {
	state := &live.policy
	state.mu.Lock()
	registered := state.hooked == client
	state.hooked = client
	state.mu.Unlock()
	if registered || client == nil {
		return
	}
	client.OnSession(func(hookCtx context.Context) {
		if inPolicySync(hookCtx) {
			return // the request that opened this session is itself the push
		}
		// hookCtx must be the one used below: it tells the client these
		// requests belong to the session being established.
		f.syncDevicePolicy(hookCtx, live, true)
	})
}

// pushPolicy sends one signed policy and records the outcome. It reports
// whether the caller should prepare and send again.
func (f *Fleet) pushPolicy(ctx context.Context, live *liveNode, client *nodeclient.Client, signed proto.SignedTrustPolicy) (again bool) {
	state := &live.policy
	status, err := client.PushTrustPolicy(ctx, signed)
	now := f.now()
	if err == nil {
		state.mu.Lock()
		state.unsupported = false
		state.device, state.deviceKnown, state.checkedAt = status, true, now
		if state.hasSigned && state.signed.Generation == signed.Generation {
			state.delivered, state.lastError, state.lastAttempt = true, "", now
		}
		state.mu.Unlock()
		return false
	}
	if errors.Is(err, nodeclient.ErrTrustUnsupported) {
		f.noteUnsupported(live, now)
		return false
	}
	if proto.CodeOf(err) == proto.ErrorConflict {
		// The device holds this generation or a newer one. Ask which.
		held, statusErr := client.TrustStatus(ctx)
		state.mu.Lock()
		defer state.mu.Unlock()
		if statusErr != nil {
			state.lastError, state.lastAttempt = "trust policy: "+publicError(err), now
			return false
		}
		state.device, state.deviceKnown, state.checkedAt = held, true, now
		if state.hasSigned && state.signed.Generation > signed.Generation {
			return false // a newer policy of ours overtook this one; nothing is wrong
		}
		// The device is at or ahead of what this gateway has signed: its
		// database was restored from a backup, or the device holds a damaged
		// file claiming a generation. The device is right to refuse; the next
		// policy is numbered past what it holds.
		if held.Generation >= signed.Generation && held.Generation > state.floor {
			state.floor = held.Generation
			return true
		}
		state.lastError, state.lastAttempt = "trust policy: "+publicError(err), now
		return false
	}
	state.mu.Lock()
	changed := state.lastError == ""
	state.lastError, state.lastAttempt = "trust policy: "+publicError(err), now
	state.mu.Unlock()
	if changed && f.logger != nil {
		f.logger.Warn("trust policy was not delivered to the device; it will be retried", "component", "nassimhub", "node_id", live.record.ID, "error", publicError(err))
	}
	return false
}

func (f *Fleet) noteUnsupported(live *liveNode, now time.Time) {
	state := &live.policy
	state.mu.Lock()
	first := !state.unsupported
	state.unsupported, state.unsupportedAt = true, now
	state.deviceKnown, state.device, state.checkedAt = false, proto.TrustStatus{}, now
	state.lastError = ""
	state.mu.Unlock()
	if first && f.logger != nil {
		// Once, and as information: an agent older than 1.4 is not a fault.
		f.logger.Info("device does not enforce a trust policy itself (agent predates protocol 1.4); the gateway's own gate applies",
			"component", "nassimhub", "node_id", live.record.ID)
	}
}

// readDeviceTrust asks the device what it holds and returns the hardware
// identity it reported, if it answered. Called once per completed poll.
func (f *Fleet) readDeviceTrust(ctx context.Context, live *liveNode, polled nodeState) (proto.HardwareIdentity, bool) {
	client, _ := live.handles()
	f.hookSession(live, client)
	state := &live.policy
	boot := polled.BootID + "\x00" + polled.AgentVersion
	now := f.now()
	state.mu.Lock()
	skip := state.unsupported && state.probedBoot == boot && now.Sub(state.unsupportedAt) < unsupportedRecheckEvery
	state.probedBoot = boot
	state.mu.Unlock()
	if skip {
		return proto.HardwareIdentity{}, false
	}
	status, err := client.TrustStatus(ctx)
	now = f.now()
	if errors.Is(err, nodeclient.ErrTrustUnsupported) {
		f.noteUnsupported(live, now)
		return proto.HardwareIdentity{}, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if err != nil {
		// Not known now. What was read before is not presented as current.
		state.deviceKnown, state.checkedAt = false, now
		return proto.HardwareIdentity{}, false
	}
	state.unsupported = false
	state.device, state.deviceKnown, state.checkedAt = status, true, now
	if state.hasSigned && state.delivered && !deviceMatches(status, state.signed.TrustPolicy) {
		// The device does not hold what was delivered (it was reset, re-paired
		// or its file was damaged), or holds it stale: send again. The same
		// document is enough for a device that restarted, and for one whose
		// validity ran out on its own count.
		//
		// A device that still calls it expired right after receiving it has
		// its wall clock set past the policy's validity. Nothing the gateway
		// signs changes that - a newer generation would expire the same way -
		// so it is neither renumbered nor resent on every poll: the device is
		// reminded at the retry interval and the view says what it reports.
		if !expiredAsDelivered(status, state.signed.TrustPolicy) || now.Sub(state.lastAttempt) >= policyRetryEvery || now.Before(state.lastAttempt) {
			state.delivered = false
		}
	}
	return status.Hardware, true
}

// deviceMatches reports whether the device holds, fresh and intact, the policy
// the gateway last signed.
func deviceMatches(status proto.TrustStatus, signed proto.TrustPolicy) bool {
	return status.Installed && !status.Damaged && !status.Stale &&
		status.Generation == signed.Generation && status.State == signed.State
}

// expiredAsDelivered reports whether the device holds exactly the policy the
// gateway signed, intact, and calls it expired.
func expiredAsDelivered(status proto.TrustStatus, signed proto.TrustPolicy) bool {
	return status.Installed && !status.Damaged && status.Freshness == proto.TrustStaleExpired &&
		status.Generation == signed.Generation && status.State == signed.State
}

// hardwareHash turns a device's reported hardware identity into the keyed hash
// the engine records. Only a well-formed socinfo digest counts; anything else
// is "not known now", exactly as an absent value is.
func (f *Fleet) hardwareHash(identity proto.HardwareIdentity) string {
	if !identity.Present || identity.Source != proto.HardwareSourceSocinfo || len(identity.SerialSHA256) != 64 {
		return ""
	}
	if strings.Trim(identity.SerialSHA256, "0123456789abcdef") != "" {
		return ""
	}
	return trustengine.KeyedHash(f.identity.PublicKey, "hardware", identity.SerialSHA256)
}

// DeviceEnforcementView is what the DEVICE reported about its own enforcement.
// It is a separate fact from the gateway's decision and is never derived from
// it.
type DeviceEnforcementView struct {
	// Known is false when the device has not been asked or did not answer
	// (offline, a failed read). The fields below are then empty, not defaults,
	// and Reason says why.
	Known bool `json:"known"`
	// Supported is false for an agent without the trust endpoints.
	Supported  bool   `json:"supported"`
	Installed  bool   `json:"installed"`
	Generation uint64 `json:"generation"`
	State      string `json:"state,omitempty"`
	// MatchesGateway is true when the device holds the generation and state
	// the gateway last signed.
	MatchesGateway bool `json:"matches_gateway"`
	Enforcing      bool `json:"enforcing"`
	Stale          bool `json:"stale"`
	// Freshness is the device's own reason for Stale: "fresh",
	// "stale_restart" (it restarted and has not been sent the policy since;
	// the next session sends it) or "stale_expired". Empty when the device
	// did not say (an agent that reports only Stale) or holds no policy.
	Freshness string `json:"freshness,omitempty"`
	Damaged   bool   `json:"damaged"`
	// CheckedAt is when the device was last asked.
	CheckedAt time.Time `json:"checked_at,omitzero"`
	Reason    string    `json:"reason,omitempty"`
	// GatewayGeneration is the generation the gateway last signed for the
	// device, 0 when it has signed none since it started.
	GatewayGeneration uint64 `json:"gateway_generation"`
	// PushError is why the last attempt to deliver a policy failed, if it did.
	PushError string `json:"push_error,omitempty"`
	// HardwareReported says whether the device reports a hardware identifier
	// (the input to module-change detection); HardwareReason is the device's
	// own reason when it does not.
	HardwareReported bool   `json:"hardware_reported"`
	HardwareReason   string `json:"hardware_reason,omitempty"`
}

func (f *Fleet) deviceEnforcementView(live *liveNode) DeviceEnforcementView {
	live.mu.Lock()
	online := live.state.Online
	live.mu.Unlock()
	state := &live.policy
	state.mu.Lock()
	defer state.mu.Unlock()
	view := DeviceEnforcementView{CheckedAt: state.checkedAt, PushError: state.lastError}
	if state.hasSigned {
		view.GatewayGeneration = state.signed.Generation
	}
	switch {
	case !online:
		view.Reason = "the device is not reachable; its enforcement is not known now"
		return view
	case state.unsupported:
		view.Known = true
		view.Reason = "the device's agent predates device-local enforcement (protocol 1.4); only the gateway's gate applies"
		return view
	case !state.deviceKnown:
		view.Reason = "the device has not reported its enforcement state yet"
		return view
	}
	device := state.device
	view.Known, view.Supported = true, true
	view.Installed, view.Generation, view.State = device.Installed, device.Generation, string(device.State)
	view.Enforcing, view.Stale, view.Damaged = device.Enforcing, device.Stale, device.Damaged
	view.Freshness = string(device.Freshness)
	if state.hasSigned && state.delivered && expiredAsDelivered(device, state.signed.TrustPolicy) {
		// Said of what the device reports, not of a time read from it.
		view.Reason = "the device reports the delivered policy as expired; outgoing use requires a fresh authenticated policy from the NAS"
	}
	view.MatchesGateway = state.hasSigned && device.Installed && !device.Damaged &&
		device.Generation == state.signed.Generation && device.State == state.signed.State
	view.HardwareReported = f.hardwareHash(device.Hardware) != ""
	if !view.HardwareReported {
		view.HardwareReason = firstNonEmpty(device.Hardware.Reason, "the device reported no usable hardware identifier")
	}
	return view
}
