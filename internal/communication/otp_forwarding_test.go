package communication

import (
	"context"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/messageevents"
	"github.com/human-agent65535/modemdeck/internal/runtimeevents"
	"github.com/human-agent65535/modemdeck/internal/store"
)

func TestOTPForwardGatePreservesCommittedMessageAndRuntimeRevision(t *testing.T) {
	at := time.Now().UTC()
	message := store.Message{ID: 42, LineID: "stable-line", EndpointLineID: "nsh:fixture:0", Content: "验证码 123456"}
	repository := &fakeRepository{snapshotResult: store.HardwareSnapshotResult{CreatedIncomingMessages: []store.Message{message}}}
	events := messageevents.NewBuffer(8)
	updates, unsubscribe := events.Subscribe()
	defer unsubscribe()
	service, err := New(connectedAgent(at), repository, events)
	if err != nil {
		t.Fatal(err)
	}
	runtime := runtimeevents.NewHub()
	if err := service.SetRuntimeEventPublisher(runtime); err != nil {
		t.Fatal(err)
	}
	calls := 0
	service.SetOTPForwardGate(func(ctx context.Context, got store.Message) bool {
		calls++
		if got.ID != message.ID || got.EndpointLineID != message.EndpointLineID {
			t.Fatal("gate lost message identity")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("gate needs a bounded context")
		}
		return false
	})
	if _, err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || runtime.Current().DataRevision != 1 || repository.snapshotResult.CreatedIncomingMessages[0].Content != message.Content {
		t.Fatal("a notification refusal must preserve the message and durable update")
	}
	select {
	case <-updates:
		t.Fatal("blocked OTP reached subscribers")
	default:
	}
	service.publishIncomingMessages([]store.Message{{ID: 43, Content: "normal message"}}, at)
	select {
	case got := <-updates:
		if got.MessageID != "43" {
			t.Fatal(got.MessageID)
		}
	default:
		t.Fatal("normal notification was blocked")
	}
	service.SetOTPForwardGate(func(context.Context, store.Message) bool { return true })
	service.publishIncomingMessages([]store.Message{message}, at)
	select {
	case got := <-updates:
		if got.MessageID != "42" {
			t.Fatal(got.MessageID)
		}
	default:
		t.Fatal("allowed OTP was blocked")
	}
	if calls != 1 {
		t.Fatal("normal SMS must not invoke the OTP gate")
	}
}
