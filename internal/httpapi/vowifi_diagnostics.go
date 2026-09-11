package httpapi

import (
	"net/http"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
)

// vowifiDiagnosticsResponse is the management API passthrough of the agent's
// honest VoWiFi reachability report. `available` stays false (with a reason)
// when the agent or its diagnostics provider is unreachable, so the UI can
// render a degraded state instead of failing hard.
type vowifiDiagnosticsResponse struct {
	ObservedAt    string               `json:"observed_at"`
	PLMN          string               `json:"plmn,omitempty"`
	EpdgFQDN      string               `json:"epdg_fqdn,omitempty"`
	OperatorName  string               `json:"operator_name,omitempty"`
	DataConnected bool                 `json:"data_connected"`
	CellularPath  vowifiPathResponse   `json:"cellular_path"`
	BroadbandPath vowifiPathResponse   `json:"broadband_path"`
	Egress        vowifiEgressResponse `json:"egress"`
	Available     bool                 `json:"available"`
	Error         string               `json:"error,omitempty"`
}

type vowifiPathResponse struct {
	Available  bool             `json:"available"`
	AnswerCode string           `json:"answer_code,omitempty"`
	Addresses  []string         `json:"addresses,omitempty"`
	DNSServers []string         `json:"dns_servers,omitempty"`
	Interface  string           `json:"interface,omitempty"`
	Error      string           `json:"error,omitempty"`
	Ike        vowifiIkeReponse `json:"ike"`
}

type vowifiIkeReponse struct {
	Attempted  bool   `json:"attempted"`
	Address    string `json:"address,omitempty"`
	Port500    bool   `json:"port_500"`
	Port4500   bool   `json:"port_4500"`
	Reachable  bool   `json:"reachable"`
	WaitMillis int    `json:"wait_millis,omitempty"`
	Error      string `json:"error,omitempty"`
}

type vowifiEgressResponse struct {
	Available bool   `json:"available"`
	PublicIP  string `json:"public_ip,omitempty"`
	ISP       string `json:"isp,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (api *API) vowifiDiagnostics(response http.ResponseWriter, request *http.Request) {
	result := vowifiDiagnosticsResponse{
		CellularPath:  vowifiPathResponse{Addresses: []string{}},
		BroadbandPath: vowifiPathResponse{Addresses: []string{}},
	}
	if api.deviceConfigurations == nil {
		result.Error = "device configuration service is not configured"
		writeJSON(response, http.StatusOK, result)
		return
	}
	diagnostics, err := api.deviceConfigurations.VowifiDiagnostics(request.Context())
	if err != nil {
		result.Error = err.Error()
		writeJSON(response, http.StatusOK, result)
		return
	}
	result.ObservedAt = diagnostics.ObservedAt.UTC().Format(time.RFC3339Nano)
	result.PLMN = diagnostics.PLMN
	result.EpdgFQDN = diagnostics.EpdgFQDN
	result.OperatorName = diagnostics.OperatorName
	result.DataConnected = diagnostics.DataConnected
	result.CellularPath = vowifiPathFromAgent(diagnostics.CellularPath)
	result.BroadbandPath = vowifiPathFromAgent(diagnostics.BroadbandPath)
	result.Egress = vowifiEgressResponse{
		Available: diagnostics.Egress.Available,
		PublicIP:  diagnostics.Egress.PublicIP,
		ISP:       diagnostics.Egress.ISP,
		Error:     diagnostics.Egress.Error,
	}
	result.Available = true
	writeJSON(response, http.StatusOK, result)
}

func vowifiPathFromAgent(path agentclient.VowifiPathResult) vowifiPathResponse {
	if path.Addresses == nil {
		path.Addresses = []string{}
	}
	if path.DNSServers == nil {
		path.DNSServers = []string{}
	}
	return vowifiPathResponse{
		Available:  path.Available,
		AnswerCode: path.AnswerCode,
		Addresses:  path.Addresses,
		DNSServers: path.DNSServers,
		Interface:  path.Interface,
		Error:      path.Error,
		Ike: vowifiIkeReponse{
			Attempted:  path.Ike.Attempted,
			Address:    path.Ike.Address,
			Port500:    path.Ike.Port500,
			Port4500:   path.Ike.Port4500,
			Reachable:  path.Ike.Reachable,
			WaitMillis: path.Ike.WaitMillis,
			Error:      path.Ike.Error,
		},
	}
}
