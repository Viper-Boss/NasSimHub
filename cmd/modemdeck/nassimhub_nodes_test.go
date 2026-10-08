package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/human-agent65535/modemdeck/internal/trustengine"
	"github.com/human-agent65535/nassimhub-node/proto"
)

func TestNodeCoreVersionNeverBlocksPairingOnABuildLabel(t *testing.T) {
	for input, want := range map[string]string{
		"":               "",
		"dev":            "",
		"1.15.0":         "1.15.0",
		"v1.15.0":        "1.15.0",
		"1.15.0-rc1":     "1.15.0-rc1",
		"fnos-local-0.1": "",
		"r122":           "",
	} {
		if got := nodeCoreVersion(input); got != want {
			t.Errorf("nodeCoreVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNodePollIntervalBounds(t *testing.T) {
	for raw, want := range map[string]int{"": 10, "2": 10, "3": 3, "300": 300, "301": 10, "x": 10} {
		t.Setenv("MODEMDECK_NASSIMHUB_POLL_SECONDS", raw)
		if got := int(nodePollInterval().Seconds()); got != want {
			t.Errorf("poll %q = %ds, want %ds", raw, got, want)
		}
	}
}

func TestTrustEnforcementRejectsATypoInsteadOfDefaulting(t *testing.T) {
	for raw, want := range map[string]trustengine.Enforcement{"": trustengine.Enforce, "enforce": trustengine.Enforce, " Monitor ": trustengine.Monitor} {
		if got, err := trustEnforcement(raw); err != nil || got != want {
			t.Fatalf("%q: %s %v", raw, got, err)
		}
	}
	for _, raw := range []string{"off", "none", "monitoring", "0"} {
		if _, err := trustEnforcement(raw); err == nil {
			t.Fatalf("%q must be an error, not a silent default", raw)
		}
	}
}

func TestNodeReleaseStoreKeyFileIsAllOrNothing(t *testing.T) {
	directory := t.TempDir()
	// No key file: the store opens and says it verifies nothing itself.
	releases, err := openNodeReleaseStore(filepath.Join(directory, "a"), "", proto.LevelStandard)
	if err != nil || releases.VerifiesReleases() {
		t.Fatalf("without keys: %v", err)
	}
	// A named file that is missing or damaged is an error, never "no keys".
	if _, err := openNodeReleaseStore(filepath.Join(directory, "b"), filepath.Join(directory, "missing.json"), proto.LevelStandard); err == nil {
		t.Fatal("a missing key file must be an error")
	}
	damaged := filepath.Join(directory, "damaged.json")
	if err := os.WriteFile(damaged, []byte(`{"version":1,"ed25519":{"k":"not-a-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openNodeReleaseStore(filepath.Join(directory, "c"), damaged, proto.LevelStandard); err == nil {
		t.Fatal("a damaged key file must be an error")
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	content, err := proto.EncodeOTAKeyFile(proto.OTAKeyring{"test": public}, nil)
	if err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(directory, "keys.json")
	if err := os.WriteFile(good, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if releases, err = openNodeReleaseStore(filepath.Join(directory, "d"), good, proto.LevelPQ); err != nil || !releases.VerifiesReleases() {
		t.Fatalf("with keys: %v", err)
	}
	// PQ_EXTREME demands dual-signed releases, which a classical-only key
	// file can never verify: refuse to start rather than refuse every release.
	if _, err := openNodeReleaseStore(filepath.Join(directory, "e"), good, proto.LevelPQExtreme); err == nil {
		t.Fatal("PQ_EXTREME with a classical-only key file must be an error")
	}
}
