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
func (m *Manager) ensureVoiceMixers(ctx context.Context, serial string) error {
	var failures []error
	for _, name := range voLTEMixers {
		enabled, err := m.readVoiceMixer(ctx, serial, name)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if enabled {
			continue
		}
		if err := m.writeVoiceMixer(ctx, serial, name); err != nil {
			failures = append(failures, err)
			continue
		}
		enabled, err = m.readVoiceMixer(ctx, serial, name)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !enabled {
			failures = append(
				failures,
				fmt.Errorf("mixer %q stayed off after being enabled", name),
			)
			continue
		}
		slog.Warn(
			"enabled a QDC507 VoLTE route mixer the vendor calibration left off",
			"component", "qdc507voice",
			"mixer", name,
		)
	}
	return errors.Join(failures...)
}

// readVoiceMixer reports whether one module mixer control is enabled. The tool
// prints "<name>: on" or "<name>: off".
func (m *Manager) readVoiceMixer(ctx context.Context, serial, name string) (bool, error) {
	result, err := m.client.Shell(
		ctx,
		serial,
		"amix "+quoteMixerName(name),
		mixerCommandTimeout,
	)
	if err != nil {
		return false, fmt.Errorf("read QDC507 mixer %q: %w", name, err)
	}
	if result.Status != 0 {
		return false, fmt.Errorf(
			"read QDC507 mixer %q: remote command returned status %d",
			name,
			result.Status,
		)
	}
	value, found := mixerValue(result.Output)
	if !found {
		return false, fmt.Errorf("read QDC507 mixer %q: unrecognised output", name)
	}
	return value == mixerOn, nil
}

// writeVoiceMixer enables one module mixer control.
func (m *Manager) writeVoiceMixer(ctx context.Context, serial, name string) error {
	result, err := m.client.Shell(
		ctx,
		serial,
		"amix "+quoteMixerName(name)+" 1",
		mixerCommandTimeout,
	)
	if err != nil {
		return fmt.Errorf("enable QDC507 mixer %q: %w", name, err)
	}
	if result.Status != 0 {
		return fmt.Errorf(
			"enable QDC507 mixer %q: remote command returned status %d",
			name,
			result.Status,
		)
	}
	return nil
}

// mixerValue extracts the value from "amix" output, which is
// "<control name>: <value>".
func mixerValue(output string) (string, bool) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		index := strings.LastIndex(line, ":")
		if index < 0 {
			continue
		}
		value := strings.TrimSpace(line[index+1:])
		if value != "" {
			return value, true
		}
	}
	return "", false
}

// shellQuote wraps a mixer control name for the module's shell. The names
// contain spaces but no quotes, so single quoting is sufficient.
func quoteMixerName(value string) string {
	if !strings.ContainsAny(value, " \t\"'\\$`") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
