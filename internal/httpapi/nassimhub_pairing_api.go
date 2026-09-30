package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
)

// NasSimHubNodeService is the Node fleet as the administration API sees it.
// Every route under /api/v1/nodes is administrator-only (adminOnlyAPIPath).
type NasSimHubNodeService interface {
	CoreID() string
	Nodes() []nassimhubnode.NodeView
	Discover(context.Context) ([]nassimhubnode.Candidate, error)
	Pair(context.Context, nassimhubnode.PairRequest) (nassimhubnode.NodeView, error)
	Unpair(context.Context, string) (bool, error)
	Refresh(context.Context, string) (nassimhubnode.NodeView, error)
}

const nodeOperationTimeout = 30 * time.Second

func (api *API) nassimhubFleetNodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		CoreID string                   `json:"core_id"`
		Nodes  []nassimhubnode.NodeView `json:"nodes"`
	}{CoreID: api.nodeFleet.CoreID(), Nodes: api.nodeFleet.Nodes()})
}

func (api *API) nassimhubFleetDiscover(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), nodeOperationTimeout)
	defer cancel()
	candidates, err := api.nodeFleet.Discover(ctx)
	if err != nil {
		api.writeInternalError(w, r, "discover NasSimHub nodes", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Candidates []nassimhubnode.Candidate `json:"candidates"`
	}{Candidates: candidates})
}

type nodePairBody struct {
	Ticket      string `json:"ticket"`
	DeviceID    string `json:"device_id"`
	Fingerprint string `json:"fingerprint"`
	DisplayName string `json:"display_name"`
}

func (api *API) nassimhubPair(w http.ResponseWriter, r *http.Request) {
	if api.nodeFleet == nil {
		writeError(w, http.StatusServiceUnavailable, "nodes_unavailable", "Node pairing is unavailable", "")
		return
	}
	var body nodePairBody
	if !decodeJSONBodyWithLimit(w, r, &body, 4<<10) {
		return
	}
	if strings.TrimSpace(body.Ticket) == "" || strings.TrimSpace(body.DeviceID) == "" ||
		strings.TrimSpace(body.Fingerprint) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "ticket, device_id and fingerprint are required", "")
		return
	}
	if len([]rune(body.DisplayName)) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_request", "display_name is too long", "display_name")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), nodeOperationTimeout)
	defer cancel()
	view, err := api.nodeFleet.Pair(ctx, nassimhubnode.PairRequest{
		Ticket:      body.Ticket,
		DeviceID:    body.DeviceID,
		Fingerprint: body.Fingerprint,
		DisplayName: body.DisplayName,
	})
	if err != nil {
		writeNodeError(api, w, r, "pair NasSimHub node", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Node nassimhubnode.NodeView `json:"node"`
	}{Node: view})
}

type nodeIDBody struct {
	DeviceID string `json:"device_id"`
}

func (api *API) nassimhubUnpair(w http.ResponseWriter, r *http.Request) {
	if api.nodeFleet == nil {
		writeError(w, http.StatusServiceUnavailable, "nodes_unavailable", "Node pairing is unavailable", "")
		return
	}
	var body nodeIDBody
	if !decodeJSONBodyWithLimit(w, r, &body, 1<<10) {
		return
	}
	if strings.TrimSpace(body.DeviceID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "device_id is required", "device_id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), nodeOperationTimeout)
	defer cancel()
	released, err := api.nodeFleet.Unpair(ctx, body.DeviceID)
	if err != nil {
		writeNodeError(api, w, r, "unpair NasSimHub node", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		DeviceID         string `json:"device_id"`
		ReleasedOnDevice bool   `json:"released_on_device"`
	}{DeviceID: strings.TrimSpace(body.DeviceID), ReleasedOnDevice: released})
}

func (api *API) nassimhubRefresh(w http.ResponseWriter, r *http.Request) {
	if api.nodeFleet == nil {
		writeError(w, http.StatusServiceUnavailable, "nodes_unavailable", "Node pairing is unavailable", "")
		return
	}
	var body nodeIDBody
	if !decodeJSONBodyWithLimit(w, r, &body, 1<<10) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), nodeOperationTimeout)
	defer cancel()
	view, err := api.nodeFleet.Refresh(ctx, body.DeviceID)
	if err != nil {
		writeNodeError(api, w, r, "refresh NasSimHub node", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Node nassimhubnode.NodeView `json:"node"`
	}{Node: view})
}

// writeNodeError maps fleet errors to stable codes the page can explain.
func writeNodeError(api *API, w http.ResponseWriter, r *http.Request, operation string, err error) {
	type mapping struct {
		target  error
		status  int
		code    string
		message string
	}
	for _, candidate := range []mapping{
		{nassimhubnode.ErrTicketInvalid, http.StatusConflict, "pairing_expired", "The confirmation expired or was already used. Search for the device again."},
		{nassimhubnode.ErrFingerprintMismatch, http.StatusConflict, "fingerprint_mismatch", "The device fingerprint does not match. Do not pair; search again."},
		{nassimhubnode.ErrAlreadyPaired, http.StatusConflict, "already_paired", "This device is already paired with this NAS."},
		{nassimhubnode.ErrKeyChanged, http.StatusConflict, "key_changed", "A device with this ID was paired before with a different key. Remove the old record first."},
		{nassimhubnode.ErrOwnedElsewhere, http.StatusConflict, "owned_elsewhere", "The device is paired with another NAS. Release it there first."},
		{nassimhubnode.ErrIncompatible, http.StatusConflict, "incompatible", "The device software is not compatible with this NAS version."},
		{nassimhubnode.ErrPairingNotConfirmed, http.StatusBadGateway, "not_confirmed", "The device did not confirm the pairing."},
		{nassimhubnode.ErrNodeUnavailable, http.StatusBadGateway, "node_unreachable", "The device is not reachable."},
		{nassimhubnode.ErrUnknownNode, http.StatusNotFound, "node_not_found", "The device is not paired with this NAS."},
	} {
		if errors.Is(err, candidate.target) {
			writeError(w, candidate.status, candidate.code, candidate.message, "")
			return
		}
	}
	if strings.Contains(err.Error(), "not the device that was discovered") {
		writeError(w, http.StatusConflict, "identity_changed", "The device answering now is not the one that was discovered. Do not pair.", "")
		return
	}
	api.writeInternalError(w, r, operation, err)
}
