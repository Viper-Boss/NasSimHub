package airsimgw

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"
)

type routedBackend struct {
	*fakeBackend
	routeMu   sync.Mutex
	revoked   string
	sentLines []string
	dialLines []string
	pcmLines  []string
}

func (b *routedBackend) Authorize(ctx context.Context, id Identity) error {
	b.routeMu.Lock()
	revoked := b.revoked
	b.routeMu.Unlock()
	if id.LineID == revoked {
		return &BackendError{Kind: ErrUnavailable, Message: "line_not_assigned"}
	}
	return b.fakeBackend.Authorize(ctx, id)
}
func (b *routedBackend) Line(ctx context.Context, id Identity) (Line, error) {
	if err := b.Authorize(ctx, id); err != nil {
		return Line{}, err
	}
	return Line{DeviceName: id.LineID, PhoneNumber: id.LineID, Operator: id.LineID}, nil
}
func (b *routedBackend) Messages(ctx context.Context, id Identity, since time.Time, limit int) ([]Message, error) {
	if err := b.Authorize(ctx, id); err != nil {
		return nil, err
	}
	return []Message{{DeliveryID: "same-local-id", Sender: "synthetic", Content: "message from " + id.LineID, Timestamp: time.Now()}}, nil
}
func (b *routedBackend) SendMessage(ctx context.Context, id Identity, to, body, requestID string) error {
	if err := b.Authorize(ctx, id); err != nil {
		return err
	}
	b.routeMu.Lock()
	b.sentLines = append(b.sentLines, id.LineID)
	b.routeMu.Unlock()
	return nil
}
func (b *routedBackend) Dial(ctx context.Context, id Identity, to, requestID string) (Call, error) {
	if err := b.Authorize(ctx, id); err != nil {
		return Call{}, err
	}
	b.routeMu.Lock()
	b.dialLines = append(b.dialLines, id.LineID)
	b.routeMu.Unlock()
	return Call{ID: "call-" + id.LineID}, nil
}
func (b *routedBackend) Calls(ctx context.Context, id Identity, limit int) ([]Call, []Call, error) {
	if err := b.Authorize(ctx, id); err != nil {
		return nil, nil, err
	}
	if id.LineID == "line-2" {
		return []Call{{ID: "call-line-2", Phase: "active", MediaAvailable: true}}, nil, nil
	}
	return []Call{}, []Call{}, nil
}
func (b *routedBackend) AttachPCM(ctx context.Context, id Identity, callID string) (PCMStream, error) {
	if err := b.Authorize(ctx, id); err != nil {
		return nil, err
	}
	b.routeMu.Lock()
	b.pcmLines = append(b.pcmLines, id.LineID)
	b.routeMu.Unlock()
	return newFakeStream(), nil
}
func multiHarness(t *testing.T) (*harness, *routedBackend, *app) {
	h := newHarness(t)
	backend := &routedBackend{fakeBackend: h.backend}
	h.gateway.SetBackend(backend)
	if err := h.gateway.ConfigureLines(true, []string{"line-1", "line-2"}); err != nil {
		t.Fatal(err)
	}
	return h, backend, pairedApp(t, h)
}
func TestMultilineSignedAppRoutesSMSAndDialWithoutScopeGrowth(t *testing.T) {
	h, b, phone := multiHarness(t)
	status, raw, _ := phone.do("GET", "/v1/health", nil)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	health := decode[struct {
		Slots []map[string]any `json:"sim_slots"`
	}](t, raw)
	if len(health.Slots) != 2 {
		t.Fatal("missing SIM accounts", string(raw))
	}
	if err := h.gateway.ConfigureLines(true, []string{"line-2", "line-1", "line-3"}); err != nil {
		t.Fatal(err)
	}
	for _, account := range []any{2, "nassimhub-line", "nassimhub-line-2"} {
		status, raw, _ = phone.do("POST", "/api/sms/send", map[string]any{"phone": "+15555550100", "message": "synthetic", "sim_account": account})
		if status != 200 {
			t.Fatal(status, string(raw))
		}
	}
	for _, account := range []any{3, "line-3", "unknown", -1, 33, 1.5} {
		status, _, _ = phone.do("POST", "/api/sms/send", map[string]any{"phone": "+15555550100", "message": "synthetic", "sim_account": account})
		if status == 200 {
			t.Fatal("expanded/invalid account accepted", account)
		}
	}
	status, raw, _ = phone.do("POST", "/api/calls/dial", map[string]any{"number": "+15555550100", "sim_account": 2, "operation_id": "synthetic-op"})
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	b.routeMu.Lock()
	defer b.routeMu.Unlock()
	if !slices.Equal(b.sentLines, []string{"line-2", "line-1", "line-2"}) || !slices.Equal(b.dialLines, []string{"line-2"}) {
		t.Fatal("incorrect routing", b.sentLines, b.dialLines)
	}
}
func TestMultilineSMSAggregationACKSourceAndCurrentRevocation(t *testing.T) {
	h, b, phone := multiHarness(t)
	status, raw, _ := phone.do("GET", "/api/sms", nil)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	messages := decode[[]map[string]any](t, raw)
	if len(messages) != 2 || messages[0]["delivery_id"] == messages[1]["delivery_id"] || messages[0]["sim_account"] == messages[1]["sim_account"] {
		t.Fatal("message provenance lost", string(raw))
	}
	if _, ok := messages[0]["line_id"]; !ok {
		t.Fatal("missing source line")
	}
	status, _, _ = phone.do("POST", "/api/sms/ack", map[string]any{"ids": []string{messages[0]["delivery_id"].(string)}})
	if status != 200 {
		t.Fatal(status)
	}
	status, raw, _ = phone.do("GET", "/api/sms", nil)
	left := decode[[]map[string]any](t, raw)
	if status != 200 || len(left) != 1 || left[0]["delivery_id"] != messages[1]["delivery_id"] {
		t.Fatal("ack crossed SIM account", string(raw))
	}
	b.routeMu.Lock()
	b.revoked = "line-2"
	b.routeMu.Unlock()
	status, _, _ = phone.do("GET", "/api/sms", nil)
	if status == 200 {
		t.Fatal("revoked line included")
	}
	status, _, _ = phone.do("POST", "/api/sms/send", map[string]any{"phone": "+15555550100", "message": "synthetic", "sim_account": 2})
	if status == 200 {
		t.Fatal("revoked line sent")
	}
	h.backend.mu.Lock()
	h.backend.forbidden = true
	h.backend.mu.Unlock()
	status, _, _ = phone.do("GET", "/v1/health", nil)
	if status != http.StatusUnauthorized || len(h.gateway.Status().Devices) != 0 {
		t.Fatal("account revocation did not retire pairing")
	}
}
func TestMultilinePairingScopePersistsAndStatusCannotMutateIt(t *testing.T) {
	h, _, _ := multiHarness(t)
	status := h.gateway.Status()
	original := slices.Clone(status.Devices[0].LineIDs)
	status.Devices[0].LineIDs[0] = "line-evil"
	if !slices.Equal(h.gateway.Status().Devices[0].LineIDs, original) {
		t.Fatal("status changed authority")
	}
	if err := h.gateway.ConfigureLines(true, []string{"line-1", "line-2", "line-3"}); err != nil {
		t.Fatal(err)
	}
	restored, err := New(Config{StatePath: h.gateway.config.StatePath, Secrets: fakeBox{}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(restored.Status().Devices[0].LineIDs, []string{"line-1", "line-2"}) || len(restored.Status().LineIDs) != 3 {
		t.Fatal("restart widened frozen pairing")
	}
}
func TestMultilineLimitsWindowCancellationAndPCMTarget(t *testing.T) {
	h, b, _ := multiHarness(t)
	for _, ids := range [][]string{{"line-1", "line-1"}, {""}} {
		if h.gateway.ConfigureLines(true, ids) == nil {
			t.Fatal("invalid lines accepted")
		}
	}
	ids := []string{}
	for i := 0; i < 33; i++ {
		ids = append(ids, fmt.Sprint("line-", i))
	}
	if h.gateway.ConfigureLines(true, ids) == nil {
		t.Fatal("33 lines accepted")
	}
	if err := h.gateway.ConfigureLines(true, ids[:32]); err != nil {
		t.Fatal(err)
	}
	window, err := h.gateway.StartPairing("admin-1")
	if err != nil {
		t.Fatal(err)
	}
	if err = h.gateway.ConfigureLines(true, []string{"line-1", "line-2"}); err != nil {
		t.Fatal(err)
	}
	if _, status, _ := h.pair(t, window.Code); status != 410 {
		t.Fatal("configuration change left stale pairing window", status)
	}
	pair := h.gateway.store.list()[0]
	identity, err := h.gateway.identity(pair, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := (scopedBackend{Backend: b, gateway: h.gateway}).AttachPCM(context.Background(), identity, "call-line-2")
	if err != nil {
		t.Fatal(err)
	}
	stream.Close()
	b.routeMu.Lock()
	defer b.routeMu.Unlock()
	if !slices.Equal(b.pcmLines, []string{"line-2"}) {
		t.Fatal("PCM used wrong line", b.pcmLines)
	}
}
