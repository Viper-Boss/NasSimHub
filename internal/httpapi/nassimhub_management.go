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
	var input struct {
		DeviceID  string `json:"device_id"`
		Transport string `json:"transport,omitempty"`
	}
	if !decodeJSONBodyWithLimit(w, r, &input, 1<<10) {
		return
	}
	if strings.TrimSpace(input.DeviceID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "device_id is required", "device_id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), nodeOperationTimeout)
	defer cancel()
	var result nassimhubnode.ManagementLink
	var err error
	if input.Transport == "usb" {
		usb, ok := api.nodeFleet.(interface {
			LocalUSBManagement(context.Context, string) (nassimhubnode.ManagementLink, error)
		})
		if !ok {
			writeError(w, 503, "admin_unavailable", "USB console is unavailable", "")
			return
		}
		result, err = usb.LocalUSBManagement(ctx, input.DeviceID)
	} else if input.Transport == "" || input.Transport == "wifi" {
		result, err = service.LocalManagement(ctx, input.DeviceID)
	} else {
		writeError(w, 400, "invalid_request", "Unknown console transport", "transport")
		return
	}
	if err != nil {
		writeNodeError(api, w, r, "open device administration", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
