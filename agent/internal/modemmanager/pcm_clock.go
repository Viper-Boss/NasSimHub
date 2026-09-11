package modemmanager

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// The secondary AUX PCM clock.
//
// This gate was found by reverse engineering the shipped firmware, not from any
// documentation, and it is the one link in the call audio chain that nothing
// else reports on. The module's atfwd_daemon contains:
//
//	/sys/devices/soc:qcom,msm-sec-auxpcm/enable_clk
//	+QCFG: "pcmclk",%d
//
// so AT+QCFG="pcmclk",<n> writes that sysfs node. The secondary AUX PCM -- the
// interface AT+QPCMV=1,2 selects and the one call audio crosses -- has its own
// clock enable, separate from the PCM interface being "on" and separate from
// the route mixers being connected. With the clock off there is no bit clock to
// carry samples, so:
//
//   - AT+QPCMV? still answers 1,2, because the interface is configured
//   - the VoLTE route mixers still read back "on", because they are
//   - the call still connects and the signalling all reports success
//   - and both directions are digital silence
//
// Which is exactly the "call connects, nobody can hear anything" symptom, and
// nothing in the existing probe would notice. So the clock is checked as part
// of bringing voice up.
//
// WHAT THIS DELIBERATELY DOES NOT DO: guess a value. The firmware also contains
// the string "pcm clk para is not connrect, need 2, now is : %d", which reads
// like a complaint about the number of PARAMETERS ("pcmclk" plus a value makes
// two), not an instruction that the value must be 2. Those are very different
// claims and the binary does not settle it. So this queries the module, acts
// only on an answer it understands, and when the reply is unrecognised it
// reports that and writes nothing -- putting a guessed number into a clock
// configuration is worse than leaving a clock alone.

const (
	quectelPCMClockQuery  = `AT+QCFG="pcmclk"`
	quectelPCMClockEnable = `AT+QCFG="pcmclk",1`
)

// pcmClockState is what the module said about its secondary PCM clock.
type pcmClockState string

const (
	// pcmClockOn means the module reported a non-zero value.
	pcmClockOn pcmClockState = "on"
	// pcmClockOff means the module reported zero.
	pcmClockOff pcmClockState = "off"
	// pcmClockUnknown covers an unparseable reply and a firmware that does not
	// implement the parameter at all. Both are reported rather than treated as
	// off, because "off" would justify a write and this does not.
	pcmClockUnknown pcmClockState = "unknown"
)

// parsePCMClock reads the value out of a +QCFG: "pcmclk",<n> reply.
//
// Real replies carry the echoed parameter name and CRLF, and some firmware
// quotes the value as well:
//
//	+QCFG: "pcmclk",1
//	+QCFG: "pcmclk",0
//
// Anything else is unknown. In particular a bare "OK", which is what a firmware
// that does not know the parameter may answer, must not read as off.
func parsePCMClock(response string) (pcmClockState, int) {
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "\r"))
		if !strings.HasPrefix(line, "+QCFG:") {
			continue
		}
		fields := splitATFields(strings.TrimSpace(strings.TrimPrefix(line, "+QCFG:")))
		if len(fields) < 2 {
			continue
		}
		if !strings.EqualFold(strings.Trim(fields[0], `"`), "pcmclk") {
			continue
		}
		value, err := strconv.Atoi(strings.Trim(fields[1], `" `))
		if err != nil {
			return pcmClockUnknown, 0
		}
		if value == 0 {
			return pcmClockOff, 0
		}
		return pcmClockOn, value
	}
	return pcmClockUnknown, 0
}

// pcmClockReport is what the probe records about the clock.
type pcmClockReport struct {
	State pcmClockState
	Value int
	// Enabled is true when this call turned the clock on. Recorded separately
	// from State so a log can distinguish "it was already on" from "we had to
	// switch it on", which is the difference between a healthy module and one
	// that comes up with its audio clock stopped.
	Enabled bool
	Reason  string
}

// ensurePCMClock reads the secondary PCM clock and switches it on if the module
// reported it off.
//
// The caller passes a command function rather than a D-Bus path so this can be
// exercised against recorded firmware replies; the real behaviour of this code
// cannot be checked on any machine without a QDC507 attached, and a function
// that can only be tested on the hardware it was written for is a function
// nobody checks.
func ensurePCMClock(
	ctx context.Context,
	command func(context.Context, string) (string, error),
) pcmClockReport {
	response, err := command(ctx, quectelPCMClockQuery)
	if err != nil {
		return pcmClockReport{
			State:  pcmClockUnknown,
			Reason: fmt.Sprintf("could not read the PCM clock: %v", err),
		}
	}

	state, value := parsePCMClock(response)
	switch state {
	case pcmClockOn:
		return pcmClockReport{State: pcmClockOn, Value: value}
	case pcmClockUnknown:
		// Nothing is written here. An unrecognised reply means either the
		// firmware does not have this parameter or it answered in a shape this
		// code does not know, and neither justifies writing a clock setting.
		return pcmClockReport{
			State:  pcmClockUnknown,
			Reason: "the module did not report a PCM clock value; left untouched",
		}
	}

	if _, err := command(ctx, quectelPCMClockEnable); err != nil {
		return pcmClockReport{
			State:  pcmClockOff,
			Reason: fmt.Sprintf("the module refused to enable the PCM clock: %v", err),
		}
	}

	// Read back rather than trusting the write. This is a clock: if it did not
	// actually come on, every other voice signal will still look healthy and
	// the call will be silent, which is the failure this whole file exists to
	// stop being invisible.
	confirmation, err := command(ctx, quectelPCMClockQuery)
	if err != nil {
		return pcmClockReport{
			State:   pcmClockUnknown,
			Enabled: true,
			Reason:  fmt.Sprintf("enabled the PCM clock but could not confirm it: %v", err),
		}
	}
	confirmed, confirmedValue := parsePCMClock(confirmation)
	if confirmed != pcmClockOn {
		return pcmClockReport{
			State:   confirmed,
			Enabled: true,
			Reason:  "the PCM clock did not stay on after being enabled",
		}
	}
	return pcmClockReport{State: pcmClockOn, Value: confirmedValue, Enabled: true}
}

// logPCMClock records the outcome. A clock that had to be switched on is
// logged at warning level on purpose: it means the module came up with its
// audio clock stopped, which is worth knowing even though this code fixed it.
func logPCMClock(operation, lineID string, report pcmClockReport) {
	switch {
	case report.State == pcmClockOn && !report.Enabled:
		slog.Debug(
			"secondary PCM clock is already running",
			"component", "modemmanager",
			"operation", operation,
			"line_id", lineID,
			"value", report.Value,
		)
	case report.State == pcmClockOn && report.Enabled:
		slog.Warn(
			"secondary PCM clock was off and has been enabled; call audio would have been silent",
			"component", "modemmanager",
			"operation", operation,
			"line_id", lineID,
			"value", report.Value,
		)
	default:
		slog.Warn(
			"secondary PCM clock state is unknown; call audio may be silent in both directions",
			"component", "modemmanager",
			"operation", operation,
			"line_id", lineID,
			"state", string(report.State),
			"reason", report.Reason,
		)
	}
}
