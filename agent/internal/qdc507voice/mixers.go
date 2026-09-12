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

// The voice route, and why every one of these switches has to be asserted.
//
// There are two halves to this, found six weeks apart, and the second one is
// the reason the first was not enough. Read both before removing anything.
//
// HALF ONE: the vendor firmware never enables the VoLTE route at all.
//
// The reason is not subtle once you
// look at the shipped image. Its voice daemon (usr/bin/quectel_daemon) brings
// audio up by writing a hard-coded command block to alsaucm_test:
//
//	open snd_soc_msm_9x07_Tomtom_I2S
//	set _verb Voice Call
//	set _enadev Auxpcm Rx
//	set _enadev Auxpcm Tx
//
// "Voice Call" is a literal with no branch anywhere near it, and in the UCM file
// that verb actually reads (/etc/snd_soc_msm/snd_soc_msm_9x07_Tomtom_I2S) it
// enables the CIRCUIT-SWITCHED route:
//
//	'SEC_AUX_PCM_RX_Voice Mixer CSVoice':1:1
//	'Voice_Tx Mixer SEC_AUX_PCM_TX_Voice':1:1
//
// The same file also defines a "VoLTE" verb whose enable sequence is the
// SEC_AUX_PCM pair — and nothing in the firmware ever selects it. So on a
// VoLTE call, which is every call on a modern network, that route is simply
// never connected.
//
// (An earlier version of this comment blamed the calibration tool for not
// reading its writes back. That was wrong: alsaucm_test is never asked to set
// these controls in the first place. Recorded here because the wrong
// explanation sends you digging through alsaucm_test for a bug that isn't
// there.)
//
// HALF TWO: the SEC_AUX_PCM pair is the wrong front end for this product.
//
// Enabling only that pair produced a call that connected and carried nothing:
// measured on hardware, two calls, sixteen consecutive two-second windows at
// rms -120 dBFS, peak 0, in both directions, with both SEC_AUX_PCM controls
// reading back "on" the whole time.
//
// The reason is which device the audio has to arrive at. The module's PCM
// bridge opens hw:0,4, the AFE PCM front end, and that is where this software
// reads and writes call audio -- the SEC_AUX_PCM controls connect the voice
// session to the secondary AUX PCM HARDWARE PINS instead, which on this board
// go nowhere anybody is listening. So the route was genuinely connected, to the
// wrong place, and every signal available said it was fine.
//
// Probed on the module before writing anything here: all four controls below
// exist on this firmware and all four read "off". The lead came from
// cellbridge-mac, which drives the same bridge binary and sets the AFE PCM
// controls in ADDITION to running the same UCM VoLTE verb -- the two are not
// alternatives, and the SEC_AUX_PCM pair stays because the verb enables it and
// a CS call on some networks uses it.
//
// CSVoice is included alongside VoLTE because the session type is the network's
// choice, not ours: a call that falls back to circuit-switched uses the CS
// session, and a route connected only for VoLTE is silent for exactly the calls
// that needed the fallback.
var afePCMMixers = []string{
	"AFE_PCM_RX_Voice Mixer VoLTE",
	"AFE_PCM_RX_Voice Mixer CSVoice",
	"VoLTE_Tx Mixer AFE_PCM_TX_VoLTE",
	"Voice_Tx Mixer AFE_PCM_TX_Voice",
}

// optionalVoiceMixers are the multimode-voice session variants.
//
// Separate from the required list because they were NOT probed on this
// firmware. cellbridge-mac sets them on the same module family, and a modem
// using a multimode voice session would need them, but a control this build
// does not have would otherwise be reported as a broken route on every
// reconcile pass. Absent is fine here; present and refusing to turn on is
// worth a line in the log and nothing more.
var optionalVoiceMixers = []string{
	"AFE_PCM_RX_Voice Mixer VoiceMMode1",
	"AFE_PCM_RX_Voice Mixer VoiceMMode2",
	"VoiceMMode1_Tx Mixer AFE_PCM_TX_MMode1",
	"VoiceMMode2_Tx Mixer AFE_PCM_TX_MMode2",
}

// voLTEMixers is every control that must be on for a call to carry audio: the
// UCM VoLTE verb's own pair, and the AFE PCM front end the bridge reads. A
// failure on any of these means the route can carry nothing.
var voLTEMixers = append(
	[]string{
		"SEC_AUX_PCM_RX_Voice Mixer VoLTE",
		"VoLTE_Tx Mixer SEC_AUX_PCM_TX_VoLTE",
	},
	afePCMMixers...,
)

// allVoiceMixers is what the reconciler writes: everything required, plus the
// optional controls, in one module command.
func allVoiceMixers() []string {
	return append(append([]string(nil), voLTEMixers...), optionalVoiceMixers...)
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
				"enabled a QDC507 voice route mixer the vendor calibration left off",
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
	// The optional controls only ever produce a log line. A firmware without
	// them is not a broken route, and treating absence as failure would report
	// a working line as broken on every pass.
	for _, name := range optionalVoiceMixers {
		switch statuses[name] {
		case mixerStatusOn, mixerStatusFixed:
		default:
			slog.Debug(
				"a multimode voice route mixer is unavailable on this firmware",
				"component", "qdc507voice",
				"mixer", name,
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
	for _, name := range allVoiceMixers() {
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
	statuses := make(map[string]string, len(allVoiceMixers()))
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
