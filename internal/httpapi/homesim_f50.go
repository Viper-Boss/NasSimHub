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
// so its settings live in the HomeSIM data directory. The admin password
// is entered in the UI and sealed at rest with the server settings key
// (SecretBox, never returned by the API). As a legacy fallback the
// password is still resolved from HOMESIM_F50_PASSWORD when no sealed
// password has been saved.

type homeSIMF50Settings struct {
	Revision int64  `json:"revision"`
	BaseURL  string `json:"base_url"`
	Username string `json:"username"`
}

type homeSIMF50Secret struct {
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

type homeSIMF50Store struct {
	homeSIMF50Settings
	Password *homeSIMF50Secret `json:"password_secret,omitempty"`
}

const (
	homeSIMF50SecretAD    = "homesim-f50"
	homeSIMF50EnvPassword = "HOMESIM_F50_PASSWORD"
	homeSIMF50MaxPassword = 128
)

func defaultHomeSIMF50Settings() homeSIMF50Settings {
	return homeSIMF50Settings{
		Revision: 1,
		BaseURL:  "http://192.168.0.1",
		Username: "admin",
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
	return true
}

func (api *API) homeSIMF50Path() (string, bool) {
	directory, ok := api.homeSIMDataDirectory()
	if !ok {
		return "", false
	}
	return filepath.Join(directory, "f50.json"), true
}

func (api *API) loadHomeSIMF50Store() homeSIMF50Store {
	store := homeSIMF50Store{homeSIMF50Settings: defaultHomeSIMF50Settings()}
	path, ok := api.homeSIMF50Path()
	if !ok {
		return store
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return store
	}
	var loaded homeSIMF50Store
	if err := json.Unmarshal(data, &loaded); err != nil || loaded.BaseURL == "" {
		return homeSIMF50Store{homeSIMF50Settings: defaultHomeSIMF50Settings()}
	}
	if loaded.Revision < 1 {
		loaded.Revision = 1
	}
	if loaded.Username == "" {
		loaded.Username = "admin"
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
	current := api.loadHomeSIMF50Store()
	if request.Method == http.MethodPut {
		var payload struct {
			Revision int64  `json:"revision"`
			BaseURL  string `json:"base_url"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decodeJSONBody(response, request, &payload) {
			return
		}
		next := homeSIMF50Settings{Revision: payload.Revision, BaseURL: payload.BaseURL, Username: payload.Username}
		if !validHomeSIMF50Settings(next) {
			writeError(response, 400, "invalid_argument", "Invalid F50 settings; base_url must be an http(s) origin and username must not be empty", "settings")
			return
		}
		if next.Revision != current.Revision {
			writeError(response, 409, "revision_conflict", "F50 settings changed; reload before saving", "revision")
			return
		}
		store := homeSIMF50Store{homeSIMF50Settings: next, Password: current.Password}
		if strings.TrimSpace(payload.Password) != "" {
			if api.settingsSecrets == nil {
				writeError(response, 503, "integration_unavailable", "Settings encryption is unavailable", "")
				return
			}
			if len(payload.Password) > homeSIMF50MaxPassword {
				writeError(response, 400, "invalid_argument", "F50 password is too long", "password")
				return
			}
			nonce, ciphertext, err := api.settingsSecrets.Seal([]byte(payload.Password), []byte(homeSIMF50SecretAD))
			if err != nil {
				api.writeInternalError(response, request, "seal F50 password", err)
				return
			}
			store.Password = &homeSIMF50Secret{Nonce: nonce, Ciphertext: ciphertext}
		}
		next.Revision++
		if err := writeHomeSIMJSON(path, store); err != nil {
			api.writeInternalError(response, request, "save F50 settings", err)
			return
		}
		current = store
	}
	passwordSet := current.Password != nil || os.Getenv(homeSIMF50EnvPassword) != ""
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, 200, map[string]any{
		"settings":     current.homeSIMF50Settings,
		"password_set": passwordSet,
		"device_kind":  "zte_f50",
		"capabilities": map[string]bool{"sms": true, "voice": false, "traffic": true},
	})
}

func (api *API) f50Client() (*devicef50.Client, error) {
	store := api.loadHomeSIMF50Store()
	password := ""
	if store.Password != nil && api.settingsSecrets != nil {
		plaintext, err := api.settingsSecrets.Open(store.Password.Nonce, store.Password.Ciphertext, []byte(homeSIMF50SecretAD))
		if err == nil {
			password = strings.TrimSpace(string(plaintext))
		}
	}
	if password == "" {
		password = strings.TrimSpace(os.Getenv(homeSIMF50EnvPassword))
	}
	if password == "" {
		return nil, errF50NotConfigured
	}
	if _, ok := api.homeSIMF50Path(); !ok {
		return nil, errF50NotConfigured
	}
	username := store.Username
	if username == "" {
		username = "admin"
	}
	return devicef50.New(store.BaseURL, username, password), nil
}

func (api *API) writeF50Error(response http.ResponseWriter, request *http.Request, err error) {
	switch {
	case err == errF50NotConfigured:
		writeError(response, 400, "f50_not_configured", "F50 admin password is not configured; set it in the connection settings", "password")
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
