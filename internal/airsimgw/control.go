package airsimgw

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/human-agent65535/modemdeck/internal/airsimproto"
)

const (
	maxControlBody   = 64 << 10
	maxLongPoll      = 30 * time.Second
	eventPollPeriod  = 400 * time.Millisecond
	historyLimit     = 20
	messageLimit     = 50
	messageLookback  = 7 * 24 * time.Hour
	gatewayVersion   = "nassimhub-airsim/1"
	gatewayAgentKind = "nassimhub"
)

// The routes the AirSIM Android gateway forwards (VoWLANControlPolicy); the
// app gets 404 for everything else, exactly as there.
var (
	controlGetRoutes = map[string]bool{
		"/v1/health": true, "/api/health": true, "/api/status": true, "/api/debug": true,
		"/api/events": true, "/api/calls/status": true, "/api/calls/events": true,
		"/api/calls/audio/host/config": true, "/api/push/status": true,
		"/api/sms": true, "/api/sms/status": true,
	}
	controlPostRoutes = map[string]bool{
		"/api/calls/dial": true, "/api/calls/answer": true, "/api/calls/reject": true,
		"/api/calls/hangup": true, "/api/calls/dtmf": true, "/api/calls/audio/mute": true,
		"/api/calls/audio/host/warmup": true, "/api/calls/audio/host/register": true,
		"/api/push/register": true, "/api/sms/send": true, "/api/sms/ack": true,
		"/api/sms/refresh": true,
	}
)

type controlRequest struct {
	response http.ResponseWriter
	request  *http.Request
	body     []byte
	nonce    string
	pairing  Pairing
	identity Identity
	backend  Backend
}

func (c *controlRequest) ctx() context.Context { return c.request.Context() }

func (c *controlRequest) write(status int, value any) { writeFixedJSON(c.response, status, value) }

func (c *controlRequest) fail(status int, code string) {
	c.write(status, map[string]string{"error": code})
}

func (g *Gateway) serveControl(response http.ResponseWriter, request *http.Request) {
	if _, ok := g.peerAllowed(request.RemoteAddr); !ok {
		writeFixedJSON(response, http.StatusForbidden, map[string]string{"error": "private_peer_required"})
		return
	}
	path := request.URL.Path
	allowed := (request.Method == http.MethodGet && controlGetRoutes[path]) ||
		(request.Method == http.MethodPost && controlPostRoutes[path])
	if !allowed {
		writeFixedJSON(response, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxControlBody+1))
	if err != nil || len(body) > maxControlBody {
		writeFixedJSON(response, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	pairing, nonce, ok := g.authenticate(request, body)
	if !ok {
		writeFixedJSON(response, http.StatusUnauthorized, map[string]string{"error": "authentication_failed"})
		return
	}
	peer := remoteHost(request.RemoteAddr)
	g.store.touch(pairing.ID, peer, g.now())
	identity, err := g.identity(pairing, peer)
	if err != nil {
		writeFixedJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "gateway_disabled"})
		return
	}
	backend := g.currentBackend()
	if backend == nil {
		writeFixedJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "agent_unavailable"})
		return
	}
	if err := backend.Authorize(request.Context(), identity); err != nil {
		// The account behind this pairing is gone or no longer an
		// administrator: the pairing is dead, not merely refused.
		if errors.Is(err, ErrForbidden) {
			g.RevokeUser(pairing.UserID)
			writeFixedJSON(response, http.StatusUnauthorized, map[string]string{"error": "authentication_failed"})
			return
		}
		code := "agent_unavailable"
		var detailed *BackendError
		if errors.As(err, &detailed) && detailed.Message != "" {
			code = detailed.Message
		}
		writeFixedJSON(response, http.StatusServiceUnavailable, map[string]string{"error": code})
		return
	}
	call := &controlRequest{
		response: response, request: request, body: body, nonce: nonce,
		pairing: pairing, identity: identity, backend: backend,
	}
	switch path {
	case "/v1/health":
		g.health(call)
	case "/api/health":
		g.agentHealth(call)
	case "/api/status":
		g.modemStatus(call)
	case "/api/events":
		g.events(call, false)
	case "/api/calls/events":
		g.events(call, true)
	case "/api/calls/status":
		status, _, err := g.snapshot(call)
		if err != nil {
			g.backendError(call, err)
			return
		}
		call.write(http.StatusOK, status.Call)
	case "/api/calls/dial":
		g.dial(call)
	case "/api/calls/answer", "/api/calls/reject", "/api/calls/hangup", "/api/calls/dtmf":
		g.callControl(call, strings.TrimPrefix(path, "/api/calls/"))
	case "/api/calls/audio/mute":
		var input struct {
			Muted bool `json:"muted"`
		}
		if !decodeBody(call, &input) {
			return
		}
		g.setMuted(pairing.ID, input.Muted)
		call.write(http.StatusOK, map[string]bool{"muted": input.Muted})
	case "/api/calls/audio/host/warmup", "/api/calls/audio/host/register":
		call.write(http.StatusOK, map[string]bool{"ok": true})
	case "/api/calls/audio/host/config":
		call.write(http.StatusOK, map[string]any{
			"vendor_id": 0, "product_id": 0, "location_id": 0,
			"route_ready": true, "route_running": g.pcmActiveFor(pairing.ID),
		})
	case "/api/push/status":
		call.write(http.StatusOK, map[string]any{
			"configured": true, "call_push_ready": false, "message_push_ready": false,
			"local_mode": true, "cloud_enabled": false, "relay_url": "", "last_error": "",
		})
	case "/api/push/register":
		// Push needs AirSIM's cloud relay. The local pairing keeps working
		// while the app is open or kept alive in the background.
		call.write(http.StatusOK, map[string]bool{"configured": true})
	case "/api/sms":
		g.listMessages(call)
	case "/api/sms/status":
		messages, err := g.pendingMessages(call)
		if err != nil {
			g.backendError(call, err)
			return
		}
		call.write(http.StatusOK, map[string]any{"auto_cleanup_me": false, "count": len(messages), "last_poll_error": ""})
	case "/api/sms/ack":
		var input struct {
			IDs []string `json:"ids"`
		}
		if !decodeBody(call, &input) {
			return
		}
		if len(input.IDs) > maxAckedMessages {
			input.IDs = input.IDs[:maxAckedMessages]
		}
		if err := g.store.ack(pairing.ID, input.IDs); err != nil {
			call.fail(http.StatusInternalServerError, "ack_failed")
			return
		}
		call.write(http.StatusOK, map[string]int{"acknowledged": len(input.IDs)})
	case "/api/sms/refresh":
		call.write(http.StatusOK, map[string]bool{"ok": true})
	case "/api/sms/send":
		g.sendMessage(call)
	default:
		// /api/debug: NasSimHub has its own diagnostics page.
		call.fail(http.StatusNotFound, "not_found")
	}
}

// authenticate finds the paired iPhone whose secret signed the request.
func (g *Gateway) authenticate(request *http.Request, body []byte) (Pairing, string, bool) {
	header := request.Header
	if header.Get(airsimproto.HeaderVersion) != "1" {
		return Pairing{}, "", false
	}
	timestamp, err := strconv.ParseInt(header.Get(airsimproto.HeaderTimestamp), 10, 64)
	if err != nil || !airsimproto.FreshTimestamp(timestamp, g.now()) {
		return Pairing{}, "", false
	}
	nonce := header.Get(airsimproto.HeaderNonce)
	signature := header.Get(airsimproto.HeaderSignature)
	if !airsimproto.ValidNonce(nonce) || signature == "" {
		return Pairing{}, "", false
	}
	// The signature covers the request target exactly as sent.
	canonical := airsimproto.Canonical(request.Method, request.RequestURI, body, timestamp, nonce)
	for _, pairing := range g.store.withSecrets() {
		if airsimproto.VerifySignature(pairing.secret, signature, canonical) {
			if !g.replay.Accept(nonce, g.now()) {
				return Pairing{}, "", false
			}
			return pairing, nonce, true
		}
	}
	return Pairing{}, "", false
}

func decodeBody(call *controlRequest, target any) bool {
	if len(call.body) == 0 {
		return true
	}
	if err := json.Unmarshal(call.body, target); err != nil {
		call.fail(http.StatusBadRequest, "bad_request")
		return false
	}
	return true
}

func (g *Gateway) backendError(call *controlRequest, err error) {
	var detailed *BackendError
	message := ""
	if errors.As(err, &detailed) {
		message = detailed.Message
	}
	status, code := http.StatusBadGateway, "agent_unavailable"
	switch {
	case errors.Is(err, ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, ErrNotFound):
		status, code = http.StatusNotFound, "call_not_found"
	case errors.Is(err, ErrConflict):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, ErrUnavailable):
		status, code = http.StatusServiceUnavailable, "unavailable"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "timeout"
	}
	if message != "" {
		// The app shows the error string; NasSimHub's own message (the
		// anti-fraud notice, "line in use") is the useful one.
		code = message
	} else if status == http.StatusBadGateway {
		g.logger.Warn("AirSIM command failed", "component", "airsim", "path", call.request.URL.Path, "error", err)
	}
	call.fail(status, code)
}

func (g *Gateway) health(call *controlRequest) {
	line, err := call.backend.Line(call.ctx(), call.identity)
	if err != nil {
		g.backendError(call, err)
		return
	}
	agentID := g.store.agentID()
	call.write(http.StatusOK, map[string]any{
		"ok":                 true,
		"transport":          "vowlan",
		"version":            1,
		"agent_id":           agentID,
		"identity_proof":     airsimproto.HealthProof(call.pairing.secret, call.nonce, agentID),
		"canonical_call_ids": true,
		"agent_kind":         gatewayAgentKind,
		"device_name":        line.DeviceName,
		"manufacturer":       line.Manufacturer,
		"model":              line.Model,
		"phone_number":       line.PhoneNumber,
		"sim_slots":          simSlots(line),
	})
}

func simSlots(line Line) []map[string]any {
	return []map[string]any{{
		"id": "nassimhub-line", "label": firstNonEmpty(line.Operator, "SIM"),
		"number": line.PhoneNumber, "default": true, "slot_index": 0,
		"carrier_name": line.Operator, "is_default_voice": true, "is_default_sms": true,
	}}
}

func cellularState(line Line) string {
	switch {
	case line.Registered:
		return "registered"
	case line.Searching:
		return "searching"
	default:
		return "unregistered"
	}
}

func (g *Gateway) agentHealth(call *controlRequest) {
	line, err := call.backend.Line(call.ctx(), call.identity)
	if err != nil {
		g.backendError(call, err)
		return
	}
	call.write(http.StatusOK, map[string]any{
		"ok": true, "version": gatewayVersion, "control_plane": "vowlan",
		"cellular_state": cellularState(line),
	})
}

func (g *Gateway) modemStatus(call *controlRequest) {
	line, err := call.backend.Line(call.ctx(), call.identity)
	if err != nil {
		g.backendError(call, err)
		return
	}
	status := map[string]any{
		"imei": line.IMEI, "firmware": line.Firmware, "iccid": line.ICCID, "imsi": line.IMSI,
		"operator": line.Operator, "sim_inserted": line.ICCID != "" || line.IMSI != "",
		"network_mode": line.NetworkMode, "radio_band": line.Band,
		"cellular_state": cellularState(line),
	}
	if line.SignalDBM != nil {
		status["signal_dbm"] = *line.SignalDBM
	}
	call.write(http.StatusOK, status)
}

// --- calls --------------------------------------------------------------

type callRecord struct {
	ID        string  `json:"id"`
	Index     int     `json:"index"`
	Direction string  `json:"direction"`
	State     string  `json:"state"`
	Number    string  `json:"number"`
	StartedAt string  `json:"started_at"`
	UpdatedAt string  `json:"updated_at"`
	EndedAt   *string `json:"ended_at,omitempty"`
	Missed    bool    `json:"missed"`
	AndroidID string  `json:"android_id"`
}

type callStatus struct {
	Active          *callRecord  `json:"active"`
	History         []callRecord `json:"history"`
	Polling         bool         `json:"polling"`
	EventDriven     bool         `json:"event_driven"`
	PollIntervalSec int          `json:"poll_interval_s"`
	LastPollError   string       `json:"last_poll_error"`
	Revision        uint64       `json:"revision"`
}

type eventStatus struct {
	Revision    uint64           `json:"revision"`
	SMSRevision uint64           `json:"sms_revision"`
	SMSPending  int              `json:"sms_pending"`
	Call        callStatus       `json:"call"`
	SIMRevision uint64           `json:"sim_revision"`
	SIMSlots    []map[string]any `json:"sim_slots,omitempty"`
}

// eventWatch turns snapshots into the monotonically changing revisions the
// app long-polls on.
type eventWatch struct {
	callPrint, smsPrint string
	revision, smsRev    uint64
}

func airsimState(call Call) string {
	switch call.Phase {
	case "ringing":
		return "incoming"
	case "dialing":
		return "dialing"
	case "connecting":
		if call.Direction == "incoming" {
			return "active"
		}
		return "alerting"
	case "active":
		return "active"
	default:
		return "ended"
	}
}

func liveCall(call Call) bool {
	switch call.Phase {
	case "ringing", "dialing", "connecting", "active":
		return true
	}
	return false
}

func iso(value time.Time) string {
	if value.IsZero() {
		value = time.Unix(0, 0)
	}
	return value.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

func toRecord(call Call, index int) callRecord {
	record := callRecord{
		ID: call.ID, Index: index, Direction: call.Direction, State: airsimState(call),
		Number: call.Number, StartedAt: iso(call.CreatedAt), UpdatedAt: iso(call.UpdatedAt),
		Missed: call.Missed, AndroidID: call.ID,
	}
	if record.Direction != "incoming" {
		record.Direction = "outgoing"
	}
	if record.State == "ended" {
		ended := iso(call.EndedAt)
		if call.EndedAt.IsZero() {
			ended = iso(call.UpdatedAt)
		}
		record.EndedAt = &ended
	}
	return record
}

func (g *Gateway) snapshot(call *controlRequest) (eventStatus, bool, error) {
	active, history, err := call.backend.Calls(call.ctx(), call.identity, historyLimit)
	if err != nil {
		return eventStatus{}, false, err
	}
	messages, err := g.pendingMessages(call)
	if err != nil {
		return eventStatus{}, false, err
	}
	status := callStatus{
		History: make([]callRecord, 0, len(history)), EventDriven: true,
		PollIntervalSec: 1, LastPollError: "",
	}
	var current *Call
	for index := range active {
		if liveCall(active[index]) {
			current = &active[index]
			break
		}
	}
	owned := g.ownedCall(call.pairing.ID)
	if current != nil {
		record := toRecord(*current, 1)
		status.Active = &record
	} else if owned != "" {
		g.setOwned(call.pairing.ID, "")
	}
	for index, item := range history {
		status.History = append(status.History, toRecord(item, index+1))
	}
	callPrint := fingerprint(status.Active, status.History)
	smsIDs := make([]string, 0, len(messages))
	for _, message := range messages {
		smsIDs = append(smsIDs, message.DeliveryID)
	}
	smsPrint := fingerprint(smsIDs)

	g.mu.Lock()
	watch := g.watches[call.pairing.ID]
	if watch == nil {
		watch = &eventWatch{revision: randomStart(), smsRev: randomStart()}
		g.watches[call.pairing.ID] = watch
	}
	changed := false
	if watch.callPrint != callPrint {
		watch.callPrint = callPrint
		watch.revision++
		changed = true
	}
	if watch.smsPrint != smsPrint {
		watch.smsPrint = smsPrint
		watch.smsRev++
		watch.revision++
		changed = true
	}
	revision, smsRevision := watch.revision, watch.smsRev
	g.mu.Unlock()

	status.Revision = revision
	return eventStatus{
		Revision: revision, SMSRevision: smsRevision, SMSPending: len(messages),
		Call: status, SIMRevision: 1,
	}, changed, nil
}

// randomStart keeps a gateway restart from replaying an old revision the app
// already saw; the app waits for "different", not "greater".
func randomStart() uint64 {
	var raw [4]byte
	_, _ = rand.Read(raw[:])
	return uint64(time.Now().Unix())<<20 | uint64(binary.BigEndian.Uint32(raw[:])&0xfffff)
}

func fingerprint(values ...any) string {
	data, _ := json.Marshal(values)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:16])
}

func (g *Gateway) events(call *controlRequest, callsOnly bool) {
	query := call.request.URL.Query()
	after, _ := strconv.ParseUint(query.Get("after"), 10, 64)
	timeout := 15 * time.Second
	if milliseconds, err := strconv.Atoi(query.Get("timeout_ms")); err == nil && milliseconds >= 0 {
		timeout = time.Duration(milliseconds) * time.Millisecond
	}
	timeout = min(timeout, maxLongPoll)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(eventPollPeriod)
	defer ticker.Stop()
	for {
		status, _, err := g.snapshot(call)
		if err != nil {
			g.backendError(call, err)
			return
		}
		if status.Revision != after || after == 0 {
			g.writeEvent(call, status, callsOnly)
			return
		}
		select {
		case <-ticker.C:
			// A phone holding a long poll is still here: keep its call.
			g.store.touch(call.pairing.ID, call.identity.Peer, g.now())
		case <-deadline.C:
			g.writeEvent(call, status, callsOnly)
			return
		case <-call.ctx().Done():
			return
		}
	}
}

func (g *Gateway) writeEvent(call *controlRequest, status eventStatus, callsOnly bool) {
	if callsOnly {
		call.write(http.StatusOK, status.Call)
		return
	}
	call.write(http.StatusOK, status)
}

func operationRequestID(prefix, operationID string) string {
	if operationID != "" && len(operationID) <= 96 {
		digest := sha256.Sum256([]byte(operationID))
		return prefix + hex.EncodeToString(digest[:12])
	}
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	return prefix + hex.EncodeToString(raw[:])
}

func (g *Gateway) dial(call *controlRequest) {
	var input struct {
		Number      string `json:"number"`
		OperationID string `json:"operation_id"`
	}
	if !decodeBody(call, &input) {
		return
	}
	number := strings.TrimSpace(input.Number)
	if number == "" || len(number) > 64 {
		call.fail(http.StatusBadRequest, "number_required")
		return
	}
	created, err := call.backend.Dial(call.ctx(), call.identity, number,
		operationRequestID("airsim-dial-", input.OperationID))
	if err != nil {
		g.backendError(call, err)
		return
	}
	g.setOwned(call.pairing.ID, created.ID)
	result := map[string]any{"ok": true, "call_id": created.ID}
	if input.OperationID != "" {
		result["operation_id"] = input.OperationID
		result["android_id"] = created.ID
		result["backend_id"] = created.ID
		result["operation_proof"] = airsimproto.DialProof(call.pairing.secret, call.nonce,
			input.OperationID, created.ID, created.ID)
	}
	call.write(http.StatusOK, result)
}

// callControl resolves the target call the way the Android gateway does: the
// call the app names, or else the line's one live call.
func (g *Gateway) callControl(call *controlRequest, action string) {
	var input struct {
		CallID string `json:"call_id"`
		Digit  string `json:"digit"`
	}
	if !decodeBody(call, &input) {
		return
	}
	active, _, err := call.backend.Calls(call.ctx(), call.identity, 0)
	if err != nil {
		g.backendError(call, err)
		return
	}
	var target *Call
	for index := range active {
		item := &active[index]
		if !liveCall(*item) {
			continue
		}
		if input.CallID == "" || input.CallID == item.ID {
			target = item
			break
		}
	}
	if target == nil {
		if action == "hangup" || action == "reject" {
			// Already gone: hanging up is idempotent for the app.
			g.setOwned(call.pairing.ID, "")
			call.write(http.StatusOK, map[string]bool{"ok": true, "rejected": action == "reject"})
			return
		}
		call.fail(http.StatusNotFound, "no_active_call")
		return
	}
	digits := ""
	if action == "dtmf" {
		digits = strings.TrimSpace(input.Digit)
		if utf8.RuneCountInString(digits) != 1 || !strings.ContainsAny(digits, "0123456789*#ABCD") {
			call.fail(http.StatusBadRequest, "invalid_digit")
			return
		}
	}
	if action == "reject" && target.Phase != "ringing" {
		action = "hangup"
	}
	if err := call.backend.CallAction(call.ctx(), call.identity, target.ID, action, digits,
		operationRequestID("airsim-"+action+"-", "")); err != nil {
		g.backendError(call, err)
		return
	}
	switch action {
	case "answer":
		g.setOwned(call.pairing.ID, target.ID)
	case "hangup", "reject":
		g.setOwned(call.pairing.ID, "")
	}
	if strings.HasSuffix(call.request.URL.Path, "/reject") {
		call.write(http.StatusOK, map[string]bool{"rejected": true})
		return
	}
	call.write(http.StatusOK, map[string]bool{"ok": true})
}

// --- messages -----------------------------------------------------------

func (g *Gateway) pendingMessages(call *controlRequest) ([]Message, error) {
	since := call.pairing.CreatedAt
	if floor := g.now().Add(-messageLookback); since.Before(floor) {
		since = floor
	}
	messages, err := call.backend.Messages(call.ctx(), call.identity, since, messageLimit)
	if err != nil {
		return nil, err
	}
	pending := messages[:0]
	for _, message := range messages {
		if message.Outgoing || message.DeliveryID == "" || g.store.isAcked(call.pairing.ID, message.DeliveryID) {
			continue
		}
		pending = append(pending, message)
	}
	return pending, nil
}

func (g *Gateway) listMessages(call *controlRequest) {
	messages, err := g.pendingMessages(call)
	if err != nil {
		g.backendError(call, err)
		return
	}
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		item := map[string]any{
			"sender": message.Sender, "content": message.Content,
			"timestamp": iso(message.Timestamp), "delivery_id": message.DeliveryID,
			"direction": "incoming",
		}
		if code := verificationCode(message.Content); code != "" {
			item["code"] = code
		}
		result = append(result, item)
	}
	call.write(http.StatusOK, result)
}

func (g *Gateway) sendMessage(call *controlRequest) {
	var input struct {
		Phone   string `json:"phone"`
		Message string `json:"message"`
	}
	if !decodeBody(call, &input) {
		return
	}
	phone := strings.TrimSpace(input.Phone)
	if phone == "" || len(phone) > 64 || input.Message == "" || utf8.RuneCountInString(input.Message) > 1600 {
		call.fail(http.StatusBadRequest, "invalid_message")
		return
	}
	if err := call.backend.SendMessage(call.ctx(), call.identity, phone, input.Message,
		operationRequestID("airsim-sms-", "")); err != nil {
		g.backendError(call, err)
		return
	}
	call.write(http.StatusOK, map[string]any{"sent": true, "segments": smsSegments(input.Message)})
}

func smsSegments(text string) int {
	ascii := true
	for _, character := range text {
		if character > 0x7f {
			ascii = false
			break
		}
	}
	length := utf8.RuneCountInString(text)
	single, multi := 70, 67
	if ascii {
		length, single, multi = len(text), 160, 153
	}
	if length <= single {
		return 1
	}
	return (length + multi - 1) / multi
}

// verificationCode picks a 4-8 digit code out of a message that says it
// carries one, for the app's copy-code button.
func verificationCode(content string) string {
	lower := strings.ToLower(content)
	if !strings.Contains(content, "验证码") && !strings.Contains(content, "校验码") &&
		!strings.Contains(content, "動態碼") && !strings.Contains(lower, "code") {
		return ""
	}
	run := 0
	for index, character := range content + " " {
		if character >= '0' && character <= '9' {
			run++
			continue
		}
		if run >= 4 && run <= 8 {
			return content[index-run : index]
		}
		run = 0
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
