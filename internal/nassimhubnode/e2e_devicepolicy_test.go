package nassimhubnode_test

// End-to-end tests of device-local trust enforcement against a real Node
// process (modem simulated, mock build of the r128 agent). They show the part
// the gateway's own gate cannot: that the DEVICE refuses, from the policy the
// gateway signed, when a request reaches it around the gateway's gate over the
// gateway's genuine session.
//
// Nothing here ran on a UFI003; the "hardware identifier" is a fixture
// directory laid out like /sys/devices/soc0.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/modemdeck/internal/trustengine"
	"github.com/human-agent65535/nassimhub-node/core/nodeclient"
	"github.com/human-agent65535/nassimhub-node/proto"
)

func policyBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("NSH_E2E_NODE_BIN")
	if binary == "" {
		t.Skip("set NSH_E2E_NODE_BIN to a mock-nassimhub-node binary built from the r128 agent")
	}
	return binary
}

// shortObservation is the default policy with observation windows a test can
// complete with three readings.
func shortObservation() trustengine.Policy {
	policy := trustengine.DefaultPolicy()
	policy.FirstObservation, policy.ExtendedObservation, policy.LongestObservation = 30*time.Minute, 30*time.Minute, 30*time.Minute
	return policy
}

func attachTrustPolicy(t *testing.T, ctx context.Context, g gateway, dataDir string, mode trustengine.Enforcement, policy trustengine.Policy) *trustengine.Engine {
	t.Helper()
	identity, err := nassimhubnode.LoadOrCreateIdentity(filepath.Join(dataDir, "nassimhub"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := trustengine.NewSQLStore(g.db)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := trustengine.New(ctx, trustengine.Options{Store: store, Key: identity.PrivateKey, Enforcement: mode, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	g.fleet.SetTrustEngine(engine)
	return engine
}

// socinfo writes a fixture directory laid out like /sys/devices/soc0. The
// serial is an obviously made-up value.
func socinfo(t *testing.T, directory, serial string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"family": "Snapdragon\n", "soc_id": "206\n", "machine": "MSM8916\n"}
	if serial != "" {
		files["serial_number"] = serial + "\n"
	} else if err := os.Remove(filepath.Join(directory, "serial_number")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// refreshUntil polls the device until its view satisfies the condition.
func refreshUntil(t *testing.T, ctx context.Context, g gateway, deviceID, what string, condition func(nassimhubnode.NodeView) bool) nassimhubnode.NodeView {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, _ = g.fleet.Refresh(ctx, deviceID)
		view := onlyNode(t, g)
		if condition(view) {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: gateway decision %+v", what, view.Trust)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// directSend calls the Node with the gateway's own pinned client and session,
// around the gateway's gate: what a buggy or compromised gateway process, or a
// replayed request, would do.
func directSend(ctx context.Context, client *nodeclient.Client, id string) error {
	_, err := client.SendSMS(ctx, proto.SendSMSRequest{RequestID: id, To: "10010", Text: "hello"})
	return err
}

func denies(status proto.TrustStatus, action proto.TrustAction) bool {
	for _, denied := range status.Deny {
		if denied == action {
			return true
		}
	}
	return false
}

func eventKinds(t *testing.T, ctx context.Context, engine *trustengine.Engine, deviceID string) string {
	t.Helper()
	events, err := engine.Events(ctx, 0, 200, deviceID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := ""
	for _, event := range events {
		kinds += event.Kind + ","
	}
	return kinds
}

func subjectOf(t *testing.T, engine *trustengine.Engine, deviceID string) trustengine.SubjectView {
	t.Helper()
	for _, subject := range engine.Subjects() {
		if subject.SubjectID == deviceID {
			return subject
		}
	}
	t.Fatalf("no trust record for %s", deviceID)
	return trustengine.SubjectView{}
}

func TestE2EDeviceItselfEnforcesTheSignedPolicy(t *testing.T) {
	binary := policyBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	soc := filepath.Join(root, "soc0")
	socinfo(t, soc, "FAKE-SERIAL-0001")
	usb, control := freePort(t), freePort(t)
	node := startNodeArgs(t, binary, filepath.Join(root, "node"), usb, control, "-socinfo-dir", soc)
	dataDir := filepath.Join(root, "nas")
	g := openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelStandard)
	engine := attachTrustPolicy(t, ctx, g, dataDir, trustengine.Enforce, shortObservation())
	paired := pairFirst(t, ctx, g)
	id := paired.DeviceID

	// A new device: the gateway decides OBSERVATION, and - a separate fact -
	// the device reports that it holds and enforces that decision.
	view := refreshUntil(t, ctx, g, id, "the device never reported the policy", func(view nassimhubnode.NodeView) bool {
		return view.Trust.Device.Known && view.Trust.Device.Installed
	})
	device := view.Trust.Device
	if view.Trust.State != "OBSERVATION" || !device.Supported || !device.Enforcing || !device.MatchesGateway || device.Stale || device.Damaged ||
		device.State != "OBSERVATION" || device.Generation == 0 || device.Generation != device.GatewayGeneration ||
		device.CheckedAt.IsZero() || device.PushError != "" || !device.HardwareReported {
		t.Fatalf("a new device holds and enforces the gateway's decision: gateway %+v", view.Trust)
	}
	first := device.Generation
	if got := subjectOf(t, engine, id); got.PolicyGeneration != first || got.ModemChanges != 0 || !got.RecordVerified {
		t.Fatalf("the generation is the trust record's, and a first hardware reading is not a change: %+v", got.Record)
	}

	// Around the gateway's gate, with the gateway's genuine session: the
	// DEVICE refuses, and nothing is sent.
	client := g.fleet.ClientForTest(id)
	if client == nil {
		t.Fatal("no client for the paired device")
	}
	err := directSend(ctx, client, "direct-1")
	if proto.CodeOf(err) != proto.ErrorPermissionDenied || !strings.Contains(err.Error(), "OBSERVATION") {
		t.Fatalf("the device itself must refuse a send while under observation: %v", err)
	}
	if _, err := client.Dial(ctx, proto.DialRequest{RequestID: "direct-dial-1", To: "10010"}); proto.CodeOf(err) != proto.ErrorPermissionDenied {
		t.Fatalf("the device itself must refuse a dial while under observation: %v", err)
	}
	if got := refreshUntil(t, ctx, g, id, "refresh", func(nassimhubnode.NodeView) bool { return true }).MessagesOnStick; got != view.MessagesOnStick {
		t.Fatalf("a message the device refused is on the device: %d -> %d", view.MessagesOnStick, got)
	}
	status, err := client.TrustStatus(ctx)
	if err != nil || status.Mode != proto.TrustEnforce || status.Refused[proto.TrustActionSendSMS] != 1 || status.Refused[proto.TrustActionDial] != 1 ||
		!denies(status, proto.TrustActionSendSMS) || !denies(status, proto.TrustActionDial) || !denies(status, proto.TrustActionDTMF) {
		t.Fatalf("device status: %+v %v", status, err)
	}
	// Receiving is not gated by the device.
	node.injectSMS("10086", "balance notice")
	refreshUntil(t, ctx, g, id, "an incoming message did not arrive while the device was restricted", func(now nassimhubnode.NodeView) bool {
		return now.MessagesOnStick > view.MessagesOnStick
	})

	// Revocation is a higher generation with state QUARANTINE.
	if err := engine.Restrict(ctx, id, "admin", "test", true); err != nil {
		t.Fatal(err)
	}
	view = refreshUntil(t, ctx, g, id, "the quarantine never reached the device", func(view nassimhubnode.NodeView) bool {
		return view.Trust.Device.State == "QUARANTINE" && view.Trust.Device.MatchesGateway
	})
	if view.Trust.Device.Generation <= first || view.Trust.State != "QUARANTINE" {
		t.Fatalf("quarantine is a newer generation: %+v", view.Trust)
	}
	quarantined := view.Trust.Device.Generation
	if status, err = client.TrustStatus(ctx); err != nil || len(status.Deny) != 4 {
		t.Fatalf("a quarantined device denies all four actions: %+v %v", status, err)
	}

	// An administrator restores it: a new generation, observation again, and
	// the device still refuses.
	if err := engine.Restore(ctx, id, "admin", "reviewed"); err != nil {
		t.Fatal(err)
	}
	view = refreshUntil(t, ctx, g, id, "the restore never reached the device", func(view nassimhubnode.NodeView) bool {
		return view.Trust.Device.State == "OBSERVATION" && view.Trust.Device.MatchesGateway
	})
	if view.Trust.Device.Generation <= quarantined {
		t.Fatalf("a restore is a newer generation: %d after %d", view.Trust.Device.Generation, quarantined)
	}
	if err := directSend(ctx, client, "direct-2"); proto.CodeOf(err) != proto.ErrorPermissionDenied {
		t.Fatalf("a restored device is under observation and still refuses: %v", err)
	}

	// The observation period completes (forced: three ten-minute healthy
	// readings against a thirty-minute window). The device then allows.
	for range 3 {
		reading := trustengine.Reading{SubjectID: id, IdentityHash: g.fleet.IdentityHashForTest(id), Healthy: true, Elapsed: 10 * time.Minute}
		if err := engine.Observe(ctx, reading); err != nil {
			t.Fatal(err)
		}
	}
	view = refreshUntil(t, ctx, g, id, "the device never received the trusted policy", func(view nassimhubnode.NodeView) bool {
		return view.Trust.State == "TRUSTED" && view.Trust.Device.State == "TRUSTED" && view.Trust.Device.MatchesGateway
	})
	trusted := view.Trust.Device.Generation
	if status, err = client.TrustStatus(ctx); err != nil || len(status.Deny) != 0 || status.Stale {
		t.Fatalf("a trusted device denies nothing: %+v %v", status, err)
	}
	if err := directSend(ctx, client, "direct-3"); err != nil {
		t.Fatalf("a trusted device sends: %v", err)
	}
	if _, err := g.fleet.SendMessage(ctx, agentclient.SendMessageRequest{RequestID: "via-gateway", LineID: view.LineEndpointID, Number: "10010", Text: "hello"}); err != nil {
		t.Fatalf("a trusted device sends through the gateway: %v", err)
	}
	if kinds := eventKinds(t, ctx, engine, id); strings.Contains(kinds, trustengine.EventModemChanged) {
		t.Fatalf("no hardware change has happened yet: %s", kinds)
	}

	// The gateway restarts. The generation continues from the trust record, so
	// the device accepts the next policy instead of refusing a lower number.
	g2 := openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelStandard)
	engine2 := attachTrustPolicy(t, ctx, g2, dataDir, trustengine.Enforce, shortObservation())
	view = refreshUntil(t, ctx, g2, id, "the restarted gateway's policy never reached the device", func(view nassimhubnode.NodeView) bool {
		return view.Trust.Device.Known && view.Trust.Device.MatchesGateway
	})
	if view.Trust.Device.Generation <= trusted || view.Trust.Device.PushError != "" || view.Trust.Device.State != "TRUSTED" {
		t.Fatalf("after a gateway restart the generation continues: %d after %d, %+v", view.Trust.Device.Generation, trusted, view.Trust.Device)
	}

	// The hardware identifier stops being readable: not known now, which is
	// not a change.
	socinfo(t, soc, "")
	view = refreshUntil(t, ctx, g2, id, "the device kept reporting a hardware identifier", func(view nassimhubnode.NodeView) bool {
		return view.Trust.Device.Known && !view.Trust.Device.HardwareReported
	})
	if view.Trust.Device.HardwareReason == "" || view.Trust.State != "TRUSTED" || subjectOf(t, engine2, id).ModemChanges != 0 {
		t.Fatalf("an absent hardware identifier is not a change: %+v", view.Trust)
	}

	// A DIFFERENT hardware identifier under the same device identity: the
	// module was replaced. MODEM_CHANGED, risk +3, observation again - and the
	// device is told and refuses.
	before := subjectOf(t, engine2, id).RiskScore
	socinfo(t, soc, "FAKE-SERIAL-0002")
	view = refreshUntil(t, ctx, g2, id, "a changed hardware identifier was not noticed", func(view nassimhubnode.NodeView) bool {
		return view.Trust.State == "OBSERVATION" && view.Trust.Device.State == "OBSERVATION" && view.Trust.Device.MatchesGateway
	})
	subject := subjectOf(t, engine2, id)
	if subject.ModemChanges != 1 || subject.RiskScore != before+3 || !strings.Contains(eventKinds(t, ctx, engine2, id), trustengine.EventModemChanged) {
		t.Fatalf("module change: %+v", subject.Record)
	}
	client2 := g2.fleet.ClientForTest(id)
	if err := directSend(ctx, client2, "direct-4"); proto.CodeOf(err) != proto.ErrorPermissionDenied {
		t.Fatalf("after a module change the device refuses again: %v", err)
	}
	// The same value again is not a second change.
	refreshUntil(t, ctx, g2, id, "refresh", func(nassimhubnode.NodeView) bool { return true })
	if got := subjectOf(t, engine2, id).ModemChanges; got != 1 {
		t.Fatalf("an unchanged identifier counted as a change: %d", got)
	}

	// The device restarts: the policy is read back from its state directory
	// and enforced before the gateway says anything.
	node.restartWith("-socinfo-dir", soc)
	if err := directSend(ctx, client2, "direct-5"); proto.CodeOf(err) != proto.ErrorPermissionDenied {
		t.Fatalf("a restarted device still refuses: %v", err)
	}

	// The decision changes to TRUSTED while the device is away, so the device
	// still holds the OBSERVATION policy when it comes back - with no sessions,
	// because it restarted. The gateway's next request to it is a forwarded
	// send. It succeeds only because the current policy is delivered on the
	// new session BEFORE the send that needed the session.
	g3 := openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelStandard)
	engine3 := attachTrustPolicy(t, ctx, g3, dataDir, trustengine.Enforce, shortObservation())
	view = refreshUntil(t, ctx, g3, id, "the device never reported the policy", func(view nassimhubnode.NodeView) bool {
		return view.Trust.Device.Known && view.Trust.Device.MatchesGateway && view.Trust.Device.State == "OBSERVATION"
	})
	node.stop()
	for range 3 {
		reading := trustengine.Reading{SubjectID: id, IdentityHash: g3.fleet.IdentityHashForTest(id), Healthy: true, Elapsed: 10 * time.Minute}
		if err := engine3.Observe(ctx, reading); err != nil {
			t.Fatal(err)
		}
	}
	if got := subjectOf(t, engine3, id); got.State != trustengine.Trusted {
		t.Fatalf("observation completed: %+v", got.Record)
	}
	// The immediate delivery fails (the device is away) and is recorded.
	for deadline := time.Now().Add(15 * time.Second); onlyNode(t, g3).Trust.Device.PushError == ""; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("a failed delivery was not recorded: %+v", onlyNode(t, g3).Trust.Device)
		}
	}
	node.start()
	if _, err := g3.fleet.SendMessage(ctx, agentclient.SendMessageRequest{RequestID: "first-on-a-fresh-session", LineID: view.LineEndpointID, Number: "10010", Text: "hello"}); err != nil {
		t.Fatalf("the policy must reach the device before the first forwarded action on a fresh session: %v", err)
	}
	if status, err = g3.fleet.ClientForTest(id).TrustStatus(ctx); err != nil || status.State != proto.TrustTrusted || len(status.Deny) != 0 {
		t.Fatalf("the device holds the trusted policy: %+v %v", status, err)
	}
	if got := onlyNode(t, g3).Trust.Device; got.PushError != "" || !got.MatchesGateway {
		t.Fatalf("the delivery on the new session is recorded: %+v", got)
	}
}

// The gateway in monitor mode signs a monitor policy: the device holds the
// real state and refuses nothing.
func TestE2EMonitorGatewayMeansTheDeviceDoesNotRefuse(t *testing.T) {
	binary := policyBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	usb, control := freePort(t), freePort(t)
	startNode(t, binary, filepath.Join(root, "node"), usb, control)
	dataDir := filepath.Join(root, "nas")
	g := openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelStandard)
	engine := attachTrustPolicy(t, ctx, g, dataDir, trustengine.Monitor, shortObservation())
	paired := pairFirst(t, ctx, g)
	id := paired.DeviceID
	view := refreshUntil(t, ctx, g, id, "the device never reported the policy", func(view nassimhubnode.NodeView) bool {
		return view.Trust.Device.Known && view.Trust.Device.Installed
	})
	device := view.Trust.Device
	if view.Trust.State != "OBSERVATION" || view.Trust.Enforced || device.State != "OBSERVATION" || device.Enforcing || !device.MatchesGateway {
		t.Fatalf("monitor: the device holds the real state and does not enforce it: %+v", view.Trust)
	}
	client := g.fleet.ClientForTest(id)
	if err := directSend(ctx, client, "monitor-1"); err != nil {
		t.Fatalf("a device under a monitor policy must not refuse: %v", err)
	}
	status, err := client.TrustStatus(ctx)
	if err != nil || status.Mode != proto.TrustMonitor || status.WouldRefuse[proto.TrustActionSendSMS] != 1 || len(status.Refused) != 0 || !denies(status, proto.TrustActionSendSMS) {
		t.Fatalf("what would have been refused is counted and reported: %+v %v", status, err)
	}
	// No hardware identifier was configured: unsupported for this device, and
	// never an event.
	if device.HardwareReported || device.HardwareReason == "" {
		t.Fatalf("a device without a hardware identifier says so: %+v", device)
	}
	if kinds := eventKinds(t, ctx, engine, id); strings.Contains(kinds, trustengine.EventModemChanged) || subjectOf(t, engine, id).ModemChanges != 0 {
		t.Fatalf("an absent hardware identifier produced an event: %s", kinds)
	}
}

// An agent that predates protocol 1.4 has no trust endpoints. That is one
// informational fact about the device, not an error, and the gateway's own
// gate still applies.
func TestE2EOlderNodeIsReportedAsNotEnforcing(t *testing.T) {
	binary := policyBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	usb, control := freePort(t), freePort(t)
	node := startNodeArgs(t, binary, filepath.Join(root, "node"), usb, control, "-trust-enforcement=false")
	dataDir := filepath.Join(root, "nas")
	g := openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelStandard)
	engine := attachTrustPolicy(t, ctx, g, dataDir, trustengine.Enforce, shortObservation())
	paired := pairFirst(t, ctx, g)
	id := paired.DeviceID
	view := refreshUntil(t, ctx, g, id, "the device was never asked", func(view nassimhubnode.NodeView) bool { return view.Trust.Device.Known })
	device := view.Trust.Device
	if device.Supported || device.Installed || device.Enforcing || device.MatchesGateway || device.Reason == "" || device.PushError != "" {
		t.Fatalf("an older agent is reported as not enforcing, with the reason and no error: %+v", device)
	}
	if !view.Online || view.LastError != "" || view.Trust.State != "OBSERVATION" {
		t.Fatalf("an older agent is not a failing device: online=%v error=%q trust=%+v", view.Online, view.LastError, view.Trust)
	}
	// Polling it again changes nothing and still records no error.
	for range 3 {
		view = refreshUntil(t, ctx, g, id, "refresh", func(nassimhubnode.NodeView) bool { return true })
	}
	if view.Trust.Device.Supported || view.Trust.Device.PushError != "" || view.LastError != "" {
		t.Fatalf("after more polls: %+v %q", view.Trust.Device, view.LastError)
	}
	// The gateway's own gate is what applies.
	_, err := g.fleet.SendMessage(ctx, agentclient.SendMessageRequest{RequestID: "old-1", LineID: view.LineEndpointID, Number: "10010", Text: "hello"})
	if operationCode(err) != "permission_denied" {
		t.Fatalf("the gateway's gate still refuses: %v", err)
	}
	// The device itself does not: this is exactly what "does not enforce" means.
	if err := directSend(ctx, g.fleet.ClientForTest(id), "old-direct"); err != nil {
		t.Fatalf("an older agent has no gate of its own: %v", err)
	}
	if kinds := eventKinds(t, ctx, engine, id); strings.Contains(kinds, trustengine.EventModemChanged) {
		t.Fatalf("journal: %s", kinds)
	}

	// The agent is updated and restarts: it is asked again and now enforces.
	node.restartWith()
	view = refreshUntil(t, ctx, g, id, "an updated agent was not noticed", func(view nassimhubnode.NodeView) bool {
		return view.Trust.Device.Supported && view.Trust.Device.Installed && view.Trust.Device.MatchesGateway
	})
	if err := directSend(ctx, g.fleet.ClientForTest(id), "new-direct"); proto.CodeOf(err) != proto.ErrorPermissionDenied {
		t.Fatalf("the updated agent enforces the policy: %v", err)
	}
}

// A policy is signed once per decision and renewed every six hours, not on
// every poll; and a policy the device will not take (here: issued in what the
// device's clock calls the future) is a recorded fact about that device that
// stops neither polling nor receiving.
func TestE2EPolicyIsRenewedOnScheduleAndAFailedPushBlocksNothing(t *testing.T) {
	binary := policyBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	usb, control := freePort(t), freePort(t)
	node := startNode(t, binary, filepath.Join(root, "node"), usb, control)
	dataDir := filepath.Join(root, "nas")
	g := openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelStandard)
	attachTrustPolicy(t, ctx, g, dataDir, trustengine.Enforce, shortObservation())
	paired := pairFirst(t, ctx, g)
	id := paired.DeviceID
	matches := func(view nassimhubnode.NodeView) bool {
		return view.Trust.Device.Known && view.Trust.Device.Installed && view.Trust.Device.MatchesGateway && view.Trust.Device.PushError == ""
	}

	// The gateway's clock is seven hours behind the device's: a policy issued
	// then is still valid for the device (24 h), so it is installed.
	offset := -(nassimhubnode.PolicyRefreshEveryForTest + time.Hour)
	g.fleet.SetClockForTest(func() time.Time { return time.Now().Add(offset) })
	refreshUntil(t, ctx, g, id, "the device never reported the policy", matches)
	held := refreshUntil(t, ctx, g, id, "the device never reported the policy", matches).Trust.Device.Generation
	for range 3 {
		if got := refreshUntil(t, ctx, g, id, "refresh", matches).Trust.Device.Generation; got != held {
			t.Fatalf("an unchanged decision was signed again on a poll: generation %d -> %d", held, got)
		}
	}

	// More than six hours later the policy is renewed with a new generation.
	g.fleet.SetClockForTest(time.Now)
	view := refreshUntil(t, ctx, g, id, "the policy was not renewed after six hours", func(view nassimhubnode.NodeView) bool {
		return matches(view) && view.Trust.Device.Generation > held
	})
	renewed := view.Trust.Device.Generation
	if view.Trust.Device.Stale {
		t.Fatalf("a renewed policy is not stale: %+v", view.Trust.Device)
	}

	// The gateway's clock jumps far ahead of the device's - which is how a
	// device that booted with an unset clock sees every gateway. The device
	// does not ask its own clock whether to accept a policy: the new
	// generation is installed and nothing is recorded as failed.
	g.fleet.SetClockForTest(func() time.Time { return time.Now().Add(nassimhubnode.PolicyRefreshEveryForTest + time.Hour) })
	view = refreshUntil(t, ctx, g, id, "a policy issued in the device's future was not installed", func(view nassimhubnode.NodeView) bool {
		return matches(view) && view.Trust.Device.Generation > renewed
	})
	if device := view.Trust.Device; device.Stale || device.Freshness != "fresh" || !view.Online || view.LastError != "" {
		t.Fatalf("a policy issued in the device's future is fresh: online=%v error=%q %+v", view.Online, view.LastError, device)
	}
	node.injectSMS("10086", "balance notice")
	refreshUntil(t, ctx, g, id, "receiving stopped", func(now nassimhubnode.NodeView) bool {
		return now.MessagesOnStick > view.MessagesOnStick
	})
	// The device enforces what it holds (a new device is under observation).
	if err := directSend(ctx, g.fleet.ClientForTest(id), "future-1"); proto.CodeOf(err) != proto.ErrorPermissionDenied || !strings.Contains(err.Error(), "OBSERVATION") {
		t.Fatalf("the device enforces the policy it holds: %v", err)
	}
}

// The device's clock takes no part in whether it can be used, and a device
// that restarts while the gateway stays up is usable at the gateway's very
// next request.
//
// The mock Node's wall clock is this machine's and cannot be set from here, so
// "the device booted at an unset date" is produced the other way round: the
// GATEWAY's clock is moved ten years ahead, which gives the device a policy
// issued ten years in its future - exactly what a device at 1970 is given.
// Simulated; nothing here ran on a UFI003.
func TestE2EDeviceClockIsNotNeededAndARestartedDeviceIsUsableAtOnce(t *testing.T) {
	binary := policyBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	usb, control := freePort(t), freePort(t)
	node := startNode(t, binary, filepath.Join(root, "node"), usb, control)
	dataDir := filepath.Join(root, "nas")
	g := openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelStandard)
	engine := attachTrustPolicy(t, ctx, g, dataDir, trustengine.Enforce, shortObservation())
	id := pairFirst(t, ctx, g).DeviceID
	refreshUntil(t, ctx, g, id, "the device never reported the policy", func(view nassimhubnode.NodeView) bool {
		return view.Trust.Device.Known && view.Trust.Device.Installed
	})
	for range 3 {
		reading := trustengine.Reading{SubjectID: id, IdentityHash: g.fleet.IdentityHashForTest(id), Healthy: true, Elapsed: 10 * time.Minute}
		if err := engine.Observe(ctx, reading); err != nil {
			t.Fatal(err)
		}
	}
	trustedAndFresh := func(view nassimhubnode.NodeView) bool {
		device := view.Trust.Device
		return view.Trust.State == "TRUSTED" && device.Known && device.State == "TRUSTED" && device.MatchesGateway && !device.Stale && device.PushError == ""
	}
	view := refreshUntil(t, ctx, g, id, "the device never received the trusted policy", trustedAndFresh)
	line, held := view.LineEndpointID, view.Trust.Device.Generation
	if view.Trust.Device.Freshness != "fresh" {
		t.Fatalf("the device says why its policy is or is not stale: %+v", view.Trust.Device)
	}
	send := func(requestID string) error {
		_, err := g.fleet.SendMessage(ctx, agentclient.SendMessageRequest{RequestID: requestID, LineID: line, Number: "10010", Text: "hello"})
		return err
	}
	if err := send("before-restart"); err != nil {
		t.Fatalf("a trusted device sends: %v", err)
	}
	// restart kills and starts the device with the gateway believing it is
	// online, as it does between two polls. A send starts a refresh of its own
	// in the background; that one is allowed to finish first, or it would find
	// the device gone and mark it offline, which is a different scenario.
	restart := func() {
		t.Helper()
		time.Sleep(500 * time.Millisecond)
		refreshUntil(t, ctx, g, id, "the device is not online", func(view nassimhubnode.NodeView) bool { return view.Online })
		node.restartWith()
	}

	// The device restarts in the middle of the gateway's session. It holds the
	// policy as stale-after-restart; the gateway's old session is refused, a
	// new one is made, the policy is sent on it first, and only then the send.
	// No poll happens in between: this is the very next request.
	restart()
	if err := send("first-after-device-restart"); err != nil {
		t.Fatalf("the very next send after a device restart must succeed for a trusted device: %v", err)
	}
	status, err := g.fleet.ClientForTest(id).TrustStatus(ctx)
	if err != nil || status.Freshness != proto.TrustFresh || status.Stale || status.Generation != held || status.State != proto.TrustTrusted {
		t.Fatalf("the same generation was sent again and made fresh (held %d): %+v %v", held, status, err)
	}
	// The same around the gateway's gate, with the gateway's client.
	restart()
	if err := directSend(ctx, g.fleet.ClientForTest(id), "direct-after-restart"); err != nil {
		t.Fatalf("a direct send on the gateway's client after a device restart: %v", err)
	}

	// Gateway ten years ahead = the device's clock far before issued_at.
	const decade = 10 * 365 * 24 * time.Hour
	g.fleet.SetClockForTest(func() time.Time { return time.Now().Add(decade) })
	view = refreshUntil(t, ctx, g, id, "a policy issued ten years in the device's future was not installed", func(view nassimhubnode.NodeView) bool {
		return trustedAndFresh(view) && view.Trust.Device.Generation > held
	})
	held = view.Trust.Device.Generation
	if view.Trust.Device.Freshness != "fresh" {
		t.Fatalf("device: %+v", view.Trust.Device)
	}
	if err := send("device-clock-in-the-past"); err != nil {
		t.Fatalf("a device whose clock is before the policy's issue time must still send: %v", err)
	}
	restart()
	if err := send("device-clock-in-the-past-after-restart"); err != nil {
		t.Fatalf("and after a restart: %v", err)
	}

	// Gateway three days behind simulates a device wall clock far ahead. A
	// freshly authenticated policy still uses its signed monotonic validity.
	g.fleet.SetClockForTest(func() time.Time { return time.Now().Add(-72 * time.Hour) })
	view = refreshUntil(t, ctx, g, id, "a forward device clock invalidated the confirmed policy", func(view nassimhubnode.NodeView) bool {
		return trustedAndFresh(view) && view.Trust.Device.Generation > held
	})
	held = view.Trust.Device.Generation
	for range 4 {
		if got := refreshUntil(t, ctx, g, id, "refresh", func(nassimhubnode.NodeView) bool { return true }).Trust.Device.Generation; got != held {
			t.Fatalf("a clock correction spent a policy generation: %d -> %d", held, got)
		}
	}
	if err := directSend(ctx, g.fleet.ClientForTest(id), "device-clock-ahead"); err != nil {
		t.Fatalf("a confirmed policy stays usable despite the wall clock: %v", err)
	}
	node.injectSMS("10086", "balance notice")
	refreshUntil(t, ctx, g, id, "receiving stopped during clock correction", func(now nassimhubnode.NodeView) bool {
		return now.MessagesOnStick > view.MessagesOnStick
	})
	g.fleet.SetClockForTest(time.Now)
	view = refreshUntil(t, ctx, g, id, "the device did not remain usable", trustedAndFresh)
	if err := send("recovered"); err != nil {
		t.Fatalf("send after clocks agree: %v", err)
	}
	// A restricted device, restarted mid-session: refused by the DEVICE at the
	// very next request, around the gateway's gate, because it is denied.
	if err := engine.Restrict(ctx, id, "admin", "test", false); err != nil {
		t.Fatal(err)
	}
	refreshUntil(t, ctx, g, id, "the restriction never reached the device", func(view nassimhubnode.NodeView) bool {
		return view.Trust.Device.State == "RESTRICTED" && view.Trust.Device.MatchesGateway
	})
	restart()
	err = directSend(ctx, g.fleet.ClientForTest(id), "restricted-after-restart")
	if proto.CodeOf(err) != proto.ErrorPermissionDenied || !strings.Contains(err.Error(), "RESTRICTED") {
		t.Fatalf("a restricted device must refuse the first send after its restart: %v", err)
	}
	if err := send("restricted-through-the-gateway"); err == nil {
		t.Fatal("the gateway's own gate still refuses a restricted device")
	}
	status, err = g.fleet.ClientForTest(id).TrustStatus(ctx)
	if err != nil || status.Refused[proto.TrustActionSendSMS] != 1 || !denies(status, proto.TrustActionSendSMS) || status.Freshness != proto.TrustFresh {
		t.Fatalf("the device refused exactly the one send that reached it: %+v %v", status, err)
	}
}
