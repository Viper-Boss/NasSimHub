package trustengine

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"time"
)

// ChainReport is the result of verifying the security journal.
type ChainReport struct {
	// OK is true when every event from SealedFrom on hashes onto its
	// predecessor, the signed head matches the last event, and every trust
	// record's last event is in the journal.
	OK bool `json:"ok"`
	// Events is how many events were verified; HeadSeq the last sequence.
	Events  int64 `json:"events"`
	HeadSeq int64 `json:"head_seq"`
	// SealedFrom is non-zero after an administrator resealed a damaged
	// journal: events before it are kept but are not vouched for.
	SealedFrom int64 `json:"sealed_from,omitempty"`
	// Problem says what failed, and FirstBadSeq where.
	Problem     string    `json:"problem,omitempty"`
	FirstBadSeq int64     `json:"first_bad_seq,omitempty"`
	VerifiedAt  time.Time `json:"verified_at"`
}

// VerifyChain re-reads and re-hashes the journal. The result is remembered and
// drives the effective state: a journal that does not verify restricts every
// subject until an administrator reseals it.
func (e *Engine) VerifyChain(ctx context.Context) (ChainReport, error) {
	report := ChainReport{OK: true, VerifiedAt: e.now().UTC()}
	head, exists, err := e.store.Head(ctx)
	if err != nil {
		return ChainReport{}, err
	}
	fail := func(seq int64, problem string) {
		if report.OK {
			report.OK, report.Problem, report.FirstBadSeq = false, problem, seq
		}
	}
	seen := map[string]bool{}
	previousHash, previousSeq, first := "", int64(0), true
	if exists {
		report.SealedFrom = head.SealedFrom
		if !ed25519.Verify(e.public, head.signedBytes(), head.Signature) {
			fail(head.Seq, "the journal head is not signed by this gateway")
		}
	}
	err = e.store.EventsFrom(ctx, report.SealedFrom, func(event Event) error {
		if first {
			// The first verified event anchors the chain. From the very start
			// that anchor is the empty hash; after a reseal it is whatever the
			// reseal event recorded, which the signed head vouches for.
			if report.SealedFrom == 0 && (event.Seq != 1 || event.PrevHash != "") {
				fail(event.Seq, "the journal does not start at its first event")
			}
			previousHash, first = event.PrevHash, false
		} else if event.Seq != previousSeq+1 {
			fail(previousSeq+1, "an event is missing from the journal")
		}
		if event.PrevHash != previousHash || ChainHash(previousHash, event) != event.Hash {
			fail(event.Seq, "an event does not hash onto its predecessor")
		}
		previousHash, previousSeq = event.Hash, event.Seq
		seen[event.Hash] = true
		report.Events++
		return nil
	})
	if err != nil {
		return ChainReport{}, err
	}
	report.HeadSeq = previousSeq
	switch {
	case !exists && report.Events > 0:
		fail(previousSeq, "the journal has events but no signed head")
	case exists && (head.Seq != previousSeq || head.Hash != previousHash):
		fail(head.Seq, "the journal's last events do not match its signed head")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	for id, record := range e.records {
		if _, already := e.damaged[id]; already {
			continue
		}
		if report.OK && record.LastEventHash != "" && !seen[record.LastEventHash] {
			e.damaged[id] = "the trust record refers to an event that is not in the journal"
		}
	}
	e.chain, e.chainSet = report, true
	return report, nil
}

// Chain returns the last verification result.
func (e *Engine) Chain() ChainReport {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.chain
}

// ErrChainIntact is returned by Reseal when there is nothing to reseal.
var ErrChainIntact = fmt.Errorf("%w: the security journal verifies; there is nothing to reseal", ErrNotAllowed)

// Reseal is the authorised recovery for a damaged journal. It does not delete
// or rewrite anything: it appends an event that says who resealed and why, and
// moves the point verification starts from to that event. Everything before it
// stays readable and is reported as unverifiable from then on. Every subject
// is put back into observation, because the history that would have justified
// anything else can no longer be checked.
func (e *Engine) Reseal(ctx context.Context, actor, reason string) (ChainReport, error) {
	current, err := e.VerifyChain(ctx)
	if err != nil {
		return ChainReport{}, err
	}
	if current.OK {
		return current, ErrChainIntact
	}
	e.mu.Lock()
	event := e.event("", EventChainResealed, "journal verification failed: "+current.Problem, "administrator "+actor,
		"verification restarts here; earlier events are kept and not vouched for. "+Sanitize(reason))
	err = e.store.Commit(ctx, nil, []Event{event}, func(previous Head, events []Event, record *Record) (Head, error) {
		head, sealErr := e.seal(previous, events, record)
		if sealErr != nil {
			return Head{}, sealErr
		}
		head.SealedFrom = head.Seq
		head.Signature = ed25519.Sign(e.key, head.signedBytes())
		return head, nil
	})
	if err != nil {
		e.mu.Unlock()
		return ChainReport{}, err
	}
	for id, record := range e.records {
		next := *record
		previous := next.State
		next.RiskScore = max(next.RiskScore, e.policy.ExtendedAt)
		if rank(next.State) < rank(Observation) {
			next.State = Observation
		}
		next.StableSeconds, next.TrustedSeconds = 0, 0
		e.pending[id] = 0
		next.RequiredSeconds = uint64(e.policy.window(next.RiskScore) / time.Second)
		next.Reason = "security journal resealed: observation period"
		if err := e.commitLocked(ctx, &next, []Event{e.event(id, EventDowngrade, "security journal resealed", "administrator "+actor,
			fmt.Sprintf("%s -> %s, observation %s", previous, next.State, time.Duration(next.RequiredSeconds)*time.Second))}); err != nil {
			e.mu.Unlock()
			return ChainReport{}, err
		}
	}
	e.mu.Unlock()
	return e.VerifyChain(ctx)
}
