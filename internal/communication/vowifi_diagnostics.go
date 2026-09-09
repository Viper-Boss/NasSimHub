package communication

import (
	"context"
	"time"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
)

// vowifiDiagnosticsTimeout covers one full agent sweep: two parallel DNS
// paths, IKE probes on UDP 500/4500 and the egress ISP lookup.
const vowifiDiagnosticsTimeout = 50 * time.Second

// VowifiDiagnostics returns the agent's honest VoWiFi reachability report.
func (s *Service) VowifiDiagnostics(ctx context.Context) (agentclient.VowifiDiagnostics, error) {
	const operation = "read vowifi diagnostics"
	if s.agent == nil {
		return agentclient.VowifiDiagnostics{}, operationError(
			CodeUnavailable,
			operation,
			"host agent is not connected",
			nil,
		)
	}
	readContext, cancel := context.WithTimeout(normalizeContext(ctx), vowifiDiagnosticsTimeout)
	defer cancel()
	diagnostics, err := s.agent.VowifiDiagnostics(readContext)
	if err != nil {
		return agentclient.VowifiDiagnostics{}, translateAgentError(operation, err)
	}
	return diagnostics, nil
}
