package communication

import (
	"context"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
)

// Call forwarding and call waiting, routed through the same line resolution
// every other agent call in this package uses.
//
// WHY THIS FILE EXISTS AT ALL. The API used to hand the agent client straight
// to the HTTP layer (`CallServices: agent` in cmd/modemdeck/main.go) instead of
// going through this service. That skipped the one step every other agent call
// performs, and it is not an optional step:
//
//	THE API AND THE AGENT DO NOT USE THE SAME LINE IDS. The API's line ID is
//	persistent and derived from the SIM's identity (line_3070...), because a
//	line has to survive being unplugged and moved to another port. The agent's
//	is derived from the hardware it currently sees
//	(line_xJUH7zFM8KQvQ2gdYLxuzwZ1). store.LineSummary.EndpointID is the
//	translation between them, and SIMStatus, USSD, connection profiles,
//	StartCall and outbound SMS all apply it.
//
// Call services did not. Every supplementary-service query therefore reached
// the agent under an ID it had never heard of, and was refused as "line was
// not found" -- five times, once per query. The agent's status call then
// aggregated those five failures into "the line did not answer any
// supplementary service query; check that it is registered on a network",
// which is what an operator saw: a confident, specific, entirely wrong claim
// about their carrier registration, on a line that was registered and
// answering AT commands perfectly.
//
// Measured on the gateway before the fix: querying the agent directly with the
// resolved endpoint ID returned the real state (unconditional off, busy
// forwarding active to a number, no-reply off, not-reachable off, waiting on)
// while the same query with the API's ID returned not-found for all five.
//
// So the fix is not a new mechanism, it is this file: use the resolution that
// already existed. Wiring the raw agent here again would reintroduce the bug
// silently, which is why there is a contract test naming this service.

type callServicesAgent interface {
	CallServicesStatus(context.Context, string) (agentclient.CallServices, error)
	ApplyCallForwarding(
		context.Context,
		string,
		agentclient.SetCallForwarding,
	) (agentclient.CallServices, error)
	ApplyCallWaiting(
		context.Context,
		string,
		agentclient.SetCallWaiting,
	) (agentclient.CallServices, error)
}

// resolveCallServices resolves the caller's line selector to an attached line
// and checks the agent can do this at all.
//
// Resolving BEFORE the call is also what turns "the network is not answering"
// into "this line is not attached": a selector naming a line with no modem
// behind it now fails here, with that reason, rather than reaching the agent
// and coming back as a network complaint.
func (s *Service) resolveCallServices(
	ctx context.Context,
	lineSelector string,
	operation string,
) (string, callServicesAgent, error) {
	line, err := s.resolveAttachedLine(ctx, lineSelector)
	if err != nil {
		return "", nil, err
	}
	agent, ok := s.agent.(callServicesAgent)
	if !ok {
		return "", nil, operationError(
			CodeNotSupported,
			operation,
			"host agent does not offer supplementary call services",
			nil,
		)
	}
	return line.EndpointID, agent, nil
}

// CallServicesStatus reads forwarding and call waiting from the network.
func (s *Service) CallServicesStatus(
	ctx context.Context,
	lineSelector string,
) (agentclient.CallServices, error) {
	const operation = "read call services"
	endpointID, agent, err := s.resolveCallServices(ctx, lineSelector, operation)
	if err != nil {
		return agentclient.CallServices{}, err
	}
	// No timeout of this package's own. Every read here is a live round trip
	// to the carrier, one query per forwarding condition, and the agent
	// already bounds each one; wrapping the set in a shorter deadline would
	// turn a slow-but-working network into a failure.
	services, err := agent.CallServicesStatus(normalizeContext(ctx), endpointID)
	if err != nil {
		return agentclient.CallServices{}, translateAgentError(operation, err)
	}
	return services, nil
}

// ApplyCallForwarding registers or erases a rule and returns what the network
// holds afterwards.
func (s *Service) ApplyCallForwarding(
	ctx context.Context,
	lineSelector string,
	request agentclient.SetCallForwarding,
) (agentclient.CallServices, error) {
	const operation = "set call forwarding"
	endpointID, agent, err := s.resolveCallServices(ctx, lineSelector, operation)
	if err != nil {
		return agentclient.CallServices{}, err
	}
	services, err := agent.ApplyCallForwarding(normalizeContext(ctx), endpointID, request)
	if err != nil {
		return agentclient.CallServices{}, translateAgentError(operation, err)
	}
	return services, nil
}

// ApplyCallWaiting turns call waiting on or off and returns what the network
// holds afterwards.
func (s *Service) ApplyCallWaiting(
	ctx context.Context,
	lineSelector string,
	request agentclient.SetCallWaiting,
) (agentclient.CallServices, error) {
	const operation = "set call waiting"
	endpointID, agent, err := s.resolveCallServices(ctx, lineSelector, operation)
	if err != nil {
		return agentclient.CallServices{}, err
	}
	services, err := agent.ApplyCallWaiting(normalizeContext(ctx), endpointID, request)
	if err != nil {
		return agentclient.CallServices{}, translateAgentError(operation, err)
	}
	return services, nil
}
