package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/calllease"
	"github.com/human-agent65535/modemdeck/internal/store"
)

// Duplicate submission.
//
// Where the single effect is decided, as implemented:
//
//   - a call start is claimed in the HTTP layer first: calllease reserves the
//     (request id, line, session) triple, and only the request that CREATED the
//     reservation may dispatch (communications.go startCall). A concurrent
//     retry is answered 409 call_request_in_progress.
//   - every hardware command (call start, call action, message send) is then
//     claimed in the store's command ledger, keyed by request id
//     (communication.Service.beginCommand -> store.BeginHardwareCommand). That
//     atomic insert is tested against the real database in
//     internal/store/hardware_commands_adversarial_test.go.
//
// No call or SMS code is changed by these tests; they only call it.

type advCallStore struct {
	mu    sync.Mutex
	calls map[string]store.Call
}

func (calls *advCallStore) CallByID(_ context.Context, id string) (store.Call, error) {
	calls.mu.Lock()
	defer calls.mu.Unlock()
	call, found := calls.calls[id]
	if !found {
		return store.Call{}, store.ErrCallNotFound
	}
	return call, nil
}

func (calls *advCallStore) ActiveCalls(context.Context) ([]store.Call, error) {
	calls.mu.Lock()
	defer calls.mu.Unlock()
	active := make([]store.Call, 0, len(calls.calls))
	for _, call := range calls.calls {
		active = append(active, call)
	}
	return active, nil
}

type advCallController struct{}

func (advCallController) EndCall(context.Context, string) error { return nil }

func TestAdversarialConcurrentDuplicateCallStartDialsOnce(t *testing.T) {
	communications := &advCommunications{
		startGate:   make(chan struct{}),
		startedOnce: make(chan struct{}),
	}
	callStore := &advCallStore{calls: map[string]store.Call{}}
	leases, err := calllease.New(callStore, advCallController{}, calllease.Options{})
	if err != nil {
		t.Fatal(err)
	}
	harness := newAdvHarness(t, func(options *Options) {
		options.Communications = communications
		options.CallLeases = leases
	})
	alice := harness.aliceClient()
	body := `{"request_id":"adv-dup-call-1","line_id":"` + advLineAlpha + `","number":"` + advPeerAlpha + `"}`

	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- alice.send(http.MethodPost, "/api/v1/calls", body) }()
	select {
	case <-communications.startedOnce:
	case <-time.After(10 * time.Second):
		t.Fatal("the first request never reached the dialler")
	}

	// The first request is now inside the hardware call. Every retry of the
	// same request, however many, must be turned away without dialling.
	const retries = 12
	var wait sync.WaitGroup
	codes := make([]string, retries)
	for index := 0; index < retries; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response := alice.send(http.MethodPost, "/api/v1/calls", body)
			codes[index] = advErrorCode(response)
			if response.Code != http.StatusConflict {
				codes[index] = response.Body.String()
			}
		}()
	}
	wait.Wait()
	for index, code := range codes {
		if code != "call_request_in_progress" {
			t.Errorf("retry %d: %s, want 409 call_request_in_progress", index, code)
		}
	}
	// A different request for the same line is refused as well: one line, one
	// call.
	other := alice.send(http.MethodPost, "/api/v1/calls",
		`{"request_id":"adv-dup-call-2","line_id":"`+advLineAlpha+`","number":"`+advPeerAlpha+`"}`)
	if other.Code < 400 {
		t.Errorf("a second call was started on a line that is already dialling: %d %s", other.Code, other.Body)
	}

	callStore.mu.Lock()
	callStore.calls["adv-call-started"] = store.Call{ID: "adv-call-started", LineID: advLineAlpha, Phase: "dialing"}
	callStore.mu.Unlock()
	close(communications.startGate)
	select {
	case response := <-first:
		advRequireStatus(t, response, http.StatusCreated, "")
	case <-time.After(10 * time.Second):
		t.Fatal("the first request never completed")
	}
	if _, starts, _ := communications.effects(); starts != 1 {
		t.Fatalf("the modem was asked to dial %d times for one request id", starts)
	}
}

func TestAdversarialDuplicateMessageSubmissionsCarryOneRequestIdentity(t *testing.T) {
	communications := &advCommunications{}
	harness := newAdvHarness(t, func(options *Options) { options.Communications = communications })
	alice := harness.aliceClient()
	body := `{"request_id":"adv-dup-msg-1","line_id":"` + advLineAlpha + `","to":"` + advPeerAlpha + `","content":"adv duplicate"}`

	var wait sync.WaitGroup
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response := alice.do(advRequest{
				method: http.MethodPost, path: "/api/v1/messages", body: body,
				headers: map[string]string{"Idempotency-Key": "adv-dup-msg-1"},
			})
			if response.Code != http.StatusCreated {
				t.Errorf("send: %d %s", response.Code, response.Body)
			}
		}()
	}
	wait.Wait()

	// The HTTP layer does not de-duplicate a message itself; its job is to
	// hand the communication service the SAME request id every time, never a
	// freshly minted one, so the service's command ledger can.
	communications.mu.Lock()
	var sends []string
	for _, input := range communications.sends {
		sends = append(sends, input.RequestID+"|"+input.LineID+"|"+input.Number+"|"+input.Text)
	}
	communications.mu.Unlock()
	if len(sends) != 8 {
		t.Fatalf("%d submissions reached the service, want 8", len(sends))
	}
	for _, send := range sends {
		if send != "adv-dup-msg-1|"+advLineAlpha+"|"+advPeerAlpha+"|adv duplicate" {
			t.Fatalf("a duplicate reached the service with a different identity: %q", send)
		}
	}

	// The two ways a client could smuggle a second identity are refused
	// before anything is sent.
	before, _, _ := communications.effects()
	response := alice.do(advRequest{
		method: http.MethodPost, path: "/api/v1/messages", body: body,
		headers: map[string]string{"Idempotency-Key": "adv-another-key"},
	})
	advRequireStatus(t, response, http.StatusBadRequest, "invalid_argument")
	response = alice.send(http.MethodPost, "/api/v1/messages",
		`{"request_id":"`+reservedInternalRequestIDPrefix+`adv","line_id":"`+advLineAlpha+`","to":"`+advPeerAlpha+`","content":"adv"}`)
	advRequireStatus(t, response, http.StatusUnprocessableEntity, "invalid_argument")
	if after, _, _ := communications.effects(); after != before {
		t.Fatal("a refused request id still reached the service")
	}
}
