package nodeclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/human-agent65535/nassimhub-node/proto"
)

// Carrying the gateway's trust decision to a Node that enforces it.
//
// The Core signs a policy with its identity key (proto.SignTrustPolicy) and
// hands it over here. The session authenticates the conversation; the device
// accepts the policy because of the policy's own signature, checked against
// the key it pinned at pairing. See proto/trustpolicy.go.

// ErrTrustUnsupported reports a Node that predates device-local enforcement
// (protocol 1.4): it has no trust endpoints. It is a fact about the device,
// not a failure.
var ErrTrustUnsupported = errors.New("node does not enforce a trust policy")

func unsupportedTrust(err error) error {
	if operation, ok := proto.AsOperationError(err); ok {
		if operation.Code == proto.ErrorNotSupported ||
			(operation.Code == proto.ErrorNotFound && (operation.Operation == "route" || operation.Operation == "")) {
			return fmt.Errorf("%w: %s", ErrTrustUnsupported, operation.Message)
		}
	}
	return err
}

// PushTrustPolicy installs a signed policy on the Node and returns what the
// Node holds afterwards. A conflict means the Node already holds this or a
// newer generation; TrustStatus says which.
func (c *Client) PushTrustPolicy(ctx context.Context, signed proto.SignedTrustPolicy) (proto.TrustStatus, error) {
	var status proto.TrustStatus
	if err := c.call(ctx, http.MethodPost, proto.TrustPolicyPath, signed, &status); err != nil {
		return proto.TrustStatus{}, unsupportedTrust(err)
	}
	return status, nil
}

// TrustStatus reads what the Node holds and whether it is enforcing it.
func (c *Client) TrustStatus(ctx context.Context) (proto.TrustStatus, error) {
	var status proto.TrustStatus
	if err := c.call(ctx, http.MethodGet, proto.TrustStatusPath, nil, &status); err != nil {
		return proto.TrustStatus{}, unsupportedTrust(err)
	}
	return status, nil
}

// OnSession registers a function that runs each time this client obtains a
// new bearer session, after the session exists and BEFORE the request that
// needed it is sent. The gateway uses it to give the device a fresh policy
// first on every fresh session, so nothing outgoing is forwarded ahead of it.
//
// The hook may use this client. It is given no way to fail the request it
// precedes: whatever it could not do is its own to record.
func (c *Client) OnSession(hook func(context.Context)) {
	c.mu.Lock()
	c.onSession = hook
	c.mu.Unlock()
}

type sessionHookKey struct{}

func inSessionHook(ctx context.Context) bool { return ctx.Value(sessionHookKey{}) != nil }

// sessionEstablished runs the hook. It is called by ensureSession with the
// session lock held.
func (c *Client) sessionEstablished(ctx context.Context) {
	c.mu.Lock()
	hook := c.onSession
	c.mu.Unlock()
	// A request made BY the hook that itself had to renew the session must
	// not run the hook again.
	if hook == nil || inSessionHook(ctx) {
		return
	}
	hook(context.WithValue(ctx, sessionHookKey{}, true))
}
