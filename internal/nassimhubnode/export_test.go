package nassimhubnode

import (
	"context"
	"time"

	"github.com/human-agent65535/nassimhub-node/core/nodeclient"
)

// Test-only access for the external test package.

// ClientForTest returns the pinned client the fleet uses for a device, so an
// end-to-end test can call the Node directly - with the gateway's genuine
// session - around the gateway's own gate.
func (f *Fleet) ClientForTest(deviceID string) *nodeclient.Client {
	live := f.lookup(deviceID)
	if live == nil {
		return nil
	}
	client, _ := live.handles()
	return client
}

// SyncDevicePolicyForTest delivers the current policy now if one is due.
func (f *Fleet) SyncDevicePolicyForTest(ctx context.Context, deviceID string) {
	if live := f.lookup(deviceID); live != nil {
		f.syncDevicePolicy(ctx, live, false)
	}
}

// SetClockForTest replaces the fleet's clock.
func (f *Fleet) SetClockForTest(now func() time.Time) { f.now = now }

// Policy timing, for tests that move the clock.
const (
	PolicyValidityForTest     = policyValidity
	PolicyRefreshEveryForTest = policyRefreshEvery
)

// IdentityHashForTest is the identity hash the fleet reports to the trust
// engine for a device, so a test can feed the engine readings of its own
// (hours of healthy observation in one call) without them reading as a
// changed identity.
func (f *Fleet) IdentityHashForTest(deviceID string) string {
	live := f.lookup(deviceID)
	if live == nil {
		return ""
	}
	return f.identityHash(live)
}
