package nassimhubnode_test

// End-to-end tests of the trust gate against a real Node process (modem
// simulated). They show that a refusal is a definite failure of the request,
// that nothing reaches the Node, and that receiving keeps working.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/modemdeck/internal/trustengine"
	"github.com/human-agent65535/nassimhub-node/proto"
)

func attachTrust(t *testing.T, ctx context.Context, g gateway, dataDir string, mode trustengine.Enforcement, now func() time.Time) *trustengine.Engine {
	t.Helper()
	identity, err := nassimhubnode.LoadOrCreateIdentity(filepath.Join(dataDir, "nassimhub"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := trustengine.NewSQLStore(g.db)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := trustengine.New(ctx, trustengine.Options{Store: store, Key: identity.PrivateKey, Enforcement: mode, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	g.fleet.SetTrustEngine(engine)
	return engine
}

func operationCode(err error) string {
	var operation *agentclient.OperationError
	if errors.As(err, &operation) {
		return operation.Code
	}
	return ""
}

func TestE2ETrustGateRefusesOutgoingUseOfANewDeviceAndKeepsReceiving(t *testing.T) {
	binary := os.Getenv("NSH_E2E_NODE_BIN")
	if binary == "" {
		t.Skip("set NSH_E2E_NODE_BIN to a mock-nassimhub-node binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	usb, control := freePort(t), freePort(t)
	node := startNode(t, binary, filepath.Join(root, "node"), usb, control)
	dataDir := filepath.Join(root, "nas")
	g := openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelStandard)
	engine := attachTrust(t, ctx, g, dataDir, trustengine.Enforce, nil)
	paired := pairFirst(t, ctx, g)
	if _, err := g.fleet.Refresh(ctx, paired.DeviceID); err != nil {
		t.Fatal(err)
	}
	view := onlyNode(t, g)
	if !view.Trust.Known || view.Trust.State != "OBSERVATION" || view.Trust.OutgoingAllowed || !view.Trust.Enforced || view.Trust.RemainingSeconds == 0 {
		t.Fatalf("a newly adopted device is under observation: %+v", view.Trust)
	}

	_, err := g.fleet.SendMessage(ctx, agentclient.SendMessageRequest{RequestID: "trust-1", LineID: view.LineEndpointID, Number: "10010", Text: "hello"})
	if operationCode(err) != "permission_denied" || !strings.Contains(err.Error(), "observation") {
		t.Fatalf("sending through a device under observation must be a definite refusal: %v", err)
	}
	// Nothing reached the Node: after a refresh it holds no outgoing message.
	if _, err := g.fleet.Refresh(ctx, paired.DeviceID); err != nil {
		t.Fatal(err)
	}
	if got := onlyNode(t, g).MessagesOnStick; got != view.MessagesOnStick {
		t.Fatalf("a refused message reached the device: %d -> %d", view.MessagesOnStick, got)
	}
	// Receiving is unaffected.
	node.injectSMS("10086", "balance notice")
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, _ = g.fleet.Refresh(ctx, paired.DeviceID)
		if onlyNode(t, g).MessagesOnStick > view.MessagesOnStick {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("an incoming message did not arrive while under observation")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// The SIM hash never contains the SIM identifier, and the journal names
	// the refusal.
	events, err := engine.Events(ctx, 0, 50, paired.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := ""
	for _, event := range events {
		kinds += event.Kind + ","
		if strings.Contains(event.Cause+event.Result, "10010") {
			t.Fatalf("the journal holds a number: %+v", event)
		}
	}
	if !strings.Contains(kinds, "PAIRING_BOUND") || !strings.Contains(kinds, "ACTION_REFUSED") {
		t.Fatalf("journal: %s", kinds)
	}

	// An administrator's quarantine and restore are visible on the device view.
	if err := engine.Restrict(ctx, paired.DeviceID, "admin", "test", true); err != nil {
		t.Fatal(err)
	}
	if got := onlyNode(t, g).Trust; got.State != "QUARANTINE" || got.OutgoingAllowed {
		t.Fatalf("quarantine: %+v", got)
	}
	_, err = g.fleet.SendMessage(ctx, agentclient.SendMessageRequest{RequestID: "trust-2", LineID: view.LineEndpointID, Number: "10010", Text: "hello"})
	if operationCode(err) != "permission_denied" || !strings.Contains(err.Error(), "quarantined") {
		t.Fatalf("quarantined send: %v", err)
	}
}

func TestE2ETrustedAndMonitoredDevicesSend(t *testing.T) {
	binary := os.Getenv("NSH_E2E_NODE_BIN")
	if binary == "" {
		t.Skip("set NSH_E2E_NODE_BIN to a mock-nassimhub-node binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// A device paired before the engine existed is grandfathered and sends.
	root := t.TempDir()
	usb, control := freePort(t), freePort(t)
	startNode(t, binary, filepath.Join(root, "node"), usb, control)
	dataDir := filepath.Join(root, "nas")
	g := openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelStandard)
	paired := pairFirst(t, ctx, g) // no engine yet
	engine := attachTrust(t, ctx, g, dataDir, trustengine.Enforce, nil)
	if err := g.fleet.GrandfatherTrust(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := g.fleet.Refresh(ctx, paired.DeviceID); err != nil {
		t.Fatal(err)
	}
	view := onlyNode(t, g)
	if view.Trust.State != "TRUSTED" || !view.Trust.OutgoingAllowed {
		t.Fatalf("a device paired before the upgrade keeps working: %+v", view.Trust)
	}
	if _, err := g.fleet.SendMessage(ctx, agentclient.SendMessageRequest{RequestID: "trust-ok", LineID: view.LineEndpointID, Number: "10010", Text: "hello"}); err != nil {
		t.Fatalf("a trusted device sends: %v", err)
	}
	if subjects := engine.Subjects(); len(subjects) != 1 || subjects[0].SIMChanges != 0 {
		t.Fatalf("the first reading of a grandfathered device is not a change: %+v", subjects)
	}

	// Monitor mode: a new device is shown as under observation, and sends.
	root = t.TempDir()
	usb, control = freePort(t), freePort(t)
	startNode(t, binary, filepath.Join(root, "node"), usb, control)
	dataDir = filepath.Join(root, "nas")
	g = openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelStandard)
	attachTrust(t, ctx, g, dataDir, trustengine.Monitor, nil)
	paired = pairFirst(t, ctx, g)
	if _, err := g.fleet.Refresh(ctx, paired.DeviceID); err != nil {
		t.Fatal(err)
	}
	view = onlyNode(t, g)
	if view.Trust.State != "OBSERVATION" || view.Trust.OutgoingAllowed || view.Trust.Enforced {
		t.Fatalf("monitor mode shows the real state and says it is not enforced: %+v", view.Trust)
	}
	if _, err := g.fleet.SendMessage(ctx, agentclient.SendMessageRequest{RequestID: "trust-mon", LineID: view.LineEndpointID, Number: "10010", Text: "hello"}); err != nil {
		t.Fatalf("monitor mode must not refuse: %v", err)
	}
}
