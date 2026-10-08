package communication

import (
	"context"
	"github.com/human-agent65535/modemdeck/internal/agentclient"
	"github.com/human-agent65535/modemdeck/internal/store"
	"testing"
	"time"
)

type dtmfNodeCaller struct {
	*fakeNodeCaller
	tones              int
	line, call, digits string
}

func (f *dtmfNodeCaller) SendDTMF(_ context.Context, line, call string, request agentclient.DTMFRequest) (agentclient.CommandReceipt, error) {
	f.tones++
	f.line, f.call, f.digits = line, call, request.Digits
	return agentclient.CommandReceipt{RequestID: request.RequestID, ResourceID: call}, nil
}
func TestNodeDTMFUsesOwningNodeAndCommandJournal(t *testing.T) {
	nodes := &dtmfNodeCaller{fakeNodeCaller: callableNodes(time.Time{})}
	service, host, repository, _ := newNodeCallService(t, nodes)
	repository.target = nodeCallTarget("active")
	repository.call = store.Call{ID: repository.target.AppID, LineID: testNodeEndpoint, EndpointLineID: testNodeEndpoint, EndpointCallID: repository.target.EndpointCallID, Phase: "active", Direction: "outgoing"}
	input := CallActionInput{RequestID: "node-dtmf-once", CallID: repository.target.AppID, Action: "dtmf", Digits: "1#"}
	for i := 0; i < 2; i++ {
		if _, err := service.CallAction(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	if nodes.tones != 1 || nodes.line != testNodeEndpoint || nodes.call != repository.target.EndpointCallID || nodes.digits != "1#" {
		t.Fatalf("node target: %+v", nodes)
	}
	if host.dtmfCallID != "" {
		t.Fatal("Node DTMF reached host modem")
	}
}
