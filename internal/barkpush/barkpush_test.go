package barkpush

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadSettings(t *testing.T) {
	dataDir := t.TempDir()
	settings := Settings{Revision: 3, ServerURL: "https://bark.example.com", CredentialRef: "HOMESIM_BARK_KEY", Group: "NasSimHub", Sound: "bell"}
	if err := SaveSettings(dataDir, settings); err != nil {
		t.Fatal(err)
	}
	loaded := LoadSettings(dataDir)
	if loaded.ServerURL != "https://bark.example.com" || loaded.Revision != 3 || loaded.Group != "NasSimHub" {
		t.Fatalf("settings round-trip failed: %+v", loaded)
	}
	if LoadSettings(t.TempDir()).ServerURL != defaultServerURL {
		t.Fatal("missing file should return defaults")
	}
}

func TestNotifyIncomingSMSPushesOnce(t *testing.T) {
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received = append(received, request.URL.Path)
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		if payload["body"] == nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dataDir := t.TempDir()
	settings := Settings{Revision: 1, ServerURL: server.URL, CredentialRef: "TEST_BARK_KEY", Group: "NasSimHub"}
	if err := SaveSettings(dataDir, settings); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_BARK_KEY", "device-key-123")

	notifier := NewNotifier(dataDir, nil)
	message := MessageInfo{MessageID: "42", Peer: "13800138000", Content: "验证码 739264"}

	notifier.NotifyIncomingSMS(context.Background(), message)
	notifier.NotifyIncomingSMS(context.Background(), message) // 去重

	if len(received) != 1 {
		t.Fatalf("expected 1 push, got %d", len(received))
	}
	if received[0] != "/device-key-123" {
		t.Fatalf("unexpected bark path: %s", received[0])
	}
}

func TestNotifySkipsWhenCredentialMissing(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits++
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dataDir := t.TempDir()
	if err := SaveSettings(dataDir, Settings{Revision: 1, ServerURL: server.URL, CredentialRef: "MISSING_BARK_KEY"}); err != nil {
		t.Fatal(err)
	}
	notifier := NewNotifier(dataDir, nil)
	notifier.NotifyIncomingSMS(context.Background(), MessageInfo{MessageID: "1", Peer: "123", Content: "hi"})
	if hits != 0 {
		t.Fatal("push attempted without credential")
	}
}

func TestSettingsFilePersistsUnderHomesimDirectory(t *testing.T) {
	dataDir := t.TempDir()
	if err := SaveSettings(dataDir, Settings{Revision: 2, ServerURL: "https://x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "homesim", "bark.json")); err != nil {
		t.Fatalf("bark.json missing: %v", err)
	}
}
