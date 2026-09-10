package qdc507voice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// mixerOn is how the module's amix tool reports an enabled boolean control.
const mixerOn = "on"

// mixerCommandTimeout bounds a single mixer read or write.
const mixerCommandTimeout = 8 * time.Second

// voLTEMixers are the module mixer switches that connect the VoLTE voice stream
// to the auxiliary PCM the USB audio path uses.
//
// They are NOT enabled by the vendor calibration even though that step logs
// "Setting mixer control: ... value: 1": the tool it drives only prints what it
// was asked to set and never reads the control back, so the route reports as
// established while the switches stay off. Measured on the QDC507 with both
// switches off: the downlink arrived as a barely audible leak and the uplink
// never reached the far end at all — the caller could not hear this side.
//
// Enabling them is what makes call audio work in both directions, so the
// reconciler asserts them on every pass rather than trusting the calibration.
var voLTEMixers = []string{
	"SEC_AUX_PCM_RX_Voice Mixer VoLTE",
	"VoLTE_Tx Mixer SEC_AUX_PCM_TX_VoLTE",
}

// ensureVoiceMixers turns on the VoLTE route switches and verifies each write by
// reading the control back. Failures are reported rather than swallowed: a
// switch that will not stay on means the route can carry no audio, which is
// otherwise invisible from every other signal ModemDeck collects.
//
// The whole check runs as one module shell command. Each adb round trip costs a
// slot on the single ADB channel the voice runtime also uses for provisioning,
// and this runs on every reconcile pass, so the reads and conditional writes are
// batched rather than issued separately.
func (m *Manager) ensureVoiceMixers(ctx context.Context, serial string) error {
	result, err := m.client.Shell(
		ctx,
		serial,
		voiceMixerScript(),
		mixerCommandTimeout,
	)
	if err != nil {
		return fmt.Errorf("ensure QDC507 VoLTE mixers: %w", err)
	}
	if result.Status != 0 {
		return fmt.Errorf(
			"ensure QDC507 VoLTE mixers: remote command returned status %d",
			result.Status,
		)
	}
	statuses := parseMixerStatuses(result.Output)
	var failures []error
	for _, name := range voLTEMixers {
		switch statuses[name] {
		case mixerStatusOn:
		case mixerStatusFixed:
			slog.Warn(
				"enabled a QDC507 VoLTE route mixer the vendor calibration left off",
				"component", "qdc507voice",
				"mixer", name,
			)
		default:
			failures = append(
				failures,
				fmt.Errorf("mixer %q is not enabled", name),
			)
		}
	}
	return errors.Join(failures...)
}

// mixerStatusOn and mixerStatusFixed are the two outcomes the module reports for
// a control that is now enabled. "fixed" means this pass had to turn it on.
const (
	mixerStatusOn    = "on"
	mixerStatusFixed = "fixed"
)

// voiceMixerScript reads every VoLTE route switch and turns on the ones that are
// off, echoing one status line per control.
//
// The reads redirect stderr into the capture because the module's amix tool
// writes the control value there, not to stdout — without the redirect the
// command substitution sees nothing, every control looks disabled, and the
// read-back that is meant to prove the write worked can never succeed.
func voiceMixerScript() string {
	var script strings.Builder
	script.WriteString("for n in")
	for _, name := range voLTEMixers {
		script.WriteString(" ")
		script.WriteString(quoteMixerName(name))
	}
	script.WriteString(
		`; do v=$(amix "$n" 2>&1); case "$v" in *": on") echo "` + mixerStatusOn + ` $n" ;; *) ` +
			`amix "$n" 1; v2=$(amix "$n" 2>&1); ` +
			`case "$v2" in *": on") echo "` + mixerStatusFixed + ` $n" ;; ` +
			`*) echo "failed $n" ;; esac ;; esac; done`,
	)
	return script.String()
}

// parseMixerStatuses maps each control named in the script's output to the last
// status reported for it.
func parseMixerStatuses(output string) map[string]string {
	statuses := make(map[string]string, len(voLTEMixers))
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		status, name, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		switch status {
		case mixerStatusOn, mixerStatusFixed, "failed":
			statuses[strings.TrimSpace(name)] = status
		}
	}
	return statuses
}

// shellQuote wraps a mixer control name for the module's shell. The names
// contain spaces but no quotes, so single quoting is sufficient.
func quoteMixerName(value string) string {
	if !strings.ContainsAny(value, " \t\"'\\$`") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
