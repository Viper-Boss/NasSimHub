package domain

import "context"

// Supplementary services: call forwarding and call waiting.
//
// These are the only settings in this product that this software does not own.
// Forwarding and call waiting live in the operator's network, against the
// subscriber, not in a file on this disk and not in the modem. Three
// consequences shape every type here:
//
// They can be changed from somewhere else. A handset with the same SIM, or the
// operator's app, or a *21# code dialled from anywhere, changes the same state.
// So this is always read from the network and never cached as configuration --
// a stored copy would be a guess that goes stale silently.
//
// They are slow and they can fail per-condition. A query is a round trip to
// the HLR, and a network may answer about unconditional forwarding while
// refusing to discuss the unreachable case. A partial answer is normal and has
// to be representable.
//
// And a change may not be applied as asked. The network is free to accept a
// registration and hold something slightly different. So a change is followed
// by a fresh read, and what the UI shows is what the network says afterwards,
// never what was requested.

// CallForwardingRule is one forwarding condition as the network reports it.
type CallForwardingRule struct {
	// Reason is "unconditional", "busy", "no_reply" or "unreachable".
	Reason string `json:"reason"`
	// Active is whether the network is forwarding for this condition.
	Active bool `json:"active"`
	// Number is the destination. It can be empty while Active is true: some
	// networks confirm a rule without echoing the number, and reporting
	// inactive in that case would be wrong in the dangerous direction.
	Number string `json:"number"`
	// Seconds is the ring delay before forwarding, for "no_reply" only.
	Seconds int `json:"seconds,omitempty"`
	// Unreadable marks a condition the network refused to report. The row is
	// still present so the list keeps its shape, but Active is meaningless and
	// the UI must say so rather than draw an off switch.
	Unreadable bool `json:"unreadable,omitempty"`
}

// CallServices is the network's answer for one line.
type CallServices struct {
	LineID string `json:"line_id"`
	// Forwarding holds one entry per condition, always in the same order.
	Forwarding []CallForwardingRule `json:"forwarding"`
	// WaitingActive is whether a second incoming call will be signalled.
	WaitingActive bool `json:"waiting_active"`
	// WaitingKnown is false when the modem said nothing about voice call
	// waiting. "Off" and "not reported" are different facts and collapsing
	// them tells the operator something untrue.
	WaitingKnown bool `json:"waiting_known"`
	// Errors maps a condition name (or "waiting") to why it could not be read.
	Errors map[string]string `json:"errors,omitempty"`
	// ObservedAt is when the network was asked. Displayed, because a
	// supplementary service read is slow enough that an operator needs to know
	// whether they are looking at a fresh answer.
	ObservedAt string `json:"observed_at"`
}

// SetCallForwardingRequest changes one condition.
type SetCallForwardingRequest struct {
	LineID string `json:"-"`
	// Reason is the condition name; an unrecognised one is refused rather than
	// defaulted, because defaulting to unconditional would divert every call.
	Reason string `json:"reason"`
	// Enabled false erases the rule at the network rather than merely
	// disabling it, so no destination is left registered behind an off switch.
	Enabled bool   `json:"enabled"`
	Number  string `json:"number"`
	// Seconds applies to "no_reply" only; 0 leaves the network's default.
	Seconds int `json:"seconds"`
}

// SetCallWaitingRequest turns call waiting on or off at the network.
type SetCallWaitingRequest struct {
	LineID  string `json:"-"`
	Enabled bool   `json:"enabled"`
}

// CallServiceProvider is implemented by a backend that can talk to the
// network's supplementary services.
//
// The setters return the state read back AFTER the change, not the state that
// was asked for. That is the whole reason they return anything: a network is
// free to accept a registration and hold something else, and a UI that echoed
// the request would show a rule that does not exist.
type CallServiceProvider interface {
	CallServicesStatus(context.Context, string) (CallServices, error)
	SetCallForwarding(context.Context, SetCallForwardingRequest) (CallServices, error)
	SetCallWaiting(context.Context, SetCallWaitingRequest) (CallServices, error)
}
