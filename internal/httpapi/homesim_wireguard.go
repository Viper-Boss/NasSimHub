package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
)

type wireGuardPeer struct {
	Name          string   `json:"name"`
	PublicKey     string   `json:"public_key"`
	Endpoint      string   `json:"endpoint"`
	AllowedIPs    []string `json:"allowed_ips"`
	NKNAddress    string   `json:"nkn_address"`
	LastNKNUpdate int64    `json:"last_nkn_update,omitempty"`
}
type wireGuardSettings struct {
	MTProto            mtProtoSettings `json:"mtproto"`
	Revision           int64           `json:"revision"`
	Enabled            bool            `json:"enabled"`
	Address            string          `json:"address"`
	ListenPort         int             `json:"listen_port"`
	AdvertisedEndpoint string          `json:"advertised_endpoint"`
	Peers              []wireGuardPeer `json:"peers"`
}

const (
	wireGuardInterfaceName    = "wg-nassim"
	wireGuardKeepaliveSeconds = 25
)

// wireGuardPeerStatus is what the live interface reports per peer. Only the
// public key travels; there is no field here that could carry a secret.
type wireGuardPeerStatus struct {
	Name          string `json:"name,omitempty"`
	PublicKey     string `json:"public_key"`
	Endpoint      string `json:"endpoint,omitempty"`
	LastHandshake int64  `json:"last_handshake_unix,omitempty"`
	RxBytes       int64  `json:"rx_bytes"`
	TxBytes       int64  `json:"tx_bytes"`
	Current       bool   `json:"current"`
}

type wireGuardStatus struct {
	MTProtoState string                `json:"mtproto_state,omitempty"`
	State        string                `json:"state"`
	PublicKey    string                `json:"public_key,omitempty"`
	Error        string                `json:"error,omitempty"`
	Revision     int64                 `json:"revision"`
	UpdatedAt    time.Time             `json:"updated_at"`
	Peers        []wireGuardPeerStatus `json:"peers,omitempty"`
}

type mtProtoSettings struct {
	Enabled     bool   `json:"enabled"`
	BindAddress string `json:"bind_address"`
	Port        int    `json:"port"`
	Secret      string `json:"secret"`
}

func validMTProto(s mtProtoSettings) bool {
	if !s.Enabled && s.BindAddress == "" && s.Secret == "" {
		return true
	}
	ip, err := netip.ParseAddr(s.BindAddress)
	if err != nil || !ip.Is4() || !ip.IsPrivate() || s.Port < 1024 || s.Port > 65535 {
		return false
	}
	if s.Secret == "" {
		return !s.Enabled
	}
	raw, err := hex.DecodeString(s.Secret)
	if err != nil || len(raw) < 20 || len(raw) > 270 || raw[0] != 0xee {
		return false
	}
	for _, c := range raw[17:] {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

func validWGEndpoint(endpoint string) bool {
	if endpoint == "" {
		return true
	}
	host, port, err := net.SplitHostPort(endpoint)
	n, portErr := strconv.Atoi(port)
	return err == nil && portErr == nil && n > 0 && n <= 65535 && host != "" && !strings.ContainsAny(host, " \t\r\n/#=;") && len(host) <= 253
}
func validWGSettings(s wireGuardSettings) bool {
	if !validMTProto(s.MTProto) {
		return false
	}
	address, err := netip.ParsePrefix(s.Address)
	if err != nil || !address.Addr().Is4() || !address.Addr().IsPrivate() || address.Bits() < 16 || address.Bits() > 30 || s.ListenPort < 1024 || s.ListenPort > 65535 || !validWGEndpoint(s.AdvertisedEndpoint) || len(s.Peers) > 64 {
		return false
	}
	keys := map[string]bool{}
	var routes []netip.Prefix
	for _, peer := range s.Peers {
		key, err := base64.StdEncoding.DecodeString(peer.PublicKey)
		if err != nil || len(key) != 32 || bytes.Equal(key, make([]byte, 32)) || keys[peer.PublicKey] || len(peer.Name) > 64 || !validWGEndpoint(peer.Endpoint) || len(peer.AllowedIPs) != 1 || len(peer.NKNAddress) > 160 || strings.ContainsAny(peer.NKNAddress, " \t\r\n") {
			return false
		}
		keys[peer.PublicKey] = true
		for _, value := range peer.AllowedIPs {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || prefix != prefix.Masked() || !prefix.Addr().Is4() || prefix.Bits() != 32 || !address.Masked().Contains(prefix.Addr()) || prefix.Addr() == address.Addr() || prefix.Addr() == address.Masked().Addr() || prefix.Addr().Next().IsValid() && !address.Masked().Contains(prefix.Addr().Next()) {
				return false
			}
			for _, prior := range routes {
				if prefix.Overlaps(prior) {
					return false
				}
			}
			routes = append(routes, prefix)
		}
	}
	return true
}
func (api *API) wireGuardDirectory() (string, error) {
	dir, ok := api.homeSIMDataDirectory()
	if !ok {
		return "", fmt.Errorf("服务器存储不可用")
	}
	return filepath.Join(dir, "wireguard"), nil
}
func (api *API) loadWireGuardSettings() (wireGuardSettings, error) {
	s := wireGuardSettings{Revision: 1, Address: "10.77.0.1/24", ListenPort: 51820, Peers: []wireGuardPeer{}}
	dir, err := api.wireGuardDirectory()
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(data, &s)
	if err == nil && !validWGSettings(s) {
		err = fmt.Errorf("WireGuard 配置无效")
	}
	return s, err
}
func (api *API) wireGuardSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, 405, "method_not_allowed", "仅支持读取和保存", "")
		return
	}
	api.wireGuardMu.Lock()
	defer api.wireGuardMu.Unlock()
	s, err := api.loadWireGuardSettings()
	if err != nil {
		api.writeInternalError(w, r, "read WireGuard settings", err)
		return
	}
	dir, _ := api.wireGuardDirectory()
	if r.Method == http.MethodPut {
		var next wireGuardSettings
		if !decodeJSONBody(w, r, &next) {
			return
		}
		if next.Revision != s.Revision {
			writeError(w, 409, "revision_conflict", "配置已更新，请刷新后重试", "revision")
			return
		}
		if next.MTProto.Enabled && next.MTProto.Secret == "" {
			if s.MTProto.Secret != "" {
				next.MTProto.Secret = s.MTProto.Secret
			} else {
				var random [16]byte
				if _, err := rand.Read(random[:]); err != nil {
					api.writeInternalError(w, r, "generate MTProto secret", err)
					return
				}
				next.MTProto.Secret = "ee" + hex.EncodeToString(random[:]) + hex.EncodeToString([]byte("www.microsoft.com"))
			}
		}
		if !validWGSettings(next) {
			writeError(w, 422, "invalid_wireguard_settings", "请检查地址、端口、公钥及路由；不同节点的路由不能重叠", "settings")
			return
		}
		next.Revision++
		for index := range next.Peers {
			next.Peers[index].LastNKNUpdate = 0
			for _, prior := range s.Peers {
				if prior.PublicKey == next.Peers[index].PublicKey && prior.NKNAddress == next.Peers[index].NKNAddress {
					next.Peers[index].LastNKNUpdate = prior.LastNKNUpdate
				}
			}
		}
		if err := writeHomeSIMJSON(filepath.Join(dir, "settings.json"), next); err != nil {
			api.writeInternalError(w, r, "save WireGuard settings", err)
			return
		}
		s = next
	}
	// The agent owns the interface: this process has no capabilities and cannot
	// create one. A PUT applies the new configuration, a GET only reads.
	status := api.wireGuardRuntimeStatus(r.Context(), s, r.Method == http.MethodPut)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{
		"settings":      s,
		"status":        status,
		"runtime_ready": status.State == string(agentclient.WireGuardStateUp) && status.Revision == s.Revision,
	})
}

// wireGuardRuntimeStatus asks the agent what the host is actually doing.
//
// When no agent is wired in (an older agent build, or a unit test), the state
// is reported as unavailable rather than invented. The previous version of this
// code returned a hardcoded "not_installed" forever, which is how a feature
// with no implementation behind it managed to look finished for so long.
func (api *API) wireGuardRuntimeStatus(
	ctx context.Context,
	settings wireGuardSettings,
	apply bool,
) wireGuardStatus {
	status := wireGuardStatus{State: "unavailable", Revision: settings.Revision, UpdatedAt: time.Now().UTC()}
	if api.wireGuardService == nil {
		status.Error = "WireGuard 运行时不可用：agent 未提供该能力"
		return status
	}
	config := agentclient.WireGuardConfig{
		InterfaceName: wireGuardInterfaceName,
		Address:       settings.Address,
		ListenPort:    settings.ListenPort,
		Enabled:       settings.Enabled,
		Revision:      settings.Revision,
	}
	for _, peer := range settings.Peers {
		config.Peers = append(config.Peers, agentclient.WireGuardPeerConfig{
			Name:       peer.Name,
			PublicKey:  peer.PublicKey,
			Endpoint:   peer.Endpoint,
			AllowedIPs: peer.AllowedIPs,
			// A NAS behind NAT has to keep its mapping alive or the peer can
			// never reach it again after the first idle period.
			PersistentKeepalive: wireGuardKeepaliveSeconds,
		})
	}
	var (
		live agentclient.WireGuardStatus
		err  error
	)
	if apply {
		live, err = api.wireGuardService.ApplyWireGuard(ctx, config)
	} else {
		live, err = api.wireGuardService.WireGuardStatusFor(ctx, config.InterfaceName, config.Enabled)
	}
	if err != nil {
		status.State = "unavailable"
		status.Error = err.Error()
		return status
	}
	return wireGuardStatus{
		State:        string(live.State),
		PublicKey:    live.PublicKey,
		Error:        live.Error,
		Revision:     live.Revision,
		UpdatedAt:    live.UpdatedAt,
		Peers:        convertWireGuardPeerStatus(live.Peers),
		MTProtoState: status.MTProtoState,
	}
}

func convertWireGuardPeerStatus(peers []agentclient.WireGuardPeerStatus) []wireGuardPeerStatus {
	converted := make([]wireGuardPeerStatus, 0, len(peers))
	for _, peer := range peers {
		converted = append(converted, wireGuardPeerStatus{
			Name:          peer.Name,
			PublicKey:     peer.PublicKey,
			Endpoint:      peer.Endpoint,
			LastHandshake: peer.LastHandshake,
			RxBytes:       peer.TransferRxBytes,
			TxBytes:       peer.TransferTxBytes,
			Current:       peer.Current,
		})
	}
	return converted
}
