package communication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
)

// NodeSource contributes NasSimHub Node lines and messages to the hardware
// snapshot and carries the commands addressed to them.
//
// Nodes are merged into every committed snapshot rather than committed on
// their own, so there is still exactly one authoritative projection and the
// existing message, notification, forwarding and line-identity paths handle
// Node traffic without a second implementation. The contribution is read from
// a cache the Node poller maintains; merging never performs network I/O.
type NodeSource interface {
	NodeSnapshot() (lines []agentclient.Line, messages []agentclient.Message, calls []agentclient.Call, revision string, nodes int)
	OwnsEndpoint(endpointLineID string) bool
	OwnsMessageID(messageID string) bool
	SendMessage(context.Context, agentclient.SendMessageRequest) (agentclient.CommandReceipt, error)
}

// nodeMessageTagger stamps node_id / sim_id on stored Node messages.
type nodeMessageTagger interface {
	TagNasSimHubMessages(context.Context) (int64, error)
}

// SetNodeSource attaches the Node fleet. It may be called once, before Run.
func (s *Service) SetNodeSource(source NodeSource) error {
	if source == nil {
		return operationError(CodeInvalidArgument, "configure nodes", "node source is required", nil)
	}
	s.coordinatorMu.Lock()
	defer s.coordinatorMu.Unlock()
	if s.nodes != nil {
		return operationError(CodeConflict, "configure nodes", "node source is already configured", nil)
	}
	s.nodes = source
	return nil
}

// mergeNodeSnapshot appends the Node contribution. With no paired Node the
// host snapshot is returned untouched, revision included, so a gateway that has
// never seen a Node behaves exactly as before.
func (s *Service) mergeNodeSnapshot(snapshot agentclient.Snapshot) (agentclient.Snapshot, bool) {
	if s.nodes == nil {
		return snapshot, false
	}
	lines, messages, calls, revision, count := s.nodes.NodeSnapshot()
	if count == 0 {
		return snapshot, false
	}
	merged := snapshot
	merged.Lines = append(append([]agentclient.Line(nil), snapshot.Lines...), lines...)
	merged.Messages = append(append([]agentclient.Message(nil), snapshot.Messages...), messages...)
	merged.Calls = append(append([]agentclient.Call(nil), snapshot.Calls...), calls...)
	merged.Revision = snapshot.Revision + "+nsh:" + revision
	return merged, true
}

func (s *Service) hasNodes() bool {
	if s.nodes == nil {
		return false
	}
	_, _, _, _, count := s.nodes.NodeSnapshot()
	return count > 0
}

// nodeOnlyHealth stands in for the host agent when the gateway has no host
// modem at all - ModemManager reports no provider, or the host agent is not
// installed - so a Node-only installation still projects its lines.
//
// It is deliberately NOT used when the host agent is merely failing (a
// timeout, a refused connection during a restart), nor when ModemManager is
// unavailable on a host that has had modem lines: that is ModemManager
// restarting, and committing a snapshot without the host lines would close
// calls that are still in progress and mark the host lines absent.
func nodeOnlyHealth(hostErr error, health agentclient.Health, hostLinesSeen bool) (agentclient.Health, bool) {
	if hostErr == nil {
		if health.Provider.Available || hostLinesSeen {
			return agentclient.Health{}, false
		}
	} else if !hostAgentAbsent(hostErr) {
		return agentclient.Health{}, false
	}
	return agentclient.Health{
		Status:       "ok",
		APIVersion:   agentclient.APIVersion,
		AgentVersion: health.AgentVersion,
		Provider: agentclient.ProviderHealth{
			Name:      "nassimhub-node",
			Available: true,
			BootEpoch: "nassimhub-nodes-only",
		},
	}, true
}

// lastSnapshotHasHostLines reports whether the last committed snapshot had a
// line that is not a Node's.
func (s *Service) lastSnapshotHasHostLines() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, line := range s.lastSnapshot.Lines {
		if !s.nodeOwnsEndpoint(line.ID) {
			return true
		}
	}
	return false
}

func hostAgentAbsent(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT)
}

func (s *Service) nodeOwnsEndpoint(endpointID string) bool {
	return s.nodes != nil && s.nodes.OwnsEndpoint(endpointID)
}

func (s *Service) nodeOwnsMessage(messageID string) bool {
	return s.nodes != nil && s.nodes.OwnsMessageID(strings.TrimSpace(messageID))
}

func (s *Service) tagNodeMessages(ctx context.Context) {
	tagger, ok := s.repository.(nodeMessageTagger)
	if !ok {
		return
	}
	if _, err := tagger.TagNasSimHubMessages(ctx); err != nil {
		s.loggerForDiagnostics().Warn("node message tagging failed", "component", "communication", "error", err)
	}
}

// nodeCallControl carries call commands to the Node that owns a line. It is
// optional: a NodeSource without it (or a gateway without Nodes) refuses Node
// call commands as not supported rather than sending them to the host agent.
type nodeCallControl interface {
	StartCall(context.Context, agentclient.StartCallRequest) (agentclient.CommandReceipt, error)
	CallAction(ctx context.Context, endpointLineID, callID, action string,
		request agentclient.CallActionRequest) (agentclient.CommandReceipt, error)
	// RefreshEndpoint polls the owning Node now, so a following snapshot
	// commit sees the call state a command just changed.
	RefreshEndpoint(ctx context.Context, endpointLineID string) error
}

func (s *Service) nodeCalls() (nodeCallControl, bool) {
	if s.nodes == nil {
		return nil, false
	}
	control, ok := s.nodes.(nodeCallControl)
	return control, ok
}

var errNodeCallsUnsupported = &agentclient.OperationError{
	Code:    "not_supported",
	Message: "call control is not available for NasSimHub Node lines",
}

// observeNodeCallLocked refreshes the Node that owns endpointLineID and then
// reads the host side exactly as refreshLocked does, returning the pieces for
// one commitSnapshotLocked call. The Node contribution is merged by the
// commit itself, so Node and host state still land in a single authoritative
// projection. Callers hold coordinatorMu.
func (s *Service) observeNodeCallLocked(
	ctx context.Context,
	endpointLineID string,
) (agentclient.Health, agentclient.Snapshot, error) {
	control, ok := s.nodeCalls()
	if !ok {
		return agentclient.Health{}, agentclient.Snapshot{}, errNodeCallsUnsupported
	}
	if err := control.RefreshEndpoint(ctx, endpointLineID); err != nil {
		return agentclient.Health{}, agentclient.Snapshot{}, fmt.Errorf("refresh node: %w", err)
	}
	health, err := s.agent.Health(ctx)
	if substitute, nodesOnly := nodeOnlyHealth(err, health, s.lastSnapshotHasHostLines()); nodesOnly {
		return substitute, agentclient.Snapshot{Revision: "nodes-only", ObservedAt: s.now().UTC()}, nil
	}
	if err != nil {
		return agentclient.Health{}, agentclient.Snapshot{}, fmt.Errorf("read host agent health: %w", err)
	}
	if health.APIVersion != agentclient.APIVersion {
		return agentclient.Health{}, agentclient.Snapshot{},
			fmt.Errorf("agent API %q does not match %q", health.APIVersion, agentclient.APIVersion)
	}
	if !health.Provider.Available {
		return agentclient.Health{}, agentclient.Snapshot{}, errors.New("ModemManager is unavailable")
	}
	if strings.TrimSpace(health.Provider.BootEpoch) == "" {
		return agentclient.Health{}, agentclient.Snapshot{}, errors.New("agent boot_epoch is missing")
	}
	snapshot, err := s.agent.Snapshot(ctx)
	if err != nil {
		return agentclient.Health{}, agentclient.Snapshot{}, fmt.Errorf("read host agent snapshot: %w", err)
	}
	if snapshot.ObservedAt.IsZero() {
		return agentclient.Health{}, agentclient.Snapshot{}, errors.New("snapshot observed_at is missing")
	}
	return health, snapshot, nil
}

// nodeCall finds a call in the Node contribution the last refresh cached.
func (s *Service) nodeCall(endpointLineID, callID string) (agentclient.Call, bool) {
	if s.nodes == nil {
		return agentclient.Call{}, false
	}
	_, _, calls, _, _ := s.nodes.NodeSnapshot()
	for _, call := range calls {
		if call.ID == callID && call.LineID == endpointLineID {
			return call, true
		}
	}
	return agentclient.Call{}, false
}

// refreshNodeBeforeReconcile polls the owning Node before a reconciling
// refreshLocked, which itself only reads the cached Node contribution.
func (s *Service) refreshNodeBeforeReconcile(ctx context.Context, endpointLineID string) error {
	if !s.nodeOwnsEndpoint(endpointLineID) {
		return nil
	}
	control, ok := s.nodeCalls()
	if !ok {
		return errNodeCallsUnsupported
	}
	return control.RefreshEndpoint(ctx, endpointLineID)
}

// endpointStartCall dials on whichever side owns the line.
func (s *Service) endpointStartCall(
	ctx context.Context,
	request agentclient.StartCallRequest,
) (agentclient.CommandReceipt, error) {
	if !s.nodeOwnsEndpoint(request.LineID) {
		return s.agent.StartCall(ctx, request)
	}
	control, ok := s.nodeCalls()
	if !ok {
		return agentclient.CommandReceipt{}, errNodeCallsUnsupported
	}
	return control.StartCall(ctx, request)
}

// endpointCallAction answers, rejects or hangs up on whichever side owns the
// line. Every call command - user actions and the service's own cleanup
// hangups - goes through here, so a Node call is never sent to the host agent.
func (s *Service) endpointCallAction(
	ctx context.Context,
	endpointLineID, endpointCallID, action string,
	request agentclient.CallActionRequest,
) (agentclient.CommandReceipt, error) {
	if !s.nodeOwnsEndpoint(endpointLineID) {
		return s.agent.CallAction(ctx, endpointCallID, action, request)
	}
	control, ok := s.nodeCalls()
	if !ok {
		return agentclient.CommandReceipt{}, errNodeCallsUnsupported
	}
	return control.CallAction(ctx, endpointLineID, endpointCallID, action, request)
}
