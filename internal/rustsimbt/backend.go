// SPDX-License-Identifier: AGPL-3.0-only
package rustsimbt

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/airsimgw"
)

type Config struct {
	SocketDirectory string     `json:"socket_directory,omitempty"`
	Peer            string     `json:"peer"`
	LineID          string     `json:"line_id"`
	Credential      Credential `json:"credential"`
}

func WithBluetooth(fallback airsimgw.Backend, config Config) (airsimgw.Backend, error) {
	if fallback == nil || !validLine(config.LineID) {
		return nil, ErrInvalid
	}
	var client *Client
	var err error
	if config.SocketDirectory != "" {
		client, err = newViaHost(config.SocketDirectory, config.Credential)
	} else {
		client, err = New(config.Peer, config.Credential)
	}
	if err != nil {
		return nil, err
	}
	return &backend{Backend: fallback, line: config.LineID, client: client}, nil
}

func validLine(value string) bool {
	return len(value) >= 1 && len(value) <= 128 && !strings.ContainsAny(value, "\r\n\x00")
}

// The existing gateway still owns downstream account checks. There is one
// upstream NAS credential; keep each active call scoped to its downstream
// pairing so another signed-in phone cannot control, renew, or hear it.
type backend struct {
	airsimgw.Backend
	line         string
	client       *Client
	mu           sync.Mutex
	call, holder string
}

func (b *backend) Line(ctx context.Context, id airsimgw.Identity) (airsimgw.Line, error) {
	if id.LineID != b.line {
		return b.Backend.Line(ctx, id)
	}
	if err := b.Authorize(ctx, id); err != nil {
		return airsimgw.Line{}, err
	}
	device, err := b.client.Device(ctx)
	if err != nil {
		return airsimgw.Line{}, mapped(err)
	}
	status, err := b.client.Status(ctx)
	if err != nil {
		return airsimgw.Line{}, mapped(err)
	}
	return airsimgw.Line{
		DeviceName: device.Name, Manufacturer: device.Manufacturer, Model: device.Model,
		PhoneNumber: device.Number, Firmware: status.Firmware, Operator: status.Operator,
		SignalDBM: status.SignalDBM, Registered: status.CellularState == "registered",
		Searching: status.CellularState == "searching",
	}, nil
}

func callTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func callView(c Call, media bool) airsimgw.Call {
	phase := c.State
	if phase == "incoming" || phase == "waiting" {
		phase = "ringing"
	}
	if phase == "alerting" {
		phase = "connecting"
	}
	return airsimgw.Call{
		ID: c.ID, Direction: c.Direction, Phase: phase, Number: c.Number,
		CreatedAt: callTime(c.Started), UpdatedAt: callTime(c.Updated), EndedAt: callTime(c.Ended),
		Missed: c.Missed, MediaAvailable: media && phase == "active",
	}
}

func (b *backend) Calls(ctx context.Context, id airsimgw.Identity, limit int) ([]airsimgw.Call, []airsimgw.Call, error) {
	if id.LineID != b.line {
		return b.Backend.Calls(ctx, id, limit)
	}
	if err := b.Authorize(ctx, id); err != nil {
		return nil, nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	active, history, err := b.client.Calls(ctx)
	if err != nil {
		return nil, nil, mapped(err)
	}
	var live []airsimgw.Call
	if active != nil {
		live = append(live, callView(*active, b.owns(id, active.ID)))
	}
	if active == nil || active.ID != b.call {
		b.client.release(b.call)
		b.call, b.holder = "", ""
	}
	var ended []airsimgw.Call
	if limit > len(history) {
		limit = len(history)
	}
	for i := 0; i < limit; i++ {
		ended = append(ended, callView(history[i], false))
	}
	return live, ended, nil
}

func (b *backend) owns(id airsimgw.Identity, call string) bool {
	return call != "" && id.PairingID != "" && b.call == call && b.holder == id.PairingID
}

func (b *backend) Dial(ctx context.Context, id airsimgw.Identity, number, request string) (airsimgw.Call, error) {
	if id.LineID != b.line {
		return b.Backend.Dial(ctx, id, number, request)
	}
	if err := b.Authorize(ctx, id); err != nil {
		return airsimgw.Call{}, err
	}
	if id.PairingID == "" {
		return airsimgw.Call{}, airsimgw.ErrForbidden
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.client.Dial(ctx, number, request)
	if err != nil {
		return airsimgw.Call{}, mapped(err)
	}
	b.call, b.holder = c.ID, id.PairingID
	return callView(c, true), nil
}

func (b *backend) CallAction(ctx context.Context, id airsimgw.Identity, call, action, digits, request string) error {
	if id.LineID != b.line {
		return b.Backend.CallAction(ctx, id, call, action, digits, request)
	}
	if err := b.Authorize(ctx, id); err != nil {
		return err
	}
	if id.PairingID == "" {
		return airsimgw.ErrForbidden
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch action {
	case "answer", "reject":
		if b.call != "" {
			return airsimgw.ErrConflict
		}
		active, _, err := b.client.Calls(ctx)
		if err != nil {
			return mapped(err)
		}
		if active == nil || active.ID != call || active.Direction != "incoming" || (active.State != "incoming" && active.State != "waiting") {
			return airsimgw.ErrConflict
		}
	case "hangup", "dtmf":
		if !b.owns(id, call) {
			return airsimgw.ErrForbidden
		}
	default:
		return airsimgw.ErrConflict
	}
	if err := b.client.Action(ctx, call, action, digits); err != nil {
		return mapped(err)
	}
	if action == "answer" {
		b.call, b.holder = call, id.PairingID
	}
	if action == "hangup" {
		b.call, b.holder = "", ""
	}
	return nil
}

func (b *backend) KeepCall(ctx context.Context, id airsimgw.Identity, call string) error {
	if id.LineID != b.line {
		return b.Backend.KeepCall(ctx, id, call)
	}
	if err := b.Authorize(ctx, id); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.owns(id, call) {
		return airsimgw.ErrForbidden
	}
	return mapped(b.client.Keep(ctx, call))
}

func (b *backend) AttachPCM(ctx context.Context, id airsimgw.Identity, call string) (airsimgw.PCMStream, error) {
	if id.LineID != b.line {
		return b.Backend.AttachPCM(ctx, id, call)
	}
	if err := b.Authorize(ctx, id); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.owns(id, call) {
		return nil, airsimgw.ErrForbidden
	}
	pcm, err := b.client.OpenPCM(ctx, call)
	if err != nil {
		return nil, mapped(err)
	}
	return pcm, nil
}

func (b *backend) Messages(ctx context.Context, id airsimgw.Identity, since time.Time, limit int) ([]airsimgw.Message, error) {
	if id.LineID != b.line {
		return b.Backend.Messages(ctx, id, since, limit)
	}
	if err := b.Authorize(ctx, id); err != nil {
		return nil, err
	}
	messages, err := b.client.Messages(ctx)
	if err != nil {
		return nil, mapped(err)
	}
	var out []airsimgw.Message
	for _, m := range messages {
		if len(out) >= limit {
			break
		}
		at := callTime(m.Timestamp)
		if m.ID == "" || at.IsZero() || at.Before(since) {
			continue
		}
		out = append(out, airsimgw.Message{DeliveryID: m.ID, Sender: m.Sender, Content: m.Content, Timestamp: at})
	}
	return out, nil
}

func (b *backend) SendMessage(ctx context.Context, id airsimgw.Identity, to, content, request string) error {
	if id.LineID != b.line {
		return b.Backend.SendMessage(ctx, id, to, content, request)
	}
	return airsimgw.ErrForbidden
}

func mapped(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrUnknown):
		return &airsimgw.BackendError{Kind: airsimgw.ErrUnavailable, Message: "RustSimHub operation outcome unknown. Check the call before retrying."}
	case errors.Is(err, ErrDenied):
		return airsimgw.ErrConflict
	default:
		return airsimgw.ErrUnavailable
	}
}

var _ airsimgw.Backend = (*backend)(nil)
