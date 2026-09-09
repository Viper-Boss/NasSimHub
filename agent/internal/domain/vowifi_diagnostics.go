package domain

import (
	"context"
	"time"
)

// VowifiDiagnostics is the honest reachability report behind the VoWiFi
// switch: it measures the three external gates a real VoWiFi (IWLAN) session
// depends on and reports what it actually saw, instead of pretending the
// feature is available.
//
//   - Modem firmware: does the module expose a VoWiFi/IWLAN stack at all?
//   - ePDG discovery: does the serving carrier publish the standard 3GPP
//     epdg.epc.mnc<mnc>.mcc<mcc>.pub.3gppnetwork.org record?
//   - IKE reachability: do UDP 500 / 4500 answer on the ePDG address?
//
// Every field is best-effort; an empty/unavailable field means "could not be
// observed", never "verified working".
type VowifiDiagnostics struct {
	ObservedAt time.Time `json:"observed_at"`

	// PLMN of the serving operator (e.g. "46011") and the standard 3GPP
	// ePDG discovery FQDN derived from it.
	PLMN     string `json:"plmn"`
	EpdgFQDN string `json:"epdg_fqdn"`

	// Serving operator name when known (informational).
	OperatorName string `json:"operator_name,omitempty"`

	// Whether a cellular data bearer is currently connected; the cellular
	// path probe depends on it.
	DataConnected bool `json:"data_connected"`

	// CellularPath resolves the ePDG FQDN through the carrier DNS servers of
	// the connected bearer, bound to the ECM interface (policy: this is the
	// path a phone would use).
	CellularPath VowifiPathResult `json:"cellular_path"`

	// BroadbandPath resolves the ePDG FQDN through the system resolver
	// (default egress = NAS broadband). This is the path our "VoWiFi over
	// broadband" idea would actually use.
	BroadbandPath VowifiPathResult `json:"broadband_path"`

	// Egress identifies the public宽带出口 (best-effort ISP lookup) so the UI
	// can explain source-carrier restrictions (e.g. carrier requirements
	// that the source IP terminate on their own broadband).
	Egress VowifiEgress `json:"egress"`
}

// VowifiPathResult is the ePDG discovery outcome for one egress path.
type VowifiPathResult struct {
	// Available means the probe ran far enough to produce an answer.
	Available bool `json:"available"`
	// AnswerCode is the DNS RCODE: NOERROR, NXDOMAIN, SERVFAIL, TIMEOUT, ...
	AnswerCode string `json:"answer_code,omitempty"`
	// Addresses returned for the FQDN (empty unless NOERROR with records).
	Addresses []string `json:"addresses,omitempty"`
	// DNS servers the query was sent to (diagnostics).
	DNSServers []string `json:"dns_servers,omitempty"`
	// Interface the query was bound to (cellular path only).
	Interface string `json:"interface,omitempty"`
	// Error is a short reason when the probe could not run or failed.
	Error string `json:"error,omitempty"`

	// Ike probes the first resolved address from the default egress.
	Ike VowifiIkeResult `json:"ike"`
}

// VowifiIkeResult is the UDP 500/4500 liveness probe against an ePDG.
type VowifiIkeResult struct {
	Attempted  bool   `json:"attempted"`
	Address    string `json:"address,omitempty"`
	Port500    bool   `json:"port_500"`
	Port4500   bool   `json:"port_4500"`
	Reachable  bool   `json:"reachable"`
	WaitMillis int    `json:"wait_millis,omitempty"`
	Error      string `json:"error,omitempty"`
}

// VowifiEgress is the public egress identity of the default route.
type VowifiEgress struct {
	Available bool   `json:"available"`
	PublicIP  string `json:"public_ip,omitempty"`
	ISP       string `json:"isp,omitempty"`
	Error     string `json:"error,omitempty"`
}

// VowifiDiagnosticsProvider is implemented by providers that can run the
// VoWiFi reachability probes. Optional: the HTTP API degrades gracefully
// when the provider does not implement it.
type VowifiDiagnosticsProvider interface {
	VowifiDiagnostics(ctx context.Context) (VowifiDiagnostics, error)
}
