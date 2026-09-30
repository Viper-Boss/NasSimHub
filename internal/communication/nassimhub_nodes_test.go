package communication

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
	"github.com/human-agent65535/modemdeck/internal/messageevents"
	"github.com/human-agent65535/modemdeck/internal/store"
)

type fakeNodeSource struct {
	mu       sync.Mutex
	lines    []agentclient.Line
	messages []agentclient.Message
	calls    []agentclient.Call
	revision string
	nodes    int
	sent     []agentclient.SendMessageRequest
	receipt  agentclient.CommandReceipt
	sendErr  error
}

func (f *fakeNodeSource) NodeSnapshot() ([]agentclient.Line, []agentclient.Message, []agentclient.Call, string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agentclient.Line(nil), f.lines...), append([]agentclient.Message(nil), f.messages...), append([]agentclient.Call(nil), f.calls...), f.revision, f.nodes
}

func (f *fakeNodeSource) OwnsEndpoint(endpointID string) bool {
	return strings.HasPrefix(endpointID, "nsh:")
}

func (f *fakeNodeSource) OwnsMessageID(messageID string) bool {
	return strings.HasPrefix(messageID, "nsh-in:") || strings.HasPrefix(messageID, "nsh-out:")
}

func (f *fakeNodeSource) SendMessage(_ context.Context, request agentclient.SendMessageRequest) (agentclient.CommandReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, request)
	return f.receipt, f.sendErr
}

func nodeLine() agentclient.Line {
	return agentclient.Line{
		ID:                  "nsh:NSH-410-0A0B0C:0",
		Model:               "UFI003",
		DeviceIdentifier:    "NSH-410-0A0B0C",
		EquipmentIdentifier: "NSH-410-0A0B0C",
		IdentityPersistent:  true,
		SIMPresent:          true,
		SIMIdentifier:       "89860000000000000001",
		IMSI:                "460010000000001",
		HomeCountryISO:      "CN",
		Capabilities:        agentclient.LineCapabilities{SendMessage: true, MessagingInterface: true},
	}
}

func onlineNodes(now time.Time) *fakeNodeSource {
	return &fakeNodeSource{
		lines: []agentclient.Line{nodeLine()},
		messages: []agentclient.Message{{
			ID:        "nsh-in:0123456789abcdef",
			LineID:    "nsh:NSH-410-0A0B0C:0",
			Number:    "10010",
			Text:      "节点短信",
			Direction: "incoming",
			State:     "received",
			Timestamp: now.Format(time.RFC3339),
		}},
		revision: "r1",
		nodes:    1,
	}
}

func TestNodeMergeLeavesHostSnapshotUntouchedWithoutNodes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 27, 8, 0, 0, 0, time.UTC)
	repository := &fakeRepository{}
	service, err := New(connectedAgent(now), repository, messageevents.NewBuffer(8))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetNodeSource(&fakeNodeSource{}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetNodeSource(&fakeNodeSource{}); err == nil {
		t.Fatal("a second node source must be refused")
	}
	if _, err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.snapshot.Lines) != 1 || repository.snapshot.Lines[0].ID != "line-1" {
		t.Fatalf("host lines changed without nodes: %+v", repository.snapshot.Lines)
	}
	if service.lastSnapshot.Revision != "snapshot-3" {
		t.Fatalf("host revision changed without nodes: %q", service.lastSnapshot.Revision)
	}
}

func TestNodeLinesAndMessagesJoinHostSnapshot(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 27, 8, 0, 0, 0, time.UTC)
	repository := &fakeRepository{}
	service, err := New(connectedAgent(now), repository, messageevents.NewBuffer(8))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetNodeSource(onlineNodes(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	endpoints := map[string]bool{}
	for _, line := range repository.snapshot.Lines {
		endpoints[line.ID] = true
	}
	if !endpoints["line-1"] || !endpoints["nsh:NSH-410-0A0B0C:0"] {
		t.Fatalf("merged snapshot lines = %+v", repository.snapshot.Lines)
	}
	if len(repository.snapshot.Messages) != 1 || repository.snapshot.Messages[0].EndpointMessageID != "nsh-in:0123456789abcdef" {
		t.Fatalf("node message not committed: %+v", repository.snapshot.Messages)
	}
	if !strings.HasSuffix(service.lastSnapshot.Revision, "+nsh:r1") {
		t.Fatalf("merged revision %q does not carry the node revision", service.lastSnapshot.Revision)
	}
}

func TestNodeCallMergesWithoutAdvertisingUnverifiedAudio(t *testing.T) {
	now := time.Date(2026, time.September, 27, 8, 0, 0, 0, time.UTC)
	service, err := New(connectedAgent(now), &fakeRepository{}, messageevents.NewBuffer(8))
	if err != nil {
		t.Fatal(err)
	}
	nodes := onlineNodes(now)
	nodes.calls = []agentclient.Call{{ID: "mm-4", LineID: nodeLine().ID, State: "active", Direction: "outgoing", Number: "10000"}}
	if err := service.SetNodeSource(nodes); err != nil {
		t.Fatal(err)
	}
	merged, ok := service.mergeNodeSnapshot(agentclient.Snapshot{Revision: "host"})
	if !ok || len(merged.Calls) != 1 || merged.Calls[0].ID != "mm-4" || merged.Calls[0].MediaAvailable {
		t.Fatalf("unsafe call merge: %+v", merged.Calls)
	}
}

func TestNodeMessagesAreNeverDeletedThroughHostAgent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 27, 8, 0, 0, 0, time.UTC)
	base := connectedAgent(now)
	base.snapshot.Messages = []agentclient.Message{{
		ID: "message_received", LineID: "line-1", Number: "+818012345678", Text: "host",
		Direction: "incoming", State: "received", StateCode: 3, Timestamp: now.Format(time.RFC3339),
	}}
	agent := &messageDeletingAgent{fakeAgent: base, deleted: make(chan string, 4)}
	service, err := New(agent, &fakeRepository{}, messageevents.NewBuffer(8))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetNodeSource(onlineNodes(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	deleted := []string{}
	timeout := time.After(300 * time.Millisecond)
collect:
	for {
		select {
		case id := <-agent.deleted:
			deleted = append(deleted, id)
		case <-timeout:
			break collect
		}
	}
	if len(deleted) != 1 || deleted[0] != "message_received" {
		t.Fatalf("host agent deletions = %v, want only the host message", deleted)
	}
}

func TestSendMessageOnNodeLineGoesToNodeOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 27, 8, 0, 0, 0, time.UTC)
	agent := connectedAgent(now)
	nodes := onlineNodes(now)
	nodes.receipt = agentclient.CommandReceipt{RequestID: "req-node-1", ResourceID: "nsh-out:req-node-1", DeliveryReportUnsupported: true}
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
	if _, err := service.SendMessage(context.Background(), SendMessageInput{
		RequestID: "req-node-1",
		LineID:    "nsh:NSH-410-0A0B0C:0",
		Number:    "10010",
		Text:      "CXLL",
	}); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if len(nodes.sent) != 1 || nodes.sent[0].LineID != "nsh:NSH-410-0A0B0C:0" || nodes.sent[0].RequestID != "req-node-1" {
		t.Fatalf("node send requests = %+v", nodes.sent)
	}
	if len(agent.messageRequests) != 0 {
		t.Fatalf("host agent must not see a node send: %+v", agent.messageRequests)
	}

	// A host line still goes to the host agent.
	agent.messageResult = agentclient.CommandReceipt{RequestID: "req-host-1", ResourceID: "host-1"}
	if _, err := service.SendMessage(context.Background(), SendMessageInput{
		RequestID: "req-host-1", LineID: "line-1", Number: "+818012345678", Text: "host",
	}); err != nil {
		t.Fatalf("host SendMessage() error = %v", err)
	}
	if len(agent.messageRequests) != 1 || len(nodes.sent) != 1 {
		t.Fatalf("host send misrouted: agent=%d node=%d", len(agent.messageRequests), len(nodes.sent))
	}
}

func TestNodesOnlyFallbackConditions(t *testing.T) {
	t.Parallel()
	absent := &os.PathError{Op: "dial", Path: "/run/modemdeck/agent.sock", Err: syscall.ENOENT}
	unavailable := agentclient.Health{APIVersion: agentclient.APIVersion, Provider: agentclient.ProviderHealth{Available: false}}
	available := agentclient.Health{APIVersion: agentclient.APIVersion, Provider: agentclient.ProviderHealth{Available: true, BootEpoch: "b"}}
	for _, test := range []struct {
		name      string
		err       error
		health    agentclient.Health
		hostLines bool
		want      bool
	}{
		{"host agent not installed", fmt.Errorf("health: %w", absent), agentclient.Health{}, false, true},
		{"host agent not installed after host lines", fmt.Errorf("health: %w", absent), agentclient.Health{}, true, true},
		{"no modem provider on a node-only box", nil, unavailable, false, true},
		{"modemmanager restarting on a host with modems", nil, unavailable, true, false},
		{"host agent timing out", context.DeadlineExceeded, agentclient.Health{}, false, false},
		{"host agent refusing during restart", errors.New("connection refused"), agentclient.Health{}, false, false},
		{"healthy host", nil, available, true, false},
	} {
		if _, got := nodeOnlyHealth(test.err, test.health, test.hostLines); got != test.want {
			t.Errorf("%s: nodes-only = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestNodesOnlyRefreshProjectsNodeLinesWithoutHostAgent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 27, 8, 0, 0, 0, time.UTC)
	agent := &fakeAgent{healthError: &os.PathError{Op: "dial", Path: "/run/modemdeck/agent.sock", Err: syscall.ENOENT}}
	repository := &fakeRepository{}
	service, err := New(agent, repository, messageevents.NewBuffer(8))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	if _, err := service.Refresh(context.Background()); err == nil {
		t.Fatal("without nodes a missing host agent must still be a refresh failure")
	}
	if err := service.SetNodeSource(onlineNodes(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background()); err != nil {
		t.Fatalf("nodes-only refresh error = %v", err)
	}
	if len(repository.snapshot.Lines) != 1 || repository.snapshot.Lines[0].ID != "nsh:NSH-410-0A0B0C:0" {
		t.Fatalf("nodes-only lines = %+v", repository.snapshot.Lines)
	}
	var _ store.HardwareSnapshot = repository.snapshot
}
