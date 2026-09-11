package agentclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WireGuard runs in the agent because this process drops every capability and
// cannot create a network interface. These calls carry configuration down and
// status back; no private key travels in either direction.

// WireGuardState mirrors the agent's honest state set. "up" is the only value
// that means traffic can actually flow.
type WireGuardState string

const (
	WireGuardStateUnsupported  WireGuardState = "unsupported"
	WireGuardStateToolsMissing WireGuardState = "tools_missing"
	WireGuardStateDisabled     WireGuardState = "disabled"
	WireGuardStateDown         WireGuardState = "down"
	WireGuardStateUp           WireGuardState = "up"
	WireGuardStateError        WireGuardState = "error"
)

type WireGuardPeerConfig struct {
	Name                string   `json:"name"`
	PublicKey           string   `json:"public_key"`
	Endpoint            string   `json:"endpoint,omitempty"`
	AllowedIPs          []string `json:"allowed_ips"`
	PersistentKeepalive int      `json:"persistent_keepalive,omitempty"`
}

type WireGuardConfig struct {
	InterfaceName string                `json:"interface_name"`
	Address       string                `json:"address"`
	ListenPort    int                   `json:"listen_port"`
	MTU           int                   `json:"mtu,omitempty"`
	Peers         []WireGuardPeerConfig `json:"peers"`
	Enabled       bool                  `json:"enabled"`
	Revision      int64                 `json:"revision"`
}

type WireGuardPeerStatus struct {
	Name            string `json:"name"`
	PublicKey       string `json:"public_key"`
	Endpoint        string `json:"endpoint,omitempty"`
	LastHandshake   int64  `json:"last_handshake_unix,omitempty"`
	TransferRxBytes int64  `json:"transfer_rx_bytes"`
	TransferTxBytes int64  `json:"transfer_tx_bytes"`
	Current         bool   `json:"current"`
}

type WireGuardStatus struct {
	State         WireGuardState        `json:"state"`
	InterfaceName string                `json:"interface_name,omitempty"`
	PublicKey     string                `json:"public_key,omitempty"`
	ListenPort    int                   `json:"listen_port,omitempty"`
	Peers         []WireGuardPeerStatus `json:"peers,omitempty"`
	Error         string                `json:"error,omitempty"`
	Revision      int64                 `json:"revision"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

// ApplyWireGuard pushes the desired configuration and returns what the host is
// actually doing afterwards.
func (client *Client) ApplyWireGuard(ctx context.Context, config WireGuardConfig) (WireGuardStatus, error) {
	var status WireGuardStatus
	if err := client.doJSON(ctx, http.MethodPut, "/v1/wireguard", config, http.StatusOK, &status); err != nil {
		return WireGuardStatus{}, err
	}
	return status, nil
}

// WireGuardStatusFor reads the live interface without changing it.
func (client *Client) WireGuardStatusFor(
	ctx context.Context,
	interfaceName string,
	enabled bool,
) (WireGuardStatus, error) {
	query := url.Values{}
	query.Set("interface", interfaceName)
	if enabled {
		query.Set("enabled", "true")
	}
	var status WireGuardStatus
	path := "/v1/wireguard?" + query.Encode()
	if err := client.doJSON(ctx, http.MethodGet, path, nil, http.StatusOK, &status); err != nil {
		return WireGuardStatus{}, err
	}
	return status, nil
}

// SetWireGuardPeerEndpoint retargets one peer, which is what an endpoint
// renegotiation does when a peer moves between networks.
func (client *Client) SetWireGuardPeerEndpoint(
	ctx context.Context,
	interfaceName string,
	publicKey string,
	endpoint string,
) error {
	if strings.TrimSpace(publicKey) == "" {
		return fmt.Errorf("peer public key is required")
	}
	input := map[string]string{
		"interface_name": interfaceName,
		"public_key":     publicKey,
		"endpoint":       endpoint,
	}
	return client.doJSON(ctx, http.MethodPost, "/v1/wireguard/peer-endpoint", input, http.StatusOK, nil)
}
