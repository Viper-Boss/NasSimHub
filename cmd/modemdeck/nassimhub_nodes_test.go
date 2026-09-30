package main

import "testing"

func TestNodeCoreVersionNeverBlocksPairingOnABuildLabel(t *testing.T) {
	for input, want := range map[string]string{
		"":               "",
		"dev":            "",
		"1.15.0":         "1.15.0",
		"v1.15.0":        "1.15.0",
		"1.15.0-rc1":     "1.15.0-rc1",
		"fnos-local-0.1": "",
		"r122":           "",
	} {
		if got := nodeCoreVersion(input); got != want {
			t.Errorf("nodeCoreVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNodePollIntervalBounds(t *testing.T) {
	for raw, want := range map[string]int{"": 10, "2": 10, "3": 3, "300": 300, "301": 10, "x": 10} {
		t.Setenv("MODEMDECK_NASSIMHUB_POLL_SECONDS", raw)
		if got := int(nodePollInterval().Seconds()); got != want {
			t.Errorf("poll %q = %ds, want %ds", raw, got, want)
		}
	}
}
