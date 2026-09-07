package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
)

// SIM PIN auto unlock persistence. The management UI enables auto unlock per
// line; the PIN is sealed with the settings key (chacha20poly1305) before it
// touches the disk. On change — and periodically, so an agent restart can
// never silently lose the configuration — the API pushes the stored PIN to
// the host agent, which submits it whenever the modem reports a SIM-PIN lock.
//
// The QDC507 re-locks its SIM after every module reboot. Without auto unlock
// the line stays PIN-locked, never registers, and IMS/VoLTE (the only voice
// path for telecom SIMs) silently dies until someone types the PIN again.

type simAutoUnlockSecret struct {
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

type simAutoUnlockEntry struct {
	Enabled   bool                 `json:"enabled"`
	Secret    *simAutoUnlockSecret `json:"secret,omitempty"`
	UpdatedAt string               `json:"updated_at,omitempty"`
}

type simAutoUnlockStore struct {
	Revision int64                          `json:"revision"`
	Entries  map[string]simAutoUnlockEntry `json:"entries"`
}

// SecretBox seals configuration secrets at rest; implemented by
// internal/secretbox.Box.
type SecretBox interface {
	Seal(plaintext, associatedData []byte) (nonce, ciphertext []byte, err error)
	Open(nonce, ciphertext, associatedData []byte) ([]byte, error)
}

const (
	simAutoUnlockMaxEntries      = 64
	simAutoUnlockSyncInterval    = time.Minute
	simAutoUnlockAssociatedData  = "sim-auto-unlock"
	reservedInternalRequestIDLen = 4
)

func validSIMAutoUnlockPIN(value string) bool {
	if len(value) < 4 || len(value) > 8 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func (api *API) simAutoUnlockPath() (string, bool) {
	directory, ok := api.homeSIMDataDirectory()
	if !ok {
		return "", false
	}
	return filepath.Join(directory, "sim-auto-unlock.json"), true
}

func (api *API) loadSIMAutoUnlockStore() simAutoUnlockStore {
	store := simAutoUnlockStore{Revision: 1, Entries: map[string]simAutoUnlockEntry{}}
	path, ok := api.simAutoUnlockPath()
	if !ok {
		return store
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return store
	}
	var loaded simAutoUnlockStore
	if err := json.Unmarshal(data, &loaded); err != nil {
		return store
	}
	if loaded.Revision < 1 {
		loaded.Revision = 1
	}
	if loaded.Entries == nil {
		loaded.Entries = map[string]simAutoUnlockEntry{}
	}
	return loaded
}

func writeSIMAutoUnlockStore(path string, store simAutoUnlockStore) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (api *API) decryptSIMAutoUnlockPIN(lineID string, entry simAutoUnlockEntry) (string, bool) {
	if api.settingsSecrets == nil || entry.Secret == nil || len(entry.Secret.Nonce) == 0 {
		return "", false
	}
	plaintext, err := api.settingsSecrets.Open(
		entry.Secret.Nonce,
		entry.Secret.Ciphertext,
		[]byte(simAutoUnlockAssociatedData+":"+lineID),
	)
	if err != nil {
		return "", false
	}
	return string(plaintext), true
}

type simAutoUnlockView struct {
	LineID    string `json:"line_id"`
	Enabled   bool   `json:"enabled"`
	HasPin    bool   `json:"has_pin"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Revision  int64  `json:"revision"`
}

type simAutoUnlockEnvelope struct {
	AutoUnlock simAutoUnlockView `json:"auto_unlock"`
}

type simAutoUnlockPutInput struct {
	RequestID string `json:"request_id"`
	Enabled   bool   `json:"enabled"`
	PIN       string `json:"pin,omitempty"`
	Revision  int64  `json:"revision"`
}

func (api *API) simAutoUnlock(response http.ResponseWriter, request *http.Request, lineID string) {
	switch request.Method {
	case http.MethodGet:
		api.simAutoUnlockGet(response, request, lineID)
	case http.MethodPut:
		api.simAutoUnlockPut(response, request, lineID)
	case http.MethodDelete:
		api.simAutoUnlockDelete(response, request, lineID)
	default:
		response.Header().Set("Allow", http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
		writeError(
			response,
			http.StatusMethodNotAllowed,
			"method_not_allowed",
			"Only GET, PUT, and DELETE are supported",
			"",
		)
	}
}

func (api *API) simAutoUnlockGet(response http.ResponseWriter, request *http.Request, lineID string) {
	if api.settingsSecrets == nil {
		writeError(response, http.StatusServiceUnavailable, "integration_unavailable", "Settings encryption is unavailable", "")
		return
	}
	store := api.loadSIMAutoUnlockStoreLocked()
	entry, found := store.Entries[lineID]
	view := simAutoUnlockView{LineID: lineID, Revision: store.Revision}
	if found {
		view.Enabled = entry.Enabled
		view.HasPin = entry.Secret != nil
		view.UpdatedAt = entry.UpdatedAt
	}
	// Lazy healing: if the agent lost the configuration (restart) while the
	// stored setting is still enabled, push it again.
	if api.lineServices != nil && view.Enabled && view.HasPin {
		if status, err := api.lineServices.SIMAutoUnlockStatus(request.Context(), lineID); err == nil && !status.Enabled {
			if pin, ok := api.decryptSIMAutoUnlockPIN(lineID, entry); ok {
				pushContext, cancel := context.WithTimeout(request.Context(), 15*time.Second)
				_, pushErr := api.lineServices.SetSIMAutoUnlock(pushContext, lineID, agentAutoUnlockRequest(newSIMAutoUnlockRequestID(), pin))
				cancel()
				if pushErr == nil {
					slog.Info("SIM auto unlock re-pushed to agent", "line_id", lineID)
				}
			}
		}
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, simAutoUnlockEnvelope{AutoUnlock: view})
}

func (api *API) simAutoUnlockPut(response http.ResponseWriter, request *http.Request, lineID string) {
	if api.settingsSecrets == nil {
		writeError(response, http.StatusServiceUnavailable, "integration_unavailable", "Settings encryption is unavailable", "")
		return
	}
	var input simAutoUnlockPutInput
	if !decodeJSONBody(response, request, &input) {
		return
	}
	if _, ok := commandRequestID(response, request, input.RequestID); !ok {
		return
	}
	if input.Enabled && !validSIMAutoUnlockPIN(input.PIN) {
		writeError(response, http.StatusBadRequest, "invalid_argument", "A 4-8 digit PIN is required to enable SIM auto unlock", "pin")
		return
	}

	api.simAutoUnlockMu.Lock()
	store := api.loadSIMAutoUnlockStore()
	if input.Revision != store.Revision {
		api.simAutoUnlockMu.Unlock()
		writeError(response, http.StatusConflict, "revision_conflict", "SIM auto unlock changed; reload before saving", "revision")
		return
	}
	entry, found := store.Entries[lineID]
	if !found {
		if len(store.Entries) >= simAutoUnlockMaxEntries {
			api.simAutoUnlockMu.Unlock()
			writeError(response, http.StatusConflict, "limit_exceeded", "Too many SIM auto unlock entries", "")
			return
		}
		entry = simAutoUnlockEntry{}
	}
	if input.Enabled {
		pin := input.PIN
		if pin == "" {
			if decrypted, ok := api.decryptSIMAutoUnlockPIN(lineID, entry); ok {
				pin = decrypted
			}
		}
		if !validSIMAutoUnlockPIN(pin) {
			api.simAutoUnlockMu.Unlock()
			writeError(response, http.StatusBadRequest, "invalid_argument", "A 4-8 digit PIN is required to enable SIM auto unlock", "pin")
			return
		}
		nonce, ciphertext, err := api.settingsSecrets.Seal(
			[]byte(pin),
			[]byte(simAutoUnlockAssociatedData+":"+lineID),
		)
		if err != nil {
			api.simAutoUnlockMu.Unlock()
			api.writeInternalError(response, request, "seal SIM auto unlock PIN", err)
			return
		}
		entry.Enabled = true
		entry.Secret = &simAutoUnlockSecret{Nonce: nonce, Ciphertext: ciphertext}
	} else {
		entry = simAutoUnlockEntry{Enabled: false}
	}
	entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	store.Revision++
	store.Entries[lineID] = entry
	path, ok := api.simAutoUnlockPath()
	if !ok {
		api.simAutoUnlockMu.Unlock()
		writeError(response, http.StatusServiceUnavailable, "integration_unavailable", "NasSimHub server storage is unavailable", "")
		return
	}
	if err := writeSIMAutoUnlockStore(path, store); err != nil {
		api.simAutoUnlockMu.Unlock()
		api.writeInternalError(response, request, "save SIM auto unlock", err)
		return
	}
	api.simAutoUnlockMu.Unlock()

	agentEnabled := api.pushSIMAutoUnlock(request.Context(), lineID, input.Enabled, input.PIN)
	entry, _ = store.Entries[lineID]
	view := simAutoUnlockView{
		LineID:    lineID,
		Enabled:   entry.Enabled,
		HasPin:    entry.Secret != nil,
		UpdatedAt: entry.UpdatedAt,
		Revision:  store.Revision,
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, simAutoUnlockEnvelope{AutoUnlock: view})
	slog.Info(
		"SIM auto unlock updated",
		"line_id", lineID,
		"enabled", input.Enabled,
		"agent_pushed", agentEnabled,
	)
}

func (api *API) simAutoUnlockDelete(response http.ResponseWriter, request *http.Request, lineID string) {
	if api.settingsSecrets == nil {
		writeError(response, http.StatusServiceUnavailable, "integration_unavailable", "Settings encryption is unavailable", "")
		return
	}
	api.simAutoUnlockMu.Lock()
	store := api.loadSIMAutoUnlockStore()
	if _, found := store.Entries[lineID]; !found {
		api.simAutoUnlockMu.Unlock()
		writeError(response, http.StatusNotFound, "not_found", "SIM auto unlock is not configured for this line", "")
		return
	}
	delete(store.Entries, lineID)
	store.Revision++
	path, ok := api.simAutoUnlockPath()
	if !ok {
		api.simAutoUnlockMu.Unlock()
		writeError(response, http.StatusServiceUnavailable, "integration_unavailable", "NasSimHub server storage is unavailable", "")
		return
	}
	if err := writeSIMAutoUnlockStore(path, store); err != nil {
		api.simAutoUnlockMu.Unlock()
		api.writeInternalError(response, request, "save SIM auto unlock", err)
		return
	}
	api.simAutoUnlockMu.Unlock()

	api.pushSIMAutoUnlock(request.Context(), lineID, false, "")
	writeJSON(response, http.StatusOK, simAutoUnlockEnvelope{AutoUnlock: simAutoUnlockView{
		LineID:   lineID,
		Revision: store.Revision,
	}})
}

// pushSIMAutoUnlock best-effort pushes the configuration to the host agent.
// Failures are tolerated: the periodic sync and the lazy GET healing will
// retry.
func (api *API) pushSIMAutoUnlock(ctx context.Context, lineID string, enabled bool, pin string) bool {
	if api.lineServices == nil {
		return false
	}
	pushContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var err error
	if enabled {
		if pin == "" {
			api.simAutoUnlockMu.Lock()
			store := api.loadSIMAutoUnlockStore()
			entry, found := store.Entries[lineID]
			api.simAutoUnlockMu.Unlock()
			if !found {
				return false
			}
			pin, _ = api.decryptSIMAutoUnlockPIN(lineID, entry)
		}
		if pin == "" {
			return false
		}
		_, err = api.lineServices.SetSIMAutoUnlock(pushContext, lineID, agentAutoUnlockRequest(newSIMAutoUnlockRequestID(), pin))
	} else {
		_, err = api.lineServices.ClearSIMAutoUnlock(pushContext, lineID)
	}
	if err != nil {
		slog.Warn("SIM auto unlock push to agent failed", "line_id", lineID, "enabled", enabled, "error", err)
		return false
	}
	return true
}

func agentAutoUnlockRequest(requestID, pin string) agentclient.SIMAutoUnlockRequest {
	return agentclient.SIMAutoUnlockRequest{
		RequestID: requestID,
		Enabled:   true,
		PIN:       pin,
	}
}

func newSIMAutoUnlockRequestID() string {
	return "sim-auto-unlock-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

// RunSIMAutoUnlockSync periodically reconciles the stored configuration with
// the host agent until ctx is cancelled. It is started by the server main.
func (api *API) RunSIMAutoUnlockSync(ctx context.Context) {
	if api.settingsSecrets == nil || api.lineServices == nil {
		return
	}
	ticker := time.NewTicker(simAutoUnlockSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			api.syncSIMAutoUnlockOnce(ctx)
		}
	}
}

func (api *API) syncSIMAutoUnlockOnce(ctx context.Context) {
	store := api.loadSIMAutoUnlockStoreLocked()
	for lineID, entry := range store.Entries {
		if !entry.Enabled || entry.Secret == nil {
			continue
		}
		status, err := api.lineServices.SIMAutoUnlockStatus(ctx, lineID)
		if err != nil || status.Enabled {
			continue
		}
		pin, ok := api.decryptSIMAutoUnlockPIN(lineID, entry)
		if !ok || pin == "" {
			continue
		}
		pushContext, cancel := context.WithTimeout(ctx, 15*time.Second)
		_, pushErr := api.lineServices.SetSIMAutoUnlock(pushContext, lineID, agentAutoUnlockRequest(newSIMAutoUnlockRequestID(), pin))
		cancel()
		if pushErr != nil {
			slog.Warn("SIM auto unlock sync push failed", "line_id", lineID, "error", pushErr)
			continue
		}
		slog.Info("SIM auto unlock synced to agent", "line_id", lineID)
	}
}

func (api *API) loadSIMAutoUnlockStoreLocked() simAutoUnlockStore {
	api.simAutoUnlockMu.Lock()
	defer api.simAutoUnlockMu.Unlock()
	return api.loadSIMAutoUnlockStore()
}
