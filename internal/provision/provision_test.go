package provision

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/license"
)

func print(machineID string, sources map[string]string) license.Fingerprint {
	return license.Fingerprint{MachineID: machineID, Sources: sources, Version: 1}
}

// The case this exists for: a data directory carried to another machine.
func TestADirectoryFromAnotherMachineIsDetected(t *testing.T) {
	stored := print("machine-a", map[string]string{
		"mmc-cid":    "aaaa1111",
		"dt-serial":  "serial-a",
		"os-machine": "uuid-a",
	})
	current := print("machine-b", map[string]string{
		"mmc-cid":    "bbbb2222",
		"dt-serial":  "serial-b",
		"os-machine": "uuid-b",
	})
	decision := Detect(stored, true, current, nil)
	if !decision.Cloned() {
		t.Fatalf("a directory from another machine was accepted: %+v", decision)
	}
	if len(decision.MatchingSources) != 0 {
		t.Errorf("nothing should match: %v", decision.MatchingSources)
	}
	if len(decision.DivergingSources) != 3 {
		t.Errorf("all three sources should diverge: %v", decision.DivergingSources)
	}
	if decision.Reason == "" {
		t.Error("a clone with no explanation is an accusation with no evidence")
	}
}

// The false positive that would be unrecoverable: a repaired board.
//
// Replace the storage in a machine whose SoC serial still reads, and the
// derived machine ID changes while that serial does not. Treating this as a
// clone would wipe a working, paid-for installation.
func TestARepairedBoardIsNotAClone(t *testing.T) {
	stored := print("machine-a", map[string]string{
		"mmc-cid":   "aaaa1111",
		"dt-serial": "serial-a",
	})
	current := print("machine-c", map[string]string{
		"mmc-cid":   "cccc3333", // storage replaced
		"dt-serial": "serial-a", // same board
	})
	decision := Detect(stored, true, current, nil)
	if decision.Cloned() {
		t.Fatal("a board with one replaced part was called a clone; this would wipe a working installation")
	}
	if len(decision.MatchingSources) != 1 || decision.MatchingSources[0] != "dt-serial" {
		t.Errorf("the surviving source was not reported: %v", decision.MatchingSources)
	}
	if decision.Reason == "" {
		t.Error("a partial change should still be explained to the operator")
	}
}

// "I cannot tell" must never become "you are a clone".
func TestAnUnreadableFingerprintIsNotAClone(t *testing.T) {
	stored := print("machine-a", map[string]string{"mmc-cid": "aaaa1111"})

	t.Run("collection failed", func(t *testing.T) {
		decision := Detect(stored, true, license.Fingerprint{}, errors.New("no hardware identifier"))
		if decision.Cloned() {
			t.Fatal("a machine with no readable identity was called a clone")
		}
	})
	t.Run("empty machine id", func(t *testing.T) {
		decision := Detect(stored, true, print("", nil), nil)
		if decision.Cloned() {
			t.Fatal("an empty current fingerprint was called a clone")
		}
	})
}

func TestAFreshInstallationIsReady(t *testing.T) {
	current := print("machine-a", map[string]string{"mmc-cid": "aaaa1111"})
	if decision := Detect(license.Fingerprint{}, false, current, nil); decision.Cloned() {
		t.Fatal("a fresh installation was called a clone")
	}
}

func TestTheSameMachineIsReady(t *testing.T) {
	same := print("machine-a", map[string]string{"mmc-cid": "aaaa1111"})
	if decision := Detect(same, true, same, nil); decision.Cloned() {
		t.Fatal("the same machine was called a clone")
	}
}

// A source that disappeared is absence, not disagreement. Counting it as
// divergence would turn a kernel upgrade that stopped exposing one sysfs path
// into a clone verdict.
func TestADisappearedSourceIsNotDivergence(t *testing.T) {
	stored := print("machine-a", map[string]string{
		"mmc-cid":   "aaaa1111",
		"dt-serial": "serial-a",
	})
	current := print("machine-a2", map[string]string{
		"mmc-cid": "aaaa1111", // still agrees
		// dt-serial no longer exposed
	})
	decision := Detect(stored, true, current, nil)
	if decision.Cloned() {
		t.Fatal("a vanished source produced a clone verdict")
	}
	for _, name := range decision.DivergingSources {
		if name == "dt-serial" {
			t.Error("an absent source was counted as diverging")
		}
	}
}

// ---------------------------------------------------------------------------
// Reset
// ---------------------------------------------------------------------------

func TestResetEmptiesEverything(t *testing.T) {
	directory := t.TempDir()
	// A spread of what really lives here, including a nested directory and a
	// dotfile, because a reset that missed either would leave an identity
	// behind.
	files := map[string]string{
		"modemdeck.db":                     "the database",
		"settings.json":                    "the nkn seed",
		"call_guard.json":                  "counters",
		"tls/server.key":                   "private key",
		"license/machine/certificate.json": "a certificate",
		"private/.secret":                  "something hidden",
	}
	for name, contents := range files {
		path := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := RequestReset(directory, Request{Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, pending := ResetPending(directory); !pending {
		t.Fatal("the marker was not detected")
	}
	if err := Reset(directory); err != nil {
		t.Fatalf("reset: %v", err)
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("the reset left files behind: %v", names)
	}
	if _, pending := ResetPending(directory); pending {
		t.Fatal("the marker survived its own reset, so every boot would wipe again")
	}
}

// A crash between the marker and the wipe must leave the instruction standing.
func TestAnInterruptedResetIsResumed(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "modemdeck.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RequestReset(directory, Request{Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	// Simulating the crash: the marker exists, nothing was removed.
	if _, pending := ResetPending(directory); !pending {
		t.Fatal("the marker did not survive")
	}
	if err := Reset(directory); err != nil {
		t.Fatal(err)
	}
	if _, pending := ResetPending(directory); pending {
		t.Fatal("still pending after a completed reset")
	}
}

// A corrupt marker still means a reset was requested: its PRESENCE is the
// decision, its contents are only for the log line afterwards.
func TestACorruptMarkerStillRequestsAReset(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(directory, MarkerName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, pending := ResetPending(directory); !pending {
		t.Fatal("a corrupt marker lost the instruction; the gateway would boot with the copied identity")
	}
}

// Requesting twice is not an error: the second request finds the reset already
// pending, which is the state it wanted.
func TestRequestingTwiceIsHarmless(t *testing.T) {
	directory := t.TempDir()
	if err := RequestReset(directory, Request{Reason: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := RequestReset(directory, Request{Reason: "second"}); err != nil {
		t.Fatalf("a second request failed: %v", err)
	}
	request, pending := ResetPending(directory)
	if !pending {
		t.Fatal("not pending")
	}
	if request.Reason != "first" {
		t.Errorf("the second request overwrote the first: %q", request.Reason)
	}
}

func TestResetRefusesAnEmptyPath(t *testing.T) {
	if err := Reset(""); err == nil {
		t.Fatal("reset accepted an empty data directory; that is a wipe of the working directory")
	}
	if err := RequestReset("", Request{}); err == nil {
		t.Fatal("a reset was requested with no data directory")
	}
}
