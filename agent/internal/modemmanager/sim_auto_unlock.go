package modemmanager

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// SIM PIN auto unlock. The API layer stores a SIM PIN per line and pushes it
// here. Whenever a snapshot observes the modem waiting for a SIM PIN (the
// QDC507 re-locks the SIM after every module reboot, which silently prevents
// IMS/VoLTE registration), the provider submits the stored PIN through the
// regular ModemManager SIM interface.
//
// Safety rules:
//   - At most one attempt runs per line at any time.
//   - A failed attempt backs off exponentially (15s doubling, 10m cap).
//   - After 3 consecutive failures the entry stops retrying until it is
//     re-saved or succeeds, so a wrong PIN can never burn retries down to a
//     PUK lock.
//   - When ModemManager reports the remaining PIN retry count and that count
//     is 1, the attempt is skipped for the same reason.
const (
	simAutoUnlockBaseDelay     = 15 * time.Second
	simAutoUnlockMaxDelay      = 10 * time.Minute
	simAutoUnlockMaxAttempts   = 3
	simAutoUnlockMinSafeRetries = 2
	simAutoUnlockLockCode      = uint32(2) // MM_MODEM_LOCK_SIM_PIN
)

type simAutoUnlockEntry struct {
	pin         string
	updatedAt   time.Time
	attempts    int
	nextAttempt time.Time
	running     bool
}

func (p *Provider) SetSIMAutoUnlock(
	ctx context.Context,
	request domain.SIMAutoUnlockRequest,
) (domain.SIMAutoUnlockStatus, error) {
	const operation = "set_sim_auto_unlock"
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.LineID = strings.TrimSpace(request.LineID)
	if err := validateRequestID(operation, request.RequestID); err != nil {
		return domain.SIMAutoUnlockStatus{}, err
	}
	if request.LineID == "" {
		return domain.SIMAutoUnlockStatus{}, domain.InvalidArgument(operation, "line id is required")
	}
	if request.Enabled && !validPIN(request.PIN) {
		return domain.SIMAutoUnlockStatus{}, domain.InvalidArgument(
			operation,
			"enabling SIM auto unlock requires a valid PIN",
		)
	}

	now := p.now().UTC()
	p.simAutoUnlockMu.Lock()
	if request.Enabled {
		p.simAutoUnlocks[request.LineID] = &simAutoUnlockEntry{
			pin:         request.PIN,
			updatedAt:   now,
			nextAttempt: now,
		}
	} else {
		delete(p.simAutoUnlocks, request.LineID)
	}
	p.simAutoUnlockMu.Unlock()

	slog.Info(
		"SIM auto unlock configuration updated",
		"component", "modemmanager",
		"operation", operation,
		"line_id", request.LineID,
		"enabled", request.Enabled,
	)
	if request.Enabled {
		p.startSIMAutoUnlockAttempt(ctx, request.LineID)
	}
	return p.SIMAutoUnlockStatus(ctx, request.LineID)
}

func (p *Provider) SIMAutoUnlockStatus(
	ctx context.Context,
	lineID string,
) (domain.SIMAutoUnlockStatus, error) {
	lineID = strings.TrimSpace(lineID)
	if lineID == "" {
		return domain.SIMAutoUnlockStatus{}, domain.InvalidArgument(
			"read_sim_auto_unlock",
			"line id is required",
		)
	}
	p.simAutoUnlockMu.Lock()
	defer p.simAutoUnlockMu.Unlock()
	entry, found := p.simAutoUnlocks[lineID]
	if !found {
		return domain.SIMAutoUnlockStatus{LineID: lineID}, nil
	}
	return domain.SIMAutoUnlockStatus{
		LineID:    lineID,
		Enabled:   true,
		UpdatedAt: entry.updatedAt,
	}, nil
}

func (p *Provider) ClearSIMAutoUnlock(
	ctx context.Context,
	lineID string,
) (domain.SIMAutoUnlockStatus, error) {
	lineID = strings.TrimSpace(lineID)
	if lineID == "" {
		return domain.SIMAutoUnlockStatus{}, domain.InvalidArgument(
			"clear_sim_auto_unlock",
			"line id is required",
		)
	}
	p.simAutoUnlockMu.Lock()
	entry, found := p.simAutoUnlocks[lineID]
	entryRunning := found && entry.running
	p.simAutoUnlockMu.Unlock()
	if entryRunning {
		return domain.SIMAutoUnlockStatus{}, domain.Conflict(
			"clear_sim_auto_unlock",
			"an auto unlock attempt is running; retry in a moment",
		)
	}
	p.simAutoUnlockMu.Lock()
	delete(p.simAutoUnlocks, lineID)
	p.simAutoUnlockMu.Unlock()
	slog.Info(
		"SIM auto unlock configuration cleared",
		"component", "modemmanager",
		"line_id", lineID,
	)
	return domain.SIMAutoUnlockStatus{LineID: lineID}, nil
}

// projectSIMAutoUnlock inspects one parsed snapshot and launches unlock
// attempts for lines that are waiting for a SIM PIN. It never blocks the
// snapshot: attempts run in their own goroutine.
func (p *Provider) projectSIMAutoUnlock(
	ctx context.Context,
	operation string,
	parsed *ParsedObjects,
	objects ManagedObjects,
	observedAt time.Time,
) {
	if parsed == nil || p.simAutoUnlocks == nil {
		return
	}
	now := observedAt
	candidates := make([]string, 0, len(parsed.Lines))
	for index := range parsed.Lines {
		line := &parsed.Lines[index]
		path, found := parsed.LinePaths[line.ID]
		if !found || !path.IsValid() {
			continue
		}
		interfaces, found := objects[path]
		if !found {
			continue
		}
		modemProperties, found := interfaces[modemInterface]
		if !found {
			continue
		}
		lockCode, _ := uint32Property(modemProperties, "UnlockRequired")
		if lockCode != simAutoUnlockLockCode {
			continue
		}
		if !p.simAutoUnlockReady(line.ID, now, modemProperties) {
			continue
		}
		candidates = append(candidates, line.ID)
	}
	for _, lineID := range candidates {
		p.startSIMAutoUnlockAttempt(ctx, lineID)
	}
}

// simAutoUnlockReady reports whether an attempt may start for a line now.
func (p *Provider) simAutoUnlockReady(
	lineID string,
	now time.Time,
	modemProperties Properties,
) bool {
	p.simAutoUnlockMu.Lock()
	defer p.simAutoUnlockMu.Unlock()
	entry, found := p.simAutoUnlocks[lineID]
	if !found || entry.pin == "" || entry.running {
		return false
	}
	if entry.attempts >= simAutoUnlockMaxAttempts || now.Before(entry.nextAttempt) {
		return false
	}
	retries, known := unlockRetriesProperty(modemProperties)["sim-pin"]
	if known && retries > 0 && retries < simAutoUnlockMinSafeRetries {
		slog.Warn(
			"SIM auto unlock skipped to protect PUK retries",
			"component", "modemmanager",
			"line_id", lineID,
			"remaining_retries", retries,
		)
		return false
	}
	return true
}

func (p *Provider) startSIMAutoUnlockAttempt(ctx context.Context, lineID string) {
	p.simAutoUnlockMu.Lock()
	entry, found := p.simAutoUnlocks[lineID]
	if !found || entry.pin == "" || entry.running {
		p.simAutoUnlockMu.Unlock()
		return
	}
	entry.running = true
	pin := entry.pin
	p.simAutoUnlockMu.Unlock()

	go func() {
		defer func() {
			p.simAutoUnlockMu.Lock()
			if current, found := p.simAutoUnlocks[lineID]; found {
				current.running = false
			}
			p.simAutoUnlockMu.Unlock()
		}()
		p.runSIMAutoUnlockAttempt(ctx, lineID, pin)
	}()
}

func (p *Provider) runSIMAutoUnlockAttempt(ctx context.Context, lineID, pin string) {
	const operation = "sim_auto_unlock_attempt"
	// The attempt outlives the snapshot request that triggered it, so it runs
	// on a detached context with its own deadline.
	attemptContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	request := domain.SIMCommandRequest{
		RequestID: fmt.Sprintf("sim-auto-unlock-%d", p.now().UnixNano()),
		Operation: domain.SIMSendPIN,
		PIN:       pin,
	}
	p.configMu.Lock()
	_, err := p.submitSIMPin(attemptContext, operation, lineID, request.PIN)
	p.configMu.Unlock()

	p.simAutoUnlockMu.Lock()
	defer p.simAutoUnlockMu.Unlock()
	entry, found := p.simAutoUnlocks[lineID]
	if !found || entry.pin != pin {
		return
	}
	if err != nil {
		entry.attempts++
		delay := simAutoUnlockBaseDelay << (entry.attempts - 1)
		if delay > simAutoUnlockMaxDelay || delay <= 0 {
			delay = simAutoUnlockMaxDelay
		}
		entry.nextAttempt = p.now().UTC().Add(delay)
		slog.Warn(
			"SIM PIN auto unlock attempt failed",
			"component", "modemmanager",
			"operation", operation,
			"line_id", lineID,
			"attempts", entry.attempts,
			"retry_in", delay.String(),
			"error", err,
		)
		return
	}
	entry.attempts = 0
	entry.updatedAt = p.now().UTC()
	entry.nextAttempt = time.Time{}
	slog.Info(
		"SIM PIN auto unlock succeeded",
		"component", "modemmanager",
		"line_id", lineID,
	)
}

// submitSIMPin submits the PIN on the line's active SIM. Callers must hold
// configMu.
func (p *Provider) submitSIMPin(
	ctx context.Context,
	operation string,
	lineID string,
	pin string,
) (dbus.ObjectPath, error) {
	line, _, _, _, err := p.resolveLineServices(ctx, operation, lineID)
	if err != nil {
		return "", err
	}
	if !line.SIMPresent || !dbus.ObjectPath(line.SIMPath).IsValid() {
		return "", domain.FailedPrecondition(operation, "line has no active SIM", nil)
	}
	simPath := dbus.ObjectPath(line.SIMPath)
	if _, err := p.call(
		ctx,
		simPath,
		simInterface+".SendPin",
		operation,
		"ModemManager failed to submit the SIM PIN",
		pin,
	); err != nil {
		return "", err
	}
	return simPath, nil
}
