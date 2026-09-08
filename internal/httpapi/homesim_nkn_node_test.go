package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestNKNNodeManager(t *testing.T) *nknNodeManager {
	t.Helper()
	t.Setenv(nknNodeDirEnv, filepath.Join(t.TempDir(), "nkn-node"))
	return newNKNNodeManager(nil)
}

func TestNKNNodeSettingsRoundTrip(t *testing.T) {
	manager := newTestNKNNodeManager(t)
	settings, err := manager.loadSettings()
	if err != nil {
		t.Fatalf("loadSettings on missing file: %v", err)
	}
	if settings.Enabled || settings.BeneficiaryAddr != "" || settings.Revision != 0 {
		t.Fatalf("unexpected factory defaults: %+v", settings)
	}
	settings = nknNodeSettings{Enabled: true, BeneficiaryAddr: "NKNAbCdEf1234567890", Revision: 3}
	if err := manager.saveSettings(settings); err != nil {
		t.Fatalf("saveSettings: %v", err)
	}
	loaded, err := manager.loadSettings()
	if err != nil {
		t.Fatalf("loadSettings after save: %v", err)
	}
	if loaded != settings {
		t.Fatalf("round trip mismatch: got %+v want %+v", loaded, settings)
	}
}

func TestNKNNodeGeneratePassword(t *testing.T) {
	first, err := nknNodeGeneratePassword()
	if err != nil {
		t.Fatalf("nknNodeGeneratePassword: %v", err)
	}
	second, err := nknNodeGeneratePassword()
	if err != nil {
		t.Fatalf("nknNodeGeneratePassword: %v", err)
	}
	if len(first) != 32 || len(second) != 32 {
		t.Fatalf("password length mismatch: %d %d", len(first), len(second))
	}
	if first == second {
		t.Fatal("two generated passwords must differ")
	}
}

func TestNKNNodeWriteNodeConfig(t *testing.T) {
	manager := newTestNKNNodeManager(t)
	if err := os.MkdirAll(manager.dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := manager.writeNodeConfig(nknNodeSettings{BeneficiaryAddr: "NKNBeneficiary1"}); err != nil {
		t.Fatalf("writeNodeConfig: %v", err)
	}
	config, err := os.ReadFile(filepath.Join(manager.dir, nknNodeConfigFile))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	text := string(config)
	for _, want := range []string{
		`"BeneficiaryAddr": "NKNBeneficiary1"`,
		`"SyncMode": "light"`,
		`"PasswordFile": "wallet.pswd"`,
		`"HttpJsonPort": 30003`,
		`"LogLevel": 1`,
		`"SeedList": [`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config.json missing %s in:\n%s", want, text)
		}
	}
	// The official mainnet seed template ships 0001..0044 — all of them must
	// be present so the first launch has plenty of connect candidates.
	if got := strings.Count(text, "mainnet-seed-"); got != 44 {
		t.Fatalf("expected 44 official mainnet seeds in config.json, got %d", got)
	}
}

func TestNKNNodeParseSeedOverrides(t *testing.T) {
	seeds, err := nknNodeParseSeedOverrides("  http://10.0.0.1:30003 , http://my-node.local:30003\n")
	if err != nil {
		t.Fatalf("valid overrides rejected: %v", err)
	}
	if len(seeds) != 2 || seeds[0] != "http://10.0.0.1:30003" || seeds[1] != "http://my-node.local:30003" {
		t.Fatalf("unexpected parse result: %v", seeds)
	}
	if seeds, err := nknNodeParseSeedOverrides(""); err != nil || seeds != nil {
		t.Fatalf("empty input should yield nil, got %v err %v", seeds, err)
	}
	for _, bad := range []string{"ftp://x", "10.0.0.1:30003", "http://bad host:30003", "http://host:99999"} {
		if _, err := nknNodeParseSeedOverrides(bad); err == nil {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

func TestNKNNodeWriteNodeConfigSeedOverrides(t *testing.T) {
	manager := newTestNKNNodeManager(t)
	if err := os.MkdirAll(manager.dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Custom seeds lead the list in the admin's own order and official
	// duplicates are dropped.
	settings := nknNodeSettings{SeedOverrides: "http://cn-node.example.com:30003, http://mainnet-seed-0001.nkn.org:30003"}
	if err := manager.writeNodeConfig(settings); err != nil {
		t.Fatalf("writeNodeConfig: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(manager.dir, nknNodeConfigFile))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	var config nknNodeConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("unmarshal config.json: %v", err)
	}
	if len(config.SeedList) < 2 {
		t.Fatalf("expected merged seed list, got %v", config.SeedList)
	}
	if config.SeedList[0] != "http://cn-node.example.com:30003" {
		t.Fatalf("custom seed should come first, got %v", config.SeedList[:2])
	}
	count := 0
	for _, seed := range config.SeedList {
		if seed == "http://mainnet-seed-0001.nkn.org:30003" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("official seed duplicated after merge: %d occurrences", count)
	}
}

func TestNKNNodeFormatBalance(t *testing.T) {
	cases := map[string]string{
		"1bc16d674ec80000": "2.00 NKN", // 2 * 1e8
		"05f5e100":         "1.00 NKN", // 1e8
		"3b9aca00":         "1.00 NKN", // 1e8 without padding
		"00":               "0.00 NKN",
		"0x2a":             "0.00 NKN", // 42 units
		"":                 "",
		"not-hex":          "",
	}
	for input, want := range cases {
		if got := nknNodeFormatBalance(input); got != want {
			t.Errorf("nknNodeFormatBalance(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNKNNodeParseNodeInfo(t *testing.T) {
	state, relay := nknNodeParseNodeInfo(map[string]any{
		"syncState":         "syncing",
		"relayMessageCount": float64(4242),
	})
	if state != "syncing" || relay != 4242 {
		t.Fatalf("unexpected parse: state=%q relay=%d", state, relay)
	}
	state, relay = nknNodeParseNodeInfo(map[string]any{
		"SyncState":           "synced",
		"relay_message_count": float64(7),
	})
	if state != "synced" || relay != 7 {
		t.Fatalf("unexpected alt-casing parse: state=%q relay=%d", state, relay)
	}
	state, relay = nknNodeParseNodeInfo(map[string]any{})
	if state != "" || relay != 0 {
		t.Fatalf("empty result should yield zero values, got state=%q relay=%d", state, relay)
	}
}

func TestNKNNodeApplyValidatesBeneficiary(t *testing.T) {
	manager := newTestNKNNodeManager(t)
	if _, err := manager.apply(nknNodeUpdateRequest{Enabled: false, BeneficiaryAddr: "not-an-nkn-address"}); err == nil {
		t.Fatal("expected invalid beneficiary address to be rejected")
	}
	// On hosts without the binaries (test hosts) enabling must be rejected.
	if _, err := manager.apply(nknNodeUpdateRequest{Enabled: true, BeneficiaryAddr: ""}); err == nil && nknNodeBinaryAvailable() {
		t.Fatal("expected enabling without binaries to fail only when binaries are absent")
	}
}

func TestNKNNodeApplyRevisionConflict(t *testing.T) {
	manager := newTestNKNNodeManager(t)
	if err := manager.saveSettings(nknNodeSettings{Enabled: false, Revision: 5}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	if _, err := manager.apply(nknNodeUpdateRequest{Enabled: false, ExpectedRevision: 4}); err == nil {
		t.Fatal("expected revision conflict")
	}
	result, err := manager.apply(nknNodeUpdateRequest{Enabled: false, ExpectedRevision: 5})
	if err != nil {
		t.Fatalf("apply with matching revision: %v", err)
	}
	if result.Settings.Revision != 6 {
		t.Fatalf("revision should increment, got %d", result.Settings.Revision)
	}
}
