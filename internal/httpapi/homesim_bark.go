package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/diagnostics"
)

// Bark is an iOS push channel. Following the NasSimHub integration rules the
// device key itself is never stored on disk: only the name of the server
// environment variable holding it (credential_ref), resolved at send time.

const defaultBarkServerURL = "https://api.day.app"

const barkRequestTimeout = 10 * time.Second

type homeSIMBarkSettings struct {
	Revision      int64  `json:"revision"`
	ServerURL     string `json:"server_url"`
	CredentialRef string `json:"credential_ref"`
	Group         string `json:"group"`
	Sound         string `json:"sound"`
}

func defaultHomeSIMBarkSettings() homeSIMBarkSettings {
	return homeSIMBarkSettings{
		Revision:      1,
		ServerURL:     defaultBarkServerURL,
		CredentialRef: "HOMESIM_BARK_KEY",
		Group:         "NasSimHub",
		Sound:         "",
	}
}

func validHomeSIMBarkSettings(settings homeSIMBarkSettings) bool {
	if settings.CredentialRef == "" || len(settings.CredentialRef) > 128 {
		return false
	}
	if strings.ContainsAny(settings.CredentialRef, " \t\r\n=") {
		return false
	}
	endpoint, err := url.Parse(settings.ServerURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return false
	}
	return len(settings.Group) <= 32 && len(settings.Sound) <= 64
}

func (api *API) homeSIMBarkPath() (string, bool) {
	directory, ok := api.homeSIMDataDirectory()
	if !ok {
		return "", false
	}
	return filepath.Join(directory, "bark.json"), true
}

func (api *API) loadHomeSIMBarkSettings() homeSIMBarkSettings {
	current := defaultHomeSIMBarkSettings()
	path, ok := api.homeSIMBarkPath()
	if !ok {
		return current
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return current
	}
	var loaded homeSIMBarkSettings
	if err := json.Unmarshal(data, &loaded); err != nil || loaded.ServerURL == "" {
		return defaultHomeSIMBarkSettings()
	}
	if loaded.Revision < 1 {
		loaded.Revision = 1
	}
	return loaded
}

func (api *API) homeSIMBark(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodPut {
		response.Header().Set("Allow", "GET, PUT")
		writeError(response, 405, "method_not_allowed", "Only GET and PUT are supported", "")
		return
	}
	path, ok := api.homeSIMBarkPath()
	if !ok {
		writeError(response, 503, "integration_unavailable", "NasSimHub server storage is unavailable", "")
		return
	}
	api.homeSIMBarkMu.Lock()
	defer api.homeSIMBarkMu.Unlock()
	current := api.loadHomeSIMBarkSettings()
	if request.Method == http.MethodPut {
		var next homeSIMBarkSettings
		if !decodeJSONBody(response, request, &next) {
			return
		}
		if !validHomeSIMBarkSettings(next) {
			writeError(response, 400, "invalid_argument", "Invalid Bark settings; server must be an HTTPS origin and credential_ref must be an environment variable name", "settings")
			return
		}
		if next.Revision != current.Revision {
			writeError(response, 409, "revision_conflict", "Bark settings changed; reload before saving", "revision")
			return
		}
		next.Revision++
		if err := writeHomeSIMJSON(path, next); err != nil {
			api.writeInternalError(response, request, "save Bark settings", err)
			return
		}
		current = next
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, 200, map[string]any{
		"settings":      current,
		"state":         "draft_only",
		"runtime_ready": false,
	})
}

// homeSIMBarkTest sends a real test push through the configured Bark
// server. The device key is resolved from the configured environment
// variable at send time, never from stored settings.
func (api *API) homeSIMBarkTest(response http.ResponseWriter, request *http.Request) {
	settings := api.loadHomeSIMBarkSettings()
	deviceKey := strings.TrimSpace(os.Getenv(settings.CredentialRef))
	if deviceKey == "" {
		writeError(response, 400, "push_not_configured", "Environment variable "+settings.CredentialRef+" is not set on the server", "credential_ref")
		return
	}
	payload := map[string]any{
		"title": "NAS猫窝",
		"body":  "NAS猫窝测试推送：Bark 通道工作正常。",
	}
	if settings.Group != "" {
		payload["group"] = settings.Group
	}
	if settings.Sound != "" {
		payload["sound"] = settings.Sound
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		api.writeInternalError(response, request, "encode Bark test payload", err)
		return
	}
	target := strings.TrimRight(settings.ServerURL, "/") + "/" + deviceKey
	barkRequest, err := http.NewRequestWithContext(request.Context(), http.MethodPost, target, bytes.NewReader(encoded))
	if err != nil {
		writeError(response, 400, "invalid_argument", "Invalid Bark server URL", "server_url")
		return
	}
	barkRequest.Header.Set("Content-Type", "application/json; charset=utf-8")
	client := &http.Client{Timeout: barkRequestTimeout, Transport: api.homeSIMOutboundTransport(request)}
	barkResponse, err := client.Do(barkRequest)
	if err != nil {
		// err quotes the request URL, whose last segment is the device key.
		writeError(response, 502, "push_failed", "Bark server unreachable: "+diagnostics.ScrubSecrets(err.Error()), "")
		return
	}
	defer barkResponse.Body.Close()
	if barkResponse.StatusCode < 200 || barkResponse.StatusCode >= 300 {
		writeError(response, 502, "push_failed", "Bark server returned HTTP "+strconv.Itoa(barkResponse.StatusCode), "")
		return
	}
	writeJSON(response, 200, map[string]any{"sent": true, "status": barkResponse.StatusCode})
}
