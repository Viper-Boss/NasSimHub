package modemmanager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// Reception the modem holds but ModemManager never reports.
//
// ModemManager builds its SMS objects from +CMTI indications and only ever
// re-reads the modem's message storage when the modem is enabled again. A
// reception whose indication never reaches ModemManager therefore stays in the
// storage, occupies a slot and remains invisible to the API — measured on the
// QDC507 with an LTE-only attach, where incoming messages surfaced anywhere
// between instantly and half an hour later, and where the user had to toggle
// airplane mode (ModemManager disable + enable) before each message appeared.
//
// The agent cannot ask ModemManager to re-read the storage, so it detects the
// divergence — the modem holds more messages than ModemManager reports — and
// performs the same bounded radio cycle the user would perform by hand, only
// automatically and only when a message would otherwise stay lost.
const (
	smsStorageQuery = "AT+CPMS?"
	smsServiceQuery = "AT+CGSMS?"

	// smsExpectedService is "packet switched preferred, circuit switched
	// fallback". The QDC507 was found reporting 1 ("circuit switched only"),
	// which cannot carry SMS on an LTE-only attach at all: there is no circuit
	// switched domain to carry it. Sending an SMS still succeeds with 1, so
	// only the reception side shows the damage.
	smsExpectedService = 2
	smsServiceWrite    = "AT+CGSMS=2"

	smsCommandTimeout = 10 * time.Second
	// smsRadioCycleTimeout bounds one disable + enable cycle of the radio.
	smsRadioCycleTimeout = 90 * time.Second
	// smsRadioCycleMinInterval keeps a stale suspicion from cycling the radio
	// over and over.
	smsRadioCycleMinInterval = 10 * time.Minute
	// smsStorageSettleDelay is how long a divergence has to persist before the
	// radio is cycled. ModemManager ingests and deletes a message it did learn
	// about within a second, so a divergence that survives this delay is not
	// part of the healthy pipeline.
	smsStorageSettleDelay = 30 * time.Second
	// smsSendQuietWindow suppresses the detection right after the agent sent a
	// message itself: that path stores the modem's own copy, which ModemManager
	// drops from its object list, and the modem may keep it. The copy is not a
	// message the user is waiting for.
	smsSendQuietWindow = 2 * time.Minute
)

// smsReachabilityState remembers what the previous pass saw about the modem's
// message storage.
type smsReachabilityState struct {
	mu          sync.Mutex
	used        int
	known       int
	observedAt  time.Time
	suspectedAt time.Time
	lastCycleAt time.Time
	lastSentAt  time.Time
}

func (state *smsReachabilityState) noteSent(at time.Time) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.lastSentAt = at
}

func (state *smsReachabilityState) markCycled(at time.Time) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.lastCycleAt = at
	state.suspectedAt = time.Time{}
}

// diverged records this pass and reports whether the modem is holding messages
// ModemManager does not report, for long enough and under conditions that make
// a radio cycle the right answer.
func (state *smsReachabilityState) diverged(used, known int, at time.Time) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.used = used
	state.known = known
	state.observedAt = at
	if used <= known {
		state.suspectedAt = time.Time{}
		return false
	}
	if !state.lastSentAt.IsZero() && at.Sub(state.lastSentAt) < smsSendQuietWindow {
		state.suspectedAt = time.Time{}
		return false
	}
	if state.suspectedAt.IsZero() {
		state.suspectedAt = at
		return false
	}
	if at.Sub(state.suspectedAt) < smsStorageSettleDelay {
		return false
	}
	if !state.lastCycleAt.IsZero() && at.Sub(state.lastCycleAt) < smsRadioCycleMinInterval {
		return false
	}
	return true
}

// smsStorageUsage parses how many messages the modem holds from an AT+CPMS?
// answer. Status report storage ("SR") is skipped: delivery reports are not
// receptions, and ModemManager tracks them separately from messages.
func smsStorageUsage(response string) (int, bool) {
	used := -1
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "+CPMS:") {
			continue
		}
		fields := strings.Split(strings.TrimSpace(strings.TrimPrefix(line, "+CPMS:")), ",")
		for index := 0; index+2 < len(fields); index += 3 {
			name := strings.ToUpper(strings.Trim(strings.TrimSpace(fields[index]), `"`))
			if name == "SR" {
				continue
			}
			count, err := strconv.Atoi(strings.TrimSpace(fields[index+1]))
			if err != nil {
				continue
			}
			if count > used {
				used = count
			}
		}
	}
	if used < 0 {
		return 0, false
	}
	return used, true
}

// smsServiceValue parses the service domain from an AT+CGSMS? answer.
func smsServiceValue(response string) (int, bool) {
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "+CGSMS:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "+CGSMS:"))
		service, err := strconv.Atoi(value)
		if err != nil {
			return 0, false
		}
		return service, true
	}
	return 0, false
}

// messageLine returns the line that owns the modem's messaging interface.
func messageLine(parsed ParsedObjects) (domain.Line, dbus.ObjectPath, bool) {
	for _, line := range parsed.Lines {
		if !line.Capabilities.MessagingInterface {
			continue
		}
		path, found := parsed.LinePaths[line.ID]
		if !found || !path.IsValid() {
			continue
		}
		return line, path, true
	}
	return domain.Line{}, "", false
}

// ReconcileSMSReachability recovers receptions ModemManager is not reporting,
// and keeps the modem's SMS service domain usable on an LTE-only attach.
func (p *Provider) ReconcileSMSReachability(ctx context.Context) error {
	parsed, err := p.snapshotContent(ctx, "reconcile_sms_reachability")
	if err != nil {
		return err
	}
	line, modemPath, found := messageLine(parsed)
	if !found {
		return nil
	}
	if err := p.ensureSMSService(ctx, line.ID); err != nil {
		slog.Warn(
			"keep the modem SMS service domain usable",
			"component", "modemmanager",
			"line_id", line.ID,
			"error", err,
		)
	}

	response, err := p.commandAT(ctx, line.ID, smsStorageQuery)
	if err != nil {
		return fmt.Errorf("read modem message storage: %w", err)
	}
	used, known := smsStorageUsage(response)
	if !known {
		return nil
	}
	now := time.Now()
	if !p.smsState.diverged(used, len(parsed.MessagePaths), now) {
		return nil
	}
	// A cycle drops the radio for a moment: never do it to an ongoing call, and
	// never against the user's own "radio off" preference.
	if len(parsed.Calls) > 0 || !p.radioStates.enabled(line.ID) {
		return nil
	}
	p.smsState.markCycled(now)
	slog.Warn(
		"the modem holds SMS ModemManager never reported; cycling the radio to surface it",
		"component", "modemmanager",
		"line_id", line.ID,
		"storage_used", used,
		"reported", len(parsed.MessagePaths),
	)
	return p.cycleRadio(modemPath)
}

// ensureSMSService repairs a modem left on a circuit-switched-only SMS domain,
// which cannot be used on an LTE-only attach.
func (p *Provider) ensureSMSService(ctx context.Context, lineID string) error {
	response, err := p.commandAT(ctx, lineID, smsServiceQuery)
	if err != nil {
		return err
	}
	service, known := smsServiceValue(response)
	if !known || service == smsExpectedService {
		return nil
	}
	if _, err := p.commandAT(ctx, lineID, smsServiceWrite); err != nil {
		return err
	}
	slog.Warn(
		"the modem SMS service domain was not usable on an LTE-only attach and has been corrected",
		"component", "modemmanager",
		"line_id", lineID,
		"previous", service,
		"expected", smsExpectedService,
	)
	return nil
}

// cycleRadio disables and re-enables the modem, the same transition the
// airplane mode switch performs, because that transition is what makes
// ModemManager read the message storage again.
func (p *Provider) cycleRadio(modemPath dbus.ObjectPath) error {
	cycleContext, cancel := context.WithTimeout(context.Background(), smsRadioCycleTimeout)
	defer cancel()
	var result error
	for _, enabled := range []bool{false, true} {
		if _, err := p.call(
			cycleContext,
			modemPath,
			modemInterface+".Enable",
			"reconcile_sms_reachability",
			"ModemManager failed to cycle the modem radio",
			enabled,
		); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}
