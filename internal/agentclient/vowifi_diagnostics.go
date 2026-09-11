package agentclient

import (
	"context"
	"net/http"
	"time"
)

// VowifiDiagnostics mirrors the agent's honest VoWiFi reachability report.
type VowifiDiagnostics struct {
	ObservedAt    time.Time        `json:"observed_at"`
	PLMN          string           `json:"plmn"`
	EpdgFQDN      string           `json:"epdg_fqdn"`
	OperatorName  string           `json:"operator_name,omitempty"`
	DataConnected bool             `json:"data_connected"`
	CellularPath  VowifiPathResult `json:"cellular_path"`
	BroadbandPath VowifiPathResult `json:"broadband_path"`
	Egress        VowifiEgress     `json:"egress"`
}

type VowifiPathResult struct {
	Available  bool            `json:"available"`
	AnswerCode string          `json:"answer_code,omitempty"`
	Addresses  []string        `json:"addresses,omitempty"`
	DNSServers []string        `json:"dns_servers,omitempty"`
	Interface  string          `json:"interface,omitempty"`
	Error      string          `json:"error,omitempty"`
	Ike        VowifiIkeResult `json:"ike"`
}

type VowifiIkeResult struct {
	Attempted  bool   `json:"attempted"`
	Address    string `json:"address,omitempty"`
	Port500    bool   `json:"port_500"`
	Port4500   bool   `json:"port_4500"`
	Reachable  bool   `json:"reachable"`
	WaitMillis int    `json:"wait_millis,omitempty"`
	Error      string `json:"error,omitempty"`
}

type VowifiEgress struct {
	Available bool   `json:"available"`
	PublicIP  string `json:"public_ip,omitempty"`
	ISP       string `json:"isp,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (client *Client) VowifiDiagnostics(ctx context.Context) (VowifiDiagnostics, error) {
	var diagnostics VowifiDiagnostics
	if err := client.doJSON(
		ctx,
		http.MethodGet,
		"/v1/vowifi/diagnostics",
		nil,
		http.StatusOK,
		&diagnostics,
	); err != nil {
		return VowifiDiagnostics{}, err
	}
	if diagnostics.CellularPath.Addresses == nil {
		diagnostics.CellularPath.Addresses = []string{}
	}
	if diagnostics.BroadbandPath.Addresses == nil {
		diagnostics.BroadbandPath.Addresses = []string{}
	}
	return diagnostics, nil
}
