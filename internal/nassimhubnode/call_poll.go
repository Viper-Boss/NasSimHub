package nassimhubnode

import (
	"context"
	"sync"
	"time"
)

// Call transitions must not wait for SMS, Wi-Fi or resource collection.
// The existing paired client retains authentication and certificate pinning.
func (f *Fleet) refreshCalls(ctx context.Context, live *liveNode) error {
	live.callMu.Lock()
	defer live.callMu.Unlock()
	client, _ := live.handles()
	query, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	calls, err := client.ListCalls(query)
	live.mu.Lock()
	live.callRevision++
	if err == nil {
		live.state.Calls = calls
		live.state.CallsKnown = true
	} else {
		live.state.Calls = nil
		live.state.CallsKnown = false
	}
	live.mu.Unlock()
	return err
}
func (f *Fleet) runCalls(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.mu.Lock()
			nodes := make([]*liveNode, 0, len(f.nodes))
			for _, live := range f.nodes {
				nodes = append(nodes, live)
			}
			f.mu.Unlock()
			before := f.contributionRevision()
			var work sync.WaitGroup
			for _, live := range nodes {
				live.mu.Lock()
				eligible := live.state.Online && !live.state.Revoked && live.state.Compatible && live.state.Capabilities.VoiceControl
				live.mu.Unlock()
				if eligible {
					work.Add(1)
					go func(live *liveNode) { defer work.Done(); _ = f.refreshCalls(ctx, live) }(live)
				}
			}
			work.Wait()
			if f.contributionRevision() != before {
				f.notify()
			}
		}
	}
}
