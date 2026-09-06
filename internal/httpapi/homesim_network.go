package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Two network supply switches for debugging the F50 and EG25-G cellular
// data paths. They are intent + state switches stored on the NAS:
//
//   - CellularShare: when enabled, the panel guides the user to route the
//     NAS (飞牛OS) or any LAN device through the cellular line proxy
//     (socks5/http) that the gateway already manages per line.
//   - HomeSIMOnly: when enabled, the gateway's own outbound requests
//     (Bark test pushes and future integrations) are routed through the
//     running cellular line proxy instead of the default network.
//
// Both default to off: with everything off the NAS keeps using its own
// (飞牛OS) network. Switching the NAS-wide default route is an OS-level
// change and stays manual by design — see the panel guidance.

type homeSIMNetworkSwitches struct {
	Revision      int64 `json:"revision"`
	CellularShare bool  `json:"cellular_share"`
	HomeSIMOnly   bool  `json:"home_sim_only"`
}

func defaultHomeSIMNetworkSwitches() homeSIMNetworkSwitches {
	return homeSIMNetworkSwitches{Revision: 1}
}

func (api *API) homeSIMNetworkSwitchesPath() (string, bool) {
	directory, ok := api.homeSIMDataDirectory()
	if !ok {
		return "", false
	}
	return filepath.Join(directory, "network-switches.json"), true
}

func (api *API) loadHomeSIMNetworkSwitches() homeSIMNetworkSwitches {
	current := defaultHomeSIMNetworkSwitches()
	path, ok := api.homeSIMNetworkSwitchesPath()
	if !ok {
		return current
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return current
	}
	var loaded homeSIMNetworkSwitches
	if err := json.Unmarshal(data, &loaded); err != nil {
		return defaultHomeSIMNetworkSwitches()
	}
	if loaded.Revision < 1 {
		loaded.Revision = 1
	}
	return loaded
}

func (api *API) homeSIMNetworkSwitches(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodPut {
		response.Header().Set("Allow", "GET, PUT")
		writeError(response, 405, "method_not_allowed", "Only GET and PUT are supported", "")
		return
	}
	path, ok := api.homeSIMNetworkSwitchesPath()
	if !ok {
		writeError(response, 503, "integration_unavailable", "HomeSIM server storage is unavailable", "")
		return
	}
	api.homeSIMNetworkSwitchesMu.Lock()
	defer api.homeSIMNetworkSwitchesMu.Unlock()
	current := api.loadHomeSIMNetworkSwitches()
	if request.Method == http.MethodPut {
		var next homeSIMNetworkSwitches
		if !decodeJSONBody(response, request, &next) {
			return
		}
		if next.Revision != current.Revision {
			writeError(response, 409, "revision_conflict", "Network switches changed; reload before saving", "revision")
			return
		}
		next.Revision++
		if err := writeHomeSIMJSON(path, next); err != nil {
			api.writeInternalError(response, request, "save network switches", err)
			return
		}
		current = next
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, 200, map[string]any{"switches": current})
}

// homeSIMOutboundProxyURL returns the SOCKS5 proxy URL that outbound
// HomeSIM requests should use when the HomeSIMOnly switch is enabled, or an
// empty string. The first running SOCKS5 proxy instance wins.
func (api *API) homeSIMOutboundProxyURL(ctx context.Context) string {
	switches := api.loadHomeSIMNetworkSwitches()
	if !switches.HomeSIMOnly || api.network == nil {
		return ""
	}
	instances, err := api.network.Proxies(ctx)
	if err != nil {
		return ""
	}
	for _, instance := range instances {
		if !instance.Enabled || !strings.EqualFold(string(instance.Mode), "socks5") {
			continue
		}
		address := strings.TrimSpace(instance.ListenAddress)
		if address == "" || address == "0.0.0.0" || address == "::" {
			address = "127.0.0.1"
		}
		return "socks5://" + address + ":" + strconv.FormatUint(uint64(instance.ListenPort), 10)
	}
	return ""
}

// homeSIMOutboundTransport builds the transport for HomeSIM outbound
// requests. When the HomeSIMOnly switch is enabled and a running SOCKS5
// line proxy exists, requests are routed through it (the cellular data
// path); otherwise the system default network is used.
func (api *API) homeSIMOutboundTransport(request *http.Request) http.RoundTripper {
	proxyURL := api.homeSIMOutboundProxyURL(request.Context())
	if proxyURL == "" {
		return nil // http.DefaultTransport
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil
	}
	return &http.Transport{Proxy: http.ProxyURL(parsed)}
}
