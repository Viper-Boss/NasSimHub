package license

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The free daily allowance, counted here and nowhere else.
//
// THIS IS NOT THE FRAUD GUARD, and keeping them apart is the single most
// important thing about this file.
//
// The fraud guard (internal/httpapi/call_guard.go) limits how many DISTINCT
// destinations a gateway may reach in a day, because contacting many strangers
// is the shape of spam. It is lifted by typing "unlock" on the dial pad, on
// purpose: an operator who says "this is my own SIM and I know what I am
// doing" should be believed.
//
// This counter limits how many messages may be sent on the FREE tier, because
// unlimited sending is what a module certificate is sold for. If the unlock
// gesture lifted this too, the paywall would be bypassed by typing a word into
// the dial pad -- and it would be bypassed by exactly the kind of user who
// reads release notes. So there is no unlock here, no operator override, and
// the two counters keep separate files even though today they happen to share
// the number 3.
//
// They also count different things, which is worth stating because the shared
// number invites the assumption that one could replace the other. Ten messages
// to one person is one destination (the fraud guard allows it) and ten sends
// (this allowance does not).

// allowanceState is one day's usage.
type allowanceState struct {
	// Date is the UTC day the counts belong to. Stored as a string so a file
	// written by an older build stays readable, and so the rollover rule is
	// visible in the file rather than implied by a timestamp.
	Date string `json:"date"`
	// Used maps a feature name to how many times it has been used today.
	Used map[string]int `json:"used"`
}

// UsageCounter tracks free-tier usage against the catalogue's daily limits.
//
// Named for what it counts rather than what it enforces, because Allowance is
// already the catalogue's description of a limit and having two types called
// that would make every call site ambiguous about which one it holds.
type UsageCounter struct {
	mu    sync.Mutex
	path  string
	state allowanceState
	now   func() time.Time
}

// NewUsageCounter opens the usage file. A missing file is an unused day.
func NewUsageCounter(dataDirectory string) *UsageCounter {
	return &UsageCounter{
		path: filepath.Join(dataDirectory, "license", "allowance.json"),
		now:  time.Now,
	}
}

// load reads the file and rolls the day over if it has changed.
func (a *UsageCounter) load() {
	today := a.now().UTC().Format("2006-01-02")
	if a.state.Date == today && a.state.Used != nil {
		return
	}
	a.state = allowanceState{Date: today, Used: map[string]int{}}
	raw, err := os.ReadFile(a.path)
	if err != nil {
		return
	}
	var stored allowanceState
	if err := json.Unmarshal(raw, &stored); err != nil {
		return
	}
	// A file from another day is discarded rather than migrated: that IS the
	// daily reset.
	if stored.Date != today || stored.Used == nil {
		return
	}
	a.state = stored
}

func (a *UsageCounter) persist() {
	encoded, err := json.Marshal(a.state)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return
	}
	// Best effort. A usage count that fails to persist must not stop somebody
	// sending a message -- the counter exists to shape a free tier, not to
	// guard anything that matters.
	_ = os.WriteFile(a.path, encoded, 0o644)
}

// Used reports today's count for a feature.
func (a *UsageCounter) Used(feature Feature) int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.load()
	return a.state.Used[string(feature)]
}

// Remaining reports how many uses are left under a decision's limit.
//
// A decision with no limit returns -1 for "unlimited", which the caller must
// distinguish from 0. Returning 0 for unlimited would read as exhausted, which
// is the worst possible confusion in a function like this.
func (a *UsageCounter) Remaining(feature Feature, decision Decision) int {
	if decision.Limit == nil {
		return -1
	}
	remaining := decision.Limit.Count - a.Used(feature)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Consume records one use and reports whether it was within the allowance.
//
// Only a use that was WITHIN the allowance is counted. An earlier version of
// this incremented on every attempt, on the theory that a caller sitting at
// the boundary could otherwise retry forever -- which is simply wrong: once
// the count has reached the limit, every further attempt is refused whether or
// not it is also counted. All that counting refusals achieved was a Used()
// that reported 47 of 3 and a Refund() that had to guess which of those were
// real. Caught by sabotaging the code and finding the test that claimed to
// cover it did not fail.
//
// The CALLER must not perform the action when this returns false.
func (a *UsageCounter) Consume(feature Feature, decision Decision) bool {
	if a == nil {
		// No storage configured. Allowing is the right failure direction here:
		// this counter shapes a free tier, and a broken data directory should
		// not take away a feature somebody is entitled to.
		return decision.Allowed
	}
	if !decision.Allowed {
		return false
	}
	if decision.Limit == nil {
		return true
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.load()
	name := string(feature)
	if a.state.Used[name] >= decision.Limit.Count {
		return false
	}
	a.state.Used[name]++
	a.persist()
	return true
}

// Refund gives back one use.
//
// Needed because the allowance has to be consumed BEFORE the action -- two
// concurrent sends must not both see the last slot as free -- and the action
// can then fail for reasons that are nobody's fault: the modem was busy, the
// network rejected it. Charging somebody a day's allowance for a message that
// never left is the kind of small unfairness that is remembered.
func (a *UsageCounter) Refund(feature Feature) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.load()
	name := string(feature)
	if a.state.Used[name] > 0 {
		a.state.Used[name]--
		a.persist()
	}
}
