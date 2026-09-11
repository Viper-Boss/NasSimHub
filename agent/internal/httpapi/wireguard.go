package httpapi

import (
	"net/http"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
	"github.com/human-agent65535/modemdeck/agent/internal/wireguard"
)

// WireGuard lives in the agent because the api container drops every capability
// and cannot create a network interface. The api sends the desired
// configuration here and gets back what the host is actually doing.
//
// Neither direction ever carries a private key: the key is generated and kept
// on this machine, and only the public key travels.

func (h *handler) putWireGuard(w http.ResponseWriter, r *http.Request) {
	if h.wireGuard == nil {
		h.writeAPIError(w, http.StatusServiceUnavailable, domain.ErrorUnavailable, "wireguard", "", "WireGuard manager is not configured")
		return
	}
	var config wireguard.Config
	if err := decodeJSON(w, r, &config); err != nil {
		h.writeAPIError(w, http.StatusBadRequest, domain.ErrorInvalidArgument, "wireguard", "", err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, h.wireGuard.Apply(r.Context(), config))
}

func (h *handler) getWireGuard(w http.ResponseWriter, r *http.Request) {
	if h.wireGuard == nil {
		h.writeAPIError(w, http.StatusServiceUnavailable, domain.ErrorUnavailable, "wireguard", "", "WireGuard manager is not configured")
		return
	}
	// The caller passes the configuration it believes is current so peer names
	// and the enabled flag can be reflected without the agent keeping a second
	// copy of the settings that could drift from the api's.
	var config wireguard.Config
	if r.URL.Query().Get("enabled") == "true" {
		config.Enabled = true
	}
	config.InterfaceName = r.URL.Query().Get("interface")
	h.writeJSON(w, http.StatusOK, h.wireGuard.Status(r.Context(), config))
}

func (h *handler) postWireGuardEndpoint(w http.ResponseWriter, r *http.Request) {
	if h.wireGuard == nil {
		h.writeAPIError(w, http.StatusServiceUnavailable, domain.ErrorUnavailable, "wireguard", "", "WireGuard manager is not configured")
		return
	}
	var input struct {
		InterfaceName string `json:"interface_name"`
		PublicKey     string `json:"public_key"`
		Endpoint      string `json:"endpoint"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		h.writeAPIError(w, http.StatusBadRequest, domain.ErrorInvalidArgument, "wireguard", "", err.Error())
		return
	}
	if err := h.wireGuard.SetPeerEndpoint(r.Context(), input.InterfaceName, input.PublicKey, input.Endpoint); err != nil {
		h.writeAPIError(w, http.StatusBadRequest, domain.ErrorInvalidArgument, "wireguard", "", err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
