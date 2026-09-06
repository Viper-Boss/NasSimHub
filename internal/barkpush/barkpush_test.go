package barkpush

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestSaveAndLoadSettings(t *testing.T) {
	dataDir := t.TempDir()
	settings := Settings{Revision: 3, ServerURL: "https://bark.example.com", CredentialRef: "HOMESIM_BARK_KEY", Group: "NasSimHub", Sound: "bell", EncryptEnabled: true, EncryptRef: "HOMESIM_BARK_AES_KEY"}
	if err := SaveSettings(dataDir, settings); err != nil {
		t.Fatal(err)
	}
	loaded := LoadSettings(dataDir)
	if loaded.ServerURL != "https://bark.example.com" || loaded.Revision != 3 || loaded.Group != "NasSimHub" || !loaded.EncryptEnabled {
		t.Fatalf("settings round-trip failed: %+v", loaded)
	}
	if LoadSettings(t.TempDir()).ServerURL != defaultServerURL {
		t.Fatal("missing file should return defaults")
	}
}

func TestNotifyIncomingSMSPushesOnce(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
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

	notifier.NotifyIncomingSMS(context.Background(), message, false)
	notifier.NotifyIncomingSMS(context.Background(), message, false) // 去重

	if len(paths) != 1 {
		t.Fatalf("expected 1 push, got %d", len(paths))
	}
	if paths[0] != "/device-key-123" {
		t.Fatalf("unexpected bark path: %s", paths[0])
	}
}

func TestNotifyUrgentUsesAlarmSound(t *testing.T) {
	var sounds []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		if sound, ok := payload["sound"].(string); ok {
			sounds = append(sounds, sound)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dataDir := t.TempDir()
	if err := SaveSettings(dataDir, Settings{Revision: 1, ServerURL: server.URL, CredentialRef: "TEST_BARK_KEY"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_BARK_KEY", "k")

	notifier := NewNotifier(dataDir, nil)
	notifier.NotifyIncomingSMS(context.Background(), MessageInfo{MessageID: "9", Peer: "13800138000", Content: "验证码 111111"}, true)
	if len(sounds) != 1 || sounds[0] != "alarm" {
		t.Fatalf("urgent sound wrong: %v", sounds)
	}
}

func TestNotifyEncryptedPush(t *testing.T) {
	var ciphertext []string
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		methods = append(methods, request.Method)
		var payload map[string]any
		_ = json.NewDecoder(request.Body).Decode(&payload)
		if ct, ok := payload["ciphertext"].(string); ok {
			ciphertext = append(ciphertext, ct)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dataDir := t.TempDir()
	if err := SaveSettings(dataDir, Settings{
		Revision:       1,
		ServerURL:      server.URL,
		CredentialRef:  "TEST_BARK_KEY",
		EncryptEnabled: true,
		EncryptRef:     "TEST_BARK_AES",
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_BARK_KEY", "device-key-123")
	t.Setenv("TEST_BARK_AES", "0123456789abcdef") // 16 字符 AES-128 key

	notifier := NewNotifier(dataDir, nil)
	notifier.NotifyIncomingSMS(context.Background(), MessageInfo{MessageID: "5", Peer: "13800138000", Content: "验证码 739264"}, false)

	if len(ciphertext) != 1 {
		t.Fatalf("expected 1 encrypted push, got %d", len(ciphertext))
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext[0])
	if err != nil {
		t.Fatalf("ciphertext not base64: %v", err)
	}
	if len(raw) <= 16 {
		t.Fatal("ciphertext too short to contain IV")
	}
	if methods[0] != http.MethodPost {
		t.Fatalf("encrypted push should use POST, got %s", methods[0])
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
	notifier.NotifyIncomingSMS(context.Background(), MessageInfo{MessageID: "1", Peer: "123", Content: "hi"}, false)
	if hits != 0 {
		t.Fatal("push attempted without credential")
	}
}

func TestEncryptedPushSkipsWhenAESKeyMissing(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits++
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dataDir := t.TempDir()
	if err := SaveSettings(dataDir, Settings{Revision: 1, ServerURL: server.URL, CredentialRef: "TEST_BARK_KEY", EncryptEnabled: true, EncryptRef: "MISSING_BARK_AES"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_BARK_KEY", "k")
	t.Setenv("MISSING_BARK_AES", "")
	os.Unsetenv("MISSING_BARK_AES")

	notifier := NewNotifier(dataDir, nil)
	notifier.NotifyIncomingSMS(context.Background(), MessageInfo{MessageID: "2", Peer: "123", Content: "hi"}, false)
	if hits != 0 {
		t.Fatal("encrypted push should be skipped without AES key")
	}
}
