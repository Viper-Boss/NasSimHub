package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

// Line quota payloads live in their own JSON file so the real data source
// (plan SMS parsing or AT queries) can be wired in later without touching
// the bootstrap contract. The field is optional for clients: when a line
// has no entry the frontend simply hides the quota rings.

type lineQuotaPayload struct {
	DataUsed   float64 `json:"data_used"`
	DataTotal  float64 `json:"data_total"`
	SmsUsed    float64 `json:"sms_used"`
	SmsTotal   float64 `json:"sms_total"`
	VoiceUsed  float64 `json:"voice_used"`
	VoiceTotal float64 `json:"voice_total"`
}

type lineQuotaStore struct {
	Revision int64                       `json:"revision"`
	Quotas   map[string]lineQuotaPayload `json:"quotas"`
}

func defaultLineQuotaStore() lineQuotaStore {
	return lineQuotaStore{Revision: 1, Quotas: map[string]lineQuotaPayload{}}
}

// homeSIMDataDirectory resolves the HomeSIM data directory next to the
// contact backup storage. Returns ok=false when server storage is not
// configured (fixture mode).
func (api *API) homeSIMDataDirectory() (string, bool) {
	if api.contactBackupDirectory == "" {
		return "", false
	}
	directory := filepath.Join(filepath.Dir(filepath.Dir(api.contactBackupDirectory)), "homesim")
	return directory, true
}

func (api *API) homeSIMQuotaPath() (string, bool) {
	directory, ok := api.homeSIMDataDirectory()
	if !ok {
		return "", false
	}
	return filepath.Join(directory, "line-quotas.json"), true
}

func validLineQuotaPayload(quota lineQuotaPayload) bool {
	if quota.DataUsed < 0 || quota.SmsUsed < 0 || quota.VoiceUsed < 0 {
		return false
	}
	if quota.DataTotal < 0 || quota.SmsTotal < 0 || quota.VoiceTotal < 0 {
		return false
	}
	return quota.DataTotal > 0 || quota.SmsTotal > 0 || quota.VoiceTotal > 0
}

func validLineQuotaStore(store lineQuotaStore) bool {
	if len(store.Quotas) > 64 {
		return false
	}
	for lineID, quota := range store.Quotas {
		if lineID == "" || !validLineQuotaPayload(quota) {
			return false
		}
	}
	return true
}

func (api *API) readLineQuotaStore() lineQuotaStore {
	current := defaultLineQuotaStore()
	path, ok := api.homeSIMQuotaPath()
	if !ok {
		return current
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return current
	}
	var loaded lineQuotaStore
	if err := json.Unmarshal(data, &loaded); err != nil || loaded.Quotas == nil {
		return defaultLineQuotaStore()
	}
	if loaded.Revision < 1 {
		loaded.Revision = 1
	}
	return loaded
}

// loadLineQuotaMap is the read-only helper used by bootstrap serialization.
func (api *API) loadLineQuotaMap() map[string]lineQuotaPayload {
	return api.readLineQuotaStore().Quotas
}

func attachLineQuotas(responses []lineSummaryResponse, quotas map[string]lineQuotaPayload) []lineSummaryResponse {
	if len(quotas) == 0 {
		return responses
	}
	for index := range responses {
		if quota, ok := quotas[responses[index].ID]; ok {
			quota := quota
			responses[index].Quota = &quota
		}
	}
	return responses
}

func (api *API) homeSIMQuotas(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodPut {
		response.Header().Set("Allow", "GET, PUT")
		writeError(response, 405, "method_not_allowed", "Only GET and PUT are supported", "")
		return
	}
	path, ok := api.homeSIMQuotaPath()
	if !ok {
		writeError(response, 503, "integration_unavailable", "NasSimHub server storage is unavailable", "")
		return
	}
	api.homeSIMQuotaMu.Lock()
	defer api.homeSIMQuotaMu.Unlock()
	current := api.readLineQuotaStore()
	if request.Method == http.MethodPut {
		var next lineQuotaStore
		if !decodeJSONBody(response, request, &next) {
			return
		}
		if next.Quotas == nil {
			writeError(response, 400, "invalid_argument", "quotas object is required", "quotas")
			return
		}
		if !validLineQuotaStore(next) {
			writeError(response, 400, "invalid_argument", "Invalid quota payload; amounts must be non-negative and at least one total above zero per line", "quotas")
			return
		}
		if next.Revision != current.Revision {
			writeError(response, 409, "revision_conflict", "Quotas changed; reload before saving", "revision")
			return
		}
		next.Revision++
		if err := writeHomeSIMJSON(path, next); err != nil {
			api.writeInternalError(response, request, "save NasSimHub quotas", err)
			return
		}
		current = next
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, 200, map[string]any{"revision": current.Revision, "quotas": current.Quotas})
}
