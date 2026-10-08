package nassimhubnode_test

// End-to-end: a device that REQUIRES dual-signed updates keeps one.
//
// The defect this guards against was in the order of two start-up steps in
// the agent's main: the stored release was verified before the post-quantum
// provider was registered. A correctly dual-signed release was accepted while
// the agent was running, started once, and then failed verification at the
// next start and was rolled back. In-process tests registered the provider
// themselves and did not see it; only real processes start with an empty
// registry, so this test uses the real binaries.
//
// Same requirements and the same limits as e2e_update_test.go: two mock Node
// binaries (NSH_E2E_NODE_UPDATE_FROM / _TO, built from the current tree), a
// simulated modem, no hardware.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"github.com/human-agent65535/nassimhub-node/proto"
)

func TestE2EDualSignedUpdateSurvivesRestartsWhenPostQuantumSignaturesAreRequired(t *testing.T) {
	binaries := updateBinariesOrSkip(t)
	for _, variant := range []struct {
		name      string
		level     proto.SecurityLevel
		arguments []string
	}{
		{"ota_signature_policy required", proto.LevelStandard, []string{"-ota-signature-policy", "required"}},
		{"security level PQ_EXTREME", proto.LevelPQExtreme, []string{"-security-level", "PQ_EXTREME"}},
	} {
		t.Run(variant.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			root := t.TempDir()
			signer := newPublisher(t)
			if signer.pq == nil {
				t.Skip("this toolchain has no crypto/mldsa; a dual-signed release cannot be made")
			}
			usb, control := freePort(t), freePort(t)
			node := superviseNode(t, binaries.from, filepath.Join(root, "node"), usb, control,
				append([]string{"-ota-keys", signer.keyFile(root)}, variant.arguments...)...)
			g := openGatewayAt(t, ctx, filepath.Join(root, "nas"), "https://"+usb, variant.level)
			paired := pairFirst(t, ctx, g)
			id := paired.DeviceID
			if paired.AgentVersion != "1.0.0" {
				t.Fatalf("the test needs a 1.0.0 base binary, the node reports %q", paired.AgentVersion)
			}
			before, err := g.fleet.NodeUpdateStatus(ctx, id)
			if err != nil || before.Status == nil || !before.Supported || before.Status.Signatures.Policy != proto.OTASignatureRequired ||
				!before.Status.Signatures.PQAvailable || before.Status.Keys.PostQuantum != 1 {
				t.Fatalf("the device must require dual-signed updates and be able to check them: %+v %v", before.Status, err)
			}

			artifact, err := os.ReadFile(binaries.to)
			if err != nil {
				t.Fatal(err)
			}
			classical, postQuantum := signer.keyrings()
			store := openReleaseStore(t, filepath.Join(root, "releases"), classical, postQuantum)

			// The policy is real: the same release with only the classical
			// signature is refused by the device.
			classicalOnly := *signer
			classicalOnly.pq = nil
			courier := openReleaseStore(t, filepath.Join(root, "courier"), nil, nil)
			if _, err := courier.ImportManifest(classicalOnly.release("r-classical", "1.1.0", artifact)); err != nil {
				t.Fatal(err)
			}
			if _, err := courier.ImportArtifact("r-classical", bytes.NewReader(artifact)); err != nil {
				t.Fatal(err)
			}
			if _, err := g.fleet.PushNodeUpdate(ctx, courier, id, "r-classical"); !errors.Is(err, nassimhubnode.ErrNodeReleaseRejected) {
				t.Fatalf("a classical-only release must be refused under REQUIRED: %v", err)
			}

			if _, err := store.ImportManifest(signer.release("r-1.1.0", "1.1.0", artifact)); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ImportArtifact("r-1.1.0", bytes.NewReader(artifact)); err != nil {
				t.Fatal(err)
			}
			pushed, err := g.fleet.PushNodeUpdate(ctx, store, id, "r-1.1.0")
			if err != nil || pushed.Status.State != proto.OTAReady || pushed.Status.Signatures.Outcome != proto.OTADualSigned {
				t.Fatalf("push of the dual-signed release: %+v %v", pushed.Status, err)
			}

			// Applied: the device restarts into 1.1.0 and is confirmed. This
			// restart is the first start-up check of the stored release.
			applied, err := g.fleet.ApplyNodeUpdate(ctx, id, "r-1.1.0",
				nassimhubnode.NodeUpdateApplyOptions{ReturnWithin: 60 * time.Second, PollEvery: 300 * time.Millisecond})
			if err != nil {
				t.Fatalf("apply: %v (%+v)", err, applied.Status)
			}
			if applied.Status.State != proto.OTAIdle || applied.Status.CurrentVersion != "1.1.0" ||
				applied.Status.ActiveRelease != "r-1.1.0" || applied.Status.FactoryVersion != "1.0.0" {
				t.Fatalf("after a confirmed update: %+v", applied.Status)
			}

			// Two more restarts (power cycles). Each begins in the 1.0.0
			// factory agent, which must verify BOTH signatures of the stored
			// release before it chains into it.
			for cycle := 1; cycle <= 2; cycle++ {
				restarts := node.restartCount()
				node.mu.Lock()
				_ = node.cmd.Process.Kill()
				node.mu.Unlock()
				deadline := time.Now().Add(20 * time.Second)
				for {
					status, err := g.fleet.NodeUpdateStatus(ctx, id)
					if err == nil && status.Reachable && node.restartCount() > restarts && status.Status != nil {
						if status.Status.CurrentVersion == "1.1.0" && status.Status.ActiveRelease == "r-1.1.0" && status.Status.State == proto.OTAIdle &&
							status.Status.Signatures.Policy == proto.OTASignatureRequired {
							break
						}
						t.Fatalf("power cycle %d: the device did not keep the dual-signed release: %+v", cycle, status.Status)
					}
					if time.Now().After(deadline) {
						t.Fatalf("power cycle %d: the device did not come back: %+v %v", cycle, status, err)
					}
					time.Sleep(200 * time.Millisecond)
				}
			}
			if after := onlyNode(t, g); after.AgentVersion != "1.1.0" || after.DeviceID != id {
				refreshed, _ := g.fleet.Refresh(ctx, id)
				if refreshed.AgentVersion != "1.1.0" {
					t.Fatalf("the device should be on the new agent: %+v", refreshed)
				}
			}
		})
	}
}
