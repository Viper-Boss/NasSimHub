package httpapi

import (
	"net/http"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

func (h *handler) getVowifiDiagnostics(w http.ResponseWriter, r *http.Request) {
	if h.vowifiDiagnostics == nil {
		h.writeAPIError(
			w,
			http.StatusNotImplemented,
			domain.ErrorNotSupported,
			"vowifi_diagnostics",
			"",
			"provider does not expose VoWiFi reachability diagnostics",
		)
		return
	}
	diagnostics, err := h.vowifiDiagnostics.VowifiDiagnostics(r.Context())
	if err != nil {
		h.writeError(w, err, "")
		return
	}
	h.writeJSON(w, http.StatusOK, diagnostics)
}
