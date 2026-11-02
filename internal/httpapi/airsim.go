package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/airsimgw"
	"github.com/human-agent65535/modemdeck/internal/auth"
	"github.com/human-agent65535/modemdeck/internal/calllease"
	"github.com/human-agent65535/modemdeck/internal/callmedia"
	"github.com/human-agent65535/modemdeck/internal/mediaapp"
	"github.com/human-agent65535/modemdeck/internal/store"
)

// AirSIM iPhone app compatibility (internal/airsimgw). The gateway owns the
// AirSIM wire protocol; this file is the NasSimHub side: the administrator
// settings that open pairing windows, and the Backend the gateway drives.
//
// Every command a paired iPhone sends is run through the same handlers a
// signed-in browser uses (line scoping, call ownership, the anti-fraud
// guard), as the account that paired it, limited to the one line the
// gateway exposes and with a member's role. The context value that carries
// that identity cannot be set from the network: it is only ever attached
// here, to requests built in process.

type airsimInternalContextKey struct{}

type airsimInternalIdentity struct {
	principal auth.Principal
	scope     callLeaseSessionScope
}

func airsimInternalFromContext(ctx context.Context) (airsimInternalIdentity, bool) {
	identity, ok := ctx.Value(airsimInternalContextKey{}).(airsimInternalIdentity)
	return identity, ok
}

// airsimLeaseScope gives every paired iPhone its own call-ownership holder,
// distinct from any browser session or other iPhone of the same account.
func airsimLeaseScope(pairingID string) callLeaseSessionScope {
	return callLeaseSessionScope(sha256.Sum256([]byte("airsim-pairing\x00" + pairingID)))
}

func airsimOwnerToken(pairingID string) string {
	digest := sha256.Sum256([]byte("airsim-media\x00" + pairingID))
	return fmt.Sprintf("airsim-%x", digest[:16])
}

type airsimBackend struct {
	api *API
}

// airsimPrincipal re-reads the pairing account on every command, so
// disabling the account, removing the administrator role or taking away the
// line takes effect on the next request.
func (b airsimBackend) airsimPrincipal(ctx context.Context, identity airsimgw.Identity) (auth.Principal, error) {
	repository, ok := b.api.repository.(userRepository)
	if !ok {
		return auth.Principal{}, airsimgw.ErrUnavailable
	}
	users, err := repository.Users(ctx)
	if err != nil {
		return auth.Principal{}, fmt.Errorf("%w: %v", airsimgw.ErrUnavailable, err)
	}
	for _, user := range users {
		if user.ID != identity.UserID {
			continue
		}
		if !user.Enabled || user.Role != auth.RoleAdmin {
			// The account itself is gone: the gateway forgets its pairings.
			return auth.Principal{}, airsimgw.ErrForbidden
		}
		if !slices.Contains(user.LineIDs, identity.LineID) {
			// Only this line is out of reach (another administrator chose
			// it in Settings): refuse, but keep the pairing for when the
			// line is assigned or changed back.
			return auth.Principal{}, &airsimgw.BackendError{Kind: airsimgw.ErrUnavailable, Message: "line_not_assigned"}
		}
		return auth.Principal{
			UserID:         user.ID,
			Username:       user.Username,
			Role:           auth.RoleMember,
			AllowedLineIDs: []string{identity.LineID},
		}, nil
	}
	return auth.Principal{}, airsimgw.ErrForbidden
}

func (b airsimBackend) context(ctx context.Context, identity airsimgw.Identity) (context.Context, error) {
	principal, err := b.airsimPrincipal(ctx, identity)
	if err != nil {
		return nil, err
	}
	scope := airsimLeaseScope(identity.PairingID)
	ctx = context.WithValue(ctx, callLeaseSessionScopeContextKey{}, scope)
	ctx = context.WithValue(ctx, airsimInternalContextKey{}, airsimInternalIdentity{principal: principal, scope: scope})
	return auth.ContextWithPrincipal(ctx, principal), nil
}

func (b airsimBackend) holder(identity airsimgw.Identity) string {
	return callLeaseHolderForScope(airsimLeaseScope(identity.PairingID))
}

// do runs one request through the API router as the pairing.
func (b airsimBackend) do(ctx context.Context, identity airsimgw.Identity, method, path string, body any) (*httptest.ResponseRecorder, error) {
	ctx, err := b.context(ctx, identity)
	if err != nil {
		return nil, err
	}
	var payload []byte
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	request := httptest.NewRequestWithContext(ctx, method, path, bytes.NewReader(payload))
	request.RemoteAddr = identity.Peer + ":0"
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("User-Agent", "AirSIM (NasSimHub gateway)")
	recorder := httptest.NewRecorder()
	b.api.ServeHTTP(recorder, request)
	if recorder.Code >= 200 && recorder.Code < 300 {
		return recorder, nil
	}
	return recorder, airsimResponseError(recorder)
}

func airsimResponseError(recorder *httptest.ResponseRecorder) error {
	var failure errorResponse
	_ = json.Unmarshal(recorder.Body.Bytes(), &failure)
	message := strings.TrimSpace(failure.Message)
	if message == "" {
		message = failure.Code
	}
	kind := airsimgw.ErrConflict
	switch recorder.Code {
	case http.StatusForbidden:
		kind = airsimgw.ErrForbidden
	case http.StatusNotFound:
		kind = airsimgw.ErrNotFound
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		kind = airsimgw.ErrUnavailable
	case http.StatusInternalServerError:
		return fmt.Errorf("NasSimHub returned %d %s", recorder.Code, failure.Code)
	}
	return &airsimgw.BackendError{Kind: kind, Message: message}
}

func (b airsimBackend) Authorize(ctx context.Context, identity airsimgw.Identity) error {
	_, err := b.airsimPrincipal(ctx, identity)
	return err
}

func (b airsimBackend) Line(ctx context.Context, identity airsimgw.Identity) (airsimgw.Line, error) {
	ctx, err := b.context(ctx, identity)
	if err != nil {
		return airsimgw.Line{}, err
	}
	lines, err := b.api.repository.Lines(ctx)
	if err != nil {
		return airsimgw.Line{}, fmt.Errorf("%w: %v", airsimgw.ErrUnavailable, err)
	}
	for _, line := range lines {
		if line.ID != identity.LineID {
			continue
		}
		result := airsimgw.Line{
			DeviceName:   firstNonEmptyString(line.LineLabel, line.DeviceName, "NasSimHub"),
			Manufacturer: "NasSimHub",
			Model:        line.Model,
			Firmware:     line.Firmware,
			PhoneNumber:  line.PhoneNumber,
			Operator:     firstNonEmptyString(line.ServingOperatorName, line.Operator, line.HomeOperatorName),
			IMEI:         line.DeviceIMEI,
			ICCID:        line.ICCID,
			IMSI:         line.IMSI,
			Registered: line.RegistrationState == "home" || line.RegistrationState == "registered" ||
				line.RegistrationState == "roaming",
			Searching: line.RegistrationState == "searching",
		}
		if line.ServingRadio != nil {
			result.NetworkMode = line.ServingRadio.AccessTechnology
			result.Band = line.ServingRadio.Band
		}
		if line.Signal != nil {
			// ModemManager reports quality as a percentage; the app wants
			// dBm. Map 0-100 % onto -113..-51 dBm (the 3GPP CSQ range).
			dbm := -113 + int(*line.Signal)*62/100
			result.SignalDBM = &dbm
		}
		return result, nil
	}
	return airsimgw.Line{}, &airsimgw.BackendError{Kind: airsimgw.ErrUnavailable, Message: "line_not_found"}
}

func airsimTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func airsimCall(call store.Call) airsimgw.Call {
	return airsimgw.Call{
		ID:             call.ID,
		Direction:      call.Direction,
		Phase:          call.Phase,
		Number:         call.RemoteNumber,
		CreatedAt:      airsimTime(firstNonEmptyString(call.StartedAt, call.CreatedAt)),
		UpdatedAt:      airsimTime(call.UpdatedAt),
		EndedAt:        airsimTime(call.EndedAt),
		Missed:         call.Missed,
		MediaAvailable: call.MediaAvailable,
	}
}

func (b airsimBackend) Calls(ctx context.Context, identity airsimgw.Identity, historyLimit int) ([]airsimgw.Call, []airsimgw.Call, error) {
	ctx, err := b.context(ctx, identity)
	if err != nil {
		return nil, nil, err
	}
	if b.api.communications == nil {
		return nil, nil, airsimgw.ErrUnavailable
	}
	live, err := b.api.communications.ActiveCalls(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", airsimgw.ErrUnavailable, err)
	}
	active := make([]airsimgw.Call, 0, len(live))
	for _, call := range live {
		if call.LineID == identity.LineID {
			active = append(active, airsimCall(call))
		}
	}
	if historyLimit <= 0 {
		return active, nil, nil
	}
	recent, err := b.api.repository.Calls(ctx, store.CallQuery{Limit: historyLimit * 2})
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", airsimgw.ErrUnavailable, err)
	}
	history := make([]airsimgw.Call, 0, historyLimit)
	for _, call := range recent {
		if call.LineID != identity.LineID || (call.Phase != "ended" && call.Phase != "failed") {
			continue
		}
		history = append(history, airsimCall(call))
		if len(history) == historyLimit {
			break
		}
	}
	return active, history, nil
}

func (b airsimBackend) Dial(ctx context.Context, identity airsimgw.Identity, number, requestID string) (airsimgw.Call, error) {
	recorder, err := b.do(ctx, identity, http.MethodPost, "/api/v1/calls", startCallRequest{
		RequestID: requestID,
		LineID:    identity.LineID,
		Number:    number,
	})
	if err != nil {
		return airsimgw.Call{}, err
	}
	var created callSessionEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil || created.Call.ID == "" {
		return airsimgw.Call{}, fmt.Errorf("decode started call: %v", err)
	}
	return airsimgw.Call{
		ID: created.Call.ID, Direction: "outgoing", Phase: created.Call.Phase, Number: number,
		CreatedAt: airsimTime(created.Call.CreatedAt), UpdatedAt: time.Now(),
	}, nil
}

func (b airsimBackend) CallAction(ctx context.Context, identity airsimgw.Identity, callID, action, digits, requestID string) error {
	switch action {
	case "answer", "reject", "hangup", "dtmf":
	default:
		return airsimgw.ErrNotFound
	}
	_, err := b.do(ctx, identity, http.MethodPost, "/api/v1/calls/"+callID+"/"+action, callActionRequest{
		RequestID: requestID,
		Digits:    digits,
	})
	return err
}

func (b airsimBackend) KeepCall(ctx context.Context, identity airsimgw.Identity, callID string) error {
	if _, err := b.airsimPrincipal(ctx, identity); err != nil {
		return err
	}
	if b.api.callLeases == nil {
		return airsimgw.ErrUnavailable
	}
	_, err := b.api.callLeases.Renew(ctx, callID, b.holder(identity))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, calllease.ErrCallNotFound), errors.Is(err, calllease.ErrCallNotActive):
		return airsimgw.ErrNotFound
	case errors.Is(err, calllease.ErrNotOwner), errors.Is(err, calllease.ErrCallOwned):
		return airsimgw.ErrConflict
	default:
		return fmt.Errorf("%w: %v", airsimgw.ErrUnavailable, err)
	}
}

func (b airsimBackend) Messages(ctx context.Context, identity airsimgw.Identity, since time.Time, limit int) ([]airsimgw.Message, error) {
	ctx, err := b.context(ctx, identity)
	if err != nil {
		return nil, err
	}
	messages, err := b.api.repository.Messages(ctx, store.MessageQuery{LineID: identity.LineID, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", airsimgw.ErrUnavailable, err)
	}
	result := make([]airsimgw.Message, 0, len(messages))
	for _, message := range messages {
		timestamp := airsimTime(firstNonEmptyString(message.Timestamp, message.CreatedAt))
		if timestamp.Before(since) {
			continue
		}
		result = append(result, airsimgw.Message{
			DeliveryID: "nassimhub-sms-" + strconv.FormatInt(message.ID, 10),
			Sender:     firstNonEmptyString(message.Peer, message.Sender),
			Content:    message.Content,
			Outgoing:   message.Direction == "outgoing" || message.Direction == "outbound",
			Timestamp:  timestamp,
		})
	}
	return result, nil
}

func (b airsimBackend) SendMessage(ctx context.Context, identity airsimgw.Identity, to, content, requestID string) error {
	_, err := b.do(ctx, identity, http.MethodPost, "/api/v1/messages", sendMessageRequest{
		RequestID: requestID,
		LineID:    identity.LineID,
		To:        to,
		Content:   content,
	})
	return err
}

type airsimPCMAttacher interface {
	AttachPCM(context.Context, string, string) (*callmedia.PCMLink, error)
}

func (b airsimBackend) AttachPCM(ctx context.Context, identity airsimgw.Identity, callID string) (airsimgw.PCMStream, error) {
	if _, err := b.airsimPrincipal(ctx, identity); err != nil {
		return nil, err
	}
	attacher, ok := b.api.callMedia.(airsimPCMAttacher)
	if !ok || b.api.callLeases == nil {
		return nil, airsimgw.ErrUnavailable
	}
	// Only the iPhone that dialled or answered the call may take its audio.
	if err := b.api.callLeases.Require(ctx, callID, b.holder(identity)); err != nil {
		return nil, airsimgw.ErrConflict
	}
	link, err := attacher.AttachPCM(ctx, callID, airsimOwnerToken(identity.PairingID))
	switch {
	case err == nil:
		return airsimPCMStream{link: link}, nil
	case errors.Is(err, mediaapp.ErrConflict):
		return nil, &airsimgw.BackendError{Kind: airsimgw.ErrConflict, Message: "media_in_use"}
	case errors.Is(err, mediaapp.ErrNotFound):
		return nil, airsimgw.ErrNotFound
	default:
		return nil, fmt.Errorf("%w: %v", airsimgw.ErrUnavailable, err)
	}
}

type airsimPCMStream struct {
	link *callmedia.PCMLink
}

func (s airsimPCMStream) SampleRate() int { return s.link.Format().SampleRate }
func (s airsimPCMStream) FrameBytes() int { return s.link.Format().FrameBytes() }
func (s airsimPCMStream) ReadDownlink(ctx context.Context) ([]byte, error) {
	return s.link.ReadDownlink(ctx)
}
func (s airsimPCMStream) WriteUplink(_ context.Context, frame []byte) error {
	queued, err := s.link.WriteUplink(frame)
	if err == nil && !queued {
		return airsimgw.ErrUplinkDropped
	}
	return err
}
func (s airsimPCMStream) Close()                { s.link.Close() }
func (s airsimPCMStream) Done() <-chan struct{} { return s.link.Done() }

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// --- administrator settings ---------------------------------------------

type airsimSettingsRequest struct {
	Enabled bool   `json:"enabled"`
	LineID  string `json:"line_id"`
}

type airsimRevokeRequest struct {
	ID string `json:"id"`
}

type airsimSettingsResponse struct {
	Available bool             `json:"available"`
	Status    *airsimgw.Status `json:"status,omitempty"`
}

func (api *API) airsimUnavailable(response http.ResponseWriter) bool {
	if api.airsim != nil {
		return false
	}
	writeError(response, http.StatusServiceUnavailable, "airsim_unavailable",
		"AirSIM compatibility is not configured on this gateway", "")
	return true
}

func (api *API) airsimSettings(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if api.airsim == nil {
			writeJSON(response, http.StatusOK, airsimSettingsResponse{Available: false})
			return
		}
		status := api.airsim.Status()
		writeJSON(response, http.StatusOK, airsimSettingsResponse{Available: true, Status: &status})
	case http.MethodPut:
		if api.airsimUnavailable(response) {
			return
		}
		var input airsimSettingsRequest
		if !decodeJSONBody(response, request, &input) {
			return
		}
		input.LineID = strings.TrimSpace(input.LineID)
		if input.Enabled {
			if input.LineID == "" {
				writeError(response, http.StatusBadRequest, "line_required", "Choose the line the iPhone will use", "line_id")
				return
			}
			if !api.requireLineAccess(response, request, input.LineID) {
				return
			}
		}
		if err := api.airsim.Configure(input.Enabled, input.LineID); err != nil {
			api.writeInternalError(response, request, "save AirSIM settings", err)
			return
		}
		status := api.airsim.Status()
		writeJSON(response, http.StatusOK, airsimSettingsResponse{Available: true, Status: &status})
	default:
		response.Header().Set("Allow", "GET, PUT")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "Only GET and PUT are supported", "")
	}
}

func (api *API) airsimPairing(response http.ResponseWriter, request *http.Request) {
	if api.airsimUnavailable(response) {
		return
	}
	switch request.Method {
	case http.MethodPost:
		principal, ok := auth.PrincipalFromContext(request.Context())
		if !ok || principal.UserID == "" {
			// A paired phone must belong to a real account so that disabling
			// the account ends it; development mode has none.
			writeError(response, http.StatusConflict, "account_required", "Sign in with an administrator account to pair an iPhone", "")
			return
		}
		lineID := api.airsim.Status().LineID
		if lineID != "" && !principal.CanAccessLine(lineID) {
			writeError(response, http.StatusForbidden, "line_not_assigned",
				"Your account is not assigned the line AirSIM uses", "line_id")
			return
		}
		window, err := api.airsim.StartPairing(principal.UserID)
		switch {
		case errors.Is(err, airsimgw.ErrDisabled):
			writeError(response, http.StatusConflict, "airsim_disabled", "Turn on AirSIM compatibility first", "")
			return
		case errors.Is(err, airsimgw.ErrNoLine):
			writeError(response, http.StatusConflict, "line_required", "Choose the line the iPhone will use", "line_id")
			return
		case errors.Is(err, airsimgw.ErrNotListening):
			writeError(response, http.StatusServiceUnavailable, "airsim_not_listening", "The pairing listener is not configured", "")
			return
		case err != nil:
			api.writeInternalError(response, request, "open AirSIM pairing window", err)
			return
		}
		writeJSON(response, http.StatusOK, window)
	case http.MethodDelete:
		api.airsim.CancelPairing()
		response.WriteHeader(http.StatusNoContent)
	default:
		response.Header().Set("Allow", "POST, DELETE")
		writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST and DELETE are supported", "")
	}
}

func (api *API) airsimRevoke(response http.ResponseWriter, request *http.Request) {
	if api.airsimUnavailable(response) {
		return
	}
	var input airsimRevokeRequest
	if !decodeJSONBody(response, request, &input) {
		return
	}
	pairingID := strings.TrimSpace(input.ID)
	if err := api.airsim.Revoke(pairingID); errors.Is(err, airsimgw.ErrPairingNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "That iPhone is not paired", "id")
		return
	} else if err != nil {
		api.writeInternalError(response, request, "revoke AirSIM pairing", err)
		return
	}
	// The iPhone loses the call it was holding along with its pairing.
	if api.callLeases != nil {
		if ended, err := api.callLeases.RevokeHolder(callLeaseHolderForScope(airsimLeaseScope(pairingID))); err == nil && len(ended) > 0 {
			api.publishLiveState()
		}
	}
	response.WriteHeader(http.StatusNoContent)
}
