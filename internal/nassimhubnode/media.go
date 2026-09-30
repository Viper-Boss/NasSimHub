package nassimhubnode

import (
	"context"
	"errors"
	"io"

	"github.com/human-agent65535/nassimhub-node/proto"
)

// OpenMedia binds a media stream to an active call on the current SIM of a
// paired Node. The Node itself rechecks the call and enforces a single lease.
func (f *Fleet) OpenMedia(ctx context.Context, endpointID, callID string) (io.ReadWriteCloser, error) {
	deviceID, ok := deviceFromEndpoint(endpointID)
	if !ok {
		return nil, errors.New("invalid Node endpoint")
	}
	live := f.lookup(deviceID)
	if live == nil {
		return nil, ErrUnknownNode
	}
	live.mu.Lock()
	state := live.state
	simID := state.Status.SIM.ICCID
	allowed := state.Online && !state.Revoked && state.Compatible && state.StatusKnown &&
		state.CallsKnown && simID != ""
	if allowed {
		allowed = false
		for _, call := range state.Calls {
			if call.ID == callID && call.SIMID == simID && call.State == proto.CallActive {
				allowed = true
				break
			}
		}
	}
	live.mu.Unlock()
	if !allowed {
		return nil, errors.New("Node call is not active on the current SIM")
	}
	client, _ := live.handles()
	return client.OpenMedia(ctx, callID)
}
