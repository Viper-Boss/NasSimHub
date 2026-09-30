package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/human-agent65535/modemdeck/internal/store"
)

const usbNodeAddress = "10.55.0.2:7580"

type nasSimHubCandidate struct {
	Source           string `json:"source"`
	BaseURL          string `json:"base_url"`
	Verified         bool   `json:"verified"`
	IdentityVerified bool   `json:"identity_verified"`
	DeviceID         string `json:"device_id,omitempty"`
}

// The USB address is fixed by the firmware interface. A successful TCP probe
// establishes reachability only; it must never be interpreted as identity or
// authorization to pair. No client-supplied destination is accepted here.
func discoverNasSimHubUSB(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error)) ([]nasSimHubCandidate, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()
	connection, err := dial(probeCtx, "tcp", usbNodeAddress)
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return []nasSimHubCandidate{}, nil
	}
	_ = connection.Close()
	return []nasSimHubCandidate{{Source: "usb", BaseURL: "https://" + usbNodeAddress, Verified: false}}, nil
}

func (api *API) nassimhubDiscover(w http.ResponseWriter, r *http.Request) {
	if api.nodeFleet != nil {
		api.nassimhubFleetDiscover(w, r)
		return
	}
	var dialer net.Dialer
	candidates, err := discoverNasSimHubUSB(r.Context(), dialer.DialContext)
	if err != nil {
		api.writeInternalError(w, r, "discover NasSimHub USB candidate", err)
		return
	}
	for index := range candidates {
		id, verifyErr := identifyNasSimHubCandidate(r.Context(), candidates[index].BaseURL)
		if verifyErr == nil {
			candidates[index].IdentityVerified = true
			candidates[index].DeviceID = id
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Candidates []nasSimHubCandidate `json:"candidates"`
	}{Candidates: candidates})
}

type nassimhubNodeLister interface {
	NasSimHubNodes(context.Context) ([]store.NasSimHubNodeSummary, error)
}

func (api *API) nassimhubNodes(w http.ResponseWriter, r *http.Request) {
	if api.nodeFleet != nil {
		api.nassimhubFleetNodes(w, r)
		return
	}
	lister, ok := api.repository.(nassimhubNodeLister)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "nodes_unavailable", "Node inventory is unavailable", "")
		return
	}
	nodes, err := lister.NasSimHubNodes(r.Context())
	if err != nil {
		api.writeInternalError(w, r, "list NasSimHub nodes", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Nodes []store.NasSimHubNodeSummary `json:"nodes"`
	}{Nodes: nodes})
}
