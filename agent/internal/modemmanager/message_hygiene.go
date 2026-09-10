package modemmanager

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// stuckMessageDeleteTimeout bounds a single wedged-SMS deletion.
const stuckMessageDeleteTimeout = 10 * time.Second

// messageStateInTransit reports whether a ModemManager SMS state is a
// transitional one. These are the two states the modem can fail to leave; the
// names mirror MM_SMS_STATE_* as projected by messageStateName.
func messageStateInTransit(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "receiving", "sending":
		return true
	default:
		return false
	}
}

// ReconcileStuckMessages frees SMS slots ModemManager has stopped progressing.
//
// A reception that never completes leaves the SMS object sitting in the
// "receiving" state indefinitely — measured on the QDC507, where a long message
// from 95555 stayed in "receiving" for over six hours. The wedged object keeps
// occupying the modem's SMS slot, so every later SMS also fails to complete and
// the user cannot receive anything until the radio is reset by hand (which is
// why toggling airplane mode appeared to "fix" it).
//
// Such a message never changes state again, so it emits no further
// ModemManager signals: no lifecycle event can ever prompt its cleanup, and an
// event-driven design would never even observe it a second time. It therefore
// has to be found by periodic inspection. Each pass records which messages are
// in transit; one still in transit on the following pass has been stalled for a
// full reconcile interval, which is far longer than a healthy concatenated
// reception takes, and is cleared. Using the interval itself as the threshold
// avoids depending on the SMSC clock, which ModemManager may not supply.
//
// The passes are driven by the agent's periodic reconciler (see
// runMessageReconciler in cmd/modemdeck-agent), so a wedged message is cleared
// shortly after it is detected regardless of whether anything is talking to the
// API.
func (p *Provider) ReconcileStuckMessages(ctx context.Context) error {
	snapshot, err := p.Snapshot(ctx)
	if err != nil {
		return err
	}
	for _, message := range p.stuckTransitMessages(snapshot.Messages) {
		deleteContext, cancel := context.WithTimeout(ctx, stuckMessageDeleteTimeout)
		deleteErr := p.DeleteMessage(deleteContext, domain.DeleteMessageRequest{
			MessageID: message.ID,
		})
		cancel()
		if deleteErr != nil {
			slog.Warn(
				"SMS stuck mid-reception could not be cleared from the modem",
				"message_id", message.ID,
				"line_id", message.LineID,
				"state", message.State,
				"error", deleteErr,
			)
			continue
		}
		slog.Warn(
			"SMS stuck mid-reception was cleared to free the modem SMS slot",
			"message_id", message.ID,
			"line_id", message.LineID,
			"state", message.State,
		)
	}
	return nil
}

// stuckTransitMessages records the messages that are in transit right now and
// returns those that were already in transit on the previous pass. Messages
// that leave the snapshot are forgotten, so the bookkeeping only ever holds
// messages in flight and cannot grow.
func (p *Provider) stuckTransitMessages(messages []domain.Message) []domain.Message {
	p.transitMu.Lock()
	defer p.transitMu.Unlock()

	inTransit := make(map[string]struct{}, len(messages))
	stuck := make([]domain.Message, 0)
	for _, message := range messages {
		if !messageStateInTransit(message.State) {
			continue
		}
		messageID := strings.TrimSpace(message.ID)
		if messageID == "" {
			continue
		}
		inTransit[messageID] = struct{}{}
		if _, seen := p.transitMessages[messageID]; seen {
			stuck = append(stuck, message)
		}
	}
	// Assigning a fresh map keeps this working for a Provider built without an
	// explicit initialization step.
	p.transitMessages = inTransit
	return stuck
}
