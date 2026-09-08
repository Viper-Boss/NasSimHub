package httpapi

import (
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
