package trustengine_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/trustengine"
)

// Tests of the r128 additions: what a device is told to enforce, the policy
// generation it is numbered with, and the hardware identifier a device
// reports. Offline, against a real SQLite database in a temporary directory.

func (f *fixture) observeHW(subject, sim, hardware string, elapsed time.Duration) {
	f.t.Helper()
	f.now = f.now.Add(elapsed)
	if err := f.engine.Observe(context.Background(), trustengine.Reading{
		SubjectID: subject, IdentityHash: "identity-of-" + subject, SIMHash: sim, HardwareHash: hardware, Healthy: true, Elapsed: elapsed,
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) policy(subject string) trustengine.DevicePolicy {
	f.t.Helper()
	policy, ok := f.engine.DevicePolicy(subject)
	if !ok {
		f.t.Fatalf("no device policy for %s", subject)
	}
	return policy
}

func denyList(policy trustengine.DevicePolicy) string {
	parts := make([]string, len(policy.Deny))
	for index, action := range policy.Deny {
		parts[index] = string(action)
	}
	return strings.Join(parts, ",")
}

func TestDevicePolicyIsDerivedFromTheEffectiveState(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, ok := f.engine.DevicePolicy(node); ok {
		t.Fatal("a device with no trust record has no policy to sign")
	}
	f.observe(node, "sim-a", true, 0)
	policy := f.policy(node)
	if policy.State != trustengine.Observation || denyList(policy) != "dial,dtmf,send_sms" || policy.Enforcement != trustengine.Enforce || policy.Generation != 0 || policy.Reason != "first binding" {
		t.Fatalf("observation: %+v", policy)
	}
	f.pass(node, "sim-a", 7*24*time.Hour)
	if policy = f.policy(node); policy.State != trustengine.Trusted || len(policy.Deny) != 0 || policy.Deny == nil {
		t.Fatalf("trusted: %+v", policy)
	}
	// Verification-code limit while trusted: only forwarding is denied.
	if err := f.engine.ObserveOTP(ctx, node, 11); err != nil {
		t.Fatal(err)
	}
	if policy = f.policy(node); policy.State != trustengine.Trusted || denyList(policy) != "forward_otp" || policy.Reason == "" {
		t.Fatalf("trusted with the code limit: %+v", policy)
	}
	if err := f.engine.ObserveOTP(ctx, node, 0); err != nil {
		t.Fatal(err)
	}
	// Restricted: the engine does not allow forwarding either.
	if err := f.engine.Restrict(ctx, node, "admin", "call 13800000000 about this, ticket 42", false); err != nil {
		t.Fatal(err)
	}
	policy = f.policy(node)
	if policy.State != trustengine.Restricted || denyList(policy) != "dial,dtmf,forward_otp,send_sms" {
		t.Fatalf("restricted: %+v", policy)
	}
	// The administrator's free text does not travel to the device.
	if policy.Reason != "restricted by an administrator" || strings.Contains(policy.Reason, "ticket") || strings.Contains(policy.Reason, "138") {
		t.Fatalf("reason sent to the device: %q", policy.Reason)
	}
	if err := f.engine.Restrict(ctx, node, "admin", "x", true); err != nil {
		t.Fatal(err)
	}
	if policy = f.policy(node); policy.State != trustengine.Quarantine || len(policy.Deny) != 4 {
		t.Fatalf("quarantine: %+v", policy)
	}
	if err := f.engine.Restore(ctx, node, "admin", "reviewed"); err != nil {
		t.Fatal(err)
	}
	if policy = f.policy(node); policy.State != trustengine.Observation || denyList(policy) != "dial,dtmf,send_sms" {
		t.Fatalf("restored: %+v", policy)
	}

	// Monitor mode is carried, with the real state.
	f.mode = trustengine.Monitor
	f.reopen()
	if policy = f.policy(node); policy.Enforcement != trustengine.Monitor || policy.State != trustengine.Observation || denyList(policy) != "dial,dtmf,send_sms" {
		t.Fatalf("monitor: %+v", policy)
	}
}

func TestPolicyGenerationIsMonotonicAndSurvivesARestart(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.engine.NextPolicyGeneration(ctx, node, 0); !errors.Is(err, trustengine.ErrUnknownSubject) {
		t.Fatalf("unknown subject: %v", err)
	}
	f.observe(node, "sim-a", true, 0)
	last := uint64(0)
	for range 5 {
		generation, err := f.engine.NextPolicyGeneration(ctx, node, 0)
		if err != nil || generation != last+1 {
			t.Fatalf("generation after %d: %d %v", last, generation, err)
		}
		last = generation
	}
	// Concurrent requests never get the same number.
	var group sync.WaitGroup
	var mu sync.Mutex
	seen := map[uint64]bool{}
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			generation, err := f.engine.NextPolicyGeneration(ctx, node, 0)
			mu.Lock()
			defer mu.Unlock()
			if err != nil || seen[generation] || generation <= 5 {
				t.Errorf("concurrent generation %d: %v (duplicate=%v)", generation, err, seen[generation])
			}
			seen[generation] = true
		}()
	}
	group.Wait()
	if got := f.policy(node).Generation; got != 25 {
		t.Fatalf("after 25 requests: %d", got)
	}

	// A restart continues from the stored number; the record still verifies.
	f.reopen()
	if view := f.subject(node); !view.RecordVerified || view.PolicyGeneration != 25 {
		t.Fatalf("after a restart: %+v", view.Record)
	}
	generation, err := f.engine.NextPolicyGeneration(ctx, node, 0)
	if err != nil || generation != 26 {
		t.Fatalf("first generation after a restart: %d %v", generation, err)
	}
	// A device that holds a higher generation than this record (a database
	// restored from a backup) is passed, not argued with.
	generation, err = f.engine.NextPolicyGeneration(ctx, node, 1000)
	if err != nil || generation != 1001 {
		t.Fatalf("moving past a device's generation: %d %v", generation, err)
	}
	generation, err = f.engine.NextPolicyGeneration(ctx, node, 3) // a lower floor changes nothing
	if err != nil || generation != 1002 {
		t.Fatalf("a lower floor: %d %v", generation, err)
	}
	if _, err := f.engine.NextPolicyGeneration(ctx, node, math.MaxUint64); !errors.Is(err, trustengine.ErrPolicyGenerationExhausted) {
		t.Fatalf("exhausted: %v", err)
	}
	f.reopen()
	if got := f.policy(node).Generation; got != 1002 {
		t.Fatalf("stored generation: %d", got)
	}
	// Handing out generations is not a state change and earns no trust.
	if view := f.subject(node); view.State != trustengine.Observation || view.StableSeconds != 0 {
		t.Fatalf("generations must not change the record's standing: %+v", view.Record)
	}
}

func TestPolicyGenerationAndHardwareHashAreCoveredByTheRecordSignature(t *testing.T) {
	for name, damage := range map[string]string{
		"policy generation lowered":  `UPDATE trust_records SET policy_generation = 1`,
		"policy generation cleared":  `UPDATE trust_records SET policy_generation = 0`,
		"hardware hash replaced":     `UPDATE trust_records SET hardware_hash = 'another'`,
		"hardware hash cleared":      `UPDATE trust_records SET hardware_hash = ''`,
		"modem changes cleared":      `UPDATE trust_records SET modem_changes = 0, risk_score = 0`,
		"all three cleared together": `UPDATE trust_records SET policy_generation = 0, hardware_hash = '', modem_changes = 0`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			f.observeHW(node, "sim-a", "hw-a", 0)
			f.observeHW(node, "sim-a", "hw-b", time.Minute)
			for range 3 {
				if _, err := f.engine.NextPolicyGeneration(ctx, node, 0); err != nil {
					t.Fatal(err)
				}
			}
			f.reopen()
			if !f.subject(node).RecordVerified {
				t.Fatal("setup: the record must verify before it is edited")
			}
			f.exec(damage)
			f.reopen()
			view := f.subject(node)
			if view.RecordVerified || view.Allowed[trustengine.ActionDial] {
				t.Fatalf("an edited record must not be believed: %+v", view)
			}
			policy := f.policy(node)
			if trustengineRank(policy.State) < trustengineRank(trustengine.Restricted) || len(policy.Deny) != 4 {
				t.Fatalf("the device is told the restrictive reading: %+v", policy)
			}
			// A generation can still be handed out for that restrictive
			// policy, and doing so does not quietly repair the record.
			before := policy.Generation
			generation, err := f.engine.NextPolicyGeneration(ctx, node, 0)
			if err != nil || generation != before+1 {
				t.Fatalf("generation on a damaged record: %d (was %d) %v", generation, before, err)
			}
			if f.subject(node).RecordVerified {
				t.Fatal("handing out a generation repaired a damaged record")
			}
			f.reopen()
			if view := f.subject(node); view.RecordVerified || view.PolicyGeneration != generation {
				t.Fatalf("after a restart the record is still damaged and the number is kept: %+v", view.Record)
			}
			if err := f.engine.Restore(ctx, node, "admin", "checked"); err != nil {
				t.Fatal(err)
			}
			if view := f.subject(node); !view.RecordVerified || view.PolicyGeneration != generation {
				t.Fatalf("restore re-signs with the generation kept: %+v", view.Record)
			}
		})
	}
}

// A record written before r128 has none of the new fields and was signed over
// the v1 bytes. Adding the columns must not turn it into a damaged record.
func TestARecordSignedBeforeTheNewColumnsStillVerifies(t *testing.T) {
	f := newFixture(t)
	updated := "2026-09-01T08:00:00Z"
	// The v1 encoding, written out independently of the engine.
	fields := []string{"nsh-trust-record-v1", node, "TRUSTED", "identity-of-" + node, "sim-a", "0", "0", "0", "0", "0", "0", "0", "false", "0", "false", "", "3", "", updated}
	signature := ed25519.Sign(f.key, []byte(strings.Join(fields, "\x1f")))
	f.exec(`INSERT INTO trust_records (subject_id, state, binding_hash, sim_hash, generation, updated_at, signature) VALUES (?, 'TRUSTED', ?, 'sim-a', 3, ?, ?)`,
		node, "identity-of-"+node, updated, signature)
	f.reopen()
	view := f.subject(node)
	if !view.RecordVerified || view.State != trustengine.Trusted || !view.Allowed[trustengine.ActionDial] || view.PolicyGeneration != 0 {
		t.Fatalf("a pre-r128 record must keep verifying: %+v", view)
	}
	// Its first policy generation and first hardware reading move it to the
	// v2 encoding, and it verifies after a restart.
	if generation, err := f.engine.NextPolicyGeneration(context.Background(), node, 0); err != nil || generation != 1 {
		t.Fatalf("first generation: %d %v", generation, err)
	}
	f.observeHW(node, "sim-a", "hw-a", time.Minute)
	f.reopen()
	if view = f.subject(node); !view.RecordVerified || view.State != trustengine.Trusted || view.ModemChanges != 0 || view.PolicyGeneration != 1 {
		t.Fatalf("after the first r128 writes: %+v", view.Record)
	}
	// The v1 signature is not accepted for a record that carries new fields.
	f.exec(`UPDATE trust_records SET policy_generation = 9, signature = ?`, signature)
	f.reopen()
	if f.subject(node).RecordVerified {
		t.Fatal("a v1 signature verified a record with r128 fields set")
	}
}

func TestHardwareChangeIsAModemChangeAndAbsenceIsNot(t *testing.T) {
	f := newFixture(t)
	f.observeHW(node, "sim-a", "", 0) // adopted before it reported anything
	f.observeHW(node, "sim-a", "hw-a", time.Minute)
	if view := f.subject(node); view.ModemChanges != 0 || view.RiskScore != 0 {
		t.Fatalf("the first hardware identifier seen is recorded without penalty: %+v", view.Record)
	}
	f.pass(node, "sim-a", 7*24*time.Hour) // readings without a hardware value
	if view := f.subject(node); view.State != trustengine.Trusted || view.ModemChanges != 0 {
		t.Fatalf("an absent value is not a change and does not stop trust being earned: %+v", view.Record)
	}
	f.observeHW(node, "sim-a", "hw-a", time.Minute)
	f.observeHW(node, "sim-a", "", time.Minute)
	f.observeHW(node, "sim-a", "hw-a", time.Minute)
	if view := f.subject(node); view.State != trustengine.Trusted || view.ModemChanges != 0 {
		t.Fatalf("the same value, with gaps, is not a change: %+v", view.Record)
	}

	// The same device identity, a different hardware identifier.
	f.observeHW(node, "sim-a", "hw-b", time.Minute)
	view := f.subject(node)
	if view.State != trustengine.Observation || view.ModemChanges != 1 || view.RiskScore != 3 || view.StableSeconds != 0 || view.RequiredSeconds != 7*24*3600 || view.SIMChanges != 0 {
		t.Fatalf("a module change restarts observation with risk +3: %+v", view.Record)
	}
	if view.Allowed[trustengine.ActionDial] || !view.Allowed[trustengine.ActionReceiveSMS] {
		t.Fatalf("after a module change the device receives and does not send: %+v", view.Allowed)
	}
	kinds := strings.Join(f.kinds(node), ",")
	if !strings.Contains(kinds, trustengine.EventModemChanged+","+trustengine.EventDowngrade) {
		t.Fatalf("journal: %s", kinds)
	}
	events, _ := f.engine.Events(context.Background(), 0, 500, node)
	for _, event := range events {
		if strings.Contains(event.Cause+event.Result, "hw-") {
			t.Fatalf("the journal holds a hardware hash: %+v", event)
		}
	}
	if policy := f.policy(node); policy.Reason != "radio module changed" || denyList(policy) != "dial,dtmf,send_sms" {
		t.Fatalf("device policy after a module change: %+v", policy)
	}
	// It survives a restart, and the absence of a value after the change is
	// still not a second change.
	f.reopen()
	f.observeHW(node, "sim-a", "", time.Minute)
	f.observeHW(node, "sim-a", "hw-b", time.Minute)
	if view = f.subject(node); view.ModemChanges != 1 || !view.RecordVerified {
		t.Fatalf("after a restart: %+v", view.Record)
	}
	// Repeats lengthen the window and reach quarantine like SIM changes do.
	f.observeHW(node, "sim-a", "hw-c", time.Minute)
	if view = f.subject(node); view.RiskScore != 6 || view.RequiredSeconds != 14*24*3600 {
		t.Fatalf("second module change: %+v", view.Record)
	}
	for index := range 5 {
		f.observeHW(node, "sim-a", fmt.Sprintf("hw-x%d", index), time.Minute)
	}
	if view = f.subject(node); view.State != trustengine.Quarantine || view.ModemChanges != 7 || view.RiskScore != 21 {
		t.Fatalf("at the threshold the device is quarantined: %+v", view.Record)
	}

	// A SIM change and a module change in one reading are both counted.
	g := newFixture(t)
	g.observeHW("NSH-410-TEST02", "sim-a", "hw-a", 0)
	g.observeHW("NSH-410-TEST02", "sim-b", "hw-b", time.Minute)
	if view := g.subject("NSH-410-TEST02"); view.SIMChanges != 1 || view.ModemChanges != 1 || view.RiskScore != 5 {
		t.Fatalf("both changes: %+v", view.Record)
	}
	// A different identity key is another device as far as the record can
	// tell: its hardware is recorded, not compared.
	if err := g.engine.Observe(context.Background(), trustengine.Reading{SubjectID: "NSH-410-TEST02", IdentityHash: "another-identity", HardwareHash: "hw-z", Healthy: true}); err != nil {
		t.Fatal(err)
	}
	if view := g.subject("NSH-410-TEST02"); view.ModemChanges != 1 || view.IdentityChanges != 1 {
		t.Fatalf("an identity change is not also a module change: %+v", view.Record)
	}
}

func TestOnChangeIsToldWhenThePolicyMayDiffer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	told := make(chan string, 16)
	f.engine.SetOnChange(func(subject string) { told <- subject })
	expect := func(what string, want bool) {
		t.Helper()
		select {
		case subject := <-told:
			if !want || subject != node {
				t.Fatalf("%s: unexpected notification for %q", what, subject)
			}
		case <-time.After(300 * time.Millisecond):
			if want {
				t.Fatalf("%s: no notification", what)
			}
		}
	}
	f.observe(node, "sim-a", true, 0)
	expect("first binding", true)
	f.observe(node, "sim-a", true, time.Minute)
	f.observe(node, "sim-a", true, time.Minute) // flushes progress; same policy
	expect("progress only", false)
	if _, err := f.engine.NextPolicyGeneration(ctx, node, 0); err != nil {
		t.Fatal(err)
	}
	expect("a generation", false)
	if err := f.engine.Restrict(ctx, node, "admin", "test", true); err != nil {
		t.Fatal(err)
	}
	expect("quarantine", true)
	if err := f.engine.Restore(ctx, node, "admin", "test"); err != nil {
		t.Fatal(err)
	}
	expect("restore", true)
	f.observeHW(node, "sim-a", "hw-a", time.Minute)
	expect("first hardware value", false)
}
