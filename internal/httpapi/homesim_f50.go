package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/devicef50"
)

var (
	errF50NotConfigured  = errors.New("F50 device key or password is not configured")
	errF50StorageMissing = errors.New("NasSimHub server storage is unavailable")
)

// The F50 (ZTE 5G MiFi) is the second supported device type. It connects
// over HTTP to the stock ZTE web backend instead of the local USB modem,
// so its settings live in the HomeSIM data directory and its password is
// resolved from an environment variable at request time (never stored).

type homeSIMF50Settings struct {
	Revision      int64  `json:"revision"`
	BaseURL       string `json:"base_url"`
	Username      string `json:"username"`
	CredentialRef string `json:"credential_ref"`
}

func defaultHomeSIMF50Settings() homeSIMF50Settings {
	return homeSIMF50Settings{
		Revision:      1,
		BaseURL:       "http://192.168.0.1",
		Username:      "admin",
		CredentialRef: "HOMESIM_F50_PASSWORD",
	}
}

func validHomeSIMF50Settings(settings homeSIMF50Settings) bool {
	if !strings.HasPrefix(settings.BaseURL, "http://") && !strings.HasPrefix(settings.BaseURL, "https://") {
		return false
	}
	if strings.Contains(settings.BaseURL, "?") || strings.Contains(settings.BaseURL, "#") || strings.HasSuffix(settings.BaseURL, "/") {
		return false
	}
	if settings.Username == "" || len(settings.Username) > 64 {
		return false
	}
	if settings.CredentialRef == "" || len(settings.CredentialRef) > 128 || strings.ContainsAny(settings.CredentialRef, " \t\r\n=") {
		return false
	}
	return true
}

func (api *API) homeSIMF50Path() (string, bool) {
	directory, ok := api.homeSIMDataDirectory()
	if !ok {
		return "", false
	}
	return filepath.Join(directory, "f50.json"), true
}

func (api *API) loadHomeSIMF50Settings() homeSIMF50Settings {
	current := defaultHomeSIMF50Settings()
	path, ok := api.homeSIMF50Path()
	if !ok {
		return current
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return current
	}
	var loaded homeSIMF50Settings
	if err := json.Unmarshal(data, &loaded); err != nil || loaded.BaseURL == "" {
		return defaultHomeSIMF50Settings()
	}
	if loaded.Revision < 1 {
		loaded.Revision = 1
	}
	return loaded
}

func (api *API) homeSIMF50(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodPut {
		response.Header().Set("Allow", "GET, PUT")
		writeError(response, 405, "method_not_allowed", "Only GET and PUT are supported", "")
		return
	}
	path, ok := api.homeSIMF50Path()
	if !ok {
		writeError(response, 503, "integration_unavailable", "NasSimHub server storage is unavailable", "")
		return
	}
	api.homeSIMF50Mu.Lock()
	defer api.homeSIMF50Mu.Unlock()
	current := api.loadHomeSIMF50Settings()
	if request.Method == http.MethodPut {
		var next homeSIMF50Settings
		if !decodeJSONBody(response, request, &next) {
			return
		}
		if !validHomeSIMF50Settings(next) {
			writeError(response, 400, "invalid_argument", "Invalid F50 settings; base_url must be an http(s) origin and credential_ref must be an environment variable name", "settings")
			return
		}
		if next.Revision != current.Revision {
			writeError(response, 409, "revision_conflict", "F50 settings changed; reload before saving", "revision")
			return
		}
		next.Revision++
		if err := writeHomeSIMJSON(path, next); err != nil {
			api.writeInternalError(response, request, "save F50 settings", err)
			return
		}
		current = next
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, 200, map[string]any{
		"settings":     current,
		"password_set": os.Getenv(current.CredentialRef) != "",
		"device_kind":  "zte_f50",
		"capabilities": map[string]bool{"sms": true, "voice": false, "traffic": true},
	})
}

func (api *API) f50Client() (*devicef50.Client, error) {
	settings := api.loadHomeSIMF50Settings()
	password := strings.TrimSpace(os.Getenv(settings.CredentialRef))
	if password == "" {
		return nil, errF50NotConfigured
	}
	if _, ok := api.homeSIMF50Path(); !ok {
		return nil, errF50NotConfigured
	}
	username := settings.Username
	if username == "" {
		username = "admin"
	}
	return devicef50.New(settings.BaseURL, username, password), nil
}

func (api *API) writeF50Error(response http.ResponseWriter, request *http.Request, err error) {
	switch {
	case err == errF50NotConfigured:
		writeError(response, 400, "f50_not_configured", "Environment variable "+api.loadHomeSIMF50Settings().CredentialRef+" is not set on the server", "credential_ref")
	case err == errF50StorageMissing:
		writeError(response, 503, "integration_unavailable", "NasSimHub server storage is unavailable", "")
	default:
		writeError(response, 502, "f50_unreachable", "F50 device error: "+err.Error(), "")
	}
}

func (api *API) f50Status(response http.ResponseWriter, request *http.Request) {
	client, err := api.f50Client()
	if err != nil {
		api.writeF50Error(response, request, err)
		return
	}
	status, err := client.Status()
	if err != nil {
		api.writeF50Error(response, request, err)
		return
	}
	writeJSON(response, 200, map[string]any{"status": status})
}

func (api *API) f50SMSList(response http.ResponseWriter, request *http.Request) {
	client, err := api.f50Client()
	if err != nil {
		api.writeF50Error(response, request, err)
		return
	}
	page := 0
	if raw := request.URL.Query().Get("page"); raw != "" {
		page, _ = strconv.Atoi(raw)
	}
	messages, err := client.SMSList(page)
	if err != nil {
		api.writeF50Error(response, request, err)
		return
	}
	writeJSON(response, 200, map[string]any{"messages": messages})
}

func (api *API) f50SMSSend(response http.ResponseWriter, request *http.Request) {
	var payload struct {
		Number  string `json:"number"`
		Content string `json:"content"`
	}
	if !decodeJSONBody(response, request, &payload) {
		return
	}
	if strings.TrimSpace(payload.Number) == "" || strings.TrimSpace(payload.Content) == "" {
		writeError(response, 400, "invalid_argument", "number and content are required", "")
		return
	}
	client, err := api.f50Client()
	if err != nil {
		api.writeF50Error(response, request, err)
		return
	}
	if err := client.SendSMS(payload.Number, payload.Content); err != nil {
		api.writeF50Error(response, request, err)
		return
	}
	writeJSON(response, 200, map[string]any{"sent": true})
}

func (api *API) f50SMSRead(response http.ResponseWriter, request *http.Request) {
	var payload struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSONBody(response, request, &payload) {
		return
	}
	if len(payload.IDs) == 0 || len(payload.IDs) > 100 {
		writeError(response, 400, "invalid_argument", "ids must contain 1..100 message ids", "ids")
		return
	}
	client, err := api.f50Client()
	if err != nil {
		api.writeF50Error(response, request, err)
		return
	}
	if err := client.MarkRead(payload.IDs); err != nil {
		api.writeF50Error(response, request, err)
		return
	}
	writeJSON(response, 200, map[string]any{"read": true})
}
