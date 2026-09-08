package communication

import (
	"context"

	"github.com/human-agent65535/modemdeck/internal/agentclient"
)

// SystemMetrics returns the host agent's host/modem health snapshot for the
// dashboard modem-status panel.
func (s *Service) SystemMetrics(ctx context.Context) (agentclient.SystemMetrics, error) {
	const operation = "read system metrics"
	if s.agent == nil {
		return agentclient.SystemMetrics{}, operationError(
			CodeUnavailable,
			operation,
			"host agent is not connected",
			nil,
		)
	}
	readContext, cancel := context.WithTimeout(normalizeContext(ctx), snapshotTimeout)
	defer cancel()
	metrics, err := s.agent.SystemMetrics(readContext)
	if err != nil {
		return agentclient.SystemMetrics{}, translateAgentError(operation, err)
	}
	return metrics, nil
}
