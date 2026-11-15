package airsimgw

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const MaxLines = 32

var ErrInvalidLines = errors.New("airsim: choose 1 to 32 distinct lines")

func normalizeLines(ids []string) ([]string, error) {
	if len(ids) > MaxLines {
		return nil, ErrInvalidLines
	}
	out := []string{}
	for _, id := range ids {
		if id == "" || id != strings.TrimSpace(id) || len(id) > 128 || slices.Contains(out, id) {
			return nil, ErrInvalidLines
		}
		out = append(out, id)
	}
	return out, nil
}
func frozenLines(p Pairing) []string {
	if len(p.LineIDs) > 0 {
		return slices.Clone(p.LineIDs)
	}
	if p.LineID != "" {
		return []string{p.LineID}
	}
	return nil
}
func slotID(index int) string {
	if index == 0 {
		return "nassimhub-line"
	}
	return "nassimhub-line-" + strconv.Itoa(index+1)
}
func (g *Gateway) ConfigureLines(enabled bool, ids []string) error {
	lines, err := normalizeLines(ids)
	if err != nil {
		return err
	}
	if enabled && len(lines) == 0 {
		return ErrNoLine
	}
	if err = g.store.setLines(enabled, lines); err != nil {
		return err
	}
	g.CancelPairing()
	return nil
}
func (g *Gateway) selectedIdentity(base Identity, index int) (Identity, error) {
	if index < 0 || index >= len(base.LineIDs) {
		return Identity{}, ErrForbidden
	}
	enabled, selected := g.store.settingsLines()
	if !enabled || !slices.Contains(selected, base.LineIDs[index]) {
		return Identity{}, ErrForbidden
	}
	out := base
	out.LineID = base.LineIDs[index]
	out.LineIDs = slices.Clone(base.LineIDs)
	return out, nil
}
func (g *Gateway) selectAccount(base Identity, raw json.RawMessage) (Identity, error) {
	index := 0
	if len(raw) > 0 && string(raw) != "null" {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			var number int
			if json.Unmarshal(raw, &number) != nil {
				return Identity{}, ErrForbidden
			}
			value = strconv.Itoa(number)
		}
		switch value {
		case "", "0", "1", "nassimhub-line":
			index = 0
		default:
			value = strings.TrimPrefix(value, "nassimhub-line-")
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				return Identity{}, ErrForbidden
			}
			index = n - 1
		}
	}
	return g.selectedIdentity(base, index)
}

// scopedBackend preserves the original per-line Backend, account checks and
// command handlers. Aggregation never supplies authority to a write operation.
type scopedBackend struct {
	Backend
	gateway *Gateway
}

func (b scopedBackend) Authorize(ctx context.Context, id Identity) error {
	if !slices.Contains(id.LineIDs, id.LineID) {
		return ErrForbidden
	}
	enabled, selected := b.gateway.store.settingsLines()
	if !enabled || !slices.Contains(selected, id.LineID) {
		return ErrForbidden
	}
	return b.Backend.Authorize(ctx, id)
}
func (b scopedBackend) identities(ctx context.Context, base Identity) ([]Identity, error) {
	out := []Identity{}
	for i := range base.LineIDs {
		id, err := b.gateway.selectedIdentity(base, i)
		if err != nil {
			continue
		}
		if err = b.Backend.Authorize(ctx, id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, ErrForbidden
	}
	return out, nil
}
func (b scopedBackend) Line(ctx context.Context, id Identity) (Line, error) {
	if err := b.Authorize(ctx, id); err != nil {
		return Line{}, err
	}
	return b.Backend.Line(ctx, id)
}
func (b scopedBackend) Calls(ctx context.Context, base Identity, limit int) ([]Call, []Call, error) {
	ids, err := b.identities(ctx, base)
	if err != nil {
		return nil, nil, err
	}
	active, history := []Call{}, []Call{}
	for _, id := range ids {
		a, h, err := b.Backend.Calls(ctx, id, limit)
		if err != nil {
			return nil, nil, err
		}
		account := slotID(slices.Index(base.LineIDs, id.LineID))
		for _, item := range a {
			item.LineID = id.LineID
			item.SIMAccount = account
			active = append(active, item)
		}
		for _, item := range h {
			item.LineID = id.LineID
			item.SIMAccount = account
			history = append(history, item)
		}
	}
	sort.SliceStable(history, func(i, j int) bool { return history[i].UpdatedAt.After(history[j].UpdatedAt) })
	if limit > 0 && len(history) > limit {
		history = history[:limit]
	}
	return active, history, nil
}
func (b scopedBackend) target(ctx context.Context, base Identity, callID string) (Identity, error) {
	active, _, err := b.Calls(ctx, base, 0)
	if err != nil {
		return Identity{}, err
	}
	for _, call := range active {
		if call.ID == callID && liveCall(call) {
			index := slices.Index(base.LineIDs, call.LineID)
			return b.gateway.selectedIdentity(base, index)
		}
	}
	return Identity{}, ErrNotFound
}
func (b scopedBackend) Dial(ctx context.Context, id Identity, number, requestID string) (Call, error) {
	if err := b.Authorize(ctx, id); err != nil {
		return Call{}, err
	}
	return b.Backend.Dial(ctx, id, number, requestID)
}
func (b scopedBackend) CallAction(ctx context.Context, base Identity, callID, action, digits, requestID string) error {
	id, err := b.target(ctx, base, callID)
	if err != nil {
		return err
	}
	return b.Backend.CallAction(ctx, id, callID, action, digits, requestID)
}
func (b scopedBackend) KeepCall(ctx context.Context, base Identity, callID string) error {
	id, err := b.target(ctx, base, callID)
	if err != nil {
		return err
	}
	return b.Backend.KeepCall(ctx, id, callID)
}
func (b scopedBackend) AttachPCM(ctx context.Context, base Identity, callID string) (PCMStream, error) {
	id, err := b.target(ctx, base, callID)
	if err != nil {
		return nil, err
	}
	return b.Backend.AttachPCM(ctx, id, callID)
}
func (b scopedBackend) SendMessage(ctx context.Context, id Identity, to, body, requestID string) error {
	if err := b.Authorize(ctx, id); err != nil {
		return err
	}
	return b.Backend.SendMessage(ctx, id, to, body, requestID)
}
func (b scopedBackend) Messages(ctx context.Context, base Identity, since time.Time, limit int) ([]Message, error) {
	ids, err := b.identities(ctx, base)
	if err != nil {
		return nil, err
	}
	out := []Message{}
	for _, id := range ids {
		messages, err := b.Backend.Messages(ctx, id, since, limit)
		if err != nil {
			return nil, err
		}
		for _, item := range messages {
			item.LineID = id.LineID
			item.SIMAccount = slotID(slices.Index(base.LineIDs, id.LineID))
			if len(base.LineIDs) > 1 {
				item.DeliveryID = "nsh-air:" + base64.RawURLEncoding.EncodeToString([]byte(id.LineID)) + ":" + item.DeliveryID
			}
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out, nil
}
func (g *Gateway) slots(call *controlRequest) ([]map[string]any, error) {
	out := []map[string]any{}
	for index := range call.identity.LineIDs {
		id, err := g.selectedIdentity(call.identity, index)
		if err != nil {
			continue
		}
		line, err := call.backend.Line(call.ctx(), id)
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": slotID(index), "sim_account": slotID(index), "line_id": id.LineID, "label": firstNonEmpty(line.Operator, "SIM"), "number": line.PhoneNumber, "default": index == 0, "slot_index": index, "carrier_name": line.Operator, "is_default_voice": index == 0, "is_default_sms": index == 0})
	}
	return out, nil
}
