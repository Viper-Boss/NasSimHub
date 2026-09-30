package httpapi

import (
	"encoding/base64"
	"net/http"

	"github.com/human-agent65535/modemdeck/agent/internal/dht"
	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// The libp2p host lives in the agent for the same reason WireGuard does: the
// api container drops every capability and cannot bind listening sockets on the
// host network. Only the public PeerID crosses this boundary.

func (h *handler) putDHT(w http.ResponseWriter, r *http.Request) {
	if h.dht == nil {
		h.writeAPIError(w, http.StatusServiceUnavailable, domain.ErrorUnavailable, "dht", "", "DHT service is not configured")
		return
	}
	var config dht.Config
	if err := decodeJSON(w, r, &config); err != nil {
		h.writeAPIError(w, http.StatusBadRequest, domain.ErrorInvalidArgument, "dht", "", err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, h.dht.Apply(r.Context(), config))
}

// postDHTDial connects to peers the api layer learned about out of band --
// addresses a paired instance sent over NKN. Apply deliberately does nothing
// while the host is already running, so this is how a late peer gets in without
// throwing away the routing table.
func (h *handler) postDHTDial(w http.ResponseWriter, r *http.Request) {
	if h.dht == nil {
		h.writeAPIError(w, http.StatusServiceUnavailable, domain.ErrorUnavailable, "dht", "", "DHT service is not configured")
		return
	}
	var request struct {
		Addresses []string `json:"addresses"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		h.writeAPIError(w, http.StatusBadRequest, domain.ErrorInvalidArgument, "dht", "", err.Error())
		return
	}
	// Bounded so a malformed or hostile caller cannot turn one request into an
	// unbounded pile of outbound dials.
	if len(request.Addresses) > 8 {
		h.writeAPIError(w, http.StatusBadRequest, domain.ErrorInvalidArgument, "dht", "addresses", "at most 8 addresses per request")
		return
	}
	dialled, err := h.dht.Dial(r.Context(), request.Addresses)
	if err != nil {
		h.writeAPIError(w, http.StatusBadGateway, domain.ErrorUnavailable, "dht", "", err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]int{"dialled": dialled})
}

func (h *handler) getDHT(w http.ResponseWriter, r *http.Request) {
	if h.dht == nil {
		h.writeAPIError(w, http.StatusServiceUnavailable, domain.ErrorUnavailable, "dht", "", "DHT service is not configured")
		return
	}
	config := dht.Config{Enabled: r.URL.Query().Get("enabled") == "true"}
	h.writeJSON(w, http.StatusOK, h.dht.Status(config))
}

// getDHTIdentity exposes the PeerID before the node is switched on, so it can be
// shown in settings and put into a pairing QR without starting the host.
func (h *handler) getDHTIdentity(w http.ResponseWriter, r *http.Request) {
	if h.dht == nil {
		h.writeAPIError(w, http.StatusServiceUnavailable, domain.ErrorUnavailable, "dht", "", "DHT service is not configured")
		return
	}
	id, err := h.dht.PeerID()
	if err != nil {
		h.writeAPIError(w, http.StatusInternalServerError, domain.ErrorInternal, "dht", "", err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"peer_id": id})
}

// putDHTRelease sets the signed release manifest this node shares with its
// DHT peers. The api sends only a document it has verified; an empty one
// stops sharing (the operator switched sharing off).
func (h *handler) putDHTRelease(w http.ResponseWriter, r *http.Request) {
	if h.dht == nil {
		h.writeAPIError(w, http.StatusServiceUnavailable, domain.ErrorUnavailable, "dht", "", "DHT service is not configured")
		return
	}
	var request struct {
		SignedBase64 string `json:"signed_base64"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		h.writeAPIError(w, http.StatusBadRequest, domain.ErrorInvalidArgument, "dht", "", err.Error())
		return
	}
	signed, err := base64.StdEncoding.DecodeString(request.SignedBase64)
	if err != nil {
		h.writeAPIError(w, http.StatusBadRequest, domain.ErrorInvalidArgument, "dht", "signed_base64", "not base64")
		return
	}
	if err := h.dht.SetRelease(signed); err != nil {
		h.writeAPIError(w, http.StatusBadRequest, domain.ErrorInvalidArgument, "dht", "signed_base64", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// postDHTReleaseFetch asks the connected peers for their signed release
// documents and returns them as they came -- unverified. The api verifies.
func (h *handler) postDHTReleaseFetch(w http.ResponseWriter, r *http.Request) {
	if h.dht == nil {
		h.writeAPIError(w, http.StatusServiceUnavailable, domain.ErrorUnavailable, "dht", "", "DHT service is not configured")
		return
	}
	releases, err := h.dht.FetchReleases(r.Context())
	if err != nil {
		h.writeAPIError(w, http.StatusServiceUnavailable, domain.ErrorUnavailable, "dht", "", err.Error())
		return
	}
	type answer struct {
		Peer         string `json:"peer"`
		SignedBase64 string `json:"signed_base64"`
	}
	answers := make([]answer, 0, len(releases))
	for _, release := range releases {
		answers = append(answers, answer{Peer: release.Peer, SignedBase64: base64.StdEncoding.EncodeToString(release.Signed)})
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"releases": answers})
}
