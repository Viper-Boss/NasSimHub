package httpapi

import (
	"context"
	"errors"
	"github.com/human-agent65535/modemdeck/internal/store"
	"net/http"
	"strings"
)

type ownNumberWriter interface {
	UpdateOwnNumber(context.Context, string, string, string) (string, error)
}

func ownNumberResourceID(path string) (string, bool) {
	if !strings.HasSuffix(path, "/own-number") {
		return "", false
	}
	return lineLabelResourceID(strings.TrimSuffix(path, "/own-number") + "/label")
}

func (api *API) ownNumberResource(w http.ResponseWriter, r *http.Request, lineID string) {
	if r.Method != http.MethodPatch {
		w.Header().Set("Allow", http.MethodPatch)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only PATCH is supported", "")
		return
	}
	if !api.requireAdmin(w, r) || !api.requireLineAccess(w, r, lineID) {
		return
	}
	var input struct {
		Number *string `json:"phone_number"`
		ICCID  string  `json:"expected_iccid"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	if input.Number == nil {
		writeError(w, http.StatusBadRequest, "invalid_argument", "phone_number is required; empty clears the manual number", "phone_number")
		return
	}
	writer, ok := api.repository.(ownNumberWriter)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "integration_unavailable", "Number settings unavailable", "")
		return
	}
	number, err := writer.UpdateOwnNumber(r.Context(), lineID, input.ICCID, *input.Number)
	switch {
	case errors.Is(err, store.ErrOwnNumberSIMChanged):
		writeError(w, http.StatusConflict, "sim_changed", "SIM changed or detached; refresh before saving", "expected_iccid")
		return
	case errors.Is(err, store.ErrOwnNumberInvalid):
		writeError(w, http.StatusBadRequest, "invalid_argument", "Invalid or already assigned subscriber number", "phone_number")
		return
	case errors.Is(err, store.ErrOwnNumberForbidden):
		writeError(w, http.StatusForbidden, "admin_required", "Administrator access is required", "")
		return
	case err != nil:
		api.writeInternalError(w, r, "save own number", err)
		return
	}
	api.publishDurableChange()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"phone_number": number, "source": "manual"})
}
