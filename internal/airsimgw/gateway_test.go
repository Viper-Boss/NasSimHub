package airsimgw

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/airsimproto"
)

// --- fakes ----------------------------------------------------------------

type fakeBox struct{}

func (fakeBox) Seal(plaintext, associated []byte) ([]byte, []byte, error) {
	return append([]byte(nil), associated...), append([]byte(nil), plaintext...), nil
}

func (fakeBox) Open(nonce, ciphertext, associated []byte) ([]byte, error) {
	if !bytes.Equal(nonce, associated) {
		return nil, errors.New("wrong associated data")
	}
	return append([]byte(nil), ciphertext...), nil
}

type fakeBackend struct {
	mu        sync.Mutex
	forbidden bool
	active    []Call
	history   []Call
	messages  []Message
	dialed    []string
	actions   []string
	sent      []string
	kept      []string
	stream    *fakeStream
	dialErr   error
}

func (f *fakeBackend) Authorize(context.Context, Identity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.forbidden {
		return ErrForbidden
	}
	return nil
}

func (f *fakeBackend) Line(context.Context, Identity) (Line, error) {
	signal := -85
	return Line{DeviceName: "NasSimHub", Manufacturer: "Quectel", Model: "EC20", PhoneNumber: "+8613800000000",
		Operator: "CMCC", Registered: true, SignalDBM: &signal}, nil
}

func (f *fakeBackend) Calls(context.Context, Identity, int) ([]Call, []Call, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.active...), append([]Call(nil), f.history...), nil
}

func (f *fakeBackend) Dial(_ context.Context, _ Identity, number, requestID string) (Call, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dialErr != nil {
		return Call{}, f.dialErr
	}
	f.dialed = append(f.dialed, number+"|"+requestID)
	call := Call{ID: "call-" + strconv.Itoa(len(f.dialed)), Direction: "outgoing", Phase: "dialing", Number: number,
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	f.active = []Call{call}
	return call, nil
}

func (f *fakeBackend) CallAction(_ context.Context, _ Identity, callID, action, digits, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, action+":"+callID+":"+digits)
	return nil
}

func (f *fakeBackend) KeepCall(_ context.Context, _ Identity, callID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kept = append(f.kept, callID)
	return nil
}

func (f *fakeBackend) Messages(context.Context, Identity, time.Time, int) ([]Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.messages...), nil
}

func (f *fakeBackend) SendMessage(_ context.Context, _ Identity, to, content, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, to+":"+content)
	return nil
}

func (f *fakeBackend) AttachPCM(_ context.Context, _ Identity, callID string) (PCMStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stream == nil {
		return nil, ErrUnavailable
	}
	return f.stream, nil
}

func (f *fakeBackend) set(update func(*fakeBackend)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	update(f)
}

// fakeStream is a 16 kHz, 20 ms host endpoint.
type fakeStream struct {
	downlink chan []byte
	uplink   chan []byte
	done     chan struct{}
	once     sync.Once
}

func newFakeStream() *fakeStream {
	return &fakeStream{downlink: make(chan []byte, 64), uplink: make(chan []byte, 256), done: make(chan struct{})}
}

func (s *fakeStream) SampleRate() int { return 16000 }
func (s *fakeStream) FrameBytes() int { return 640 }
func (s *fakeStream) ReadDownlink(ctx context.Context) ([]byte, error) {
	select {
	case frame := <-s.downlink:
		return frame, nil
	case <-s.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (s *fakeStream) WriteUplink(_ context.Context, frame []byte) error {
	select {
	case s.uplink <- append([]byte(nil), frame...):
		return nil
	default:
		return ErrUplinkDropped
	}
}
func (s *fakeStream) Close()                { s.once.Do(func() { close(s.done) }) }
func (s *fakeStream) Done() <-chan struct{} { return s.done }

// --- harness --------------------------------------------------------------

type harness struct {
	gateway *Gateway
	backend *fakeBackend
	cancel  context.CancelFunc
	ports   map[string]int
	now     time.Time
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	control, pcm, pairing := freeAddress(t), freeAddress(t), freeAddress(t)
	gateway, err := New(Config{
		ControlAddress: control, PCMAddress: pcm, PairingAddress: pairing,
		StatePath: filepath.Join(t.TempDir(), "airsim.json"),
		Secrets:   fakeBox{},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{}
	gateway.SetBackend(backend)
	if err := gateway.Configure(true, "line-1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- gateway.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	for deadline := time.Now().Add(3 * time.Second); !gateway.Status().Listening; {
		if time.Now().After(deadline) {
			t.Fatal("gateway did not start listening")
		}
		time.Sleep(10 * time.Millisecond)
	}
	port := func(address string) int {
		_, value, _ := net.SplitHostPort(address)
		number, _ := strconv.Atoi(value)
		return number
	}
	return &harness{gateway: gateway, backend: backend, cancel: cancel,
		ports: map[string]int{"control": port(control), "pcm": port(pcm), "pairing": port(pairing)}}
}

// app is the iPhone side, written from SamsungPairingClient.swift and
// VoWLANAuth.swift.
type app struct {
	t       *testing.T
	secret  []byte
	agentID string
	control int
	pcm     int
}

func rawHTTP(t *testing.T, port int, method, path string, body []byte) (int, string, []byte) {
	t.Helper()
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "%s %s HTTP/1.1\r\nHost: airsim-pair.local\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", method, path, len(body))
	_, _ = conn.Write(body)
	received, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	head, payload, ok := bytes.Cut(received, []byte("\r\n\r\n"))
	if !ok {
		t.Fatalf("no header terminator in %q", received)
	}
	lines := strings.Split(string(head), "\r\n")
	status, _ := strconv.Atoi(strings.Fields(lines[0])[1])
	return status, string(head), payload
}

// assertAppParsable applies SamsungPairingHTTPResponse.decode's rules.
func assertAppParsable(t *testing.T, head string, payload []byte) {
	t.Helper()
	lengths := 0
	for _, line := range strings.Split(head, "\r\n")[1:] {
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "transfer-encoding:") {
			t.Fatalf("response uses Transfer-Encoding: %q", head)
		}
		if strings.HasPrefix(lower, "content-length:") {
			lengths++
			if value, _ := strconv.Atoi(strings.TrimSpace(line[15:])); value != len(payload) {
				t.Fatalf("Content-Length %d, body %d", value, len(payload))
			}
		}
	}
	if lengths != 1 {
		t.Fatalf("%d Content-Length headers in %q", lengths, head)
	}
}

func (h *harness) pair(t *testing.T, code string) (*app, int, []byte) {
	t.Helper()
	status, head, payload := rawHTTP(t, h.ports["pairing"], "GET", "/pair/v1/info", nil)
	assertAppParsable(t, head, payload)
	if status != http.StatusOK {
		return nil, status, payload
	}
	var info pairingInfo
	if err := json.Unmarshal(payload, &info); err != nil {
		t.Fatal(err)
	}
	if info.Product != "airsim-pair" || info.Version != 1 {
		t.Fatalf("info = %+v", info)
	}
	serverKey, _ := airsimproto.DecodeBase64URL(info.ServerPublicKey)
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	registration, _ := json.Marshal(map[string]any{
		"cloud_enabled": false, "local_mode": true, "device_id": "ios-device-ABCD",
		"device_secret": "x", "voip_token": "", "alert_token": "", "relay_url": "",
		"vowlan_secret": airsimproto.Base64URL(secret),
	})
	client, _ := ecdh.X25519().GenerateKey(rand.Reader)
	sealed, err := airsimproto.SealRegistration(client, serverKey, registration, info.SessionID, code, nil)
	if err != nil {
		t.Fatal(err)
	}
	clientKey := airsimproto.Base64URL(client.PublicKey().Bytes())
	claim, _ := json.Marshal(pairingClaim{Version: 1, SessionID: info.SessionID, ClientPublicKey: clientKey,
		SealedRegistration: airsimproto.Base64URL(sealed), WantDirectRoute: true})
	status, head, payload = rawHTTP(t, h.ports["pairing"], "POST", "/pair/v1/claim", claim)
	assertAppParsable(t, head, payload)
	if status != http.StatusOK {
		return nil, status, payload
	}
	var result pairingResult
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Configured || result.DeviceIDHint != "ABCD" ||
		result.RouteProof != airsimproto.DirectRouteProof(secret, info.SessionID, clientKey, result.AgentID, result.ControlPort, result.PCMPort) {
		t.Fatalf("claim result = %+v", result)
	}
	if result.ControlPort != h.ports["control"] || result.PCMPort != h.ports["pcm"] {
		t.Fatalf("advertised ports %d/%d", result.ControlPort, result.PCMPort)
	}
	return &app{t: t, secret: secret, agentID: result.AgentID, control: result.ControlPort, pcm: result.PCMPort}, status, payload
}

func (a *app) do(method, target string, body any) (int, []byte, string) {
	a.t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	nonce := fmt.Sprintf("nonce-%d-%d", time.Now().UnixNano(), len(target))
	timestamp := time.Now().Unix()
	signature := airsimproto.Sign(a.secret, airsimproto.Canonical(method, target, payload, timestamp, nonce))
	request, _ := http.NewRequest(method, "http://127.0.0.1:"+strconv.Itoa(a.control)+target, bytes.NewReader(payload))
	request.Header.Set(airsimproto.HeaderVersion, "1")
	request.Header.Set(airsimproto.HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	request.Header.Set(airsimproto.HeaderNonce, nonce)
	request.Header.Set(airsimproto.HeaderSignature, signature)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		a.t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	return response.StatusCode, data, nonce
}

func decode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return value
}

// --- tests ----------------------------------------------------------------

func TestPairingNeedsAnOpenWindowAndTheRightCode(t *testing.T) {
	h := newHarness(t)
	if _, status, _ := h.pair(t, "000000"); status != http.StatusGone {
		t.Fatalf("pairing without a window: status %d, want 410", status)
	}
	window, err := h.gateway.StartPairing("admin-1")
	if err != nil {
		t.Fatal(err)
	}
	wrong := "000000"
	if window.Code == wrong {
		wrong = "000001"
	}
	if _, status, _ := h.pair(t, wrong); status != http.StatusUnauthorized {
		t.Fatalf("wrong code: status %d, want 401", status)
	}
	phone, status, _ := h.pair(t, window.Code)
	if status != http.StatusOK || phone == nil {
		t.Fatalf("pairing failed: %d", status)
	}
	if h.gateway.Status().Pairing != nil {
		t.Fatal("the pairing window stayed open after a successful claim")
	}
	devices := h.gateway.Status().Devices
	if len(devices) != 1 || devices[0].UserID != "admin-1" || devices[0].DeviceHint != "ABCD" {
		t.Fatalf("devices = %+v", devices)
	}
	if _, status, _ := h.pair(t, window.Code); status != http.StatusGone {
		t.Fatalf("second claim: status %d, want 410", status)
	}
}

func pairedApp(t *testing.T, h *harness) *app {
	t.Helper()
	window, err := h.gateway.StartPairing("admin-1")
	if err != nil {
		t.Fatal(err)
	}
	phone, status, payload := h.pair(t, window.Code)
	if status != http.StatusOK {
		t.Fatalf("pair: %d %s", status, payload)
	}
	return phone
}

func TestSignedHealthProvesTheGatewayHoldsTheSecret(t *testing.T) {
	h := newHarness(t)
	phone := pairedApp(t, h)
	status, body, nonce := phone.do("GET", "/v1/health", nil)
	if status != http.StatusOK {
		t.Fatalf("health: %d %s", status, body)
	}
	health := decode[map[string]any](t, body)
	if health["ok"] != true || health["canonical_call_ids"] != true || health["agent_id"] != phone.agentID ||
		health["identity_proof"] != airsimproto.HealthProof(phone.secret, nonce, phone.agentID) {
		t.Fatalf("health = %s", body)
	}

	// Unsigned, wrongly signed and replayed requests are all refused.
	response, err := http.Get("http://127.0.0.1:" + strconv.Itoa(phone.control) + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned health: %d", response.StatusCode)
	}
	stranger := &app{t: t, secret: bytes.Repeat([]byte{7}, 32), control: phone.control}
	if status, _, _ := stranger.do("GET", "/v1/health", nil); status != http.StatusUnauthorized {
		t.Fatalf("unknown secret: %d", status)
	}
	if status, _, _ := phone.do("GET", "/api/calls/history/ack", nil); status != http.StatusNotFound {
		t.Fatalf("unlisted route: %d", status)
	}

	// Revoking the device kills its signature at once.
	if err := h.gateway.Revoke(h.gateway.Status().Devices[0].ID); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := phone.do("GET", "/v1/health", nil); status != http.StatusUnauthorized {
		t.Fatalf("revoked device: %d", status)
	}
}

func TestDisabledAccountRevokesItsPairings(t *testing.T) {
	h := newHarness(t)
	phone := pairedApp(t, h)
	h.backend.set(func(f *fakeBackend) { f.forbidden = true })
	if status, _, _ := phone.do("GET", "/api/health", nil); status != http.StatusUnauthorized {
		t.Fatalf("forbidden account: %d", status)
	}
	if devices := h.gateway.Status().Devices; len(devices) != 0 {
		t.Fatalf("pairings kept for a forbidden account: %+v", devices)
	}
}

func TestOwnedDialReceiptAndCallControl(t *testing.T) {
	h := newHarness(t)
	phone := pairedApp(t, h)
	status, body, nonce := phone.do("POST", "/api/calls/dial", map[string]string{"number": "10086", "operation_id": "op-1"})
	if status != http.StatusOK {
		t.Fatalf("dial: %d %s", status, body)
	}
	receipt := decode[map[string]any](t, body)
	if receipt["operation_id"] != "op-1" || receipt["android_id"] != "call-1" || receipt["backend_id"] != "call-1" ||
		receipt["operation_proof"] != airsimproto.DialProof(phone.secret, nonce, "op-1", "call-1", "call-1") {
		t.Fatalf("receipt = %s", body)
	}
	if !strings.HasPrefix(h.backend.dialed[0], "10086|airsim-dial-") {
		t.Fatalf("dialed = %v", h.backend.dialed)
	}

	status, body, _ = phone.do("GET", "/api/calls/status", nil)
	current := decode[callStatus](t, body)
	if status != http.StatusOK || current.Active == nil || current.Active.State != "dialing" ||
		current.Active.Direction != "outgoing" || current.Active.ID != "call-1" {
		t.Fatalf("call status: %d %s", status, body)
	}
	if _, err := time.Parse(time.RFC3339Nano, current.Active.StartedAt); err != nil {
		t.Fatalf("started_at %q is not ISO 8601", current.Active.StartedAt)
	}

	h.backend.set(func(f *fakeBackend) { f.active[0].Phase = "connecting" })
	_, body, _ = phone.do("GET", "/api/calls/status", nil)
	if state := decode[callStatus](t, body).Active.State; state != "alerting" {
		t.Fatalf("connecting outgoing call state = %q", state)
	}
	if status, body, _ := phone.do("POST", "/api/calls/dtmf", map[string]string{"digit": "5"}); status != http.StatusOK {
		t.Fatalf("dtmf: %d %s", status, body)
	}
	if status, body, _ := phone.do("POST", "/api/calls/dtmf", map[string]string{"digit": "55"}); status != http.StatusBadRequest {
		t.Fatalf("bad dtmf: %d %s", status, body)
	}
	if status, body, _ := phone.do("POST", "/api/calls/reject", map[string]string{"call_id": "call-1"}); status != http.StatusOK ||
		decode[map[string]bool](t, body)["rejected"] != true {
		t.Fatalf("reject: %d %s", status, body)
	}
	want := []string{"dtmf:call-1:5", "hangup:call-1:"}
	if strings.Join(h.backend.actions, ",") != strings.Join(want, ",") {
		t.Fatalf("actions = %v, want %v (rejecting a call that is not ringing hangs it up)", h.backend.actions, want)
	}

	h.backend.set(func(f *fakeBackend) {
		f.active = nil
		f.dialErr = &BackendError{Kind: ErrForbidden, Message: "fraud_guard"}
	})
	if status, body, _ := phone.do("POST", "/api/calls/dial", map[string]string{"number": "10010"}); status != http.StatusForbidden ||
		decode[map[string]string](t, body)["error"] != "fraud_guard" {
		t.Fatalf("guarded dial: %d %s", status, body)
	}
}

func TestEventsLongPollWakesOnIncomingCallAndSMS(t *testing.T) {
	h := newHarness(t)
	phone := pairedApp(t, h)
	_, body, _ := phone.do("GET", "/api/events?after=0&timeout_ms=1000", nil)
	first := decode[eventStatus](t, body)

	go func() {
		time.Sleep(300 * time.Millisecond)
		h.backend.set(func(f *fakeBackend) {
			f.active = []Call{{ID: "call-in", Direction: "incoming", Phase: "ringing", Number: "10010",
				CreatedAt: time.Now(), UpdatedAt: time.Now()}}
		})
	}()
	started := time.Now()
	_, body, _ = phone.do("GET", "/api/events?after="+strconv.FormatUint(first.Revision, 10)+"&timeout_ms=10000", nil)
	second := decode[eventStatus](t, body)
	if time.Since(started) > 5*time.Second || second.Revision == first.Revision ||
		second.Call.Active == nil || second.Call.Active.State != "incoming" {
		t.Fatalf("event after incoming call: %s", body)
	}

	// With nothing new, the poll returns at its timeout with the same revision.
	started = time.Now()
	_, body, _ = phone.do("GET", "/api/events?after="+strconv.FormatUint(second.Revision, 10)+"&timeout_ms=600", nil)
	if same := decode[eventStatus](t, body); same.Revision != second.Revision || time.Since(started) < 500*time.Millisecond {
		t.Fatalf("idle poll: %s after %v", body, time.Since(started))
	}

	h.backend.set(func(f *fakeBackend) {
		f.messages = []Message{{DeliveryID: "m1", Sender: "10690000", Content: "您的验证码是 482913，5分钟内有效",
			Timestamp: time.Now()}}
	})
	_, body, _ = phone.do("GET", "/api/events?after="+strconv.FormatUint(second.Revision, 10)+"&timeout_ms=3000", nil)
	third := decode[eventStatus](t, body)
	if third.SMSPending != 1 || third.SMSRevision == second.SMSRevision {
		t.Fatalf("event after SMS: %s", body)
	}
	_, body, _ = phone.do("GET", "/api/sms", nil)
	messages := decode[[]map[string]any](t, body)
	if len(messages) != 1 || messages[0]["code"] != "482913" || messages[0]["delivery_id"] != "m1" {
		t.Fatalf("sms = %s", body)
	}
	if status, _, _ := phone.do("POST", "/api/sms/ack", map[string]any{"ids": []string{"m1"}}); status != http.StatusOK {
		t.Fatalf("ack: %d", status)
	}
	_, body, _ = phone.do("GET", "/api/sms", nil)
	if len(decode[[]map[string]any](t, body)) != 0 {
		t.Fatalf("acked message still pending: %s", body)
	}
	if status, body, _ := phone.do("POST", "/api/sms/send", map[string]string{"phone": "10086", "message": "CXLL"}); status != http.StatusOK ||
		decode[map[string]any](t, body)["segments"] != float64(1) {
		t.Fatalf("send: %d %s", status, body)
	}
}

func TestPCMBridgesAudioAtTheRightRates(t *testing.T) {
	h := newHarness(t)
	phone := pairedApp(t, h)
	stream := newFakeStream()
	h.backend.set(func(f *fakeBackend) {
		f.stream = stream
		f.active = []Call{{ID: "call-1", Direction: "outgoing", Phase: "dialing", Number: "10086"}}
	})

	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(phone.pcm))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	timestamp := time.Now().Unix()
	nonce := "pcm-nonce-123"
	signature := airsimproto.Sign(phone.secret, airsimproto.PCMCanonical(airsimproto.PCMPreface{Timestamp: timestamp, Nonce: nonce}))
	fmt.Fprintf(conn, "AIRSIMVWL1 %d %s %s\n", timestamp, nonce, signature)
	reader := bufio.NewReader(conn)
	ready := make([]byte, len(airsimproto.PCMReady))
	if _, err := io.ReadFull(reader, ready); err != nil || string(ready) != airsimproto.PCMReady {
		t.Fatalf("ready = %q, %v", ready, err)
	}

	// While the call rings the phone hears silence.
	frame := make([]byte, airsimproto.PCMFrameBytes)
	if _, err := io.ReadFull(reader, frame); err != nil || !bytes.Equal(frame, make([]byte, len(frame))) {
		t.Fatalf("pre-answer frame = %v, %v", frame[:8], err)
	}

	h.backend.set(func(f *fakeBackend) {
		f.active[0].Phase = "active"
		f.active[0].MediaAvailable = true
	})
	// The far end: a 16 kHz frame of constant 1000 must reach the phone as
	// 8 kHz 1000s.
	host := make([]byte, 640)
	for index := 0; index < 320; index++ {
		binary.LittleEndian.PutUint16(host[index*2:], 1000)
	}
	go func() {
		for range 20 {
			stream.downlink <- host
			time.Sleep(20 * time.Millisecond)
		}
	}()
	for {
		if _, err := io.ReadFull(reader, frame); err != nil {
			t.Fatalf("downlink: %v", err)
		}
		if int16(binary.LittleEndian.Uint16(frame)) == 1000 {
			break
		}
	}

	// The phone's 8 kHz frames reach the call upsampled to 16 kHz.
	phoneFrame := make([]byte, airsimproto.PCMFrameBytes)
	negative := int16(-2000)
	for index := 0; index < 160; index++ {
		binary.LittleEndian.PutUint16(phoneFrame[index*2:], uint16(negative))
	}
	go func() {
		for range 10 {
			_, _ = conn.Write(phoneFrame)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	select {
	case got := <-stream.uplink:
		if len(got) != 640 {
			t.Fatalf("uplink frame %d bytes", len(got))
		}
		if last := int16(binary.LittleEndian.Uint16(got[638:])); last != -2000 {
			t.Fatalf("uplink sample = %d", last)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no uplink audio reached the call")
	}

	// A second phone is refused while one is connected.
	second, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(phone.pcm))
	if err != nil {
		t.Fatal(err)
	}
	signature = airsimproto.Sign(phone.secret, airsimproto.PCMCanonical(airsimproto.PCMPreface{Timestamp: timestamp, Nonce: "pcm-nonce-456"}))
	fmt.Fprintf(second, "AIRSIMVWL1 %d %s %s\n", timestamp, "pcm-nonce-456", signature)
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, _ := second.Read(make([]byte, 16)); n != 0 {
		t.Fatal("a second PCM session was accepted")
	}
	second.Close()

	// When the call's media ends the connection closes.
	stream.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, err := reader.Read(frame); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				t.Fatal("PCM connection stayed open after the call media ended")
			}
			return
		}
	}
}

func TestPCMRejectsAReplayedPreface(t *testing.T) {
	h := newHarness(t)
	phone := pairedApp(t, h)
	h.backend.set(func(f *fakeBackend) { f.active = []Call{{ID: "c", Phase: "dialing"}} })
	timestamp := time.Now().Unix()
	line := fmt.Sprintf("AIRSIMVWL1 %d %s %s\n", timestamp, "pcm-replay-1",
		airsimproto.Sign(phone.secret, airsimproto.PCMCanonical(airsimproto.PCMPreface{Timestamp: timestamp, Nonce: "pcm-replay-1"})))
	for attempt, wantReady := range []bool{true, false} {
		conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(phone.pcm))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(conn, line)
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		ready := make([]byte, len(airsimproto.PCMReady))
		_, err = io.ReadFull(conn, ready)
		if got := err == nil && string(ready) == airsimproto.PCMReady; got != wantReady {
			t.Fatalf("attempt %d ready = %t", attempt, got)
		}
		conn.Close()
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPrivatePeerPolicy(t *testing.T) {
	for address, want := range map[string]bool{
		"127.0.0.1": true, "192.168.1.20": true, "10.0.0.3": true, "172.20.0.1": true,
		"100.101.102.103": true, "fe80::1": true, "fd00::1": true, "::ffff:192.168.1.9": true,
		"8.8.8.8": false, "2001:4860::8888": false, "100.128.0.1": false,
	} {
		if got := PrivatePeer(netip.MustParseAddr(address)); got != want {
			t.Errorf("PrivatePeer(%s) = %t, want %t", address, got, want)
		}
	}
}

func TestResampler(t *testing.T) {
	up := resampler{from: 8000, to: 16000}
	input := make([]byte, 4)
	binary.LittleEndian.PutUint16(input, 100)
	binary.LittleEndian.PutUint16(input[2:], 300)
	out := up.convert(input)
	var got []int16
	for index := 0; index < len(out); index += 2 {
		got = append(got, int16(binary.LittleEndian.Uint16(out[index:])))
	}
	if fmt.Sprint(got) != "[50 100 200 300]" {
		t.Fatalf("upsampled = %v", got)
	}
	down := resampler{from: 16000, to: 8000}
	out = down.convert(out)
	if len(out) != 4 || int16(binary.LittleEndian.Uint16(out)) != 75 || int16(binary.LittleEndian.Uint16(out[2:])) != 250 {
		t.Fatalf("downsampled = %v", out)
	}
}

func TestStoreKeepsSecretsSealedAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "airsim.json")
	first, err := openStore(path, fakeBox{})
	if err != nil {
		t.Fatal(err)
	}
	secret := bytes.Repeat([]byte{9}, 32)
	added, err := first.add(Pairing{UserID: "u1", CreatedAt: time.Now()}, secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.ack(added.ID, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	second, err := openStore(path, fakeBox{})
	if err != nil {
		t.Fatal(err)
	}
	if second.agentID() != first.agentID() || !second.isAcked(added.ID, "b") {
		t.Fatal("state did not survive a restart")
	}
	pairings := second.withSecrets()
	if len(pairings) != 1 || !bytes.Equal(pairings[0].secret, secret) {
		t.Fatalf("pairings after restart = %+v", pairings)
	}
	// Metadata listings never carry the secret, and neither does JSON.
	encoded, _ := json.Marshal(second.list())
	if bytes.Contains(encoded, []byte("secret")) {
		t.Fatalf("listing leaks the secret: %s", encoded)
	}
}
