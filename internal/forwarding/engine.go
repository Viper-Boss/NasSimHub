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
	stepMu    sync.Mutex
}

func expired(e Event, now time.Time) bool {
	return e.OccurredAt.IsZero() || e.OccurredAt.After(now.Add(2*time.Minute)) || !now.Before(e.OccurredAt.Add(MaxAge))
}
func (e *Engine) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := e.Step(ctx, time.Now().UTC()); err != nil {
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
			if err := e.deliver(ctx, t, now); err != nil {
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
	return e.Store.Prune(ctx, now)
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
	if err = e.Authorize(ctx, c.OwnerID, t.Event, ch); err != nil {
		return e.Store.Cancel(ctx, t, "authorization_revoked")
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
		return e.Store.Cancel(ctx, t, "authorization_revoked")
	}
	rendered, err := c.Render(rule, t.Event)
	if err != nil {
		return e.Store.Finish(ctx, t, "failed", "template_invalid", now)
	}
	err = e.Sender.Send(context.WithValue(sendCtx, ownerKey{}, c.OwnerID), ch, rendered, taskRequestID(t.ID))
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	if err == nil {
		return e.Store.Finish(finishCtx, t, "sent", "", now)
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

type ownerKey struct{}

func SenderOwner(ctx context.Context) string { id, _ := ctx.Value(ownerKey{}).(string); return id }

func ContextWithSenderOwner(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ownerKey{}, id)
}
