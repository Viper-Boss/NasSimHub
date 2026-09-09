package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Anti-fraud dial guard: residential gateways are attractive for abuse, so
// outgoing dials are limited to a small number of distinct destinations per
// day. Calling the same number repeatedly (family, classmates, food delivery)
// is never restricted; only dialing many different numbers in one day trips
// the guard. The limit can be lifted by the operator via the dial pad
// ("unlock"), which is persisted server-side in call_guard.json.

const callGuardDailyLimit = 5

type callGuardState struct {
	Date     string   `json:"date"`
	Numbers  []string `json:"numbers"`
	Unlocked bool     `json:"unlocked"`
}

type callGuard struct {
	mu    sync.Mutex
	path  string
	state callGuardState
}

func (api *API) callGuardStore() (*callGuard, bool) {
	api.callGuardOnce.Do(func() {
		directory, ok := api.homeSIMDataDirectory()
		if !ok {
			return
		}
		api.callGuardInstance = newCallGuard(filepath.Join(directory, "call_guard.json"))
	})
	return api.callGuardInstance, api.callGuardInstance != nil
}

func newCallGuard(path string) *callGuard {
	guard := &callGuard{path: path}
	if data, err := os.ReadFile(path); err == nil {
		var state callGuardState
		if json.Unmarshal(data, &state) == nil && state.Date != "" {
			guard.state = state
		}
	}
	guard.rolloverLocked()
	return guard
}

func (g *callGuard) rolloverLocked() {
	today := time.Now().Format("2006-01-02")
	if g.state.Date != today {
		g.state.Date = today
		g.state.Numbers = nil
	}
}

func normalizeCallGuardNumber(number string) string {
	var builder strings.Builder
	for _, runeValue := range strings.TrimSpace(number) {
		switch runeValue {
		case ' ', '-', '(', ')', '.':
			continue
		}
		builder.WriteRune(runeValue)
	}
	return builder.String()
}

// Attempt records an outgoing dial target. It returns true when the call
// must be blocked because the daily distinct-number budget is exhausted and
// the guard has not been unlocked. Re-dialing a number used earlier today is
// always allowed.
func (g *callGuard) Attempt(number string) bool {
	normalized := normalizeCallGuardNumber(number)
	if normalized == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rolloverLocked()
	for _, existing := range g.state.Numbers {
		if existing == normalized {
			return false
		}
	}
	if !g.state.Unlocked && len(g.state.Numbers) >= callGuardDailyLimit {
		return true
	}
	g.state.Numbers = append(g.state.Numbers, normalized)
	g.saveLocked()
	return false
}

func (g *callGuard) Unlock() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state.Unlocked {
		return
	}
	g.state.Unlocked = true
	g.saveLocked()
}

func (g *callGuard) saveLocked() {
	data, err := json.MarshalIndent(g.state, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(g.path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(g.path, data, 0o600)
}

func (api *API) callGuardResource(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST is supported", "")
		return
	}
	if !api.requireAdmin(response, request) {
		return
	}
	guard, ok := api.callGuardStore()
	if !ok {
		writeError(response, http.StatusServiceUnavailable, "call_guard_unavailable", "The dial guard is unavailable", "")
		return
	}
	var input struct {
		Action string `json:"action"`
	}
	if !decodeJSONBody(response, request, &input) {
		return
	}
	if strings.TrimSpace(input.Action) != "unlock" {
		writeError(response, http.StatusBadRequest, "invalid_argument", "action must be unlock", "action")
		return
	}
	guard.Unlock()
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, map[string]bool{"unlocked": true})
}
