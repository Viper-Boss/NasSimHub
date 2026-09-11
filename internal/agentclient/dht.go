package agentclient

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// The libp2p host runs in the agent, for the same reason WireGuard does: this
// process has no capabilities and cannot bind host sockets. Only the public
// PeerID crosses this boundary — never the identity key.

type DHTState string

const (
	DHTStateDisabled DHTState = "disabled"
	DHTStateStarting DHTState = "starting"
	// DHTStateIsolated means the host runs but has found nobody. A fresh
	// install with no entry point genuinely cannot join.
	DHTStateIsolated DHTState = "isolated"
	DHTStateReady    DHTState = "ready"
	DHTStateError    DHTState = "error"
)

type DHTConfig struct {
	Enabled        bool     `json:"enabled"`
	ListenPort     int      `json:"listen_port"`
	BootstrapPeers []string `json:"bootstrap_peers"`
	// AuthorizedPeers is the connection allowlist. Empty accepts any peer on
	// this project's protocol; one entry makes the gate strict.
	AuthorizedPeers []string `json:"authorized_peers"`
	// DisableLANDiscovery turns off mDNS. On by default: it is the only
	// bootstrap source needing no configuration, so two instances on one
	// network find each other without anyone exchanging an address.
	DisableLANDiscovery bool `json:"disable_lan_discovery"`
}

type DHTStatus struct {
	State               DHTState `json:"state"`
	PeerID              string   `json:"peer_id,omitempty"`
	Addresses           []string `json:"addresses,omitempty"`
	ConnectedPeers      int      `json:"connected_peers"`
	RoutingPeers        int      `json:"routing_peers"`
	BootstrapConfigured bool     `json:"bootstrap_configured"`
	// LANDiscovery and LANPeers explain a lot about why a node did or did not
	// find anybody, which is otherwise invisible.
	LANDiscovery bool      `json:"lan_discovery"`
	LANPeers     int       `json:"lan_peers"`
	Error        string    `json:"error,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (client *Client) ApplyDHT(ctx context.Context, config DHTConfig) (DHTStatus, error) {
	var status DHTStatus
	if err := client.doJSON(ctx, http.MethodPut, "/v1/dht", config, http.StatusOK, &status); err != nil {
		return DHTStatus{}, err
	}
	return status, nil
}

func (client *Client) DHTStatus(ctx context.Context, enabled bool) (DHTStatus, error) {
	query := url.Values{}
	if enabled {
		query.Set("enabled", "true")
	}
	var status DHTStatus
	if err := client.doJSON(ctx, http.MethodGet, "/v1/dht?"+query.Encode(), nil, http.StatusOK, &status); err != nil {
		return DHTStatus{}, err
	}
	return status, nil
}

// DHTPeerID reads the identity without starting the node, so it can be shown
// and shared before the feature is switched on.
func (client *Client) DHTPeerID(ctx context.Context) (string, error) {
	var payload struct {
		PeerID string `json:"peer_id"`
	}
	if err := client.doJSON(ctx, http.MethodGet, "/v1/dht/identity", nil, http.StatusOK, &payload); err != nil {
		return "", err
	}
	return payload.PeerID, nil
}

// DialDHT asks the agent to connect to peers learned out of band, such as
// addresses a paired instance announced over NKN.
func (client *Client) DialDHT(ctx context.Context, addresses []string) (int, error) {
	request := struct {
		Addresses []string `json:"addresses"`
	}{Addresses: addresses}
	var result struct {
		Dialled int `json:"dialled"`
	}
	if err := client.doJSON(ctx, http.MethodPost, "/v1/dht/dial", request, http.StatusOK, &result); err != nil {
		return 0, err
	}
	return result.Dialled, nil
}
