package modemmanager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTrafficAccumulatorAccumulatesAcrossResets(t *testing.T) {
	acc := newTrafficAccumulator(filepath.Join(t.TempDir(), "traffic.json"))

	rx, tx := acc.apply("enx1", 100, 50)
	if rx != 100 || tx != 50 {
		t.Fatalf("first apply = %d/%d, want 100/50", rx, tx)
	}
	rx, tx = acc.apply("enx1", 900, 400)
	if rx != 900 || tx != 400 {
		t.Fatalf("growth apply = %d/%d, want 900/400", rx, tx)
	}
	// Interface recreated: raw counters drop to near zero.
	rx, tx = acc.apply("enx1", 30, 15)
	if rx != 930 || tx != 415 {
		t.Fatalf("post-reset apply = %d/%d, want 930/415", rx, tx)
	}
	// And keeps growing from the new baseline.
	rx, _ = acc.apply("enx1", 130, 0)
	if rx != 1030 {
		t.Fatalf("post-reset growth = %d, want 1030", rx)
	}
}

func TestTrafficAccumulatorSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "traffic.json")

	first := newTrafficAccumulator(path)
	first.apply("enx1", 1000, 500)
	first.apply("enx1", 2000, 1000)

	// A new accumulator (simulating an agent restart) must fold the last
	// observed raw counters into the baseline instead of restarting at zero.
	second := newTrafficAccumulator(path)
	rx, tx := second.apply("enx1", 1500, 700)
	if rx != 3500 || tx != 1700 {
		t.Fatalf("after restart = %d/%d, want 3500/1700", rx, tx)
	}
}

func TestTrafficAccumulatorTracksInterfacesIndependently(t *testing.T) {
	acc := newTrafficAccumulator("")

	acc.apply("enx1", 1000, 500)
	acc.apply("eth0", 10, 10)
	// enx1 resets while eth0 keeps its own baseline untouched.
	rx, tx := acc.apply("enx1", 0, 0)
	if rx != 1000 || tx != 500 {
		t.Fatalf("enx1 after reset = %d/%d, want 1000/500", rx, tx)
	}
	// eth0 grows: the new reading already includes the old bytes.
	rx, _ = acc.apply("eth0", 20, 0)
	if rx != 20 {
		t.Fatalf("eth0 growth = %d, want 20", rx)
	}
}

func TestTrafficAccumulatorPersistsStateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "traffic.json")
	acc := newTrafficAccumulator(path)
	acc.apply("enx1", 100, 50)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	var state trafficState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("state file not valid JSON: %v", err)
	}
	if state.Baselines["enx1"].RX != 0 || state.Last["enx1"].RX != 100 || state.Last["enx1"].TX != 50 {
		t.Fatalf("unexpected persisted state: %+v", state)
	}
}
