package httpapi

import (
	"context"
	"github.com/human-agent65535/modemdeck/internal/nassimhubnode"
	"net/http"
	"strings"
)

type localManagementService interface {
	LocalManagement(context.Context, string) (nassimhubnode.ManagementLink, error)
}

// This route inherits the administration and CSRF gates on /api/v1/nodes/.
// The one-use setup proof never belongs in inventory responses or logs.
func (api *API) nassimhubLocalManagement(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	service, ok := api.nodeFleet.(localManagementService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "admin_unavailable", "Device administration is unavailable", "")
		return
	}
	var input nodeIDBody
	if !decodeJSONBodyWithLimit(w, r, &input, 1<<10) {
		return
	}
	if strings.TrimSpace(input.DeviceID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "device_id is required", "device_id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), nodeOperationTimeout)
	defer cancel()
	result, err := service.LocalManagement(ctx, input.DeviceID)
	if err != nil {
		writeNodeError(api, w, r, "open device administration", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
