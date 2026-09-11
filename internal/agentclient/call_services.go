package agentclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Call forwarding and call waiting live in the operator's network, so nothing
// here is cached: every call is a round trip through the agent to the modem to
// the HLR. That is slow by nature, and the timeouts on this path are longer
// than the rest of the agent API for that reason.

// CallForwardReason names a forwarding condition. These strings cross the wire
// in both directions and match the agent's own names.
const (
	CallForwardUnconditional = "unconditional"
	CallForwardBusy          = "busy"
	CallForwardNoReply       = "no_reply"
	CallForwardUnreachable   = "unreachable"
)

// CallForwardingRule is one condition as the network reports it.
type CallForwardingRule struct {
	Reason string `json:"reason"`
	Active bool   `json:"active"`
	Number string `json:"number"`
	// Seconds is the ring delay before forwarding; "no_reply" only.
	Seconds int `json:"seconds,omitempty"`
	// Unreadable means the network refused to report this condition. Active is
	// meaningless when it is set.
	Unreadable bool `json:"unreadable,omitempty"`
}

// CallServices is the network's answer for one line.
type CallServices struct {
	LineID     string               `json:"line_id"`
	Forwarding []CallForwardingRule `json:"forwarding"`
	// WaitingActive is whether a second incoming call will be signalled;
	// WaitingKnown is false when the modem said nothing about it, which is not
	// the same as off.
	WaitingActive bool              `json:"waiting_active"`
	WaitingKnown  bool              `json:"waiting_known"`
	Errors        map[string]string `json:"errors,omitempty"`
	ObservedAt    string            `json:"observed_at"`
}

// SetCallForwarding changes one condition.
type SetCallForwarding struct {
	Reason  string `json:"reason"`
	Enabled bool   `json:"enabled"`
	Number  string `json:"number"`
	Seconds int    `json:"seconds"`
}

// SetCallWaiting turns call waiting on or off.
type SetCallWaiting struct {
	Enabled bool `json:"enabled"`
}

type callServicesEnvelope struct {
	Services CallServices `json:"call_services"`
}

// CallServicesStatus reads forwarding and call waiting from the network.
func (client *Client) CallServicesStatus(ctx context.Context, lineID string) (CallServices, error) {
	var payload callServicesEnvelope
	path := fmt.Sprintf("/v1/lines/%s/call-services", url.PathEscape(lineID))
	if err := client.doJSON(ctx, http.MethodGet, path, nil, http.StatusOK, &payload); err != nil {
		return CallServices{}, err
	}
	return payload.Services, nil
}

// ApplyCallForwarding registers or erases a rule and returns what the network
// holds afterwards -- not what was asked for. The distinction matters: a
// network can accept a registration and hold a normalised number, a rounded
// delay, or nothing at all.
func (client *Client) ApplyCallForwarding(
	ctx context.Context,
	lineID string,
	change SetCallForwarding,
) (CallServices, error) {
	var payload callServicesEnvelope
	path := fmt.Sprintf("/v1/lines/%s/call-services/forwarding", url.PathEscape(lineID))
	if err := client.doJSON(ctx, http.MethodPut, path, change, http.StatusOK, &payload); err != nil {
		return CallServices{}, err
	}
	return payload.Services, nil
}

// ApplyCallWaiting turns call waiting on or off and returns the state read
// back from the network.
func (client *Client) ApplyCallWaiting(
	ctx context.Context,
	lineID string,
	change SetCallWaiting,
) (CallServices, error) {
	var payload callServicesEnvelope
	path := fmt.Sprintf("/v1/lines/%s/call-services/waiting", url.PathEscape(lineID))
	if err := client.doJSON(ctx, http.MethodPut, path, change, http.StatusOK, &payload); err != nil {
		return CallServices{}, err
	}
	return payload.Services, nil
}
