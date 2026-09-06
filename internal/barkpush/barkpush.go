// Package barkpush sends iOS push notifications through a Bark server
// (https://bark.day.app style API) when new SMS arrive. Following the
// HomeSIM integration rules, the device key itself is never stored on
// disk: settings only reference the name of the server environment
// variable that holds it, resolved at send time.
package barkpush

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultServerURL = "https://api.day.app"
	requestTimeout   = 10 * time.Second
	recentCapacity   = 256
)

// Settings mirrors homesim/bark.json on the NAS data directory.
type Settings struct {
	Revision      int64  `json:"revision"`
	ServerURL     string `json:"server_url"`
	CredentialRef string `json:"credential_ref"`
	Group         string `json:"group"`
	Sound         string `json:"sound"`
}

func defaultSettings() Settings {
	return Settings{
		Revision:      1,
		ServerURL:     defaultServerURL,
		CredentialRef: "HOMESIM_BARK_KEY",
		Group:         "NasSimHub",
	}
}

// LoadSettings reads bark.json from the HomeSIM data directory. Returns
// the defaults when the file is missing or invalid.
func LoadSettings(dataDir string) Settings {
	current := defaultSettings()
	data, err := os.ReadFile(filepath.Join(dataDir, "homesim", "bark.json"))
	if err != nil {
		return current
	}
	var loaded Settings
	if err := json.Unmarshal(data, &loaded); err != nil || loaded.ServerURL == "" {
		return defaultSettings()
	}
	if loaded.Revision < 1 {
		loaded.Revision = 1
	}
	return loaded
}

// SaveSettings persists settings atomically (temp file + rename, 0600).
func SaveSettings(dataDir string, settings Settings) error {
	if err := os.MkdirAll(filepath.Join(dataDir, "homesim"), 0700); err != nil {
		return err
	}
	path := filepath.Join(dataDir, "homesim", "bark.json")
	file, err := os.CreateTemp(filepath.Join(dataDir, "homesim"), ".bark-*")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if err := json.NewEncoder(file).Encode(settings); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// Notifier pushes SMS notifications to the configured Bark server.
type Notifier struct {
	dataDir string
	client  *http.Client
	logger  interface{ Warn(msg string, args ...any) }

	mu     sync.Mutex
	recent map[string]struct{}
	order  []string
}

// NewNotifier creates a notifier bound to the HomeSIM data directory.
func NewNotifier(dataDir string, logger interface{ Warn(msg string, args ...any) }) *Notifier {
	return &Notifier{
		dataDir: dataDir,
		client:  &http.Client{Timeout: requestTimeout},
		logger:  logger,
		recent:  make(map[string]struct{}, recentCapacity),
	}
}

// MessageInfo is one incoming SMS notification request.
type MessageInfo struct {
	MessageID string
	Peer      string
	Content   string
}

func (n *Notifier) seen(messageID string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.recent[messageID]; ok {
		return true
	}
	n.recent[messageID] = struct{}{}
	n.order = append(n.order, messageID)
	if len(n.order) > recentCapacity {
		oldest := n.order[0]
		n.order = n.order[1:]
		delete(n.recent, oldest)
	}
	return false
}

// NotifyIncomingSMS pushes one incoming SMS. It never blocks longer than
// the request timeout and never panics the caller's snapshot commit path.
func (n *Notifier) NotifyIncomingSMS(ctx context.Context, message MessageInfo) {
	if n == nil || message.MessageID == "" {
		return
	}
	if n.seen(message.MessageID) {
		return
	}
	settings := LoadSettings(n.dataDir)
	key := strings.TrimSpace(os.Getenv(settings.CredentialRef))
	if key == "" {
		if n.logger != nil {
			n.logger.Warn("bark push skipped: credential environment variable is not set",
				"component", "barkpush",
				"credential_ref", settings.CredentialRef,
			)
		}
		return
	}
	title := strings.TrimSpace(message.Peer)
	if title == "" {
		title = "SMS"
	}
	body := message.Content
	if len([]rune(body)) > 220 {
		body = string([]rune(body)[:220]) + "…"
	}
	payload := map[string]any{"title": title, "body": body}
	if settings.Group != "" {
		payload["group"] = settings.Group
	}
	if settings.Sound != "" {
		payload["sound"] = settings.Sound
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	target := strings.TrimRight(settings.ServerURL, "/") + "/" + url.PathEscape(key)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(encoded)))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := n.client.Do(request)
	if err != nil {
		if n.logger != nil {
			n.logger.Warn("bark push failed", "component", "barkpush", "error", err)
		}
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if n.logger != nil {
			n.logger.Warn("bark push rejected", "component", "barkpush", "status", response.StatusCode)
		}
	}
}
