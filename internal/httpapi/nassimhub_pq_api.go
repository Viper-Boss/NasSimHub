package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
)

type nodePQPinService interface {
	ResetPQPin(context.Context, string) (nassimhubnode.NodeView, error)
}

// nassimhubResetPQPin forgets the post-quantum identity key recorded for one
// Node, so the key it presents next is verified and recorded in its place.
//
// This is how an administrator accepts a deliberate key rotation. It inherits
// the administrator and CSRF gates on /api/v1/nodes/, takes only the device ID
// (never a key - the gateway records what the device proves, not what a
// request says), and does not touch the Ed25519 pin made at pairing.
func (api *API) nassimhubResetPQPin(w http.ResponseWriter, r *http.Request) {
	service, ok := api.nodeFleet.(nodePQPinService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "nodes_unavailable", "Node identity management is unavailable", "")
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
	view, err := service.ResetPQPin(ctx, body.DeviceID)
	if err != nil {
		writeNodeError(api, w, r, "reset NasSimHub node post-quantum pin", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Node nassimhubnode.NodeView `json:"node"`
	}{Node: view})
}
