package modemmanager

import (
	"context"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
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
	configuration, _, _, err := p.readDeviceConfiguration(bounded, line.ID, operation)
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
