package communication

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
	"github.com/human-agent65535/modemdeck/internal/messageevents"
	"github.com/human-agent65535/modemdeck/internal/store"
)

const testNodeEndpoint = "nsh:NSH-410-0A0B0C:0"

type recordedNodeAction struct {
	line, callID, action, requestID string
}

// fakeNodeCaller is a Node fleet that also takes call commands.
type fakeNodeCaller struct {
	*fakeNodeSource

	callMu      sync.Mutex
	dials       []agentclient.StartCallRequest
	dialReceipt agentclient.CommandReceipt
	dialErr     error
	onDial      func(*fakeNodeSource)
	actions     []recordedNodeAction
	actionErr   error
	onAction    func(*fakeNodeSource, string)
	refreshes   []string
	refreshErr  error
}

func (f *fakeNodeCaller) StartCall(_ context.Context, request agentclient.StartCallRequest) (agentclient.CommandReceipt, error) {
	f.callMu.Lock()
	f.dials = append(f.dials, request)
	receipt, err, hook := f.dialReceipt, f.dialErr, f.onDial
	f.callMu.Unlock()
	if hook != nil {
		f.fakeNodeSource.mu.Lock()
		hook(f.fakeNodeSource)
		f.fakeNodeSource.mu.Unlock()
	}
	return receipt, err
}

func (f *fakeNodeCaller) CallAction(
	_ context.Context,
	line, callID, action string,
	request agentclient.CallActionRequest,
) (agentclient.CommandReceipt, error) {
	f.callMu.Lock()
	f.actions = append(f.actions, recordedNodeAction{line, callID, action, request.RequestID})
	err, hook := f.actionErr, f.onAction
	f.callMu.Unlock()
	if hook != nil {
		f.fakeNodeSource.mu.Lock()
		hook(f.fakeNodeSource, action)
		f.fakeNodeSource.mu.Unlock()
	}
	if err != nil {
		return agentclient.CommandReceipt{}, err
	}
	return agentclient.CommandReceipt{RequestID: request.RequestID, ResourceID: callID}, nil
}

func (f *fakeNodeCaller) RefreshEndpoint(_ context.Context, line string) error {
	f.callMu.Lock()
	defer f.callMu.Unlock()
	f.refreshes = append(f.refreshes, line)
	return f.refreshErr
}

func (f *fakeNodeCaller) recorded() ([]agentclient.StartCallRequest, []recordedNodeAction, []string) {
	f.callMu.Lock()
	defer f.callMu.Unlock()
	return append([]agentclient.StartCallRequest(nil), f.dials...),
		append([]recordedNodeAction(nil), f.actions...),
		append([]string(nil), f.refreshes...)
}

// callableNodes is a paired Node whose line advertises dialling - the state
// the NAS will only reach once voice control and audio are verified.
func callableNodes(now time.Time) *fakeNodeCaller {
	nodes := onlineNodes(now)
	nodes.lines[0].Capabilities.Dial = true
	nodes.lines[0].Capabilities.AnswerCall = true
	nodes.lines[0].Capabilities.HangupCall = true
	nodes.lines[0].Capabilities.RejectCall = true
	return &fakeNodeCaller{fakeNodeSource: nodes}
}

func nodeCall(id, state string) agentclient.Call {
	return agentclient.Call{
		ID: id, LineID: testNodeEndpoint, Number: "10000",
		Direction: "outgoing", State: state, Bearer: "volte",
	}
}

// newNodeCallService builds a service with a host modem (line-1) and one Node,
// refreshed once so the Node line is live.
func newNodeCallService(t *testing.T, nodes NodeSource) (*Service, *controlLeaseEventAgent, *fakeRepository, time.Time) {
	t.Helper()
	now := time.Date(2026, time.September, 29, 9, 0, 0, 0, time.UTC)
	base := connectedAgent(now)
	base.health.Provider.Capabilities.ControlLease = true
	agent := &controlLeaseEventAgent{fakeAgent: base, released: make(chan struct{})}
	repository := &fakeRepository{}
	service, err := New(agent, repository, messageevents.NewBuffer(8))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	if err := service.SetNodeSource(nodes); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return service, agent, repository, now
}

func TestStartCallOnNodeLineDialsOnTheNodeAndCommitsItsState(t *testing.T) {
	t.Parallel()
	nodes := callableNodes(time.Time{})
	nodes.dialReceipt = agentclient.CommandReceipt{RequestID: "request-node-dial", ResourceID: "mm-call-1"}
	nodes.onDial = func(n *fakeNodeSource) { n.calls = []agentclient.Call{nodeCall("mm-call-1", "dialing")} }
	service, agent, repository, _ := newNodeCallService(t, nodes)

	call, err := service.StartCall(context.Background(), StartCallInput{
		RequestID: "request-node-dial",
		LineID:    testNodeEndpoint,
		Number:    "10000",
	})
	if err != nil {
		t.Fatalf("StartCall() error = %v", err)
	}
	dials, actions, refreshes := nodes.recorded()
	if len(dials) != 1 || dials[0].LineID != testNodeEndpoint || dials[0].RequestID != "request-node-dial" || dials[0].Number != "10000" {
		t.Fatalf("node dials = %+v", dials)
	}
	if len(agent.startRequests) != 0 {
		t.Fatalf("host agent must not dial a Node line: %+v", agent.startRequests)
	}
	if got := agent.renewals.Load(); got != 0 {
		t.Fatalf("host control lease taken for a Node call: %d renewals", got)
	}
	if len(actions) != 0 {
		t.Fatalf("a successful dial must not hang up: %+v", actions)
	}
	if len(refreshes) == 0 || refreshes[0] != testNodeEndpoint {
		t.Fatalf("the Node must be polled before the outcome is committed: %v", refreshes)
	}
	wantID := stableInstanceID("call", testNodeEndpoint, "mm-call-1")
	if call.ID != wantID || repository.callInput.RequestID != "request-node-dial" ||
		repository.callInput.EndpointLineID != testNodeEndpoint {
		t.Fatalf("stored call = %+v, input = %+v", call, repository.callInput)
	}
	command := repository.commands["request-node-dial"]
	if command.Status != store.HardwareCommandCompleted || command.ResourceID != wantID {
		t.Fatalf("command = %+v", command)
	}

	// The same request replays the stored call and reaches no device.
	again, err := service.StartCall(context.Background(), StartCallInput{
		RequestID: "request-node-dial", LineID: testNodeEndpoint, Number: "10000",
	})
	if err != nil || again.ID != wantID {
		t.Fatalf("replay = %+v, %v", again, err)
	}
	if dials, _, _ := nodes.recorded(); len(dials) != 1 {
		t.Fatalf("replay dialled again: %+v", dials)
	}
}

func TestStartCallOnNodeLineRefusedByTheNodeLeavesNothingInFlight(t *testing.T) {
	t.Parallel()
	nodes := callableNodes(time.Time{})
	nodes.dialErr = &agentclient.OperationError{Code: "failed_precondition", Message: "IMS is not registered"}
	service, agent, repository, _ := newNodeCallService(t, nodes)

	_, err := service.StartCall(context.Background(), StartCallInput{
		RequestID: "request-node-refused", LineID: testNodeEndpoint, Number: "10000",
	})
	if !errors.Is(err, ErrFailedPrecondition) {
		t.Fatalf("StartCall() error = %v, want failed precondition", err)
	}
	_, actions, _ := nodes.recorded()
	if len(actions) != 0 || agent.actionName != "" {
		t.Fatalf("a definite refusal must not trigger a hangup: node=%+v host=%q", actions, agent.actionName)
	}
	if command := repository.commands["request-node-refused"]; command.Status != store.HardwareCommandFailed ||
		command.ErrorCode != "failed_precondition" {
		t.Fatalf("command = %+v", command)
	}
}

func TestStartCallOnNodeLineHangsUpOnTheNodeWhenTheCallCannotBeObserved(t *testing.T) {
	t.Parallel()
	nodes := callableNodes(time.Time{})
	nodes.dialReceipt = agentclient.CommandReceipt{RequestID: "request-node-unobserved", ResourceID: "mm-call-7"}
	// The Node accepts but its call list never shows the call.
	service, agent, repository, _ := newNodeCallService(t, nodes)

	_, err := service.StartCall(context.Background(), StartCallInput{
		RequestID: "request-node-unobserved", LineID: testNodeEndpoint, Number: "10000",
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("StartCall() error = %v, want unavailable", err)
	}
	_, actions, _ := nodes.recorded()
	if len(actions) != 1 || actions[0].action != "hangup" || actions[0].callID != "mm-call-7" ||
		actions[0].line != testNodeEndpoint {
		t.Fatalf("cleanup must hang up the accepted call on the Node: %+v", actions)
	}
	if agent.actionName != "" {
		t.Fatalf("cleanup went to the host agent: %q %q", agent.actionName, agent.actionCallID)
	}
	if repository.callInput.AppID != "" {
		t.Fatalf("invented call state was persisted: %+v", repository.callInput)
	}
	if command := repository.commands["request-node-unobserved"]; command.Status != store.HardwareCommandIndeterminate {
		t.Fatalf("command status = %q, want indeterminate", command.Status)
	}
}

func TestStartCallOnNodeLineTransportFailureIsReconciledNotRetried(t *testing.T) {
	t.Parallel()
	nodes := callableNodes(time.Time{})
	nodes.dialErr = errors.New("node start_call result unknown: connection reset")
	service, agent, repository, _ := newNodeCallService(t, nodes)

	_, err := service.StartCall(context.Background(), StartCallInput{
		RequestID: "request-node-reset", LineID: testNodeEndpoint, Number: "10000",
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("StartCall() error = %v, want unavailable", err)
	}
	dials, _, refreshes := nodes.recorded()
	if len(dials) != 1 {
		t.Fatalf("an indeterminate dial must not be retried: %+v", dials)
	}
	if len(refreshes) == 0 {
		t.Fatal("an indeterminate dial must be reconciled against a fresh Node poll")
	}
	if len(agent.startRequests) != 0 {
		t.Fatalf("host agent dialled: %+v", agent.startRequests)
	}
	if command := repository.commands["request-node-reset"]; command.Status != store.HardwareCommandIndeterminate {
		t.Fatalf("command status = %q, want indeterminate", command.Status)
	}
}

func nodeCallTarget(phase string) store.CallControlTarget {
	return store.CallControlTarget{
		AppID:          stableInstanceID("call", testNodeEndpoint, "mm-call-3"),
		LineID:         testNodeEndpoint,
		EndpointLineID: testNodeEndpoint,
		EndpointCallID: "mm-call-3",
		Number:         "10000",
		Direction:      "incoming",
		Phase:          phase,
		Bearer:         "volte",
		Revision:       2,
	}
}

func TestCallActionOnNodeCallGoesToTheNode(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"answer", "hangup", "reject"} {
		t.Run(action, func(t *testing.T) {
			nodes := callableNodes(time.Time{})
			service, agent, repository, _ := newNodeCallService(t, nodes)
			repository.target = nodeCallTarget("ringing")
			repository.call = store.Call{ID: repository.target.AppID, LineID: testNodeEndpoint,
				EndpointLineID: testNodeEndpoint, EndpointCallID: "mm-call-3", Phase: "ringing", Direction: "incoming"}

			_, err := service.CallAction(context.Background(), CallActionInput{
				RequestID: "request-node-" + action, CallID: repository.target.AppID, Action: action,
			})
			if err != nil {
				t.Fatalf("CallAction(%s) error = %v", action, err)
			}
			_, actions, refreshes := nodes.recorded()
			if len(actions) != 1 || actions[0].action != action || actions[0].callID != "mm-call-3" ||
				actions[0].requestID != "request-node-"+action {
				t.Fatalf("node actions = %+v", actions)
			}
			if agent.actionName != "" || len(agent.actionRequests) != 0 {
				t.Fatalf("host agent received a Node call action: %q", agent.actionName)
			}
			if got := agent.renewals.Load(); got != 0 {
				t.Fatalf("host control lease taken for a Node call: %d", got)
			}
			if len(refreshes) == 0 {
				t.Fatal("the Node must be polled after the action")
			}
		})
	}
}

func TestDTMFOnNodeCallIsRefusedAsNotSupported(t *testing.T) {
	t.Parallel()
	nodes := callableNodes(time.Time{})
	service, agent, repository, _ := newNodeCallService(t, nodes)
	repository.target = nodeCallTarget("active")

	_, err := service.CallAction(context.Background(), CallActionInput{
		RequestID: "request-node-dtmf", CallID: repository.target.AppID, Action: "dtmf", Digits: "1",
	})
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("DTMF error = %v, want not supported", err)
	}
	_, actions, _ := nodes.recorded()
	if len(actions) != 0 || agent.dtmfCallID != "" {
		t.Fatalf("DTMF reached a device: node=%+v host=%q", actions, agent.dtmfCallID)
	}
	if command := repository.commands["request-node-dtmf"]; command.Status != store.HardwareCommandFailed {
		t.Fatalf("command = %+v", command)
	}
}

func TestNodeCallsWithoutCallControlAreNotSentToTheHost(t *testing.T) {
	t.Parallel()
	nodes := onlineNodes(time.Time{}) // a fleet with no call control
	nodes.lines[0].Capabilities.Dial = true
	service, agent, repository, _ := newNodeCallService(t, nodes)

	_, err := service.StartCall(context.Background(), StartCallInput{
		RequestID: "request-node-nocontrol", LineID: testNodeEndpoint, Number: "10000",
	})
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("StartCall() error = %v, want not supported", err)
	}
	if len(agent.startRequests) != 0 {
		t.Fatalf("host agent dialled a Node line: %+v", agent.startRequests)
	}

	repository.target = nodeCallTarget("active")
	_, err = service.CallAction(context.Background(), CallActionInput{
		RequestID: "request-node-nocontrol-hangup", CallID: repository.target.AppID, Action: "hangup",
	})
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("CallAction() error = %v, want not supported", err)
	}
	if agent.actionName != "" {
		t.Fatalf("host agent received a Node hangup: %q", agent.actionName)
	}
}

func TestNodeLineWithoutDialCapabilityCannotBeDialled(t *testing.T) {
	t.Parallel()
	nodes := &fakeNodeCaller{fakeNodeSource: onlineNodes(time.Time{})} // Dial not advertised
	service, _, _, _ := newNodeCallService(t, nodes)

	_, err := service.StartCall(context.Background(), StartCallInput{
		RequestID: "request-node-nodial", LineID: testNodeEndpoint, Number: "10000",
	})
	if err == nil {
		t.Fatal("a Node line that does not advertise dialling must not be dialled")
	}
	if dials, _, _ := nodes.recorded(); len(dials) != 0 {
		t.Fatalf("dial reached the Node: %+v", dials)
	}
}
