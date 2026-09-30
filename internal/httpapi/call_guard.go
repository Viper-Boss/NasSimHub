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

// Anti-fraud outbound guard: residential gateways are attractive for abuse, so
// outgoing calls and messages are each limited to a small number of DISTINCT
// destinations per day. Contacting the same number repeatedly (family,
// classmates, food delivery) is never restricted; only reaching many different
// numbers in one day trips the guard, because that is the shape of spam and
// fraud rather than of ordinary use.
//
// Both channels are guarded, with their own budget and their own file, because
// five calls and three messages are different amounts of the same kind of
// harm. The unlock is shared: an operator who has told the software they are
// not a spammer should not have to say it twice, and a partial unlock would
// leave them re-tripping a guard they thought they had lifted.
//
// The limit is lifted by typing "unlock" on the dial pad, persisted
// server-side. It is deliberately not a settings checkbox: the friction is the
// point, and a guard that is one obvious click away guards nothing.

const (
	callGuardDailyLimit    = 5
	messageGuardDailyLimit = 3
)

type callGuardState struct {
	Date     string   `json:"date"`
	Numbers  []string `json:"numbers"`
	Unlocked bool     `json:"unlocked"`
}

type callGuard struct {
	mu    sync.Mutex
	path  string
	limit int
	state callGuardState
}

func (api *API) callGuardStore() (*callGuard, bool) {
	api.callGuardOnce.Do(func() {
		directory, ok := api.homeSIMDataDirectory()
		if !ok {
			return
		}
		api.callGuardInstance = newCallGuard(
			filepath.Join(directory, "call_guard.json"),
			callGuardDailyLimit,
		)
	})
	return api.callGuardInstance, api.callGuardInstance != nil
}

// messageGuardStore is the same guard with its own budget and its own file, so
// a busy day of texting cannot consume the allowance for calls or the reverse.
func (api *API) messageGuardStore() (*callGuard, bool) {
	api.messageGuardOnce.Do(func() {
		directory, ok := api.homeSIMDataDirectory()
		if !ok {
			return
		}
		api.messageGuardInstance = newCallGuard(
			filepath.Join(directory, "message_guard.json"),
			messageGuardDailyLimit,
		)
	})
	return api.messageGuardInstance, api.messageGuardInstance != nil
}

func newCallGuard(path string, limit int) *callGuard {
	guard := &callGuard{path: path, limit: limit}
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
	if !g.state.Unlocked && len(g.state.Numbers) >= g.limit {
		return true
	}
	g.state.Numbers = append(g.state.Numbers, normalized)
	g.saveLocked()
	return false
}

// Remaining reports how many further distinct destinations are allowed today,
// and whether the guard is lifted.
//
// The UI needs this to warn while there is still something to do about it. A
// guard that only speaks when it blocks the send has already cost the operator
// the message they were writing.
func (g *callGuard) Remaining() (int, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rolloverLocked()
	if g.state.Unlocked {
		return 0, true
	}
	remaining := g.limit - len(g.state.Numbers)
	if remaining < 0 {
		remaining = 0
	}
	return remaining, false
}

// Usage reports today's distinct destinations, the budget, whether the guard
// is lifted, and the day the count belongs to.
//
// Read-only, and a count rather than the numbers: the dashboard needs to say
// how much of the allowance is gone, and has no business listing who was
// called.
func (g *callGuard) Usage() (used, limit int, unlocked bool, date string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rolloverLocked()
	return len(g.state.Numbers), g.limit, g.state.Unlocked, g.state.Date
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
	// The same gesture lifts both channels: the operator is saying who they
	// are, not which feature they want.
	if messages, ok := api.messageGuardStore(); ok {
		messages.Unlock()
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, map[string]bool{"unlocked": true})
}

// nextGuardRollover is the next local midnight after now: the instant
// rolloverLocked, which compares local calendar dates, starts a new day.
func nextGuardRollover(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
}

// moduleSecurity answers GET /api/v1/security/module, the dashboard's
// module-security card.
//
// It reports only what this gateway actually runs: the anti-fraud guard's
// budgets, today's use of them, and whether it has been lifted. The card's
// other rows -- a risk level, module and system integrity, secure boot, a
// trust period -- have nothing behind them in this product, so they are left
// out of the response rather than filled in, and the card says "not wired"
// for each. A field present here is a claim; an absent one is not.
func (api *API) moduleSecurity(response http.ResponseWriter, request *http.Request) {
	if !api.requireAdmin(response, request) {
		return
	}
	guard := map[string]any{}
	unlocked := false
	if calls, ok := api.callGuardStore(); ok {
		used, limit, lifted, date := calls.Usage()
		guard["call_used"], guard["call_limit"], guard["date"] = used, limit, date
		unlocked = unlocked || lifted
	}
	if messages, ok := api.messageGuardStore(); ok {
		used, limit, lifted, date := messages.Usage()
		guard["message_used"], guard["message_limit"] = used, limit
		if _, set := guard["date"]; !set {
			guard["date"] = date
		}
		unlocked = unlocked || lifted
	}
	body := map[string]any{}
	if len(guard) > 0 {
		// One unlock lifts both channels (see callGuardResource), so either
		// being lifted is the state worth reporting.
		guard["unlocked"] = unlocked
		// When today's counts go back to zero, as an absolute instant. Said by
		// the gateway rather than assumed by the browser: the rollover is the
		// GATEWAY's local midnight, and a container with no TZ set is on UTC
		// -- 08:00 in Beijing, not the midnight a reader would guess.
		guard["resets_at"] = nextGuardRollover(time.Now()).Format(time.RFC3339)
		body["guard"] = guard
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, body)
}
