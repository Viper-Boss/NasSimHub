package httpapi

import (
	"net/http"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

func (h *handler) getSystemMetrics(w http.ResponseWriter, r *http.Request) {
	if h.systemMetrics == nil {
		h.writeAPIError(
			w,
			http.StatusNotImplemented,
			domain.ErrorNotSupported,
			"system_metrics",
			"",
			"provider does not expose host/modem system metrics",
		)
		return
	}
	metrics, err := h.systemMetrics.SystemMetrics(r.Context())
	if err != nil {
		h.writeError(w, err, "")
		return
	}
	h.writeJSON(w, http.StatusOK, metrics)
}
