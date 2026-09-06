package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

// homeSIMCallFilter persists the incoming-call filter policy. Execution
// runs on the NAS once a live call source is connected: whitelist first,
// then explicit blacklist, then (when a tag provider is configured) tag
// threshold matching. Every call id is evaluated exactly once while it is
// still ringing; the filter never hangs up a call the user already took.

type homeSIMCallFilter struct {
	Revision   int64    `json:"revision"`
	Mode       string   `json:"mode"` // off | blacklist | whitelist
	Whitelist  []string `json:"whitelist"`
	Blacklist  []string `json:"blacklist"`
	MinReports int      `json:"min_reports"`
}

func defaultHomeSIMCallFilter() homeSIMCallFilter {
	return homeSIMCallFilter{
		Revision:   1,
		Mode:       "off",
		Whitelist:  []string{},
		Blacklist:  []string{},
		MinReports: 10,
	}
}

func validHomeSIMCallFilter(filter homeSIMCallFilter) bool {
	switch filter.Mode {
	case "off":
		return true
	case "blacklist", "whitelist":
	default:
		return false
	}
	if filter.MinReports < 1 || filter.MinReports > 1000 {
		return false
	}
	if len(filter.Blacklist) > 500 || len(filter.Whitelist) > 500 {
		return false
	}
	seen := map[string]bool{}
	for _, number := range append(append([]string{}, filter.Blacklist...), filter.Whitelist...) {
		if !homeSIMPhone.MatchString(number) || seen[number] {
			return false
		}
		seen[number] = true
	}
	return true
}

func (api *API) homeSIMCallFilterPath() (string, bool) {
	directory, ok := api.homeSIMDataDirectory()
	if !ok {
		return "", false
	}
	return filepath.Join(directory, "call-filter.json"), true
}

func (api *API) homeSIMCallFilter(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodPut {
		response.Header().Set("Allow", "GET, PUT")
		writeError(response, 405, "method_not_allowed", "Only GET and PUT are supported", "")
		return
	}
	path, ok := api.homeSIMCallFilterPath()
	if !ok {
		writeError(response, 503, "integration_unavailable", "HomeSIM server storage is unavailable", "")
		return
	}
	api.homeSIMCallFilterMu.Lock()
	defer api.homeSIMCallFilterMu.Unlock()
	current := defaultHomeSIMCallFilter()
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &current)
	}
	if err != nil && !os.IsNotExist(err) {
		api.writeInternalError(response, request, "read HomeSIM call filter", err)
		return
	}
	if request.Method == http.MethodPut {
		var next homeSIMCallFilter
		if !decodeJSONBody(response, request, &next) {
			return
		}
		if !validHomeSIMCallFilter(next) {
			writeError(response, 400, "invalid_argument", "Invalid call filter; mode must be off, blacklist or whitelist and numbers must be mainland mobile numbers", "filter")
			return
		}
		if next.Revision != current.Revision {
			writeError(response, 409, "revision_conflict", "Call filter changed; reload before saving", "revision")
			return
		}
		next.Revision++
		if next.Whitelist == nil {
			next.Whitelist = []string{}
		}
		if next.Blacklist == nil {
			next.Blacklist = []string{}
		}
		if err := writeHomeSIMJSON(path, next); err != nil {
			api.writeInternalError(response, request, "save HomeSIM call filter", err)
			return
		}
		current = next
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, 200, map[string]any{
		"filter":        current,
		"state":         "draft_only",
		"runtime_ready": false,
	})
}
