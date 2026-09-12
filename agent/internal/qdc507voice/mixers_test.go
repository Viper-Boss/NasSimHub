package qdc507voice

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"
)

// mixerClient models the module's amix tool as a set of readable, writable
// boolean controls, and emulates the single batched script ensureVoiceMixers
// runs.
type mixerClient struct {
	mu        sync.Mutex
	values    map[string]string
	writable  bool
	readFails bool
	writes    int
	commands  []string
}

func newMixerClient() *mixerClient {
	values := make(map[string]string, len(voLTEMixers))
	for _, name := range voLTEMixers {
		values[name] = "off"
	}
	return &mixerClient{values: values, writable: true}
}

func (c *mixerClient) Shell(
	_ context.Context,
	_ string,
	command string,
	_ time.Duration,
) (ShellResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commands = append(c.commands, command)
	if c.readFails {
		return ShellResult{Status: 1}, nil
	}
	if !strings.Contains(command, "for n in") {
		return ShellResult{Status: 1}, nil
	}
	var output strings.Builder
	for _, name := range voLTEMixers {
		switch {
		case c.values[name] == mixerOn:
			fmt.Fprintf(&output, "on %s\n", name)
		case c.writable:
			c.values[name] = mixerOn
			c.writes++
			fmt.Fprintf(&output, "fixed %s\n", name)
		default:
			fmt.Fprintf(&output, "failed %s\n", name)
		}
	}
	return ShellResult{Output: output.String()}, nil
}

func (c *mixerClient) Devices(context.Context) ([]Device, error) { return nil, nil }

func (c *mixerClient) Push(
	context.Context,
	string,
	string,
	string,
	fs.FileMode,
	time.Duration,
) error {
	return nil
}

func TestEnsureVoiceMixersEnablesWhatCalibrationLeftOff(t *testing.T) {
	t.Parallel()

	client := newMixerClient()
	manager := &Manager{client: client}

	if err := manager.ensureVoiceMixers(context.Background(), "serial"); err != nil {
		t.Fatalf("ensureVoiceMixers() error = %v", err)
	}
	for _, name := range voLTEMixers {
		if client.values[name] != mixerOn {
			t.Errorf("mixer %q = %q, want on", name, client.values[name])
		}
	}
	if len(client.commands) != 1 {
		t.Errorf("commands = %d, want a single batched command", len(client.commands))
	}
}

func TestEnsureVoiceMixersLeavesEnabledControlsAlone(t *testing.T) {
	t.Parallel()

	client := newMixerClient()
	for _, name := range voLTEMixers {
		client.values[name] = mixerOn
	}
	manager := &Manager{client: client}

	if err := manager.ensureVoiceMixers(context.Background(), "serial"); err != nil {
		t.Fatalf("ensureVoiceMixers() error = %v", err)
	}
	if client.writes != 0 {
		t.Fatalf("writes = %d, want none for already-enabled mixers", client.writes)
	}
}

func TestEnsureVoiceMixersReportsAControlThatStaysOff(t *testing.T) {
	t.Parallel()

	client := newMixerClient()
	client.writable = false
	manager := &Manager{client: client}

	err := manager.ensureVoiceMixers(context.Background(), "serial")
	if err == nil {
		t.Fatal("ensureVoiceMixers() error = nil, want a failure")
	}
	if !strings.Contains(err.Error(), "is not enabled") {
		t.Fatalf("ensureVoiceMixers() error = %v, want it to name the disabled mixer", err)
	}
}

func TestEnsureVoiceMixersReportsAFailedCommand(t *testing.T) {
	t.Parallel()

	client := newMixerClient()
	client.readFails = true
	manager := &Manager{client: client}

	if err := manager.ensureVoiceMixers(context.Background(), "serial"); err == nil {
		t.Fatal("ensureVoiceMixers() error = nil, want a failure")
	}
}

func TestParseMixerStatuses(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		"on SEC_AUX_PCM_RX_Voice Mixer VoLTE",
		"fixed VoLTE_Tx Mixer SEC_AUX_PCM_TX_VoLTE",
		"",
	}, "\n")
	statuses := parseMixerStatuses(output)
	if statuses["SEC_AUX_PCM_RX_Voice Mixer VoLTE"] != mixerStatusOn {
		t.Errorf("first status = %q, want on", statuses["SEC_AUX_PCM_RX_Voice Mixer VoLTE"])
	}
	if statuses["VoLTE_Tx Mixer SEC_AUX_PCM_TX_VoLTE"] != mixerStatusFixed {
		t.Errorf(
			"second status = %q, want fixed",
			statuses["VoLTE_Tx Mixer SEC_AUX_PCM_TX_VoLTE"],
		)
	}
	if len(parseMixerStatuses("")) != 0 {
		t.Error("empty output should report no statuses")
	}
	// Lines that are not status lines must be ignored rather than misparsed.
	if len(parseMixerStatuses("can't find control\n")) != 0 {
		t.Error("unrecognised output should report no statuses")
	}
}

func TestVoiceMixerScriptMergesStderrIntoTheReads(t *testing.T) {
	t.Parallel()

	script := voiceMixerScript()
	// The module's amix writes the control value to stderr, so a read without
	// the redirect captures nothing, every control looks disabled, and the
	// read-back that proves the write worked can never pass.
	if got := strings.Count(script, "2>&1"); got != 2 {
		t.Fatalf("script = %q, want both amix reads to merge stderr (found %d)", script, got)
	}
	for _, name := range voLTEMixers {
		if !strings.Contains(script, quoteMixerName(name)) {
			t.Fatalf("script = %q, want the quoted control %q", script, name)
		}
	}
}

func TestQuoteMixerName(t *testing.T) {
	t.Parallel()

	if got := quoteMixerName("VoLTE"); got != "VoLTE" {
		t.Errorf("quoteMixerName(bare) = %q, want it unquoted", got)
	}
	quoted := quoteMixerName("SEC_AUX_PCM_RX_Voice Mixer VoLTE")
	if quoted != "'SEC_AUX_PCM_RX_Voice Mixer VoLTE'" {
		t.Errorf("quoteMixerName(spaced) = %q", quoted)
	}
}

// The AFE PCM front end must be in the required list.
//
// This is the control set that was missing when calls connected and carried
// nothing. Probed on hardware: all four exist and all four were off, while the
// two SEC_AUX_PCM controls this code already asserted read "on" throughout
// sixteen consecutive silent measurement windows. The bridge reads hw:0,4 --
// the AFE PCM front end -- so connecting the voice session only to the
// secondary AUX PCM pins routes the call somewhere nothing is listening.
func TestTheAFEPCMFrontEndIsRequired(t *testing.T) {
	required := map[string]bool{}
	for _, name := range voLTEMixers {
		required[name] = true
	}
	for _, name := range []string{
		"AFE_PCM_RX_Voice Mixer VoLTE",
		"AFE_PCM_RX_Voice Mixer CSVoice",
		"VoLTE_Tx Mixer AFE_PCM_TX_VoLTE",
		"Voice_Tx Mixer AFE_PCM_TX_Voice",
	} {
		if !required[name] {
			t.Fatalf(
				"%q is not asserted; the bridge reads the AFE PCM front end, so without "+
					"it a call connects and carries nothing in either direction",
				name,
			)
		}
	}
	// And the UCM verb's own pair stays: the verb enables it, and a call that
	// falls back to circuit-switched uses it.
	for _, name := range []string{
		"SEC_AUX_PCM_RX_Voice Mixer VoLTE",
		"VoLTE_Tx Mixer SEC_AUX_PCM_TX_VoLTE",
	} {
		if !required[name] {
			t.Fatalf("%q was dropped; the UCM VoLTE verb enables it", name)
		}
	}
}

// A firmware without the multimode controls is not a broken route.
//
// They were never probed on this build, so treating their absence as a failure
// would report a working line as broken on every reconcile pass. Present and
// refusing to turn on is worth a log line and nothing more.
func TestMissingMultimodeMixersAreNotAFailure(t *testing.T) {
	output := ""
	for _, name := range voLTEMixers {
		output += mixerStatusOn + " " + name + "\n"
	}
	for _, name := range optionalVoiceMixers {
		output += "failed " + name + "\n"
	}
	manager := testManager(&fixedOutputClient{output: output})
	if err := manager.ensureVoiceMixers(context.Background(), "transport:2"); err != nil {
		t.Fatalf("a firmware with no multimode controls was reported as broken: %v", err)
	}
}

// But a REQUIRED control that will not turn on is a failure, or this tolerance
// would swallow the fault it was written around.
func TestARequiredMixerThatWillNotEnableIsAFailure(t *testing.T) {
	for _, missing := range []string{
		"AFE_PCM_RX_Voice Mixer VoLTE",
		"Voice_Tx Mixer AFE_PCM_TX_Voice",
		"SEC_AUX_PCM_RX_Voice Mixer VoLTE",
	} {
		output := ""
		for _, name := range voLTEMixers {
			status := mixerStatusOn
			if name == missing {
				status = "failed"
			}
			output += status + " " + name + "\n"
		}
		manager := testManager(&fixedOutputClient{output: output})
		err := manager.ensureVoiceMixers(context.Background(), "transport:2")
		if err == nil {
			t.Fatalf("%q refused to turn on and the route was reported as healthy", missing)
		}
		if !strings.Contains(err.Error(), missing) {
			t.Fatalf("the failure does not name %q: %v", missing, err)
		}
	}
}

// fixedOutputClient answers every shell command with one canned mixer report.
type fixedOutputClient struct {
	output string
}

func (c *fixedOutputClient) Devices(context.Context) ([]Device, error) {
	return []Device{{Selector: "transport:2", State: "device", USB: "usb:1-10"}}, nil
}

func (c *fixedOutputClient) Shell(
	context.Context, string, string, time.Duration,
) (ShellResult, error) {
	return ShellResult{Output: c.output}, nil
}

func (c *fixedOutputClient) Push(
	context.Context, string, string, string, fs.FileMode, time.Duration,
) error {
	return nil
}
