package nassimhubnode

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
	"github.com/human-agent65535/modemdeck/internal/store"
	"github.com/human-agent65535/nassimhub-node/proto"
)

func TestIncomingMessageIDIsStableAcrossStorageAndIndex(t *testing.T) {
	at := time.Date(2026, 9, 27, 8, 30, 0, 0, time.FixedZone("CST", 8*3600))
	a := incomingMessageID("8986001", "10010", at, "您的验证码是 123456", "mm-3")
	b := incomingMessageID("8986001", "10010", at.UTC(), "您的验证码是 123456", "mm-17")
	if a != b {
		t.Fatalf("same delivery produced different identities: %s vs %s", a, b)
	}
	if !strings.HasPrefix(a, incomingMessagePrefix) {
		t.Fatalf("identity %q lacks prefix", a)
	}
	for name, other := range map[string]string{
		"sim":  incomingMessageID("8986002", "10010", at, "您的验证码是 123456", "mm-3"),
		"peer": incomingMessageID("8986001", "10086", at, "您的验证码是 123456", "mm-3"),
		"time": incomingMessageID("8986001", "10010", at.Add(time.Second), "您的验证码是 123456", "mm-3"),
		"text": incomingMessageID("8986001", "10010", at, "您的验证码是 654321", "mm-3"),
	} {
		if other == a {
			t.Errorf("changing %s did not change the identity", name)
		}
	}
	// Without a timestamp the index is the only discriminator.
	if incomingMessageID("s", "p", time.Time{}, "t", "mm-1") == incomingMessageID("s", "p", time.Time{}, "t", "mm-2") {
		t.Error("timestamp-less messages with different indices collapsed")
	}
}

func TestNodeRequestIDFitsAgentAlphabetAndIsDeterministic(t *testing.T) {
	accepted := regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	for _, requestID := range []string{"req-1", "带中文的请求", strings.Repeat("x", 500), "a/b c?d"} {
		mapped := nodeRequestID(requestID)
		if !accepted.MatchString(mapped) {
			t.Errorf("request %q mapped to %q, outside the agent alphabet", requestID, mapped)
		}
		if nodeRequestID(requestID) != mapped {
			t.Errorf("request %q mapped non-deterministically", requestID)
		}
	}
	if nodeRequestID("a") == nodeRequestID("b") {
		t.Error("distinct requests collapsed")
	}
}

func TestClassifySendErrorSeparatesDefiniteFromUncertain(t *testing.T) {
	definite := []proto.ErrorCode{proto.ErrorInvalidArgument, proto.ErrorFailedPrecondition, proto.ErrorNetworkRejected, proto.ErrorNotSupported}
	for _, code := range definite {
		err := classifySendError(&proto.OperationError{Code: code, Operation: "send_sms", Message: "no"})
		var operation *agentclient.OperationError
		if !errors.As(err, &operation) {
			t.Errorf("%s must be a definite failure, got %v", code, err)
		}
	}
	uncertain := []error{
		errors.New("connection reset"),
		context.DeadlineExceeded,
		&proto.OperationError{Code: proto.ErrorUnavailable, Message: "result is uncertain"},
		&proto.OperationError{Code: proto.ErrorInternal, Message: "boom"},
	}
	for _, input := range uncertain {
		err := classifySendError(input)
		var operation *agentclient.OperationError
		if errors.As(err, &operation) {
			t.Errorf("%v must be indeterminate, got definite %v", input, err)
		}
	}
	err := classifySendError(&proto.OperationError{Code: proto.ErrorUnavailable, Message: "modem is offline"})
	var operation *agentclient.OperationError
	if !errors.As(err, &operation) || operation.Code != "unavailable" {
		t.Errorf("a refused-before-submit send must be definite, got %v", err)
	}
}

func TestFingerprintFormatAndNormalisation(t *testing.T) {
	public, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	shown := Fingerprint(public)
	if !regexp.MustCompile(`^([0-9A-F]{4} ){7}[0-9A-F]{4}$`).MatchString(shown) {
		t.Fatalf("fingerprint %q is not eight groups of four", shown)
	}
	typed := strings.ToLower(strings.ReplaceAll(shown, " ", "-"))
	if normalizeFingerprint(typed) != normalizeFingerprint(shown) {
		t.Fatal("case and separators must not matter when comparing fingerprints")
	}
}

func TestTicketsAreSingleUseSupersededAndExpire(t *testing.T) {
	now := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	fleet := &Fleet{now: func() time.Time { return now }, tickets: map[string]ticket{}, nodes: map[string]*liveNode{}}

	first, _, err := fleet.issueTicket(ticket{deviceID: "NSH-410-A"})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := fleet.issueTicket(ticket{deviceID: "NSH-410-A"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fleet.takeTicket(first); ok {
		t.Fatal("a superseded ticket must not be accepted")
	}
	if _, ok := fleet.takeTicket(second); !ok {
		t.Fatal("the current ticket must be accepted once")
	}
	if _, ok := fleet.takeTicket(second); ok {
		t.Fatal("a ticket must be single use")
	}

	expiring, _, err := fleet.issueTicket(ticket{deviceID: "NSH-410-B"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(ticketLifetime)
	if _, ok := fleet.takeTicket(expiring); ok {
		t.Fatal("an expired ticket must not be accepted")
	}

	for index := 0; index < maxTickets; index++ {
		if _, _, err := fleet.issueTicket(ticket{deviceID: fmt.Sprintf("NSH-410-%02d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := fleet.issueTicket(ticket{deviceID: "NSH-410-ZZ"}); err == nil {
		t.Fatal("outstanding tickets must be bounded")
	}
}

func readyState() nodeState {
	dbm := -80.0
	return nodeState{
		Online:        true,
		Compatible:    true,
		StatusKnown:   true,
		MessagesKnown: true,
		Model:         "UFI003",
		AgentVersion:  "0.9.0",
		Capabilities:  proto.Capabilities{SMS: true},
		Status: proto.ModemStatus{
			State: proto.ModemReady,
			SIM: proto.SIMInfo{
				State:        proto.SIMReady,
				ICCID:        "89860000000000000001",
				IMSI:         "460011234567890",
				OperatorName: "CHN-UNICOM",
				PhoneNumber:  "+8613000000000",
			},
			Network: proto.NetworkStatus{Registration: proto.RegRegistered, AccessTechnology: proto.AccessLTE, OperatorCode: "46001"},
			Signal:  proto.Signal{Known: true, Bars: 3, DBM: &dbm},
		},
	}
}

func TestNodeResourcesOnlyShownForOnlineStatus(t *testing.T) {
	live := &liveNode{record: store.NasSimHubNodeRecord{ID: "NSH-410-TEST"}, state: readyState()}
	live.state.Status.Resources = &proto.SystemResources{ProcessCount: 115, MemoryTotal: 398229504}
	if got := live.view("test").Resources; got == nil || got.ProcessCount != 115 {
		t.Fatalf("resources = %+v", got)
	}
	live.state.Online = false
	if live.view("test").Resources != nil {
		t.Fatal("offline node exposes stale resource readings")
	}
	live.state.Online = true
	live.state.StatusKnown = false
	if live.view("test").Resources != nil {
		t.Fatal("unknown status exposes resource readings")
	}
}

func TestContributionProjectsLineAndMessages(t *testing.T) {
	live := &liveNode{
		record: store.NasSimHubNodeRecord{ID: "NSH-410-0A0B0C"},
		sent:   map[string]sentMessage{"mm-9": {requestID: "req-42", peer: "10010", text: "CXLL"}},
		state:  readyState(),
	}
	at := time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)
	live.state.Messages = []proto.SMS{
		{ID: "mm-1", Direction: proto.DirectionIncoming, State: proto.SMSReceived, Peer: "10010", Text: "长短信第一段与第二段已合并", Timestamp: at},
		{ID: "mm-2", Direction: proto.DirectionIncoming, State: proto.SMSReceived, Peer: "10010", Text: "长短信第一段与第二段已合并", Timestamp: at},
		{ID: "mm-3", SIMID: "89860000000000000099", Direction: proto.DirectionIncoming, State: proto.SMSReceived, Peer: "1", Text: "old card", Timestamp: at},
		{ID: "mm-4", Direction: proto.DirectionIncoming, State: proto.SMSReceived, Peer: "", Text: "no sender", Timestamp: at},
		{ID: "mm-9", Direction: proto.DirectionOutgoing, State: proto.SMSSent, Peer: "10010", Text: "CXLL", Timestamp: at},
		{ID: "mm-10", Direction: proto.DirectionOutgoing, State: proto.SMSSent, Peer: "10086", Text: "sent elsewhere", Timestamp: at},
	}
	line, messages, ok := live.contribution()
	if !ok {
		t.Fatal("a ready node must contribute")
	}
	if line.ID != "nsh:NSH-410-0A0B0C:0" || line.SIMIdentifier != "89860000000000000001" || line.HomeCountryISO != "CN" {
		t.Fatalf("unexpected line identity: %+v", line)
	}
	if !line.Capabilities.SendMessage || line.SignalQuality != 60 || line.RegistrationState == "" {
		t.Fatalf("unexpected line state: %+v", line)
	}
	if len(line.OwnNumbers) != 1 {
		t.Fatalf("own number not projected: %+v", line.OwnNumbers)
	}
	ids := map[string]agentclient.Message{}
	for _, message := range messages {
		ids[message.ID] = message
	}
	// mm-1 and mm-2 are the SIM and modem copies of one delivery.
	if len(messages) != 3 || len(ids) != 2 {
		t.Fatalf("want two distinct messages (one incoming, one ours), got %+v", messages)
	}
	ours, ok := ids["nsh-out:req-42"]
	if !ok || ours.State != "sent" || ours.Direction != "outgoing" {
		t.Fatalf("our outgoing message not matched to its request: %+v", ids)
	}
	for _, message := range messages {
		if message.Text == "sent elsewhere" || message.Text == "old card" {
			t.Fatalf("message %q must not be projected", message.Text)
		}
	}
}

func TestCallContributionFiltersOldSIMAndNeverAdvertisesMedia(t *testing.T) {
	state := readyState()
	state.CallsKnown = true
	state.Calls = []proto.Call{
		{ID: "mm-4", SIMID: state.Status.SIM.ICCID, Direction: proto.DirectionOutgoing, State: proto.CallActive, Peer: "10000"},
		{ID: "mm-5", SIMID: "old-sim", State: proto.CallActive},
		{ID: "mm-6", State: proto.CallTerminated},
	}
	live := &liveNode{record: store.NasSimHubNodeRecord{ID: "node-1"}, state: state}
	calls := live.callContribution()
	if len(calls) != 1 || calls[0].ID != "mm-4" || calls[0].LineID != lineEndpointID("node-1") {
		t.Fatalf("unexpected projected calls: %+v", calls)
	}
	if calls[0].MediaAvailable || calls[0].MediaConfigured || calls[0].AudioPort != "" {
		t.Fatalf("unverified media advertised: %+v", calls[0])
	}
	state.CallsKnown = false
	live.state = state
	if got := live.callContribution(); len(got) != 0 {
		t.Fatalf("stale calls survived failed read: %+v", got)
	}
}

func TestContributionWithheldUntilUsable(t *testing.T) {
	for name, mutate := range map[string]func(*nodeState){
		"offline":      func(s *nodeState) { s.Online = false },
		"revoked":      func(s *nodeState) { s.Revoked = true },
		"incompatible": func(s *nodeState) { s.Compatible = false },
		"no status":    func(s *nodeState) { s.StatusKnown = false },
		"no iccid":     func(s *nodeState) { s.Status.SIM.ICCID = "" },
	} {
		state := readyState()
		mutate(&state)
		live := &liveNode{record: store.NasSimHubNodeRecord{ID: "N"}, state: state, sent: map[string]sentMessage{}}
		if _, _, ok := live.contribution(); ok {
			t.Errorf("%s: node must not contribute a line", name)
		}
	}
}

func TestEndpointOwnership(t *testing.T) {
	fleet := &Fleet{}
	if !fleet.OwnsEndpoint("nsh:NSH-410-0A0B0C:0") || fleet.OwnsEndpoint("/org/freedesktop/ModemManager1/Modem/0") {
		t.Fatal("endpoint ownership misclassified")
	}
	if !fleet.OwnsMessageID("nsh-in:abc") || !fleet.OwnsMessageID("nsh-out:r") || fleet.OwnsMessageID("mm-3") {
		t.Fatal("message ownership misclassified")
	}
	if device, ok := deviceFromEndpoint("nsh:NSH-410-0A0B0C:0"); !ok || device != "NSH-410-0A0B0C" {
		t.Fatalf("device not recovered: %q %v", device, ok)
	}
	for _, bad := range []string{"nsh:", "nsh::0", "nsh:X:1", "nsh:X", "modem:0"} {
		if _, ok := deviceFromEndpoint(bad); ok {
			t.Errorf("%q must not resolve to a device", bad)
		}
	}
}

func TestSendMessageRefusesWithoutNetworkWhenNotReady(t *testing.T) {
	state := readyState()
	state.Status.SIM.State = proto.SIMPINLocked
	fleet := &Fleet{now: time.Now, nodes: map[string]*liveNode{
		"N": {record: store.NasSimHubNodeRecord{ID: "N"}, state: state, sent: map[string]sentMessage{}},
	}}
	_, err := fleet.SendMessage(context.Background(), agentclient.SendMessageRequest{LineID: "nsh:N:0", RequestID: "r", Number: "10010", Text: "x"})
	var operation *agentclient.OperationError
	if !errors.As(err, &operation) || operation.Code != "failed_precondition" {
		t.Fatalf("a locked SIM must be a definite precondition failure, got %v", err)
	}
	_, err = fleet.SendMessage(context.Background(), agentclient.SendMessageRequest{LineID: "nsh:OTHER:0", RequestID: "r", Number: "1", Text: "x"})
	if !errors.As(err, &operation) || operation.Code != "not_found" {
		t.Fatalf("an unknown node must be not_found, got %v", err)
	}
}

func TestPublicErrorHidesAddressesAndNamesPinFailures(t *testing.T) {
	if got := publicError(errors.New(`Get "https://10.55.0.2:7580/v1/node": tls: failed to verify certificate: x509: certificate signed by unknown authority`)); got != pinRejectedDiagnosis {
		t.Fatalf("pin failure diagnosed as %q", got)
	}
	if got := publicError(fmt.Errorf("wrap: no usable endpoint for this node: dial tcp 10.55.0.2:7580: connection refused")); strings.Contains(got, "10.55") {
		t.Fatalf("diagnosis leaks an address: %q", got)
	}
}

func TestIdentityPersistsWithPrivateMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nassimhub")
	first, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.CoreID != second.CoreID || !strings.HasPrefix(first.CoreID, "nas-") {
		t.Fatalf("identity not stable: %q vs %q", first.CoreID, second.CoreID)
	}
	info, err := os.Stat(filepath.Join(dir, IdentityFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("identity file mode %v, want 0600", info.Mode().Perm())
	}
}

// An IMS registration on the Node is signalling only. Until the Node also
// reports call control AND call audio, the NAS must neither call the line
// voice-capable nor give the line any call capability.
func TestVoLTESignallingAloneNeverOpensCalling(t *testing.T) {
	for name, caps := range map[string]proto.Capabilities{
		"ims registered, no control, no audio": {SMS: true, VoLTE: proto.TriYes},
		"control without audio":                {SMS: true, VoLTE: proto.TriYes, VoiceControl: true},
		"audio without control":                {SMS: true, VoLTE: proto.TriYes, VoiceAudio: true},
	} {
		state := readyState()
		state.Capabilities = caps
		live := &liveNode{record: store.NasSimHubNodeRecord{ID: "NSH-410-0A0B0C"}, state: state, sent: map[string]sentMessage{}}
		view := live.view("nas-test")
		if view.VoiceCapable {
			t.Errorf("%s: voice_capable must be false", name)
		}
		if view.VoLTE != proto.TriYes {
			t.Errorf("%s: volte = %q, want yes", name, view.VoLTE)
		}
		encoded, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"volte":"yes"`, `"voice_capable":false`} {
			if !strings.Contains(string(encoded), want) {
				t.Errorf("%s: JSON lacks %s: %s", name, want, encoded)
			}
		}
		line, _, ok := live.contribution()
		if !ok {
			t.Fatalf("%s: ready node must contribute a line", name)
		}
		c := line.Capabilities
		if c.Dial || c.AnswerCall || c.HangupCall || c.RejectCall || c.SendDTMF {
			t.Errorf("%s: node line must not offer call actions: %+v", name, c)
		}
	}
}

func TestVoLTEUnstatedIsUnknown(t *testing.T) {
	for _, stated := range []proto.Tristate{"", "maybe", proto.TriUnknown} {
		state := readyState()
		state.Capabilities.VoLTE = stated
		live := &liveNode{record: store.NasSimHubNodeRecord{ID: "N"}, state: state, sent: map[string]sentMessage{}}
		if got := live.view("nas-test").VoLTE; got != proto.TriUnknown {
			t.Errorf("VoLTE %q shown as %q, want unknown", stated, got)
		}
	}
	for _, stated := range []proto.Tristate{proto.TriYes, proto.TriNo} {
		state := readyState()
		state.Capabilities.VoLTE = stated
		live := &liveNode{record: store.NasSimHubNodeRecord{ID: "N"}, state: state, sent: map[string]sentMessage{}}
		if got := live.view("nas-test").VoLTE; got != stated {
			t.Errorf("VoLTE %q shown as %q", stated, got)
		}
	}
}

func TestNodeCallCommandsRefuseDefinitelyWhenNodeCannotCall(t *testing.T) {
	for name, mutate := range map[string]func(*nodeState){
		"offline":          func(s *nodeState) { s.Online = false },
		"revoked":          func(s *nodeState) { s.Revoked = true },
		"no voice control": func(s *nodeState) { s.Capabilities.VoiceControl = false },
		"sim locked":       func(s *nodeState) { s.Status.SIM.State = proto.SIMPINLocked },
	} {
		state := readyState()
		state.Capabilities.VoiceControl = true
		mutate(&state)
		fleet := &Fleet{now: time.Now, nodes: map[string]*liveNode{
			"N": {record: store.NasSimHubNodeRecord{ID: "N"}, state: state, sent: map[string]sentMessage{}},
		}}
		_, err := fleet.StartCall(context.Background(), agentclient.StartCallRequest{LineID: "nsh:N:0", RequestID: "r", Number: "10000"})
		var definiteErr *agentclient.OperationError
		if !errors.As(err, &definiteErr) {
			t.Errorf("%s: dial must be refused definitely, got %v", name, err)
		}
		_, err = fleet.CallAction(context.Background(), "nsh:N:0", "mm-call-1", "hangup", agentclient.CallActionRequest{RequestID: "r"})
		if !errors.As(err, &definiteErr) {
			t.Errorf("%s: hangup must be refused definitely, got %v", name, err)
		}
	}
}

func TestNodeCallActionValidatesInput(t *testing.T) {
	state := readyState()
	state.Capabilities.VoiceControl = true
	fleet := &Fleet{now: time.Now, nodes: map[string]*liveNode{
		"N": {record: store.NasSimHubNodeRecord{ID: "N"}, state: state, sent: map[string]sentMessage{}},
	}}
	var definiteErr *agentclient.OperationError
	for _, bad := range []string{"", "../node", "mm call", "a/b", strings.Repeat("x", 65)} {
		_, err := fleet.CallAction(context.Background(), "nsh:N:0", bad, "hangup", agentclient.CallActionRequest{RequestID: "r"})
		if !errors.As(err, &definiteErr) || definiteErr.Code != "invalid_argument" {
			t.Errorf("call id %q: %v", bad, err)
		}
	}
	_, err := fleet.CallAction(context.Background(), "nsh:N:0", "mm-call-1", "dtmf", agentclient.CallActionRequest{RequestID: "r"})
	if !errors.As(err, &definiteErr) || definiteErr.Code != "not_supported" {
		t.Errorf("dtmf: %v", err)
	}
	_, err = fleet.CallAction(context.Background(), "line-1", "mm-call-1", "hangup", agentclient.CallActionRequest{RequestID: "r"})
	if !errors.As(err, &definiteErr) || definiteErr.Code != "not_found" {
		t.Errorf("host line: %v", err)
	}
	_, err = fleet.StartCall(context.Background(), agentclient.StartCallRequest{LineID: "nsh:N:0", RequestID: "r", Number: "  "})
	if !errors.As(err, &definiteErr) || definiteErr.Code != "invalid_argument" {
		t.Errorf("empty number: %v", err)
	}
}

func TestClassifyCallErrorSeparatesDefiniteFromUncertain(t *testing.T) {
	for _, code := range []proto.ErrorCode{proto.ErrorInvalidArgument, proto.ErrorFailedPrecondition,
		proto.ErrorNotFound, proto.ErrorNotSupported, proto.ErrorConflict, proto.ErrorNetworkRejected} {
		var op *agentclient.OperationError
		if !errors.As(classifyCallError("start_call", &proto.OperationError{Code: code}), &op) || op.Code != string(code) {
			t.Errorf("%s must be definite", code)
		}
	}
	for _, input := range []error{
		errors.New("connection reset"),
		context.DeadlineExceeded,
		&proto.OperationError{Code: proto.ErrorUnavailable, Message: "call result is uncertain"},
		&proto.OperationError{Code: proto.ErrorInternal},
	} {
		var op *agentclient.OperationError
		if errors.As(classifyCallError("start_call", input), &op) {
			t.Errorf("%v must be indeterminate", input)
		}
	}
}

func TestNodeCallRequestIDIsDeterministicAndSeparateFromSMS(t *testing.T) {
	accepted := regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	id := nodeCallRequestID("带中文 / request")
	if !accepted.MatchString(id) || nodeCallRequestID("带中文 / request") != id {
		t.Fatalf("call request id %q", id)
	}
	if nodeCallRequestID("same") == nodeRequestID("same") {
		t.Fatal("call and SMS request ids must not collide on the Node")
	}
}
