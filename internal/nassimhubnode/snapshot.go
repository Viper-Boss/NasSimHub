package nassimhubnode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
	"github.com/human-agent65535/modemdeck/internal/store"
	"github.com/human-agent65535/nassimhub-node/proto"
)

// Each Node SIM is projected as one line whose endpoint ID is
// "nsh:<device_id>:<slot>". The gateway's hardware projection then treats it
// exactly like a host-agent line: the stable line is resolved by ICCID, so the
// card keeps its line and history if it moves between a Node and a host modem.
//
// Message identity. A Node reports ModemManager object indices ("mm-7"),
// which are renumbered whenever ModemManager restarts, so they cannot be the
// stored identity. Incoming messages are identified by a digest of what the
// network delivered - SIM, sender, SMSC timestamp and text - which is stable
// across restarts and identical for the SIM and modem storage copies.
// Outgoing messages sent through this gateway are already recorded under
// their request ID; the Node's copy is matched to that row only while the
// index is known to belong to it, and otherwise ignored rather than guessed.

const (
	incomingMessagePrefix = "nsh-in:"
	outgoingMessagePrefix = "nsh-out:"
	nodeLineSlot          = "0"
)

// ModemManager enumerations, as the host agent reports them.
const (
	mmStateFailed     = -1
	mmStateLocked     = 2
	mmStateEnabled    = 6
	mmStateSearching  = 7
	mmStateRegistered = 8
	mmStateConnected  = 11

	mmRegistrationIdle      = 0
	mmRegistrationHome      = 1
	mmRegistrationSearching = 2
	mmRegistrationDenied    = 3
	mmRegistrationUnknown   = 4
	mmRegistrationRoaming   = 5

	mmAccessGSM  = 1 << 1
	mmAccessUMTS = 1 << 5
	mmAccessLTE  = 1 << 14
)

func lineEndpointID(deviceID string) string {
	return store.NasSimHubNodeEndpointPrefix + deviceID + ":" + nodeLineSlot
}

// OwnsEndpoint reports whether an endpoint line ID belongs to a Node.
func (f *Fleet) OwnsEndpoint(endpointID string) bool {
	return strings.HasPrefix(strings.TrimSpace(endpointID), store.NasSimHubNodeEndpointPrefix)
}

// OwnsMessageID reports whether a message identity was minted here. The host
// agent must never be asked to delete one.
func (f *Fleet) OwnsMessageID(messageID string) bool {
	messageID = strings.TrimSpace(messageID)
	return strings.HasPrefix(messageID, incomingMessagePrefix) || strings.HasPrefix(messageID, outgoingMessagePrefix)
}

func deviceFromEndpoint(endpointID string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(endpointID), store.NasSimHubNodeEndpointPrefix)
	if !ok {
		return "", false
	}
	device, slot, ok := strings.Cut(rest, ":")
	if !ok || device == "" || slot != nodeLineSlot {
		return "", false
	}
	return device, true
}

// Contribution is what the Nodes add to one hardware snapshot.
type Contribution struct {
	Lines    []agentclient.Line
	Messages []agentclient.Message
	Calls    []agentclient.Call
	Revision string
	Nodes    int
}

// Snapshot returns the current Node contribution from cached poll results. It
// never performs network I/O, so it cannot slow the host refresh.
func (f *Fleet) Snapshot() Contribution {
	f.mu.Lock()
	nodes := make([]*liveNode, 0, len(f.nodes))
	for _, live := range f.nodes {
		nodes = append(nodes, live)
	}
	f.mu.Unlock()
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].record.ID < nodes[j].record.ID })
	contribution := Contribution{Nodes: len(nodes)}
	digest := sha256.New()
	for _, live := range nodes {
		line, messages, ok := live.contribution()
		if !ok {
			fmt.Fprintf(digest, "%s|absent\n", live.record.ID)
			continue
		}
		contribution.Lines = append(contribution.Lines, line)
		contribution.Messages = append(contribution.Messages, messages...)
		calls := live.callContribution()
		contribution.Calls = append(contribution.Calls, calls...)
		fmt.Fprintf(digest, "%s|%s|%s|%s|%d|%s|%s|%t|%t\n", line.ID, line.SIMIdentifier,
			line.State, line.RegistrationState, line.SignalQuality/20, line.OperatorName,
			firstString(line.OwnNumbers), line.Capabilities.SendMessage, line.Capabilities.Dial)
		for _, message := range messages {
			fmt.Fprintf(digest, "%s|%s\n", message.ID, message.State)
		}
		for _, call := range calls {
			fmt.Fprintf(digest, "%s|%s|%s|%s|%s\n", call.LineID, call.ID, call.State, call.Direction, call.Number)
		}
	}
	contribution.Revision = hex.EncodeToString(digest.Sum(nil)[:12])
	return contribution
}

// callContribution publishes call state for the current SIM. Media remains
// unavailable until the NAS media opener and Node audio service are enabled.
func (l *liveNode) callContribution() []agentclient.Call {
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.state
	if !state.Online || state.Revoked || !state.Compatible || !state.StatusKnown || !state.CallsKnown {
		return nil
	}
	simID := state.Status.SIM.ICCID
	if simID == "" {
		return nil
	}
	endpoint := lineEndpointID(l.record.ID)
	result := make([]agentclient.Call, 0, len(state.Calls))
	for _, call := range state.Calls {
		if call.ID == "" || call.SIMID != simID || call.State == proto.CallTerminated {
			continue
		}
		var audioFormat *agentclient.CallAudioFormat
		audioPort := ""
		if nodeVoiceReady(state) {
			audioPort = "nsh:q6voice"
			audioFormat = &agentclient.CallAudioFormat{Encoding: "pcm", Resolution: "s16le", Rate: 16000}
		}
		result = append(result, agentclient.Call{
			ID: call.ID, LineID: endpoint, Number: call.Peer,
			Direction: string(call.Direction), State: string(call.State),
			StateReason: call.EndReason, Bearer: "volte",
			AudioPort:       audioPort,
			AudioFormat:     audioFormat,
			MediaAvailable:  nodeVoiceReady(state) && call.State == proto.CallActive,
			MediaConfigured: nodeVoiceReady(state),
		})
	}
	return result
}

func (f *Fleet) contributionRevision() string {
	return f.Snapshot().Revision
}

func (l *liveNode) contribution() (agentclient.Line, []agentclient.Message, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.state
	if !state.Online || state.Revoked || !state.Compatible || !state.StatusKnown {
		return agentclient.Line{}, nil, false
	}
	sim := state.Status.SIM
	if sim.ICCID == "" {
		return agentclient.Line{}, nil, false
	}
	endpoint := lineEndpointID(l.record.ID)
	line := projectNodeLine(l.record.ID, endpoint, state)
	if !state.MessagesKnown {
		return line, nil, true
	}
	messages := make([]agentclient.Message, 0, len(state.Messages))
	for _, message := range state.Messages {
		if message.SIMID != "" && message.SIMID != sim.ICCID {
			continue // a message from a card no longer in the slot
		}
		projected, ok := l.projectMessageLocked(endpoint, sim.ICCID, message)
		if ok {
			messages = append(messages, projected)
		}
	}
	return line, messages, true
}

func nodeVoiceReady(state nodeState) bool {
	return state.Online && !state.Revoked && state.Compatible && state.StatusKnown &&
		state.Status.SIM.State == proto.SIMReady && state.Capabilities.VoLTE == proto.TriYes &&
		state.Capabilities.VoiceControl && state.Capabilities.VoiceAudio
}

func projectNodeLine(deviceID, endpoint string, state nodeState) agentclient.Line {
	status := state.Status
	sim := status.SIM
	network := status.Network
	simReady := sim.State == proto.SIMReady
	line := agentclient.Line{
		ID:                   endpoint,
		Manufacturer:         "NasSimHub",
		Model:                firstNonEmpty(state.Model, "UFI003"),
		Revision:             state.AgentVersion,
		DeviceIdentifier:     deviceID,
		EquipmentIdentifier:  deviceID,
		IdentityPersistent:   true,
		IdentitySource:       "nassimhub-node",
		SavedPolicySupported: false,
		SIMPresent:           sim.State != proto.SIMMissing && sim.State != "",
		SIMIdentifier:        sim.ICCID,
		IMSI:                 sim.IMSI,
		HomeOperatorCode:     firstNonEmpty(sim.OperatorCode, mccmncOf(sim.IMSI)),
		HomeOperatorName:     sim.OperatorName,
		HomeCountryISO:       countryForIMSI(sim.IMSI),
		ServingOperatorCode:  network.OperatorCode,
		ServingOperatorName:  network.OperatorName,
		OperatorIdentifier:   firstNonEmpty(network.OperatorCode, sim.OperatorCode),
		OperatorName:         firstNonEmpty(network.OperatorName, sim.OperatorName),
		Roaming:              network.Roaming || network.Registration == proto.RegRoaming,
		Capabilities: agentclient.LineCapabilities{
			ModemInterface:     true,
			SIMInterface:       true,
			MessagingInterface: state.Capabilities.SMS,
			VoiceInterface:     nodeVoiceReady(state),
			Dial:               nodeVoiceReady(state), AnswerCall: nodeVoiceReady(state),
			HangupCall: nodeVoiceReady(state), RejectCall: nodeVoiceReady(state),
			Media:       nodeVoiceReady(state),
			SendMessage: state.Capabilities.SMS && simReady,
		},
	}
	if phone := strings.TrimSpace(sim.PhoneNumber); phone != "" {
		line.OwnNumbers = []string{phone}
	}
	line.ServingCountryISO = line.HomeCountryISO
	line.State, line.StateCode = modemState(status)
	line.RegistrationStateKnown = true
	line.RegistrationState, line.RegistrationStateCode = registrationState(network.Registration)
	if technology, ok := accessTechnology(network.AccessTechnology); ok {
		line.AccessTechnologies = technology
		line.AccessTechnologiesKnown = true
	}
	signal := status.Signal
	if signal.Known {
		line.SignalQualityKnown = true
		line.SignalQualityRecent = true
		line.SignalMetricsRecent = true
		bars := signal.Bars
		if bars < 0 {
			bars = 0
		}
		if bars > 5 {
			bars = 5
		}
		line.SignalQuality = uint32(bars * 20)
		line.SignalDBM = signal.DBM
		line.SignalRSRP = signal.RSRP
		line.SignalRSRQ = signal.RSRQ
		line.SignalSNR = signal.SNR
	}
	return line
}

func (l *liveNode) projectMessageLocked(endpoint, simID string, message proto.SMS) (agentclient.Message, bool) {
	peer := strings.TrimSpace(message.Peer)
	text := message.Text
	if peer == "" || strings.TrimSpace(text) == "" {
		return agentclient.Message{}, false
	}
	projected := agentclient.Message{
		LineID: endpoint,
		Number: peer,
		Text:   text,
	}
	if !message.Timestamp.IsZero() {
		projected.Timestamp = message.Timestamp.UTC().Format(time.RFC3339)
	}
	switch message.Direction {
	case proto.DirectionIncoming:
		if message.State != proto.SMSReceived {
			return agentclient.Message{}, false
		}
		projected.ID = incomingMessageID(simID, peer, message.Timestamp, text, message.ID)
		projected.Direction = "incoming"
		projected.State = "received"
		return projected, true
	case proto.DirectionOutgoing:
		sent, ok := l.sent[message.ID]
		if !ok || sent.peer != peer || sent.text != text {
			return agentclient.Message{}, false
		}
		projected.ID = outgoingMessagePrefix + sent.requestID
		projected.Direction = "outgoing"
		switch message.State {
		case proto.SMSSent:
			projected.State = "sent"
		case proto.SMSFailed:
			projected.State = "failed"
		default:
			projected.State = "sending"
		}
		return projected, true
	}
	return agentclient.Message{}, false
}

// incomingMessageID is stable across ModemManager restarts and identical for
// the SIM and modem storage copies of one delivery. The fallback index is used
// only when the network supplied no timestamp, which a real SMS-DELIVER always
// carries.
func incomingMessageID(simID, peer string, timestamp time.Time, text, fallback string) string {
	digest := sha256.New()
	fmt.Fprintf(digest, "v1\x00%s\x00%s\x00", simID, peer)
	if timestamp.IsZero() {
		fmt.Fprintf(digest, "index:%s", fallback)
	} else {
		fmt.Fprintf(digest, "%d", timestamp.UTC().Unix())
	}
	digest.Write([]byte{0})
	digest.Write([]byte(text))
	return incomingMessagePrefix + hex.EncodeToString(digest.Sum(nil)[:16])
}

// SendMessage submits an SMS through the Node that owns the line.
//
// The Node's request ID is derived from the gateway's, deterministically, so a
// retry of the same gateway request reaches the Node's own idempotency record
// and can never produce a second SMS. Only failures the Node reports as
// definite are returned as agent operation errors; anything else (a timeout, a
// dropped link, "result is uncertain") is returned as a plain error, which the
// communication service records as indeterminate.
func (f *Fleet) SendMessage(ctx context.Context, request agentclient.SendMessageRequest) (agentclient.CommandReceipt, error) {
	deviceID, ok := deviceFromEndpoint(request.LineID)
	if !ok {
		return agentclient.CommandReceipt{}, &agentclient.OperationError{Code: "not_found", Operation: "send_message", Message: "the line does not belong to a NasSimHub Node"}
	}
	live := f.lookup(deviceID)
	if live == nil {
		return agentclient.CommandReceipt{}, &agentclient.OperationError{Code: "not_found", Operation: "send_message", Message: ErrUnknownNode.Error()}
	}
	live.mu.Lock()
	state := live.state
	client := live.client
	live.mu.Unlock()
	if !state.Online || state.Revoked {
		return agentclient.CommandReceipt{}, &agentclient.OperationError{Code: "unavailable", Operation: "send_message", Message: ErrNodeUnavailable.Error()}
	}
	if !state.StatusKnown || state.Status.SIM.State != proto.SIMReady {
		return agentclient.CommandReceipt{}, &agentclient.OperationError{Code: "failed_precondition", Operation: "send_message", Message: ErrNodeNotReady.Error()}
	}
	number := strings.TrimSpace(request.Number)
	response, err := client.SendSMS(ctx, proto.SendSMSRequest{
		RequestID: nodeRequestID(request.RequestID),
		To:        number,
		Text:      request.Text,
	})
	if err != nil {
		return agentclient.CommandReceipt{}, classifySendError(err)
	}
	if response.MessageID != "" {
		live.mu.Lock()
		if len(live.sent) >= maxSentRemembered {
			pruneSent(live.sent)
		}
		live.sent[response.MessageID] = sentMessage{
			requestID: request.RequestID,
			peer:      number,
			text:      request.Text,
			at:        f.now(),
		}
		live.mu.Unlock()
	}
	go f.Refresh(context.WithoutCancel(ctx), deviceID) //nolint:errcheck // best-effort state pickup
	return agentclient.CommandReceipt{
		RequestID:                 request.RequestID,
		ResourceID:                outgoingMessagePrefix + request.RequestID,
		DeliveryReportUnsupported: true,
	}, nil
}

func pruneSent(sent map[string]sentMessage) {
	oldestKey := ""
	var oldest time.Time
	for key, value := range sent {
		if oldestKey == "" || value.at.Before(oldest) {
			oldestKey, oldest = key, value.at
		}
	}
	delete(sent, oldestKey)
}

// nodeRequestID maps any gateway request ID onto the Node's accepted alphabet
// ([A-Za-z0-9_-]{1,128}) without losing uniqueness.
func nodeRequestID(requestID string) string {
	digest := sha256.Sum256([]byte("nas-sms\x00" + requestID))
	return "nas-" + hex.EncodeToString(digest[:16])
}

func classifySendError(err error) error {
	var operation *proto.OperationError
	if !errors.As(err, &operation) {
		return fmt.Errorf("node send result unknown: %w", err)
	}
	switch operation.Code {
	case proto.ErrorInvalidArgument, proto.ErrorFailedPrecondition, proto.ErrorNotFound,
		proto.ErrorNotSupported, proto.ErrorPermissionDenied, proto.ErrorNetworkRejected:
		return &agentclient.OperationError{Code: string(operation.Code), Operation: "send_message", Message: operation.Message}
	case proto.ErrorUnavailable:
		if strings.Contains(strings.ToLower(operation.Message), "uncertain") {
			return fmt.Errorf("node send result unknown: %w", err)
		}
		return &agentclient.OperationError{Code: "unavailable", Operation: "send_message", Message: operation.Message}
	}
	return fmt.Errorf("node send result unknown: %w", err)
}

func modemState(status proto.ModemStatus) (string, int32) {
	switch {
	case status.State == proto.ModemFailed:
		return "failed", mmStateFailed
	case status.SIM.State == proto.SIMPINLocked || status.SIM.State == proto.SIMPUKLocked:
		return "locked", mmStateLocked
	case status.Network.Registration == proto.RegRegistered || status.Network.Registration == proto.RegRoaming:
		if status.Network.DataConnected {
			return "connected", mmStateConnected
		}
		return "registered", mmStateRegistered
	case status.Network.Registration == proto.RegSearching:
		return "searching", mmStateSearching
	case status.State == proto.ModemReady:
		return "enabled", mmStateEnabled
	}
	return "unknown", 0
}

func registrationState(registration proto.RegistrationState) (string, uint32) {
	switch registration {
	case proto.RegRegistered:
		return "home", mmRegistrationHome
	case proto.RegRoaming:
		return "roaming", mmRegistrationRoaming
	case proto.RegSearching:
		return "searching", mmRegistrationSearching
	case proto.RegDenied:
		return "denied", mmRegistrationDenied
	case proto.RegUnregistered:
		return "idle", mmRegistrationIdle
	}
	return "unknown", mmRegistrationUnknown
}

func accessTechnology(technology proto.AccessTechnology) (uint32, bool) {
	switch technology {
	case proto.AccessLTE:
		return mmAccessLTE, true
	case proto.AccessUMTS:
		return mmAccessUMTS, true
	case proto.AccessGSM:
		return mmAccessGSM, true
	}
	return 0, false
}

func mccmncOf(imsi string) string {
	imsi = strings.TrimSpace(imsi)
	if len(imsi) < 5 {
		return ""
	}
	return imsi[:5]
}

// countryForIMSI covers the networks a UFI003 is realistically used on. An
// unknown MCC leaves the region empty, which the line model tolerates.
func countryForIMSI(imsi string) string {
	imsi = strings.TrimSpace(imsi)
	if len(imsi) < 3 {
		return ""
	}
	switch imsi[:3] {
	case "460", "461":
		return "CN"
	case "454":
		return "HK"
	case "455":
		return "MO"
	case "466":
		return "TW"
	case "440", "441":
		return "JP"
	case "450":
		return "KR"
	case "525":
		return "SG"
	case "234", "235":
		return "GB"
	case "310", "311", "312", "313", "314", "315", "316":
		return "US"
	}
	return ""
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// NodeSnapshot adapts Snapshot to communication.NodeSource.
func (f *Fleet) NodeSnapshot() ([]agentclient.Line, []agentclient.Message, []agentclient.Call, string, int) {
	contribution := f.Snapshot()
	return contribution.Lines, contribution.Messages, contribution.Calls, contribution.Revision, contribution.Nodes
}
