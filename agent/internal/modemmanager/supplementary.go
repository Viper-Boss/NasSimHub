package modemmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// Call forwarding and call waiting, over AT to the network.
//
// The timeout is the thing to get right here, and it is why these do not reuse
// commandAT. That helper waits modemCommandTimeoutSeconds (5), which is
// generous for a local query like AT+CLCC and far too short for a
// supplementary service: CCFC and CCWA are round trips to the operator's HLR,
// and 10 to 20 seconds is ordinary on a congested network. At 5 seconds a
// perfectly successful registration reports failure while the network goes on
// to apply it -- the worst outcome available, because the operator then either
// sets it twice or believes forwarding is off while every call is being
// diverted.

// supplementaryTimeoutSeconds is the ModemManager timeout for these commands.
//
// 30 seconds is chosen against the network rather than against the modem: the
// GSM spec allows the network its own time to answer, handsets wait about this
// long before giving up, and a query that needs longer than this has almost
// certainly failed rather than being slow.
const supplementaryTimeoutSeconds = uint32(30)

// commandSupplementary runs one AT command with the network-length timeout.
func (p *Provider) commandSupplementary(
	ctx context.Context,
	lineID string,
	operation string,
	command string,
) (string, error) {
	lineID = strings.TrimSpace(lineID)
	if lineID == "" {
		return "", domain.InvalidArgument(operation, "line id is required")
	}
	parsed, err := p.snapshotContent(ctx, operation)
	if err != nil {
		return "", err
	}
	line, found := findLine(parsed.Lines, lineID)
	if !found {
		return "", domain.NotFound(operation, "line was not found")
	}
	if !line.Capabilities.ModemInterface {
		return "", domain.NotSupported(operation, "line does not expose the ModemManager Modem interface")
	}
	path, found := parsed.LinePaths[line.ID]
	if !found || !path.IsValid() {
		return "", domain.NotFound(operation, "line does not have a routable ModemManager path")
	}
	body, err := p.call(
		ctx,
		path,
		modemInterface+".Command",
		operation,
		"the network did not answer the supplementary service request",
		command,
		supplementaryTimeoutSeconds,
	)
	if err != nil {
		return "", err
	}
	var response string
	if err := dbus.Store(body, &response); err != nil {
		return "", domain.Internal(operation, "the modem's response was malformed", err)
	}
	return strings.TrimSpace(response), nil
}

// CallServicesStatus reads forwarding and call waiting from the network.
//
// Every condition is queried separately because that is what the standard
// offers -- there is no "read all forwarding" query that returns the
// conditions apart. A failure on one condition does not fail the others: a
// network that refuses CFNRc still has useful things to say about CFU, and an
// operator staring at an error where four rows should be cannot tell which.
func (p *Provider) CallServicesStatus(ctx context.Context, lineID string) (domain.CallServices, error) {
	const operation = "read_call_services"
	services := domain.CallServices{
		LineID:     strings.TrimSpace(lineID),
		ObservedAt: p.now().UTC().Format(time.RFC3339),
	}
	// Counted rather than re-derived from the stringified messages in
	// services.Errors, which keep the text but lose the code.
	unknownLine := 0
	var lastFailure error
	note := func(err error) {
		lastFailure = err
		var operationError *domain.OperationError
		if errors.As(err, &operationError) && operationError.Code == domain.ErrorNotFound {
			unknownLine++
		}
	}

	for _, reason := range readableForwardReasons {
		command := fmt.Sprintf("AT+CCFC=%d,2", int(reason))
		response, err := p.commandSupplementary(ctx, lineID, operation, command)
		if err != nil {
			if services.Errors == nil {
				services.Errors = map[string]string{}
			}
			services.Errors[reason.String()] = err.Error()
			note(err)
			// The row still appears, marked unreadable, so the list keeps its
			// shape and the UI shows a known-unknown rather than a gap or an
			// off switch it has no business drawing.
			services.Forwarding = append(services.Forwarding, domain.CallForwardingRule{
				Reason:     reason.String(),
				Unreadable: true,
			})
			continue
		}
		rule := parseCCFC(reason, response)
		services.Forwarding = append(services.Forwarding, domain.CallForwardingRule{
			Reason:  rule.Reason.String(),
			Active:  rule.Active,
			Number:  rule.Number,
			Seconds: rule.Seconds,
		})
	}

	// The first parameter enables the unsolicited +CCWA notification for this
	// session as a side effect, which is wanted: without it the modem accepts
	// a second call without telling anybody.
	waiting, err := p.commandSupplementary(ctx, lineID, operation, fmt.Sprintf("AT+CCWA=1,2,%d", voiceClass))
	if err != nil {
		if services.Errors == nil {
			services.Errors = map[string]string{}
		}
		services.Errors["waiting"] = err.Error()
		note(err)
	} else {
		services.WaitingActive, services.WaitingKnown = parseCCWA(waiting)
	}

	// An error on every single query means the line is not reachable at all,
	// not that four services happen to be broken. Saying so once is more
	// useful than four copies of the same message.
	//
	// WHAT IT BLAMES MATTERS. This used to say the line was probably not
	// registered on a network, which is one explanation among several and was
	// the wrong one in the case that actually shipped: the caller was passing
	// a line ID this agent does not use, so every query was refused with "line
	// was not found" before a single AT command was sent. An operator chasing
	// a carrier registration problem on a line that was registered, answering
	// AT, and showing full signal has been sent somewhere with nothing to
	// find. So the aggregate now separates the two: a line this agent cannot
	// identify is reported as exactly that, and only a line it CAN identify
	// gets the network explanation.
	if len(services.Errors) > len(readableForwardReasons) {
		// Every failure was "this agent does not know that line". That is a
		// caller problem, not a radio problem, and it is worth its own error
		// rather than an extra probe: the information is already in hand.
		if unknownLine == len(services.Errors) && lastFailure != nil {
			return services, lastFailure
		}
		return services, domain.Unavailable(
			operation,
			"the line did not answer any supplementary service query; it is reachable, so check that it is registered on a network and that the subscription includes these services",
			nil,
		)
	}
	return services, nil
}

// SetCallForwarding registers or erases a forwarding rule, then reports what
// the network holds afterwards.
//
// The read-back is not politeness. A network may accept a registration and
// hold something different -- a normalised number, a rounded delay, or nothing
// at all if the subscription does not include the service. Echoing the request
// back would show the operator a rule that does not exist, and forwarding is
// the one setting where believing a wrong answer means missing every call.
func (p *Provider) SetCallForwarding(
	ctx context.Context,
	request domain.SetCallForwardingRequest,
) (domain.CallServices, error) {
	const operation = "set_call_forwarding"
	reason, err := ParseForwardReason(request.Reason)
	if err != nil {
		return domain.CallServices{}, domain.InvalidArgument(operation, err.Error())
	}
	if err := validNoReplyDelay(request.Seconds); err != nil {
		return domain.CallServices{}, domain.InvalidArgument(operation, err.Error())
	}
	command, err := forwardingCommand(reason, request.Enabled, request.Number, request.Seconds)
	if err != nil {
		return domain.CallServices{}, domain.InvalidArgument(operation, err.Error())
	}
	if _, err := p.commandSupplementary(ctx, request.LineID, operation, command); err != nil {
		return domain.CallServices{}, err
	}
	return p.CallServicesStatus(ctx, request.LineID)
}

// SetCallWaiting turns call waiting on or off at the network, then reports
// what the network holds afterwards. Same reasoning as SetCallForwarding.
func (p *Provider) SetCallWaiting(
	ctx context.Context,
	request domain.SetCallWaitingRequest,
) (domain.CallServices, error) {
	const operation = "set_call_waiting"
	mode := 0
	if request.Enabled {
		mode = 1
	}
	// The leading 1 keeps the unsolicited +CCWA notification enabled whichever
	// way the service itself is being set, so a later incoming second call is
	// still announced.
	command := fmt.Sprintf("AT+CCWA=1,%d,%d", mode, voiceClass)
	if _, err := p.commandSupplementary(ctx, request.LineID, operation, command); err != nil {
		return domain.CallServices{}, err
	}
	return p.CallServicesStatus(ctx, request.LineID)
}
