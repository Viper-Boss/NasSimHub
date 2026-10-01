package nassimhubnode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
	"github.com/human-agent65535/nassimhub-node/proto"
)

// Call control for Node lines.
//
// The communication service owns the command journal, idempotency, receipt
// validation and the authoritative snapshot; these methods only carry one
// command to the Node that owns the line and translate the answer into the
// shapes the host-agent path already understands:
//
//   - a definite refusal by the Node becomes *agentclient.OperationError, so
//     the command is journalled as failed and nothing is left to clean up;
//   - anything else (timeout, dropped link, "result is uncertain") is returned
//     as a plain error, which the service treats as indeterminate and
//     reconciles against a fresh snapshot, hanging up if a call did start.
//
// None of this opens calling on its own: the service only reaches these
// methods for a line whose capabilities allow it, and Node lines advertise no
// call capability until voice control and voice audio are both verified.

// nodeCallIDPattern bounds call IDs before they are placed in a request path.
var nodeCallIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// StartCall asks the Node that owns request.LineID to dial.
func (f *Fleet) StartCall(ctx context.Context, request agentclient.StartCallRequest) (agentclient.CommandReceipt, error) {
	const operation = "start_call"
	live, err := f.callableNode(request.LineID, operation)
	if err != nil {
		return agentclient.CommandReceipt{}, err
	}
	number := strings.TrimSpace(request.Number)
	if number == "" {
		return agentclient.CommandReceipt{}, definite(operation, proto.ErrorInvalidArgument, "number is required")
	}
	nodeRequest := nodeCallRequestID(request.RequestID)
	client, _ := live.handles()
	receipt, err := client.Dial(ctx, proto.DialRequest{RequestID: nodeRequest, To: number})
	if err != nil {
		return agentclient.CommandReceipt{}, classifyCallError(operation, err)
	}
	if receipt.RequestID != nodeRequest {
		return agentclient.CommandReceipt{}, errors.New("node dial receipt does not echo the request")
	}
	if !nodeCallIDPattern.MatchString(receipt.CallID) {
		return agentclient.CommandReceipt{}, errors.New("node dial receipt has an invalid call id")
	}
	return agentclient.CommandReceipt{RequestID: request.RequestID, ResourceID: receipt.CallID}, nil
}

// CallAction answers, rejects or hangs up a call on the Node that owns the
// line. A Node has no separate reject: hanging up a ringing incoming call
// declines it.
func (f *Fleet) CallAction(
	ctx context.Context,
	endpointLineID, callID, action string,
	request agentclient.CallActionRequest,
) (agentclient.CommandReceipt, error) {
	operation := "call_" + action
	live, err := f.callableNode(endpointLineID, operation)
	if err != nil {
		return agentclient.CommandReceipt{}, err
	}
	callID = strings.TrimSpace(callID)
	if !nodeCallIDPattern.MatchString(callID) {
		return agentclient.CommandReceipt{}, definite(operation, proto.ErrorInvalidArgument, "call id is invalid")
	}
	client, _ := live.handles()
	var receipt proto.CallReceipt
	switch action {
	case "answer":
		receipt, err = client.Answer(ctx, callID)
	case "hangup", "reject":
		receipt, err = client.Hangup(ctx, callID)
	default:
		return agentclient.CommandReceipt{}, definite(operation, proto.ErrorNotSupported,
			fmt.Sprintf("%s is not supported on NasSimHub Node lines", action))
	}
	if err != nil {
		return agentclient.CommandReceipt{}, classifyCallError(operation, err)
	}
	if receipt.CallID != callID {
		return agentclient.CommandReceipt{}, errors.New("node call receipt does not match the controlled call")
	}
	return agentclient.CommandReceipt{RequestID: request.RequestID, ResourceID: callID}, nil
}

// RefreshEndpoint polls the Node that owns a line now, so the next snapshot
// commit reflects the call state a command just changed rather than the last
// periodic poll.
func (f *Fleet) RefreshEndpoint(ctx context.Context, endpointLineID string) error {
	deviceID, ok := deviceFromEndpoint(endpointLineID)
	if !ok {
		return errors.New("not a NasSimHub Node line")
	}
	live := f.lookup(deviceID)
	if live == nil {
		return ErrUnknownNode
	}
	return f.refreshCalls(ctx, live)
}

func (f *Fleet) callableNode(endpointLineID, operation string) (*liveNode, error) {
	deviceID, ok := deviceFromEndpoint(endpointLineID)
	if !ok {
		return nil, definite(operation, proto.ErrorNotFound, "the line does not belong to a NasSimHub Node")
	}
	live := f.lookup(deviceID)
	if live == nil {
		return nil, definite(operation, proto.ErrorNotFound, ErrUnknownNode.Error())
	}
	live.mu.Lock()
	state := live.state
	live.mu.Unlock()
	switch {
	case !state.Online || state.Revoked:
		return nil, definite(operation, proto.ErrorUnavailable, ErrNodeUnavailable.Error())
	case !state.Capabilities.VoiceControl:
		return nil, definite(operation, proto.ErrorNotSupported, "call control is not enabled on this Node")
	case !state.StatusKnown || state.Status.SIM.State != proto.SIMReady:
		return nil, definite(operation, proto.ErrorFailedPrecondition, ErrNodeNotReady.Error())
	}
	return live, nil
}

func definite(operation string, code proto.ErrorCode, message string) error {
	return &agentclient.OperationError{Code: string(code), Operation: operation, Message: message}
}

// classifyCallError separates what the Node refused (nothing happened) from
// what may have happened (the service must reconcile).
func classifyCallError(operation string, err error) error {
	var nodeErr *proto.OperationError
	if !errors.As(err, &nodeErr) {
		return fmt.Errorf("node %s result unknown: %w", operation, err)
	}
	switch nodeErr.Code {
	case proto.ErrorInvalidArgument, proto.ErrorFailedPrecondition, proto.ErrorNotFound,
		proto.ErrorNotSupported, proto.ErrorPermissionDenied, proto.ErrorNetworkRejected,
		proto.ErrorConflict:
		return &agentclient.OperationError{Code: string(nodeErr.Code), Operation: operation, Message: nodeErr.Message}
	case proto.ErrorUnavailable:
		if strings.Contains(strings.ToLower(nodeErr.Message), "uncertain") {
			return fmt.Errorf("node %s result unknown: %w", operation, err)
		}
		return &agentclient.OperationError{Code: "unavailable", Operation: operation, Message: nodeErr.Message}
	}
	return fmt.Errorf("node %s result unknown: %w", operation, err)
}

// nodeCallRequestID maps a gateway request ID onto the Node's request alphabet.
// It is deterministic, so a retried command reaches the Node's own
// idempotency record instead of placing a second call.
func nodeCallRequestID(requestID string) string {
	digest := sha256.Sum256([]byte("nas-call\x00" + requestID))
	return "nas-" + hex.EncodeToString(digest[:16])
}
