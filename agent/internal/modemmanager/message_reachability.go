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
//
// Measured behaviour worth remembering: right after a radio cycle the modem's
// new-message indications work again — one message was reported 24 seconds
// after the network sent it — and the reception degrades again within roughly
// half an hour (the next login code stayed in storage for 79 seconds until the
// next cycle). Why indications stop reaching ModemManager in between is still
// unknown; that is why the recovery is reactive and does not cycle the radio on
// a schedule.
const (
	smsStorageQuery = "AT+CPMS?"
	smsServiceQuery = "AT+CGSMS?"

	// urcPortQuery reads where the modem reports unsolicited indications
	// (incoming-call RING, +CMTI, ...). Measured on the QDC507: the modem was
	// shipped reporting them to "usbat" only, and incoming calls never reached
	// ModemManager at all — the call history held outgoing calls exclusively.
	urcPortQuery = "AT+QURCCFG?"
	// urcPortExpected is "report every indication on every port", so the port
	// ModemManager listens on always receives them.
	urcPortExpected = "all"
	urcPortWrite    = "AT+QURCCFG=\"urcport\",\"all\""
	// urcPortCheckInterval throttles the guard. The write only happens when a
	// read positively reports a wrong port, so the check is cheap, but the
	// firmware answers this query unreliably and there is no reason to probe
	// it every pass.
	urcPortCheckInterval = 10 * time.Minute
	// urcBlindWriteInterval bounds how often the guard rewrites the port
	// without being able to read it first. Measured on the QDC507: after the
	// port was written to "all", the firmware answers every AT+QURCCFG?
	// with "Unknown error" (across ModemManager restarts), so the guard
	// cannot rely on reading — yet indications keep drifting back to the
	// single "usbat" port (calls received hours after the write were lost
	// again). The write is idempotent and cheap; the throttle just spares
	// the modem's non-volatile memory.
	urcBlindWriteInterval = 30 * time.Minute

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
	// part of the healthy pipeline. Kept short because the message being
	// recovered is usually a login code with a five minute lifetime, and
	// because a radio cycle only costs about two seconds (measured).
	smsStorageSettleDelay = 10 * time.Second
	// smsSendQuietWindow suppresses the detection right after the agent sent a
	// message itself: that path stores the modem's own copy, which ModemManager
	// drops from its object list, and the modem may keep it. The copy shows up
	// within seconds of the send, so a short window is enough.
	smsSendQuietWindow = time.Minute
)

// smsReachabilityState remembers what the previous pass saw about the modem's
// message storage.
type smsReachabilityState struct {
	mu                sync.Mutex
	used              int
	known             int
	observedAt        time.Time
	suspectedAt       time.Time
	lastCycleAt       time.Time
	lastSentAt        time.Time
	urcCheckedAt      time.Time
	urcBlindWroteAt   time.Time
	urcCycleWroteLine string
	urcCycleWroteAt   time.Time
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

// urcPortDue reports and records whether this pass should re-check the
// modem's indication port.
func (state *smsReachabilityState) urcPortDue(at time.Time) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.urcCheckedAt.IsZero() && at.Sub(state.urcCheckedAt) < urcPortCheckInterval {
		return false
	}
	state.urcCheckedAt = at
	return true
}

// urcBlindWriteDue reports and records whether the guard may rewrite the
// indication port without a successful read first.
func (state *smsReachabilityState) urcBlindWriteDue(at time.Time) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.urcBlindWroteAt.IsZero() && at.Sub(state.urcBlindWroteAt) < urcBlindWriteInterval {
		return false
	}
	state.urcBlindWroteAt = at
	return true
}

// urcCycleRewriteDue reports and records whether the indication port should
// be rewritten right after a radio cycle on the given line.
func (state *smsReachabilityState) urcCycleRewriteDue(lineID string, at time.Time) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.urcCycleWroteLine == lineID && at.Sub(state.urcCycleWroteAt) < urcBlindWriteInterval {
		return false
	}
	state.urcCycleWroteLine = lineID
	state.urcCycleWroteAt = at
	return true
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

// urcPortValue parses the indication port from an AT+QURCCFG? answer such as
// `+QURCCFG: "urcport","usbat"`.
func urcPortValue(response string) (string, bool) {
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "+QURCCFG:") {
			continue
		}
		fields := strings.Split(strings.TrimPrefix(line, "+QURCCFG:"), ",")
		if len(fields) < 2 {
			return "", false
		}
		return strings.Trim(strings.TrimSpace(fields[1]), `"`), true
	}
	return "", false
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

// commandATBounded runs one reachability AT command under smsCommandTimeout.
//
// The reconciler's own attempt budget is minutes wide, and an AT command that
// stalls holds the modem's write lock for that whole budget — which is how the
// agent ends up reported as unavailable while nothing is actually wrong. Each
// probe here is a single short read, so bound it on its own.
func (p *Provider) commandATBounded(
	ctx context.Context,
	lineID string,
	command string,
) (string, error) {
	commandContext, cancel := context.WithTimeout(ctx, smsCommandTimeout)
	defer cancel()
	return p.commandAT(commandContext, lineID, command)
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
	if p.smsState.urcPortDue(time.Now()) {
		if err := p.ensureURCPort(ctx, line.ID); err != nil {
			// The read is unreliable on this firmware ("Unknown error" through
			// ModemManager was observed right after writing the port), and a
			// guard that cannot read must not act. Stay quiet at debug level.
			slog.Debug(
				"read the modem indication port",
				"component", "modemmanager",
				"line_id", line.ID,
				"error", err,
			)
		}
	}

	response, err := p.commandATBounded(ctx, line.ID, smsStorageQuery)
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
	return p.cycleRadio(line.ID, modemPath)
}

// ensureSMSService repairs a modem left on a circuit-switched-only SMS domain,
// which cannot be used on an LTE-only attach.
func (p *Provider) ensureSMSService(ctx context.Context, lineID string) error {
	response, err := p.commandATBounded(ctx, lineID, smsServiceQuery)
	if err != nil {
		return err
	}
	service, known := smsServiceValue(response)
	if !known || service == smsExpectedService {
		return nil
	}
	if _, err := p.commandATBounded(ctx, lineID, smsServiceWrite); err != nil {
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

// ensureURCPort repairs a modem that reports its unsolicited indications
// (incoming-call RING, +CMTI, ...) to a single USB AT port instead of every
// port. With the shipped "usbat" setting, incoming calls never reached
// ModemManager at all on the QDC507.
//
// The firmware answers AT+QURCCFG? with "Unknown error" once the port is on
// "all" (measured, across ModemManager restarts), so an unreadable query is
// not evidence the port is wrong — the guard falls back to a throttled blind
// rewrite, because indications measurably drift back to the single port.
func (p *Provider) ensureURCPort(ctx context.Context, lineID string) error {
	response, err := p.commandATBounded(ctx, lineID, urcPortQuery)
	if err != nil {
		if !p.smsState.urcBlindWriteDue(time.Now()) {
			return err
		}
		if _, writeErr := p.commandATBounded(ctx, lineID, urcPortWrite); writeErr != nil {
			return errors.Join(err, writeErr)
		}
		slog.Warn(
			"the modem indication port could not be read and has been rewritten unconditionally",
			"component", "modemmanager",
			"line_id", lineID,
			"read_error", err,
			"port", urcPortExpected,
		)
		return nil
	}
	port, known := urcPortValue(response)
	if !known || port == urcPortExpected {
		return nil
	}
	if _, err := p.commandATBounded(ctx, lineID, urcPortWrite); err != nil {
		return err
	}
	slog.Warn(
		"the modem reported its indications to one USB AT port and has been corrected",
		"component", "modemmanager",
		"line_id", lineID,
		"previous", port,
		"expected", urcPortExpected,
	)
	return nil
}

// rewriteURCPortAfterCycle rewrites the indication port right after a radio
// cycle: re-enabling the modem is when the firmware resets its indication
// routing, and a call received afterwards was measurably lost again until
// the port was rewritten.
func (p *Provider) rewriteURCPortAfterCycle(lineID string) {
	if _, err := p.commandATBounded(context.Background(), lineID, urcPortWrite); err != nil {
		slog.Warn(
			"rewrite the modem indication port after the radio cycle",
			"component", "modemmanager",
			"line_id", lineID,
			"error", err,
		)
	}
}

// cycleRadio disables and re-enables the modem, the same transition the
// airplane mode switch performs, because that transition is what makes
// ModemManager read the message storage again.
func (p *Provider) cycleRadio(lineID string, modemPath dbus.ObjectPath) error {
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
	// The re-enabled modem resets its indication routing; put the port back
	// on "all" before anything else can be missed.
	if result == nil {
		time.Sleep(3 * time.Second)
		p.rewriteURCPortAfterCycle(lineID)
	}
	return result
}
