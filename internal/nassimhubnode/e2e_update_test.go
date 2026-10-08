package nassimhubnode_test

// End-to-end tests of carrying an agent update to a Node.
//
// They run only when NSH_E2E_NODE_UPDATE_FROM and NSH_E2E_NODE_UPDATE_TO point
// at two mock-nassimhub-node binaries built with different versions
// (-ldflags "-X main.version=1.0.0" and "...=1.1.0"). The Node is a real
// process: it verifies the real signatures, stores the release, replaces
// itself with the new binary by exec and is restarted by this test the way
// systemd restarts the agent. Only the modem is simulated. Nothing here
// touches hardware, and passing here is not a device result.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/nassimhub-node/proto"
)

type updateBinaries struct{ from, to string }

func updateBinariesOrSkip(t *testing.T) updateBinaries {
	t.Helper()
	from, to := os.Getenv("NSH_E2E_NODE_UPDATE_FROM"), os.Getenv("NSH_E2E_NODE_UPDATE_TO")
	if from == "" || to == "" {
		t.Skip("set NSH_E2E_NODE_UPDATE_FROM and NSH_E2E_NODE_UPDATE_TO to two mock-nassimhub-node binaries of different versions")
	}
	return updateBinaries{from: from, to: to}
}

// supervisedNode keeps a Node process running, restarting it when it exits on
// its own - which is how an update takes effect.
type supervisedNode struct {
	t         *testing.T
	binary    string
	arguments []string
	usb       string

	mu       sync.Mutex
	cmd      *exec.Cmd
	stopped  bool
	restarts int
	done     chan struct{}
}

func superviseNode(t *testing.T, binary, stateDir, usb, control string, extra ...string) *supervisedNode {
	t.Helper()
	node := &supervisedNode{t: t, binary: binary, usb: usb, done: make(chan struct{}),
		arguments: append([]string{"-tls", "-platform", "msm8916", "-scenario", "unicom",
			"-state-dir", stateDir, "-usb-listen", usb, "-wifi-listen", "", "-control-listen", control}, extra...)}
	go node.loop()
	t.Cleanup(node.stop)
	node.waitListening()
	return node
}

func (n *supervisedNode) loop() {
	defer close(n.done)
	for {
		n.mu.Lock()
		if n.stopped {
			n.mu.Unlock()
			return
		}
		cmd := exec.Command(n.binary, n.arguments...)
		if err := cmd.Start(); err != nil {
			n.mu.Unlock()
			return
		}
		n.cmd = cmd
		n.mu.Unlock()
		_ = cmd.Wait()
		n.mu.Lock()
		n.restarts++
		n.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
}

func (n *supervisedNode) stop() {
	n.mu.Lock()
	n.stopped = true
	if n.cmd != nil && n.cmd.Process != nil {
		_ = n.cmd.Process.Kill()
	}
	n.mu.Unlock()
	<-n.done
}

func (n *supervisedNode) restartCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.restarts
}

func (n *supervisedNode) waitListening() {
	n.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if connection, err := net.DialTimeout("tcp", n.usb, 200*time.Millisecond); err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	n.t.Fatal("node did not start")
}

// publisher is a release signer for the test: the keys exist only in memory.
type publisher struct {
	t       *testing.T
	private ed25519.PrivateKey
	public  ed25519.PublicKey
	pq      *proto.MLDSASigner
}

func newPublisher(t *testing.T) *publisher {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p := &publisher{t: t, private: private, public: public}
	if proto.EnableStandardPQ() {
		seed, err := proto.NewMLDSASeed()
		if err != nil {
			t.Fatal(err)
		}
		signer, err := proto.NewMLDSASigner(proto.PQSigMLDSA87, seed)
		if err != nil {
			t.Fatal(err)
		}
		p.pq = signer
	}
	return p
}

func (p *publisher) keyrings() (proto.OTAKeyring, proto.OTAPQKeyring) {
	classical := proto.OTAKeyring{"test-2026": p.public}
	postQuantum := proto.OTAPQKeyring{}
	if p.pq != nil {
		postQuantum["test-pq-2026"] = p.pq.Identity()
	}
	return classical, postQuantum
}

func (p *publisher) keyFile(directory string) string {
	p.t.Helper()
	classical, postQuantum := p.keyrings()
	content, err := proto.EncodeOTAKeyFile(classical, postQuantum)
	if err != nil {
		p.t.Fatal(err)
	}
	path := filepath.Join(directory, "ota-keys.json")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		p.t.Fatal(err)
	}
	return path
}

func (p *publisher) release(id, version string, artifact []byte) []byte {
	p.t.Helper()
	return p.releaseSignedBy(p.private, id, version, artifact)
}

func (p *publisher) releaseSignedBy(key ed25519.PrivateKey, id, version string, artifact []byte) []byte {
	p.t.Helper()
	manifest := proto.OTAManifest{
		SchemaVersion: proto.OTASchemaVersion, ReleaseID: id, Version: version,
		Channel: proto.OTAChannelStable, Platform: proto.PlatformMSM8916, ReleasedAt: time.Now().UTC().Truncate(time.Second),
		ProtocolMajor: proto.ProtocolMajor, ProtocolMinor: proto.ProtocolMinor, MinCoreVersion: "1.0.0",
		Artifacts: []proto.OTAArtifact{{Kind: proto.OTAArtifactAgent, Name: "nassimhub-agent",
			SHA256: proto.ArtifactDigest(artifact), Size: int64(len(artifact)), URL: "https://invalid.example/agent", Arch: runtime.GOARCH}},
	}
	pqKey := ""
	if p.pq != nil {
		pqKey = "test-pq-2026"
	}
	var pqSigner proto.PQSigner
	if p.pq != nil {
		pqSigner = p.pq
	}
	signed, err := proto.SignManifestHybrid(manifest, "test-2026", key, pqKey, pqSigner)
	if err != nil {
		p.t.Fatal(err)
	}
	raw, err := json.Marshal(signed)
	if err != nil {
		p.t.Fatal(err)
	}
	return raw
}

func openReleaseStore(t *testing.T, directory string, classical proto.OTAKeyring, postQuantum proto.OTAPQKeyring) *nassimhubnode.NodeReleaseStore {
	t.Helper()
	store, err := nassimhubnode.OpenNodeReleaseStore(nassimhubnode.NodeReleaseStoreOptions{
		Dir: directory, Keyring: classical, PQKeyring: postQuantum,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestE2EAgentUpdateIsCarriedAppliedAndConfirmed(t *testing.T) {
	binaries := updateBinariesOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	signer := newPublisher(t)
	keyFile := signer.keyFile(root)
	usb, control := freePort(t), freePort(t)
	node := superviseNode(t, binaries.from, filepath.Join(root, "node"), usb, control, "-ota-keys", keyFile)
	g := openGatewayAt(t, ctx, filepath.Join(root, "nas"), "https://"+usb, proto.LevelStandard)
	paired := pairFirst(t, ctx, g)
	if paired.AgentVersion != "1.0.0" {
		t.Fatalf("the test needs a 1.0.0 base binary, the node reports %q", paired.AgentVersion)
	}

	before, err := g.fleet.NodeUpdateStatus(ctx, paired.DeviceID)
	if err != nil || !before.Reachable || !before.Supported || before.Status == nil {
		t.Fatalf("status before: %+v %v", before, err)
	}
	if before.Status.State != proto.OTAIdle || before.Status.ActiveRelease != "" || before.Status.Keys.Classical != 1 {
		t.Fatalf("a fresh device should be idle on its factory agent: %+v", before.Status)
	}

	artifact, err := os.ReadFile(binaries.to)
	if err != nil {
		t.Fatal(err)
	}
	classical, postQuantum := signer.keyrings()
	store := openReleaseStore(t, filepath.Join(root, "releases"), classical, postQuantum)
	release, err := store.ImportManifest(signer.release("r-1.1.0", "1.1.0", artifact))
	if err != nil {
		t.Fatalf("import manifest: %v", err)
	}
	if release.Gateway != nassimhubnode.GatewayVerified || release.ArtifactReady {
		t.Fatalf("release after manifest import: %+v", release)
	}
	if signer.pq != nil && release.Signatures != string(proto.OTADualSigned) {
		t.Fatalf("a dual-signed manifest should verify as hybrid, got %q", release.Signatures)
	}
	if _, err := g.fleet.PushNodeUpdate(ctx, store, paired.DeviceID, "r-1.1.0"); !errors.Is(err, nassimhubnode.ErrNodeReleaseIncomplete) {
		t.Fatalf("a release without its artifact must not be pushed: %v", err)
	}
	if _, err := store.ImportArtifact("r-1.1.0", bytes.NewReader(append([]byte{0}, artifact[1:]...))); !errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) {
		t.Fatalf("an artifact that does not match the manifest must be refused: %v", err)
	}
	if release, err = store.ImportArtifact("r-1.1.0", bytes.NewReader(artifact)); err != nil || !release.ArtifactReady {
		t.Fatalf("import artifact: %+v %v", release, err)
	}

	pushed, err := g.fleet.PushNodeUpdate(ctx, store, paired.DeviceID, "r-1.1.0")
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if pushed.Status.State != proto.OTAReady || pushed.Status.TargetVersion != "1.1.0" || pushed.Status.CurrentVersion != "1.0.0" {
		t.Fatalf("after push the device should hold a validated release and still run the old agent: %+v", pushed.Status)
	}
	if node.restartCount() != 0 {
		t.Fatal("pushing an update must not restart the device")
	}
	if signer.pq != nil && pushed.Status.Signatures.Outcome != proto.OTADualSigned {
		t.Fatalf("the device should report the hybrid verification it made: %+v", pushed.Status.Signatures)
	}
	// Pushing again is harmless: nothing is transferred twice.
	if again, err := g.fleet.PushNodeUpdate(ctx, store, paired.DeviceID, "r-1.1.0"); err != nil || again.Status.State != proto.OTAReady {
		t.Fatalf("second push: %+v %v", again, err)
	}
	if _, err := g.fleet.ApplyNodeUpdate(ctx, paired.DeviceID, "r-other", nassimhubnode.NodeUpdateApplyOptions{}); !errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) {
		t.Fatalf("applying a release the device did not validate must be refused: %v", err)
	}

	applied, err := g.fleet.ApplyNodeUpdate(ctx, paired.DeviceID, "r-1.1.0",
		nassimhubnode.NodeUpdateApplyOptions{ReturnWithin: 60 * time.Second, PollEvery: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("apply: %v (%+v)", err, applied.Status)
	}
	if applied.Status.State != proto.OTAIdle || applied.Status.CurrentVersion != "1.1.0" ||
		applied.Status.ActiveRelease != "r-1.1.0" || applied.Status.FactoryVersion != "1.0.0" {
		t.Fatalf("after a confirmed update: %+v", applied.Status)
	}
	if node.restartCount() < 1 {
		t.Fatal("the update should have restarted the device")
	}
	after := onlyNode(t, g)
	if !after.Online || after.AgentVersion != "1.1.0" || after.DeviceID != paired.DeviceID || after.Fingerprint != paired.Fingerprint {
		t.Fatalf("the device should be the same device on the new agent: %+v", after)
	}

	// The update survives another restart of the device (a power cycle): the
	// factory agent starts, re-verifies the release and chains into it.
	restarts := node.restartCount()
	node.mu.Lock()
	_ = node.cmd.Process.Kill()
	node.mu.Unlock()
	deadline := time.Now().Add(20 * time.Second)
	for {
		status, err := g.fleet.NodeUpdateStatus(ctx, paired.DeviceID)
		if err == nil && status.Reachable && node.restartCount() > restarts && status.Status != nil &&
			status.Status.CurrentVersion == "1.1.0" && status.Status.ActiveRelease == "r-1.1.0" && status.Status.State == proto.OTAIdle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after a power cycle the device should still run the confirmed release: %+v %v", status, err)
		}
		time.Sleep(300 * time.Millisecond)
	}

	// An older release is refused by the device: no downgrade.
	oldArtifact, err := os.ReadFile(binaries.from)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ImportManifest(signer.release("r-1.0.0", "1.0.0", oldArtifact)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ImportArtifact("r-1.0.0", bytes.NewReader(oldArtifact)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.fleet.PushNodeUpdate(ctx, store, paired.DeviceID, "r-1.0.0"); !errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) {
		t.Fatalf("a downgrade must be refused by the device: %v", err)
	}
}

func TestE2EAgentUpdateFromUnknownPublisherIsRefusedTwice(t *testing.T) {
	binaries := updateBinariesOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	signer := newPublisher(t)
	keyFile := signer.keyFile(root)
	usb, control := freePort(t), freePort(t)
	node := superviseNode(t, binaries.from, filepath.Join(root, "node"), usb, control, "-ota-keys", keyFile)
	g := openGatewayAt(t, ctx, filepath.Join(root, "nas"), "https://"+usb, proto.LevelStandard)
	paired := pairFirst(t, ctx, g)

	artifact, err := os.ReadFile(binaries.to)
	if err != nil {
		t.Fatal(err)
	}
	_, stranger, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forged := signer.releaseSignedBy(stranger, "r-forged", "1.1.0", artifact)

	// A gateway with release keys refuses to store it at all.
	classical, postQuantum := signer.keyrings()
	verifying := openReleaseStore(t, filepath.Join(root, "verifying"), classical, postQuantum)
	if _, err := verifying.ImportManifest(forged); !errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) {
		t.Fatalf("a gateway with keys must refuse a manifest it cannot verify: %v", err)
	}
	if len(verifying.Releases()) != 0 {
		t.Fatal("a refused manifest must not be stored")
	}

	// A gateway without keys carries it, says it verified nothing, and the
	// DEVICE refuses it.
	courier := openReleaseStore(t, filepath.Join(root, "courier"), nil, nil)
	release, err := courier.ImportManifest(forged)
	if err != nil || release.Gateway != nassimhubnode.GatewayUnverified || release.Signatures != "" {
		t.Fatalf("a gateway without keys must report the release as unverified: %+v %v", release, err)
	}
	if _, err := courier.ImportArtifact("r-forged", bytes.NewReader(artifact)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.fleet.PushNodeUpdate(ctx, courier, paired.DeviceID, "r-forged"); !errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) {
		t.Fatalf("the device must refuse a manifest from an unknown publisher: %v", err)
	}
	status, err := g.fleet.NodeUpdateStatus(ctx, paired.DeviceID)
	if err != nil || status.Status == nil || status.Status.CurrentVersion != "1.0.0" || status.Status.ActiveRelease != "" {
		t.Fatalf("a refused update must leave the device as it was: %+v %v", status, err)
	}
	if node.restartCount() != 0 {
		t.Fatal("a refused update must not restart the device")
	}

	// A correctly signed release whose artifact is not an agent is refused by
	// the device when it tries to run it, and nothing is switched.
	junk := []byte("#!/bin/sh\nexit 3\n" + strings.Repeat("x", 4096))
	if _, err := verifying.ImportManifest(signer.release("r-junk", "1.2.0", junk)); err != nil {
		t.Fatal(err)
	}
	if _, err := verifying.ImportArtifact("r-junk", bytes.NewReader(junk)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.fleet.PushNodeUpdate(ctx, verifying, paired.DeviceID, "r-junk"); !errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) {
		t.Fatalf("an artifact that does not run as an agent must be refused: %v", err)
	}
	if _, err := g.fleet.ApplyNodeUpdate(ctx, paired.DeviceID, "r-junk", nassimhubnode.NodeUpdateApplyOptions{}); !errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) {
		t.Fatalf("a release that failed validation must not be applicable: %v", err)
	}
	status, err = g.fleet.NodeUpdateStatus(ctx, paired.DeviceID)
	if err != nil || status.Status == nil || status.Status.CurrentVersion != "1.0.0" || status.Status.ActiveRelease != "" {
		t.Fatalf("the device must still run its own agent: %+v %v", status, err)
	}
}

func TestE2ENodeWithoutReleaseKeysReportsUpdatesUnsupported(t *testing.T) {
	binaries := updateBinariesOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	root := t.TempDir()
	usb, control := freePort(t), freePort(t)
	superviseNode(t, binaries.from, filepath.Join(root, "node"), usb, control)
	g := openGatewayAt(t, ctx, filepath.Join(root, "nas"), "https://"+usb, proto.LevelStandard)
	paired := pairFirst(t, ctx, g)
	status, err := g.fleet.NodeUpdateStatus(ctx, paired.DeviceID)
	if err != nil || !status.Reachable || status.Supported || status.Reason == "" {
		t.Fatalf("a device without release keys must say updates are unsupported and why: %+v %v", status, err)
	}
	if _, err := g.fleet.NodeUpdateStatus(ctx, "nsh-unknown"); !errors.Is(err, nassimhubnode.ErrUnknownNode) {
		t.Fatalf("unknown node: %v", err)
	}
}
