package modemmanager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Traffic accounting that survives network interface recreation and agent
// restarts. The kernel resets /sys/class/net/*/statistics counters to zero
// whenever a USB ECM interface is re-created (every mobile-data toggle), so
// raw counters cannot be summed over time. The accumulator keeps a persisted
// baseline per interface name: whenever the raw counter drops below the last
// observed value a reset is detected and the previous reading is folded into
// the baseline. The returned value is baseline + raw, i.e. monotonic across
// resets as long as the state file survives.

type trafficCounters struct {
	RX uint64 `json:"rx"`
	TX uint64 `json:"tx"`
}

type trafficState struct {
	Baselines map[string]trafficCounters `json:"baselines"`
	Last      map[string]trafficCounters `json:"last"`
}

type trafficAccumulator struct {
	mu     sync.Mutex
	path   string
	state  trafficState
	loaded bool
}

func newTrafficAccumulator(path string) *trafficAccumulator {
	return &trafficAccumulator{
		path: path,
		state: trafficState{
			Baselines: make(map[string]trafficCounters),
			Last:      make(map[string]trafficCounters),
		},
	}
}

// load reads the persisted state once. A missing or corrupt file simply
// starts from zero.
func (a *trafficAccumulator) load() {
	if a.loaded || a.path == "" {
		a.loaded = true
		return
	}
	data, err := os.ReadFile(a.path)
	if err == nil {
		var state trafficState
		if json.Unmarshal(data, &state) == nil {
			if state.Baselines != nil {
				a.state.Baselines = state.Baselines
			}
			if state.Last != nil {
				a.state.Last = state.Last
			}
		}
	}
	a.loaded = true
}

// apply folds one raw observation into the cumulative counters and returns
// the monotonic rx/tx totals for the interface. The state file is rewritten
// on every observation: the poller runs on a 10s cadence and the file is a
// few hundred bytes, and persisting the last-seen readings immediately is
// what keeps restarts from double counting or losing the current interval.
func (a *trafficAccumulator) apply(name string, rx, tx uint64) (uint64, uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.load()

	baseline := a.state.Baselines[name]
	last, seen := a.state.Last[name]

	if !seen {
		// First observation of this interface in this state file: start the
		// baseline at zero and anchor the last-seen reading, so cumulative
		// growth from here on is additive without double counting.
		a.state.Baselines[name] = trafficCounters{}
		a.state.Last[name] = trafficCounters{RX: rx, TX: tx}
		a.persistLocked()
		return rx, tx
	}

	persist := false
	if rx < last.RX {
		baseline.RX += last.RX
		persist = true
	}
	if tx < last.TX {
		baseline.TX += last.TX
		persist = true
	}
	if persist {
		a.state.Baselines[name] = baseline
	}
	a.state.Last[name] = trafficCounters{RX: rx, TX: tx}

	a.persistLocked()
	return baseline.RX + rx, baseline.TX + tx
}

func (a *trafficAccumulator) persistLocked() {
	if a.path == "" {
		return
	}
	data, err := json.Marshal(a.state)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, a.path); err != nil {
		_ = os.Remove(tmp)
	}
}
