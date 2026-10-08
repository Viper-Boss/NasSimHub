package nassimhubnode_test

// End-to-end tests of post-quantum identity against a real Node process.
//
// Like e2e_test.go they run only when NSH_E2E_NODE_BIN points at a
// mock-nassimhub-node binary, and additionally only when both that binary and
// this test were built with a toolchain that has crypto/mldsa. The Node speaks
// the real protocol over the real device-certificate TLS with real ML-DSA
// keys; only its modem is simulated. Nothing here touches hardware.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/communication"
	"github.com/human-agent65535/modemdeck/internal/messageevents"
	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/modemdeck/internal/platform/database"
	"github.com/human-agent65535/modemdeck/internal/store"
	"github.com/human-agent65535/nassimhub-node/proto"
)

const nodePQKeyFile = "pq-identity.json"

func pqBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("NSH_E2E_NODE_BIN")
	if binary == "" {
		t.Skip("set NSH_E2E_NODE_BIN to a mock-nassimhub-node binary (see scripts/nassimhub-node-e2e.sh)")
	}
	if !proto.EnableStandardPQ() {
		t.Skip("this toolchain has no crypto/mldsa; build with " + proto.GoVersionForMLDSA)
	}
	return binary
}

// openGatewayAt is openGateway with a security level, the way the gateway
// binary assembles it: the core key is created only for a level that offers one.
func openGatewayAt(t *testing.T, ctx context.Context, dataDir, usbURL string, level proto.SecurityLevel) gateway {
	t.Helper()
	db, err := database.Open(ctx, database.Config{TargetPath: filepath.Join(dataDir, "modemdeck.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository, err := store.New(db)
	if err != nil {
		t.Fatal(err)
	}
	identityDir := filepath.Join(dataDir, "nassimhub")
	identity, err := nassimhubnode.LoadOrCreateIdentity(identityDir)
	if err != nil {
		t.Fatal(err)
	}
	if level != proto.LevelStandard {
		state, err := nassimhubnode.EnsureCorePQ(identityDir, &identity, proto.PQAlgorithmFor(level))
		if err != nil || state != nassimhubnode.CorePQActive {
			t.Fatalf("core post-quantum key: %s %v", state, err)
		}
	}
	fleet, err := nassimhubnode.New(ctx, nassimhubnode.Options{
		Identity: identity, Repository: repository, CoreVersion: "1.15.0",
		USBBaseURL: usbURL, Timeout: 3 * time.Second, SecurityLevel: level,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := communication.New(hostWithoutModem{}, repository, messageevents.NewBuffer(64))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetNodeSource(fleet); err != nil {
		t.Fatal(err)
	}
	return gateway{db: db, repository: repository, fleet: fleet, service: service}
}

func pairFirst(t *testing.T, ctx context.Context, g gateway) nassimhubnode.NodeView {
	t.Helper()
	candidates, err := g.fleet.Discover(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].Ticket == "" {
		t.Fatalf("discover: %+v %v", candidates, err)
	}
	view, err := g.fleet.Pair(ctx, nassimhubnode.PairRequest{
		Ticket: candidates[0].Ticket, DeviceID: candidates[0].DeviceID, Fingerprint: candidates[0].Fingerprint,
	})
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	return view
}

func onlyNode(t *testing.T, g gateway) nassimhubnode.NodeView {
	t.Helper()
	nodes := g.fleet.Nodes()
	if len(nodes) != 1 {
		t.Fatalf("expected one node, have %d", len(nodes))
	}
	return nodes[0]
}

func hasNodeLine(status communication.Status) bool {
	for _, line := range status.Lines {
		if strings.HasPrefix(line.EndpointID, store.NasSimHubNodeEndpointPrefix) {
			return true
		}
	}
	return false
}

// startNodeArgs starts a Node whose very first boot already carries the given
// flags, so the keys it generates are the ones that configuration generates.
func startNodeArgs(t *testing.T, binary, stateDir, usb, control string, arguments ...string) *e2eNode {
	t.Helper()
	node := &e2eNode{t: t, binary: binary, stateDir: stateDir, usb: usb, control: control, extraArgs: arguments}
	node.start()
	t.Cleanup(node.stop)
	return node
}

func (n *e2eNode) restartWith(arguments ...string) {
	n.t.Helper()
	n.stop()
	n.extraArgs = arguments
	n.start()
}

// STANDARD: verify and report, demand nothing, break nothing.
func TestE2EStandardLevelReportsDualIdentityWithoutEnforcingIt(t *testing.T) {
	binary := pqBinary(t)
	ctx := context.Background()
	work := t.TempDir()
	usb, control := freePort(t), freePort(t)
	node := startNode(t, binary, filepath.Join(work, "node"), usb, control)
	g := openGatewayAt(t, ctx, filepath.Join(work, "nas"), "https://"+usb, proto.LevelStandard)

	view := pairFirst(t, ctx, g)
	if !view.Online || view.Identity.Outcome != nassimhubnode.IdentityOutcomeDual ||
		view.Identity.PinState != nassimhubnode.PinStatePinned || view.Identity.Enforced ||
		view.Identity.Algorithm != "ml-dsa-65" || view.Identity.Fingerprint == "" ||
		view.Identity.Backend != "crypto/mldsa" || !view.Identity.Available || !view.Identity.AttestationSupported {
		t.Fatalf("identity at STANDARD: %+v", view.Identity)
	}
	if view.Identity.CoreKeyOffered || view.Identity.CoreKeyPinned {
		t.Fatalf("a STANDARD gateway offered a post-quantum key of its own: %+v", view.Identity)
	}
	// The session's key agreement is a separate fact, reported separately.
	if !view.LinkSecurity.Known || view.LinkSecurity.PostQuantum != "yes" || view.LinkSecurity.Group != "X25519MLKEM768" ||
		view.LinkSecurity.TLSVersion != "TLS 1.3" || view.LinkSecurity.RequestedLevel != "STANDARD" {
		t.Fatalf("link security: %+v", view.LinkSecurity)
	}
	pinnedFingerprint := view.Identity.Fingerprint

	// The agent is rolled back to a build with no post-quantum identity. At
	// STANDARD the device keeps working - classically - and the page says what
	// happened. The pin is not dropped.
	node.restartWith("-pq-identity=false")
	status := g.sync(t, ctx)
	view = onlyNode(t, g)
	if !view.Online || view.IdentityRefused || !hasNodeLine(status) {
		t.Fatalf("a STANDARD gateway refused a device that lost its post-quantum key: %+v", view)
	}
	if view.Identity.Outcome != nassimhubnode.IdentityOutcomeClassicalOnly ||
		view.Identity.PinState != nassimhubnode.PinStateWithdrawn || view.Identity.Reason == "" ||
		view.Identity.Fingerprint != pinnedFingerprint {
		t.Fatalf("identity after rollback: %+v", view.Identity)
	}

	// The key comes back (agent updated again): dual, pinned, no residue.
	node.restartWith()
	g.sync(t, ctx)
	view = onlyNode(t, g)
	if view.Identity.Outcome != nassimhubnode.IdentityOutcomeDual || view.Identity.PinState != nassimhubnode.PinStatePinned ||
		view.Identity.Reason != "" || view.Identity.Fingerprint != pinnedFingerprint {
		t.Fatalf("identity after the key returned: %+v", view.Identity)
	}

	// A DIFFERENT key appears. Not refused at STANDARD, but not accepted as
	// the device's identity either, and the recorded pin does not move.
	if err := os.Remove(filepath.Join(node.stateDir, nodePQKeyFile)); err != nil {
		t.Fatal(err)
	}
	node.restartWith()
	g.sync(t, ctx)
	view = onlyNode(t, g)
	if !view.Online || view.Identity.Outcome != nassimhubnode.IdentityOutcomeClassicalOnly ||
		view.Identity.PinState != nassimhubnode.PinStateChanged || view.Identity.Fingerprint != pinnedFingerprint {
		t.Fatalf("identity after a silent key change: %+v", view.Identity)
	}
	// The administrator accepts the new key.
	view, err := g.fleet.ResetPQPin(ctx, view.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Identity.Outcome != nassimhubnode.IdentityOutcomeDual || view.Identity.PinState != nassimhubnode.PinStatePinned ||
		view.Identity.Fingerprint == pinnedFingerprint || view.Identity.Fingerprint == "" {
		t.Fatalf("identity after the pin was reset: %+v", view.Identity)
	}
}

// PQ: the pin is enforced in both directions.
func TestE2EPQLevelPinsBothSidesAndRefusesDowngrades(t *testing.T) {
	binary := pqBinary(t)
	ctx := context.Background()
	work := t.TempDir()
	usb, control := freePort(t), freePort(t)
	node := startNode(t, binary, filepath.Join(work, "node"), usb, control)
	dataDir := filepath.Join(work, "nas")
	g := openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelPQ)

	view := pairFirst(t, ctx, g)
	if !view.Online || view.Identity.Outcome != nassimhubnode.IdentityOutcomeDual || !view.Identity.Enforced ||
		view.Identity.PinState != nassimhubnode.PinStatePinned || !view.Identity.CoreKeyOffered || !view.Identity.CoreKeyPinned ||
		view.Identity.Policy != "preferred" {
		t.Fatalf("identity at PQ: %+v", view.Identity)
	}
	pinnedFingerprint := view.Identity.Fingerprint
	if status := g.sync(t, ctx); !hasNodeLine(status) {
		t.Fatal("a verified node contributes no line")
	}

	// The gateway restarts: same core key, same pin, no re-pairing.
	g.db.Close()
	g = openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelPQ)
	g.sync(t, ctx)
	view = onlyNode(t, g)
	if !view.Online || view.Identity.Outcome != nassimhubnode.IdentityOutcomeDual || view.Identity.Fingerprint != pinnedFingerprint ||
		!view.Identity.CoreKeyPinned || view.Identity.PinnedAt.IsZero() {
		t.Fatalf("identity after a gateway restart: %+v", view)
	}

	// The device stops presenting its post-quantum key. Refused: no line, no
	// messages, the reason shown, and nothing read from the device.
	node.restartWith("-pq-identity=false")
	node.injectSMS("10010", "this message must not be ingested from a refused device")
	status := g.sync(t, ctx)
	view = onlyNode(t, g)
	if view.Online || !view.IdentityRefused || hasNodeLine(status) {
		t.Fatalf("a downgraded device was not refused at PQ: online=%v refused=%v", view.Online, view.IdentityRefused)
	}
	if view.Identity.Outcome != nassimhubnode.IdentityOutcomeRefused || view.Identity.PinState != nassimhubnode.PinStateWithdrawn ||
		!strings.HasPrefix(view.LastError, "identity: ") || view.MessagesOnStick != 0 {
		t.Fatalf("refused view: %+v / %+v", view.Identity, view)
	}
	if err := g.fleet.SetWiFiEndpoint(ctx, view.DeviceID, ""); err != nil {
		t.Fatal(err)
	}

	// A different key, validly signed: refused as a substitution.
	if err := os.Remove(filepath.Join(node.stateDir, nodePQKeyFile)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	node.restartWith()
	status = g.sync(t, ctx)
	view = onlyNode(t, g)
	if view.Online || hasNodeLine(status) || view.Identity.PinState != nassimhubnode.PinStateChanged ||
		view.Identity.Outcome != nassimhubnode.IdentityOutcomeRefused || view.Identity.Fingerprint != pinnedFingerprint {
		t.Fatalf("a substituted key was not refused: %+v", view.Identity)
	}

	// The administrator accepts the rotation explicitly; the device returns.
	view, err := g.fleet.ResetPQPin(ctx, view.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Online || view.Identity.Outcome != nassimhubnode.IdentityOutcomeDual || view.Identity.Fingerprint == pinnedFingerprint {
		t.Fatalf("after accepting the rotation: %+v", view)
	}
	if status := g.sync(t, ctx); !hasNodeLine(status) {
		t.Fatal("the device did not return after the rotation was accepted")
	}

	// The gateway is lowered to STANDARD and stops presenting its key. The
	// Node pinned it, so the Node - correctly - refuses the gateway. Lowering
	// the level is not a way around a pin.
	g.db.Close()
	g = openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelStandard)
	g.sync(t, ctx)
	view = onlyNode(t, g)
	if view.Online || !strings.Contains(view.LastError, "post-quantum") {
		t.Fatalf("a gateway that dropped its pinned key was still accepted: online=%v error=%q", view.Online, view.LastError)
	}
	// Raising it again restores service with the same key.
	g.db.Close()
	g = openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelPQ)
	g.sync(t, ctx)
	if view = onlyNode(t, g); !view.Online || view.Identity.Outcome != nassimhubnode.IdentityOutcomeDual {
		t.Fatalf("after restoring the level: %+v", view)
	}

	// Unpairing forgets the pin with the rest of the trust.
	if _, err := g.fleet.Unpair(ctx, view.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := g.repository.NasSimHubNodePQPin(ctx, view.DeviceID); !errors.Is(err, store.ErrNasSimHubNodePQPinNotFound) {
		t.Fatalf("pin after unpair: %v", err)
	}
}

// A pairing made at STANDARD is upgraded in place when the level is raised.
func TestE2ERaisingTheLevelUpgradesAnExistingPairing(t *testing.T) {
	binary := pqBinary(t)
	ctx := context.Background()
	work := t.TempDir()
	usb, control := freePort(t), freePort(t)
	startNode(t, binary, filepath.Join(work, "node"), usb, control)
	dataDir := filepath.Join(work, "nas")

	g := openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelStandard)
	view := pairFirst(t, ctx, g)
	if view.Identity.CoreKeyPinned || view.Identity.CoreKeyOffered {
		t.Fatalf("STANDARD pairing: %+v", view.Identity)
	}
	coreID := g.fleet.CoreID()
	g.db.Close()

	g = openGatewayAt(t, ctx, dataDir, "https://"+usb, proto.LevelPQ)
	if g.fleet.CoreID() != coreID {
		t.Fatal("gaining a post-quantum key changed the core id")
	}
	status := g.sync(t, ctx)
	view = onlyNode(t, g)
	if !view.Online || !hasNodeLine(status) || !view.Identity.CoreKeyOffered || !view.Identity.CoreKeyPinned ||
		view.Identity.Outcome != nassimhubnode.IdentityOutcomeDual || !view.Identity.Enforced {
		t.Fatalf("after raising the level: %+v online=%v", view.Identity, view.Online)
	}
}

// PQ_EXTREME: nothing degrades, on either side.
func TestE2EExtremeLevelRequiresDualIdentityAndTheLargestGroup(t *testing.T) {
	binary := pqBinary(t)
	ctx := context.Background()
	work := t.TempDir()

	// A device with no post-quantum identity is not adopted at all.
	usb, control := freePort(t), freePort(t)
	classical := startNodeArgs(t, binary, filepath.Join(work, "classical"), usb, control,
		"-security-level", "PQ_EXTREME", "-pq-identity=false")
	g := openGatewayAt(t, ctx, filepath.Join(work, "nas-a"), "https://"+usb, proto.LevelPQExtreme)
	candidates, err := g.fleet.Discover(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].Ticket == "" {
		t.Fatalf("discover: %+v %v", candidates, err)
	}
	if _, err := g.fleet.Pair(ctx, nassimhubnode.PairRequest{
		Ticket: candidates[0].Ticket, DeviceID: candidates[0].DeviceID, Fingerprint: candidates[0].Fingerprint,
	}); !errors.Is(err, nassimhubnode.ErrNodeNeedsPQ) {
		t.Fatalf("PQ_EXTREME adopted a device without a post-quantum identity: %v", err)
	}
	if len(g.fleet.Nodes()) != 0 || classical.publicDocument()["pairing_state"] != "UNPAIRED" {
		t.Fatal("a refused adoption left state behind")
	}
	classical.stop()

	// Both sides at PQ_EXTREME: ML-DSA-87, SecP384r1MLKEM1024, pinned both ways.
	usb, control = freePort(t), freePort(t)
	// A device whose key was generated at a lower level holds ML-DSA-65. Raising
	// its level does not rotate the key, and PQ_EXTREME does not accept it.
	node := startNode(t, binary, filepath.Join(work, "extreme"), usb, control)
	node.restartWith("-security-level", "PQ_EXTREME")
	g = openGatewayAt(t, ctx, filepath.Join(work, "nas-b"), "https://"+usb, proto.LevelPQExtreme)
	candidates, err = g.fleet.Discover(ctx)
	if err != nil || len(candidates) != 1 || candidates[0].Ticket == "" {
		t.Fatalf("discover: %+v %v", candidates, err)
	}
	if _, err := g.fleet.Pair(ctx, nassimhubnode.PairRequest{
		Ticket: candidates[0].Ticket, DeviceID: candidates[0].DeviceID, Fingerprint: candidates[0].Fingerprint,
	}); !errors.Is(err, nassimhubnode.ErrNodeNeedsPQ) || !strings.Contains(err.Error(), "ml-dsa-87") {
		t.Fatalf("PQ_EXTREME adopted a device with an ml-dsa-65 identity: %v", err)
	}
	// The owner rotates the key deliberately; the device id does not change.
	deviceID := candidates[0].DeviceID
	if err := os.Remove(filepath.Join(node.stateDir, nodePQKeyFile)); err != nil {
		t.Fatal(err)
	}
	node.restartWith("-security-level", "PQ_EXTREME")
	if node.publicDocument()["device_id"] != deviceID {
		t.Fatal("rotating the post-quantum key changed the device id")
	}
	view := pairFirst(t, ctx, g)
	if !view.Online || view.Identity.Outcome != nassimhubnode.IdentityOutcomeDual || view.Identity.Algorithm != "ml-dsa-87" ||
		view.Identity.Policy != "required" || !view.Identity.CoreKeyPinned {
		t.Fatalf("identity at PQ_EXTREME: %+v", view.Identity)
	}
	if view.LinkSecurity.Group != "SecP384r1MLKEM1024" || view.LinkSecurity.PostQuantum != "yes" || view.LinkSecurity.NodePolicy != "required" {
		t.Fatalf("link security at PQ_EXTREME: %+v", view.LinkSecurity)
	}

	// A STANDARD gateway cannot adopt a PQ_EXTREME device: the handshake has
	// no group in common, and discovery reports nothing usable rather than a
	// device that would then fail.
	usb2, control2 := freePort(t), freePort(t)
	startNodeArgs(t, binary, filepath.Join(work, "strict"), usb2, control2, "-security-level", "PQ_EXTREME")
	standard := openGatewayAt(t, ctx, filepath.Join(work, "nas-c"), "https://"+usb2, proto.LevelStandard)
	found, _ := standard.fleet.Discover(ctx)
	for _, candidate := range found {
		if candidate.Ticket != "" {
			t.Fatalf("a STANDARD gateway was offered a PQ_EXTREME device: %+v", candidate)
		}
	}

	// A PQ gateway shares no key agreement group with it either (PQ offers
	// X25519MLKEM768; PQ_EXTREME accepts only SecP384r1MLKEM1024).
	pq := openGatewayAt(t, ctx, filepath.Join(work, "nas-d"), "https://"+usb2, proto.LevelPQ)
	found, _ = pq.fleet.Discover(ctx)
	for _, candidate := range found {
		if candidate.Ticket != "" {
			t.Fatalf("a PQ gateway was offered a PQ_EXTREME device: %+v", candidate)
		}
	}
}

// An agent that predates attestation: fine at STANDARD and PQ, visible as such.
func TestE2EAnOlderAgentIsReportedAsClassicalNotAsFailed(t *testing.T) {
	binary := pqBinary(t)
	ctx := context.Background()
	work := t.TempDir()
	usb, control := freePort(t), freePort(t)
	node := startNode(t, binary, filepath.Join(work, "node"), usb, control)
	node.restartWith("-pq-identity=false")

	g := openGatewayAt(t, ctx, filepath.Join(work, "nas"), "https://"+usb, proto.LevelPQ)
	view := pairFirst(t, ctx, g)
	if !view.Online || view.IdentityRefused || view.Identity.Outcome != nassimhubnode.IdentityOutcomeClassicalOnly ||
		view.Identity.PinState != nassimhubnode.PinStateNone || view.Identity.Reason != "" {
		t.Fatalf("an older device at PQ: %+v", view.Identity)
	}
	if status := g.sync(t, ctx); !hasNodeLine(status) {
		t.Fatal("an older device contributes no line at PQ")
	}
}
