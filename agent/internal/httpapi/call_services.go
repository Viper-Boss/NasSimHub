package httpapi

import (
	"net/http"
	"strings"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// Supplementary services over HTTP.
//
// Reads are a GET and changes are a PUT, which is the right shape even though
// a change here is not idempotent in the usual sense: the same PUT twice
// leaves the network in the same state, which is what PUT promises. There is
// deliberately no POST "toggle" -- a toggle sends the caller's belief about
// the current state, and with a service that a handset can change from
// somewhere else that belief is regularly wrong.

type callServicesResponse struct {
	Services domain.CallServices `json:"call_services"`
}

func (h *handler) getCallServices(response http.ResponseWriter, request *http.Request) {
	if h.callServices == nil {
		h.writeAPIError(
			response, http.StatusNotImplemented, domain.ErrorNotSupported,
			"read_call_services", "", "call forwarding and call waiting are unavailable on this build",
		)
		return
	}
	services, err := h.callServices.CallServicesStatus(
		request.Context(),
		strings.TrimSpace(request.PathValue("id")),
	)
	if err != nil {
		h.writeError(response, err, "")
		return
	}
	h.writeJSON(response, http.StatusOK, callServicesResponse{Services: services})
}

func (h *handler) putCallForwarding(response http.ResponseWriter, request *http.Request) {
	if h.callServices == nil {
		h.writeAPIError(
			response, http.StatusNotImplemented, domain.ErrorNotSupported,
			"set_call_forwarding", "", "call forwarding is unavailable on this build",
		)
		return
	}
	var input domain.SetCallForwardingRequest
	if err := decodeJSON(response, request, &input); err != nil {
		h.writeAPIError(
			response, http.StatusBadRequest, domain.ErrorInvalidArgument,
			"set_call_forwarding", "", "invalid JSON request",
		)
		return
	}
	input.LineID = strings.TrimSpace(request.PathValue("id"))
	services, err := h.callServices.SetCallForwarding(request.Context(), input)
	if err != nil {
		h.writeError(response, err, "")
		return
	}
	h.writeJSON(response, http.StatusOK, callServicesResponse{Services: services})
}

func (h *handler) putCallWaiting(response http.ResponseWriter, request *http.Request) {
	if h.callServices == nil {
		h.writeAPIError(
			response, http.StatusNotImplemented, domain.ErrorNotSupported,
			"set_call_waiting", "", "call waiting is unavailable on this build",
		)
		return
	}
	var input domain.SetCallWaitingRequest
	if err := decodeJSON(response, request, &input); err != nil {
		h.writeAPIError(
			response, http.StatusBadRequest, domain.ErrorInvalidArgument,
			"set_call_waiting", "", "invalid JSON request",
		)
		return
	}
	input.LineID = strings.TrimSpace(request.PathValue("id"))
	services, err := h.callServices.SetCallWaiting(request.Context(), input)
	if err != nil {
		h.writeError(response, err, "")
		return
	}
	h.writeJSON(response, http.StatusOK, callServicesResponse{Services: services})
}
