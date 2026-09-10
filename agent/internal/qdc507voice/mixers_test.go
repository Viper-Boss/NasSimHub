package qdc507voice

import (
	"context"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"
)

// mixerClient models the module's amix tool as a set of readable, writable
// boolean controls.
type mixerClient struct {
	mu        sync.Mutex
	values    map[string]string
	writable  bool
	readFails bool
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
	name, write := parseMixerCommand(command)
	if name == "" {
		return ShellResult{Status: 1}, nil
	}
	if write {
		if c.writable {
			c.values[name] = mixerOn
		}
		return ShellResult{}, nil
	}
	return ShellResult{Output: name + ": " + c.values[name]}, nil
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

// parseMixerCommand decodes the two command shapes ensureVoiceMixers issues:
// `amix '<name>'` reads and `amix '<name>' 1` writes.
func parseMixerCommand(command string) (string, bool) {
	const prefix = "amix "
	if !strings.HasPrefix(command, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(command, prefix)
	write := strings.HasSuffix(rest, " 1")
	if write {
		rest = strings.TrimSuffix(rest, " 1")
	}
	name := strings.Trim(rest, "'")
	if name == "" {
		return "", false
	}
	return name, write
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
	for _, command := range client.commands {
		if strings.HasSuffix(command, " 1") {
			t.Errorf("unexpected write to an already-enabled mixer: %q", command)
		}
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
	if !strings.Contains(err.Error(), "stayed off") {
		t.Fatalf("ensureVoiceMixers() error = %v, want it to mention staying off", err)
	}
}

func TestEnsureVoiceMixersReportsAnUnreadableControl(t *testing.T) {
	t.Parallel()

	client := newMixerClient()
	client.readFails = true
	manager := &Manager{client: client}

	if err := manager.ensureVoiceMixers(context.Background(), "serial"); err == nil {
		t.Fatal("ensureVoiceMixers() error = nil, want a failure")
	}
}

func TestMixerValue(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		output string
		want   string
		found  bool
	}{
		"enabled":  {output: "SEC_AUX_PCM_RX_Voice Mixer VoLTE: on\n", want: "on", found: true},
		"disabled": {output: "VoLTE_Tx Mixer SEC_AUX_PCM_TX_VoLTE: off", want: "off", found: true},
		"empty output is not a value": {output: "", found: false},
		"an unreadable control is not a value": {
			output: "can't find control",
			found:  false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			value, found := mixerValue(test.output)
			if found != test.found {
				t.Fatalf("found = %t, want %t", found, test.found)
			}
			if found && value != test.want {
				t.Fatalf("value = %q, want %q", value, test.want)
			}
		})
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
