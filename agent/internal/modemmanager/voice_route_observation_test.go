package modemmanager

import (
	"context"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// The call transitions the module voice runtime needs must actually reach it.
//
// This test exists for the same reason the DHT wiring test in internal/httpapi
// does: the consequence of the hook never firing is not a crash or an error but
// a feature that is permanently absent, wearing the face of a healthy system.
// If the runtime is never told a call ended, the module's route session is
// never restarted, every call after the first is silent in both directions, and
// every signal the software collects -- the bridge process, its log, the USB
// audio enable flag, both PCM substreams -- still reports a working audio path.
// There is nothing to notice at runtime, so it has to be asserted here.

type recordingVoiceObserver struct {
	mu          sync.Mutex
	observation []bool
	lines       []string
}

func (o *recordingVoiceObserver) Status(domain.Line) domain.QDC507VoiceRuntimeStatus {
	return domain.QDC507VoiceRuntimeStatus{}
}

func (o *recordingVoiceObserver) ObserveCallActivity(line domain.Line, active bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lines = append(o.lines, line.ID)
	o.observation = append(o.observation, active)
}

func (o *recordingVoiceObserver) seen() ([]string, []bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.lines...), append([]bool(nil), o.observation...)
}

func quectelVoiceLine() domain.Line {
	return domain.Line{
		ID:             "line-1",
		Model:          "QDC507",
		Revision:       "QDC507GLEFM21",
		PhysicalDevice: "/sys/devices/pci0000:00/usb1/1-10",
	}
}

func TestALiveCallIsReportedToTheVoiceRuntime(t *testing.T) {
	t.Parallel()
	observer := &recordingVoiceObserver{}
	provider := &Provider{qdc507Voice: observer}
	provider.observeVoiceRouteActivity(&ParsedObjects{
		Lines: []domain.Line{quectelVoiceLine()},
		Calls: []domain.Call{{ID: "call-1", LineID: "line-1", State: "active"}},
	})
	lines, seen := observer.seen()
	if len(seen) != 1 || !seen[0] || lines[0] != "line-1" {
		t.Fatalf("a live call was reported as %v on %v", seen, lines)
	}
}

// The end of a call is reported as the ABSENCE of one, so a line with no calls
// has to be reported too. Reporting only the lines that currently have calls
// would mean the runtime never learns a call finished, which is the only moment
// that matters.
func TestALineWithNoCallsIsStillReported(t *testing.T) {
	t.Parallel()
	observer := &recordingVoiceObserver{}
	provider := &Provider{qdc507Voice: observer}
	provider.observeVoiceRouteActivity(&ParsedObjects{
		Lines: []domain.Line{quectelVoiceLine()},
	})
	_, seen := observer.seen()
	if len(seen) != 1 || seen[0] {
		t.Fatalf(
			"a line with no calls reported %v; the runtime can never learn a "+
				"call ended and the route session is never restarted",
			seen,
		)
	}
}

// A terminated call is a record of one that has finished. Counting it as live
// holds the line open until the record is swept, and the next call then runs on
// a session that has already been used.
func TestATerminatedCallDoesNotHoldTheLineOpen(t *testing.T) {
	t.Parallel()
	observer := &recordingVoiceObserver{}
	provider := &Provider{qdc507Voice: observer}
	provider.observeVoiceRouteActivity(&ParsedObjects{
		Lines: []domain.Line{quectelVoiceLine()},
		Calls: []domain.Call{{ID: "call-1", LineID: "line-1", State: "terminated"}},
	})
	_, seen := observer.seen()
	if len(seen) != 1 || seen[0] {
		t.Fatalf("a terminated call was reported as a live one: %v", seen)
	}
}

// Only lines whose modules have this runtime are reported; a line on another
// modem has no module-side route session to restart.
func TestLinesWithoutTheQuectelVoiceRuntimeAreNotReported(t *testing.T) {
	t.Parallel()
	observer := &recordingVoiceObserver{}
	provider := &Provider{qdc507Voice: observer}
	provider.observeVoiceRouteActivity(&ParsedObjects{
		Lines: []domain.Line{{ID: "line-2", Model: "SIM7600", Revision: "SIM7600G"}},
		Calls: []domain.Call{{ID: "call-1", LineID: "line-2", State: "active"}},
	})
	if _, seen := observer.seen(); len(seen) != 0 {
		t.Fatalf("an unrelated modem's line was reported: %v", seen)
	}
}

// A runtime that does not implement the observation must not be a crash, since
// the interface is optional and a test fake or an older runtime will not have
// it.
func TestAVoiceRuntimeWithoutTheObservationIsTolerated(t *testing.T) {
	t.Parallel()
	provider := &Provider{}
	provider.observeVoiceRouteActivity(&ParsedObjects{
		Lines: []domain.Line{quectelVoiceLine()},
		Calls: []domain.Call{{ID: "call-1", LineID: "line-1", State: "active"}},
	})
}

// And the projection has to call it. Asserted through the projection entry
// point rather than the helper, because a helper nobody calls is exactly the
// failure this file is about.
func TestTheVoiceProjectionReportsCallActivity(t *testing.T) {
	t.Parallel()
	observer := &recordingVoiceObserver{}
	provider := newTestProvider(newFakeCaller(emptyLineObjects(false, true)))
	provider.qdc507Voice = observer
	parsed := ParsedObjects{
		Lines:        []domain.Line{quectelVoiceLine()},
		Calls:        []domain.Call{{ID: "call-1", LineID: "line-1", State: "active"}},
		LinePaths:    map[string]dbus.ObjectPath{},
		CallBackends: map[string]callControlBackend{},
		ATCallLines:  map[string]string{},
	}
	provider.projectVoiceCapabilities(context.Background(), "test", &parsed)
	if _, seen := observer.seen(); len(seen) == 0 {
		t.Fatal(
			"projectVoiceCapabilities did not report call activity, so the " +
				"module route session is never restarted between calls",
		)
	}
}
