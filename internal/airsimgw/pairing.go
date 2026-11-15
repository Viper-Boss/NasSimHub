package airsimgw

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/airsimproto"
)

type pairingInfo struct {
	Product         string `json:"product"`
	Version         int    `json:"version"`
	SessionID       string `json:"session_id"`
	ServerPublicKey string `json:"server_public_key"`
	ExpiresAt       int64  `json:"expires_at"`
	ServerTime      int64  `json:"server_time"`
}

type pairingClaim struct {
	Version            int    `json:"version"`
	SessionID          string `json:"session_id"`
	ClientPublicKey    string `json:"client_public_key"`
	SealedRegistration string `json:"sealed_registration"`
	WantDirectRoute    bool   `json:"want_direct_route"`
}

// pairingRegistration is the part of the app's AgentPushRegistration the
// gateway uses. The push tokens and relay settings are for AirSIM's cloud
// relay, which NasSimHub does not run, and are not kept.
type pairingRegistration struct {
	VoWLANSecret string `json:"vowlan_secret"`
	DeviceID     string `json:"device_id"`
}

type pairingResult struct {
	Configured   bool   `json:"configured"`
	DeviceIDHint string `json:"device_id_hint,omitempty"`
	AgentID      string `json:"agent_id"`
	ControlPort  int    `json:"control_port"`
	PCMPort      int    `json:"pcm_port"`
	RouteProof   string `json:"route_proof"`
}

// servePairing answers the app's manual pairing exchange. The app parses
// the response itself: exactly one Content-Length, no Transfer-Encoding, and
// the connection closed after the body.
func (g *Gateway) servePairing(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Connection", "close")
	if _, ok := g.peerAllowed(request.RemoteAddr); !ok {
		writeFixedJSON(response, http.StatusForbidden, map[string]string{"error": "private_network_required"})
		return
	}
	g.mu.Lock()
	window := g.openWindowLocked()
	g.mu.Unlock()
	if window == nil {
		// Also when no window was ever opened: the app then tells the user
		// the pairing window expired and to start again, which is the fix.
		writeFixedJSON(response, http.StatusGone, map[string]string{"error": "pairing_expired"})
		return
	}
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/pair/v1/info":
		writeFixedJSON(response, http.StatusOK, pairingInfo{
			Product:         "airsim-pair",
			Version:         1,
			SessionID:       window.session.ID,
			ServerPublicKey: airsimproto.Base64URL(window.session.PublicKey()),
			ExpiresAt:       window.session.ExpiresAt.UnixMilli(),
			ServerTime:      g.now().UnixMilli(),
		})
	case request.Method == http.MethodPost && request.URL.Path == "/pair/v1/claim":
		g.claim(response, request, window)
	default:
		writeFixedJSON(response, http.StatusNotFound, map[string]string{"error": "not_found"})
	}
}

func (g *Gateway) claim(response http.ResponseWriter, request *http.Request, window *pairingWindow) {
	body, err := io.ReadAll(io.LimitReader(request.Body, 64<<10))
	if err != nil {
		writeFixedJSON(response, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	var claim pairingClaim
	if err := json.Unmarshal(body, &claim); err != nil || claim.Version != 1 || claim.SessionID != window.session.ID {
		writeFixedJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid_session"})
		return
	}
	clientKey, keyErr := airsimproto.DecodeBase64URL(claim.ClientPublicKey)
	sealed, sealedErr := airsimproto.DecodeBase64URL(claim.SealedRegistration)
	if keyErr != nil || sealedErr != nil {
		writeFixedJSON(response, http.StatusUnauthorized, map[string]string{"error": "pairing_auth_failed"})
		return
	}
	plaintext, err := window.session.Open(clientKey, sealed, g.now())
	switch {
	case errors.Is(err, airsimproto.ErrPairingExpired), errors.Is(err, airsimproto.ErrPairingUsed):
		writeFixedJSON(response, http.StatusGone, map[string]string{"error": "pairing_expired"})
		return
	case err != nil:
		g.logger.Info("AirSIM pairing claim rejected", "component", "airsim", "reason", "authentication")
		writeFixedJSON(response, http.StatusUnauthorized, map[string]string{"error": "pairing_auth_failed"})
		return
	}
	defer clear(plaintext)
	var registration pairingRegistration
	if err := json.Unmarshal(plaintext, &registration); err != nil {
		writeFixedJSON(response, http.StatusBadRequest, map[string]string{"error": "invalid_registration"})
		return
	}
	secret, err := airsimproto.DecodeBase64URL(registration.VoWLANSecret)
	if err != nil || len(secret) < airsimproto.MinSecretBytes {
		writeFixedJSON(response, http.StatusBadRequest, map[string]string{"error": "vowlan_secret_required"})
		return
	}
	defer clear(secret)
	hint := deviceHint(registration.DeviceID)
	g.mu.Lock()
	currentWindow := g.window == window && g.openWindowLocked() != nil
	g.mu.Unlock()
	if !currentWindow {
		writeFixedJSON(response, http.StatusGone, map[string]string{"error": "pairing_expired"})
		return
	}
	backend := g.currentBackend()
	if backend == nil {
		writeFixedJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "agent_unavailable"})
		return
	}
	for _, lineID := range window.lineIDs {
		if err := backend.Authorize(request.Context(), Identity{UserID: window.userID, LineID: lineID, LineIDs: window.lineIDs}); err != nil {
			writeFixedJSON(response, http.StatusForbidden, map[string]string{"error": "line_not_assigned"})
			return
		}
	}

	pairing, err := g.store.add(Pairing{
		UserID:     window.userID,
		LineIDs:    window.lineIDs,
		DeviceHint: hint,
		CreatedAt:  g.now().UTC(),
		LastSeenAt: g.now().UTC(),
		LastPeer:   remoteHost(request.RemoteAddr),
	}, secret)
	if errors.Is(err, ErrTooManyPairings) {
		writeFixedJSON(response, http.StatusConflict, map[string]string{"error": "too_many_paired_devices"})
		return
	}
	if err != nil {
		g.logger.Error("store AirSIM pairing", "component", "airsim", "error", err)
		writeFixedJSON(response, http.StatusInternalServerError, map[string]string{"error": "pairing_store_failed"})
		return
	}
	g.mu.Lock()
	if g.window == window {
		g.window = nil
	}
	paired := pairing
	g.lastPaired = &paired
	g.mu.Unlock()
	agentID := g.store.agentID()
	g.logger.Info("AirSIM iPhone paired", "component", "airsim", "pairing_id", pairing.ID)
	writeFixedJSON(response, http.StatusOK, pairingResult{
		Configured:   true,
		DeviceIDHint: hint,
		AgentID:      agentID,
		ControlPort:  g.config.ControlPort,
		PCMPort:      g.config.PCMPort,
		RouteProof: airsimproto.DirectRouteProof(secret, window.session.ID, claim.ClientPublicKey,
			agentID, g.config.ControlPort, g.config.PCMPort),
	})
}

func deviceHint(deviceID string) string {
	deviceID = strings.TrimSpace(deviceID)
	if len(deviceID) < 4 {
		return ""
	}
	hint := deviceID[len(deviceID)-4:]
	for _, character := range hint {
		if character > 0x7e || character < 0x21 {
			return ""
		}
	}
	return hint
}

func remoteHost(remote string) string {
	if index := strings.LastIndexByte(remote, ':'); index > 0 {
		return strings.Trim(remote[:index], "[]")
	}
	return remote
}

// writeFixedJSON writes a body with an explicit Content-Length, which keeps
// net/http from ever choosing chunked encoding.
func writeFixedJSON(response http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"error":"internal_error"}`)
	}
	header := response.Header()
	header.Set("Content-Type", "application/json")
	header.Set("Content-Length", strconv.Itoa(len(body)))
	header.Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_, _ = response.Write(body)
}
