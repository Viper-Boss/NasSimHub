package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
)

// The DHT is the second control path beside NKN: a way for two instances to
// find each other when NKN is unavailable or blocked. It carries discovery and
// routing only — no messages, no call audio, no keys.
//
// It runs by default, like NKN beside it: a redundant control path that ships
// switched off is not redundancy. What makes that defensible is confinement,
// not obscurity -- the node speaks only this project's protocol prefix, never
// the public IPFS DHT, and relays nothing for anyone. See agent/internal/dht.

const (
	dhtDefaultListenPort  = 4001
	dhtMaxBootstrapPeers  = 8
	dhtMaxAuthorizedPeers = 16
	dhtMaxPeerLength      = 200
)

type dhtSettings struct {
	Revision       int64    `json:"revision"`
	Enabled        bool     `json:"enabled"`
	ListenPort     int      `json:"listen_port"`
	BootstrapPeers []string `json:"bootstrap_peers"`
	// AuthorizedPeers restricts who may connect at all. Empty means any peer
	// that speaks this project's protocol, which is what a fresh node needs so
	// its partner can reach it; one entry makes the gate strict.
	AuthorizedPeers []string `json:"authorized_peers"`
	// DisableLANDiscovery turns off mDNS; it is on unless asked otherwise.
	DisableLANDiscovery bool `json:"disable_lan_discovery"`
}

type dhtStatusView struct {
	State               string    `json:"state"`
	PeerID              string    `json:"peer_id,omitempty"`
	Addresses           []string  `json:"addresses,omitempty"`
	ConnectedPeers      int       `json:"connected_peers"`
	RoutingPeers        int       `json:"routing_peers"`
	BootstrapConfigured bool      `json:"bootstrap_configured"`
	LANDiscovery        bool      `json:"lan_discovery"`
	LANPeers            int       `json:"lan_peers"`
	Error               string    `json:"error,omitempty"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (api *API) dhtDirectory() (string, error) {
	dir, ok := api.homeSIMDataDirectory()
	if !ok {
		return "", fmt.Errorf("服务器存储不可用")
	}
	return filepath.Join(dir, "dht"), nil
}

func (api *API) loadDHTSettings() (dhtSettings, error) {
	// On by default, like NKN beside it: handoff section 13 has every install
	// join the DHT, and a redundant control path that ships switched off is not
	// redundancy. It is safe to default on only because the node is confined to
	// this project's own protocol prefix and carries nothing for anyone else --
	// see the dht package for what that confinement is.
	settings := dhtSettings{
		Revision:        1,
		Enabled:         true,
		ListenPort:      dhtDefaultListenPort,
		BootstrapPeers:  []string{},
		AuthorizedPeers: []string{},
	}
	dir, err := api.dhtDirectory()
	if err != nil {
		return settings, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if os.IsNotExist(err) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return settings, fmt.Errorf("DHT 配置已损坏，未做任何修改")
	}
	if settings.BootstrapPeers == nil {
		settings.BootstrapPeers = []string{}
	}
	if settings.AuthorizedPeers == nil {
		settings.AuthorizedPeers = []string{}
	}
	return settings, nil
}

// validDHTSettings keeps the stored configuration within what the agent will
// accept, so an invalid entry is refused here rather than silently dropped
// further down where nobody would see the reason.
func validDHTSettings(settings dhtSettings) error {
	if settings.ListenPort < 1024 || settings.ListenPort > 65535 {
		return fmt.Errorf("监听端口必须在 1024-65535 之间")
	}
	if len(settings.BootstrapPeers) > dhtMaxBootstrapPeers {
		return fmt.Errorf("引导节点最多 %d 个", dhtMaxBootstrapPeers)
	}
	for _, address := range settings.BootstrapPeers {
		address = strings.TrimSpace(address)
		if address == "" || len(address) > dhtMaxPeerLength {
			return fmt.Errorf("引导节点地址无效")
		}
		// A bootstrap entry without /p2p/<id> cannot be dialled: libp2p needs
		// the peer identity, not just a host and port.
		if !strings.HasPrefix(address, "/") || !strings.Contains(address, "/p2p/") {
			return fmt.Errorf("引导节点必须是包含 /p2p/<PeerID> 的 multiaddr")
		}
	}
	if len(settings.AuthorizedPeers) > dhtMaxAuthorizedPeers {
		return fmt.Errorf("授权节点最多 %d 个", dhtMaxAuthorizedPeers)
	}
	for _, id := range settings.AuthorizedPeers {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > dhtMaxPeerLength {
			return fmt.Errorf("授权节点 PeerID 无效")
		}
		// A PeerID, not a multiaddr: the allowlist is about identity, and an
		// address here would silently authorize nobody.
		if strings.Contains(id, "/") {
			return fmt.Errorf("授权节点填 PeerID 本身，不要填 multiaddr")
		}
	}
	return nil
}

func (api *API) dhtSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, 405, "method_not_allowed", "仅支持读取和保存", "")
		return
	}
	api.dhtMu.Lock()
	defer api.dhtMu.Unlock()

	settings, err := api.loadDHTSettings()
	if err != nil {
		api.writeInternalError(w, r, "read DHT settings", err)
		return
	}
	if r.Method == http.MethodPut {
		var next dhtSettings
		if !decodeJSONBody(w, r, &next) {
			return
		}
		if next.Revision != settings.Revision {
			writeError(w, 409, "revision_conflict", "配置已更新，请刷新后重试", "revision")
			return
		}
		if next.ListenPort == 0 {
			next.ListenPort = dhtDefaultListenPort
		}
		cleaned := make([]string, 0, len(next.BootstrapPeers))
		for _, address := range next.BootstrapPeers {
			address = strings.TrimSpace(address)
			if address != "" {
				cleaned = append(cleaned, address)
			}
		}
		next.BootstrapPeers = cleaned
		cleanedPeers := make([]string, 0, len(next.AuthorizedPeers))
		for _, id := range next.AuthorizedPeers {
			id = strings.TrimSpace(id)
			if id != "" {
				cleanedPeers = append(cleanedPeers, id)
			}
		}
		next.AuthorizedPeers = cleanedPeers
		if err := validDHTSettings(next); err != nil {
			writeError(w, 422, "invalid_dht_settings", err.Error(), "settings")
			return
		}
		next.Revision = settings.Revision + 1
		dir, _ := api.dhtDirectory()
		if err := writeHomeSIMJSON(filepath.Join(dir, "settings.json"), next); err != nil {
			api.writeInternalError(w, r, "save DHT settings", err)
			return
		}
		settings = next
	}

	status := api.dhtRuntimeStatus(r.Context(), settings, r.Method == http.MethodPut)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"settings": settings, "status": status})
}

// dhtRuntimeStatus asks the agent what the node is actually doing. With no
// agent the state is reported as unavailable rather than invented — the same
// rule the WireGuard panel follows.
func (api *API) dhtRuntimeStatus(
	ctx context.Context,
	settings dhtSettings,
	apply bool,
) dhtStatusView {
	if api.dhtService == nil {
		return dhtStatusView{
			State:     "unavailable",
			Error:     "DHT 运行时不可用：agent 未提供该能力",
			UpdatedAt: time.Now().UTC(),
		}
	}
	config := agentclient.DHTConfig{
		Enabled:             settings.Enabled,
		ListenPort:          settings.ListenPort,
		BootstrapPeers:      settings.BootstrapPeers,
		AuthorizedPeers:     settings.AuthorizedPeers,
		DisableLANDiscovery: settings.DisableLANDiscovery,
	}
	var (
		live agentclient.DHTStatus
		err  error
	)
	if apply {
		live, err = api.dhtService.ApplyDHT(ctx, config)
	} else {
		live, err = api.dhtService.DHTStatus(ctx, settings.Enabled)
	}
	if err != nil {
		return dhtStatusView{State: "unavailable", Error: err.Error(), UpdatedAt: time.Now().UTC()}
	}
	return dhtStatusView{
		State:               string(live.State),
		PeerID:              live.PeerID,
		Addresses:           live.Addresses,
		ConnectedPeers:      live.ConnectedPeers,
		RoutingPeers:        live.RoutingPeers,
		BootstrapConfigured: live.BootstrapConfigured,
		LANDiscovery:        live.LANDiscovery,
		LANPeers:            live.LANPeers,
		Error:               live.Error,
		UpdatedAt:           live.UpdatedAt,
	}
}
