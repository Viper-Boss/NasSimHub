package modemmanager

import (
	"context"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/human-agent65535/modemdeck/agent/internal/domain"
	"github.com/human-agent65535/modemdeck/agent/internal/operator"
	"github.com/human-agent65535/modemdeck/agent/internal/vowifi"
)

// vowifiDiagnosticsTimeout bounds one full sweep: two parallel DNS paths
// (~3s each server), an IKE liveness probe (2x3s) and the egress ISP lookup
// (~4s), plus ModemManager D-Bus reads on top.
const vowifiDiagnosticsTimeout = 40 * time.Second

// VowifiDiagnostics runs the honest VoWiFi reachability sweep for the first
// line: ePDG discovery over the cellular path (carrier DNS bound to the ECM
// interface) and the broadband path (system resolver), IKE liveness on
// UDP 500/4500, and a best-effort public egress identification.
func (p *Provider) VowifiDiagnostics(ctx context.Context) (domain.VowifiDiagnostics, error) {
	const operation = "vowifi_diagnostics"
	bounded, cancel := context.WithTimeout(ctx, vowifiDiagnosticsTimeout)
	defer cancel()

	identity, err := p.resolveProviderIdentity(bounded, operation)
	if err != nil {
		return domain.VowifiDiagnostics{}, err
	}
	objects, err := p.managedObjects(bounded, operation)
	if err != nil {
		return domain.VowifiDiagnostics{}, err
	}
	parsed := ParseManagedObjects(objects, identity)
	if len(parsed.Lines) == 0 {
		return domain.VowifiDiagnostics{}, domain.NotFound(operation, "no line is available for VoWiFi diagnostics")
	}
	line := parsed.Lines[0]
	configuration, _, modemPath, err := p.readDeviceConfiguration(bounded, line.ID, operation)
	if err != nil {
		return domain.VowifiDiagnostics{}, err
	}

	result := domain.VowifiDiagnostics{
		ObservedAt:    time.Now().UTC(),
		DataConnected: configuration.NetworkEnabled,
	}
	result.PLMN = strings.TrimSpace(line.ServingOperatorCode)
	result.OperatorName = strings.TrimSpace(line.ServingOperatorName)
	if result.PLMN == "" {
		result.PLMN = strings.TrimSpace(line.HomeOperatorCode)
	}
	if result.OperatorName == "" {
		result.OperatorName = strings.TrimSpace(line.HomeOperatorName)
	}
	// Many vendor modules (including the QDC507) never fill ModemManager's
	// operator properties; ask the modem directly over the AT passthrough.
	if result.PLMN == "" {
		if plmn, name := p.servingPLMNViaAT(bounded, modemPath, operation); plmn != "" {
			result.PLMN = plmn
			if result.OperatorName == "" {
				result.OperatorName = name
			}
		}
	}
	if details, found := operator.Lookup(result.PLMN); found && result.OperatorName == "" {
		result.OperatorName = details.Name
	}
	result.EpdgFQDN = epdgFQDN(result.PLMN)

	request := vowifi.Request{FQDN: result.EpdgFQDN}
	if result.DataConnected {
		for _, connection := range configuration.DataConnections {
			if !connection.Connected {
				continue
			}
			if request.CellularInterface == "" && connection.Interface != "" {
				request.CellularInterface = connection.Interface
			}
			request.CellularDNSServers = append(
				request.CellularDNSServers,
				connection.IPv4.DNS...,
			)
			request.CellularDNSServers = append(
				request.CellularDNSServers,
				connection.IPv6.DNS...,
			)
			if request.CellularInterface != "" && len(request.CellularDNSServers) > 0 {
				break
			}
		}
	}
	outcome := vowifi.Run(bounded, request)
	result.CellularPath = outcome.Cellular
	result.BroadbandPath = outcome.Broadband
	result.Egress = outcome.Egress
	return result, nil
}

// servingPLMNViaAT asks the modem for the serving operator over the AT
// passthrough and returns a numeric PLMN plus a readable name when possible.
// Best-effort: any failure yields an empty PLMN.
func (p *Provider) servingPLMNViaAT(
	ctx context.Context,
	modemPath dbus.ObjectPath,
	operation string,
) (string, string) {
	// Quectel-style QNWINFO reports the numeric serving PLMN directly, which
	// the QDC507 fills reliably (unlike ModemManager's operator properties).
	if response, err := p.commandATPath(ctx, modemPath, operation, "AT+QNWINFO"); err == nil {
		if plmn := parseQNWINFOPlmn(response); plmn != "" {
			return plmn, ""
		}
	}
	// Fallback: +COPS? with whatever report format the module was left in.
	response, err := p.commandATPath(ctx, modemPath, operation, "AT+COPS?")
	if err != nil {
		return "", ""
	}
	// Expected shapes: +COPS: <mode>,<format>,"<oper>"[,<Act>]
	// <format> 2 = numeric PLMN, 0 = long alphanumeric name.
	start := strings.Index(response, "\"")
	end := strings.LastIndex(response, "\"")
	if start < 0 || end <= start {
		return "", ""
	}
	oper := strings.TrimSpace(response[start+1 : end])
	if oper == "" {
		return "", ""
	}
	allDigits := true
	for _, character := range oper {
		if character < '0' || character > '9' {
			allDigits = false
			break
		}
	}
	if allDigits && len(oper) >= 5 && len(oper) <= 6 {
		return oper, ""
	}
	if plmn, found := servingOperatorByLongName[oper]; found {
		return plmn, oper
	}
	return "", ""
}

// parseQNWINFOPlmn extracts the numeric PLMN from a +QNWINFO response:
// `+QNWINFO: "FDD LTE","46001","LTE BAND 3",1650` -> "46001".
func parseQNWINFOPlmn(response string) string {
	fields := strings.Split(response, ",")
	if len(fields) < 2 {
		return ""
	}
	plmn := strings.Trim(strings.TrimSpace(fields[1]), "\"")
	if len(plmn) < 5 || len(plmn) > 6 {
		return ""
	}
	for _, character := range plmn {
		if character < '0' || character > '9' {
			return ""
		}
	}
	return plmn
}

// servingOperatorByLongName maps the long alphanumeric operator names the
// Chinese carriers put in +COPS? onto their numeric PLMNs, for modules that
// never report the numeric format.
var servingOperatorByLongName = map[string]string{
	"CHN-CT":        "46011",
	"CHN-UNICOM":    "46001",
	"CMCC":          "46000",
	"CMCC+":         "46002",
	"China Mobile":  "46000",
	"China Unicom":  "46001",
	"China Telecom": "46011",
}

// epdgFQDN derives the standard 3GPP ePDG discovery name from a PLMN like
// "46011" (MCC 460, MNC 011 padded to three digits).
func epdgFQDN(plmn string) string {
	digits := make([]rune, 0, len(plmn))
	for _, character := range plmn {
		if character >= '0' && character <= '9' {
			digits = append(digits, character)
		}
	}
	if len(digits) < 5 || len(digits) > 6 {
		return ""
	}
	mcc := string(digits[:3])
	mnc := string(digits[3:])
	for len(mnc) < 3 {
		mnc = "0" + mnc
	}
	return "epdg.epc.mnc" + mnc + ".mcc" + mcc + ".pub.3gppnetwork.org"
}
