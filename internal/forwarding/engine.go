// SPDX-License-Identifier: AGPL-3.0-only
package forwarding

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Sender interface {
	Send(context.Context, Channel, Rendered, string) error
}
type Authorizer func(context.Context, string, Event, Channel) error
type Engine struct {
	Store     *SQLStore
	Sender    Sender
	Authorize Authorizer
	Report    func(string)
	Now       func() time.Time
	stepMu    sync.Mutex
}

func expired(e Event, now time.Time) bool {
	return e.OccurredAt.IsZero() || e.OccurredAt.After(now.Add(2*time.Minute)) || !now.Before(e.OccurredAt.Add(MaxAge))
}
func (e *Engine) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := e.Step(ctx, e.currentTime()); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if e.Report != nil {
				e.Report("forwarding_queue_unavailable")
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (e *Engine) Step(ctx context.Context, now time.Time) error {
	e.stepMu.Lock()
	defer e.stepMu.Unlock()
	if e.Store == nil || e.Sender == nil || e.Authorize == nil {
		return ErrUnavailable
	}
	if err := e.Store.Ingest(ctx, now); err != nil {
		return err
	}
	tasks, err := e.Store.Due(ctx, now)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	pool := make(chan struct{}, 4)
	var mu sync.Mutex
	var first error
	for _, t := range tasks {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case pool <- struct{}{}:
		}
		wg.Add(1)
		go func(t Task) {
			defer wg.Done()
			defer func() { <-pool }()
			if err := e.deliver(ctx, t, e.currentTime()); err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}(t)
	}
	wg.Wait()
	if first != nil {
		return first
	}
	return e.Store.Prune(ctx, e.currentTime())
}
func (e *Engine) deliver(ctx context.Context, t Task, now time.Time) error {
	if expired(t.Event, now) {
		return e.Store.Cancel(ctx, t, "expired")
	}
	if t.Attempts >= MaxAttempts {
		return e.Store.Cancel(ctx, t, "attempt_limit")
	}
	c, err := e.Store.Config(ctx)
	if err != nil {
		return err
	}
	if !c.Enabled || c.Revision != t.Revision {
		return e.Store.Cancel(ctx, t, "configuration_changed")
	}
	var ch Channel
	var rule Rule
	foundChannel, foundRule := false, false
	for _, candidate := range c.Channels {
		if candidate.ID == t.ChannelID {
			ch = candidate
			foundChannel = ch.Enabled
		}
	}
	for _, candidate := range c.Rules {
		if candidate.ID == t.RuleID {
			rule = candidate
			foundRule = rule.Matches(t.Event)
		}
	}
	if !foundChannel || !foundRule {
		return e.Store.Cancel(ctx, t, "configuration_changed")
	}
	if next := rule.Quiet.Next(now); next.After(now) {
		return e.Store.Defer(ctx, t, now, next)
	}
	if err = e.Authorize(ctx, c.OwnerID, t.Event, ch); err != nil {
		return e.authorizationFailure(ctx, t, err, false)
	}
	now = e.currentTime()
	if expired(t.Event, now) {
		return e.Store.Cancel(ctx, t, "expired")
	}
	claimed, ok, err := e.Store.Claim(ctx, t, now)
	if err != nil || !ok {
		return err
	}
	t = claimed
	sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Re-read both authority and the switch at the final public send boundary.
	current, err := e.Store.Config(sendCtx)
	if err != nil {
		return err
	}
	if !current.Enabled || current.Revision != c.Revision {
		return e.Store.Cancel(ctx, t, "configuration_changed")
	}
	if err = e.Authorize(sendCtx, c.OwnerID, t.Event, ch); err != nil {
		return e.authorizationFailure(ctx, t, err, true)
	}
	now = e.currentTime()
	if expired(t.Event, now) {
		return e.Store.Cancel(ctx, t, "expired")
	}
	if next := rule.Quiet.Next(now); next.After(now) {
		return e.Store.Defer(ctx, t, now, next)
	}
	rendered, err := c.Render(rule, t.Event)
	if err != nil {
		return e.Store.Finish(ctx, t, "failed", "template_invalid", now)
	}
	err = e.Sender.Send(context.WithValue(sendCtx, ownerKey{}, c.OwnerID), ch, rendered, taskRequestID(t.ID))
	now = e.currentTime()
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	if err == nil {
		return e.Store.Finish(finishCtx, t, "sent", "", now)
	}
	// The in-process SMS adapter can fail its final principal lookup before
	// entering the business handler. No send was attempted in that case.
	if ch.Type == "sms" && errors.Is(err, ErrUnavailable) {
		return e.authorizationFailure(finishCtx, t, err, true)
	}
	if errors.Is(err, ErrDenied) {
		return e.Store.Finish(finishCtx, t, "failed", "configuration_invalid", now)
	}
	if t.Attempts >= MaxAttempts {
		return e.Store.Finish(finishCtx, t, "failed", "attempt_limit", now)
	}
	// Only fixed classes are persisted: transport errors can include raw URLs,
	// credentials, SMTP replies and message bodies. Retries are at-least-once.
	return e.Store.Finish(finishCtx, t, "pending", "delivery_failed", now.Add(time.Duration(1<<t.Attempts)*15*time.Second))
}

func (e *Engine) authorizationFailure(ctx context.Context, t Task, cause error, claimed bool) error {
	if errors.Is(cause, ErrDenied) {
		return e.Store.Cancel(ctx, t, "authorization_revoked")
	}
	// Failure to establish current permission must prevent sending, but is not
	// evidence of revocation. Keep the payload and refund only this worker's
	// unused claim so an outage cannot exhaust the delivery attempt budget.
	retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := e.Store.deferAuthorization(retryCtx, t, e.currentTime(), claimed); err != nil {
		return err
	}
	return ErrUnavailable
}

type ownerKey struct{}

func SenderOwner(ctx context.Context) string { id, _ := ctx.Value(ownerKey{}).(string); return id }

func ContextWithSenderOwner(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ownerKey{}, id)
}

// A batch can wait behind four slow senders. Each claim, final send check and
// retry uses its own wall time rather than granting leases from batch start.
func (e *Engine) currentTime() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}
