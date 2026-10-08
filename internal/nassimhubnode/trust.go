package nassimhubnode

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
	"github.com/human-agent65535/modemdeck/internal/trustengine"
)

// The fleet and the trust engine.
//
// The rules about how far a device may be used live in internal/trustengine.
// This file is the whole of the fleet's part: it tells the engine what each
// poll established, and it asks the engine before placing a call or sending a
// message through a device. A refusal is a DEFINITE failure of that one
// request on that one line - it is never retried and never sent through
// another modem instead.

// SetTrustEngine attaches the engine. Without one the fleet behaves as it did
// before trust records existed.
func (f *Fleet) SetTrustEngine(engine *trustengine.Engine) {
	f.mu.Lock()
	f.trust = engine
	f.mu.Unlock()
	if engine == nil {
		return
	}
	// When the engine's decision for a device changes, the device is told at
	// once rather than at its next poll. The engine calls this on a goroutine
	// of its own.
	engine.SetOnChange(func(subject string) {
		live := f.lookup(subject)
		if live == nil {
			return
		}
		live.mu.Lock()
		online := live.state.Online
		live.mu.Unlock()
		if !online {
			return // delivered with the session when the device is back
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*f.timeout)
		defer cancel()
		f.syncDevicePolicy(ctx, live, false)
	})
}

// Trust returns the attached engine, or nil.
func (f *Fleet) Trust() *trustengine.Engine { return f.trustEngine() }

func (f *Fleet) trustEngine() *trustengine.Engine {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.trust
}

// identityHash binds a trust record to this gateway and the device's pinned
// identity key.
func (f *Fleet) identityHash(live *liveNode) string {
	return trustengine.KeyedHash(f.identity.PublicKey, "identity", live.record.ID, string(live.record.PublicKey))
}

// GrandfatherTrust records the devices paired before trust records existed as
// trusted, once; see trustengine.Engine.Grandfather.
func (f *Fleet) GrandfatherTrust(ctx context.Context) error {
	engine := f.trustEngine()
	if engine == nil {
		return nil
	}
	f.mu.Lock()
	subjects := make(map[string]string, len(f.nodes))
	for id, live := range f.nodes {
		subjects[id] = f.identityHash(live)
	}
	f.mu.Unlock()
	return engine.Grandfather(ctx, subjects)
}

// observeTrust reports one completed poll. Elapsed time comes from the
// monotonic clock reading in time.Time, so changing the wall clock neither
// earns nor loses trust.
func (f *Fleet) observeTrust(ctx context.Context, live *liveNode, state nodeState) {
	engine := f.trustEngine()
	if engine == nil {
		return
	}
	now := time.Now()
	live.mu.Lock()
	elapsed := time.Duration(0)
	if !live.lastTrust.IsZero() {
		elapsed = now.Sub(live.lastTrust)
	}
	live.lastTrust = now
	live.mu.Unlock()
	reading := trustengine.Reading{
		SubjectID:    live.record.ID,
		IdentityHash: f.identityHash(live),
		Healthy:      state.Online && state.Compatible && !state.IdentityRefused && !state.Revoked,
		Elapsed:      elapsed,
	}
	if state.StatusKnown && state.Status.SIM.ICCID != "" {
		// A keyed one-way hash: the engine and its journal never hold the SIM
		// identifier itself.
		reading.SIMHash = trustengine.KeyedHash(f.identity.PublicKey, "sim", state.Status.SIM.ICCID)
	}
	// What the device says about its own enforcement, and the hardware
	// identifier it reports. Neither read can fail the poll: a device that
	// does not answer simply contributes nothing this time.
	deviceCtx, cancel := context.WithTimeout(ctx, 2*f.timeout)
	defer cancel()
	if state.Online {
		if hardware, answered := f.readDeviceTrust(deviceCtx, live, state); answered {
			// Keyed again with the Core key, like the SIM hash. An absent or
			// malformed value is an empty hash: not known now, not a change.
			reading.HardwareHash = f.hardwareHash(hardware)
		}
	}
	if err := engine.Observe(ctx, reading); err != nil && f.logger != nil {
		f.logger.Warn("trust observation was not recorded", "node_id", live.record.ID, "error", err)
	}
	if state.Online {
		// After the observation, so a change it caused is in the policy.
		f.syncDevicePolicy(deviceCtx, live, false)
	}
}

// requireTrust is the gate in front of an outgoing action through a device.
func (f *Fleet) requireTrust(ctx context.Context, deviceID string, action trustengine.Action, operation string) error {
	engine := f.trustEngine()
	if engine == nil {
		return nil
	}
	err := engine.Require(ctx, deviceID, action)
	var refusal *trustengine.ErrRefused
	if errors.As(err, &refusal) {
		message := "this device is in its observation period: it receives calls and messages, and outgoing use starts when the period ends"
		switch refusal.State {
		case trustengine.Restricted:
			message = "outgoing use of this device is restricted"
		case trustengine.Quarantine:
			message = "this device is quarantined; an administrator must review it"
		case trustengine.Unbound:
			message = "this device has no trust record yet"
		}
		if reason := strings.TrimSpace(refusal.Reason); reason != "" && refusal.State != trustengine.Observation {
			message += " (" + reason + ")"
		}
		return &agentclient.OperationError{Code: "permission_denied", Operation: operation, Message: message}
	}
	if err == nil {
		// The gateway allows it. Before it is forwarded the device is given
		// the current policy if it does not have it yet (a decision that
		// changed since the last poll); when it does, this makes no request.
		if live := f.lookup(deviceID); live != nil {
			f.syncDevicePolicy(ctx, live, false)
		}
	}
	return err
}

// TrustView is the trust state shown with a device.
type TrustView struct {
	// Known is false when no engine is attached or the device has no record;
	// the other fields are then empty rather than defaulted.
	Known            bool   `json:"known"`
	State            string `json:"state,omitempty"`
	EffectiveState   string `json:"effective_state,omitempty"`
	Reason           string `json:"reason,omitempty"`
	RemainingSeconds uint64 `json:"remaining_seconds,omitempty"`
	OutgoingAllowed  bool   `json:"outgoing_allowed"`
	// Enforced is false in monitor mode: the state is evaluated and shown,
	// and nothing is refused.
	Enforced bool `json:"enforced"`
	// Device is what the DEVICE reports about its own enforcement. Everything
	// above is the gateway's decision ("NAS 显示状态"); this is whether the
	// device holds and applies it ("设备已执行"). They are separate facts.
	Device DeviceEnforcementView `json:"device"`
}

func (f *Fleet) trustView(deviceID string) TrustView {
	engine := f.trustEngine()
	if engine == nil {
		return TrustView{Device: DeviceEnforcementView{Reason: "trust records are not available on this gateway"}}
	}
	device := DeviceEnforcementView{Reason: "the device is not paired with this gateway"}
	if live := f.lookup(deviceID); live != nil {
		device = f.deviceEnforcementView(live)
	}
	for _, subject := range engine.Subjects() {
		if subject.SubjectID != deviceID {
			continue
		}
		return TrustView{
			Known: true, State: string(subject.State), EffectiveState: string(subject.EffectiveState),
			Reason: firstNonEmpty(subject.EffectiveReason, subject.Reason), RemainingSeconds: subject.RemainingSeconds,
			OutgoingAllowed: subject.Allowed[trustengine.ActionDial] && subject.Allowed[trustengine.ActionSendSMS],
			Enforced:        engine.Enforcement() == trustengine.Enforce,
			Device:          device,
		}
	}
	return TrustView{Enforced: engine.Enforcement() == trustengine.Enforce, Device: device}
}
