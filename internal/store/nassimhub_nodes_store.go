package store

import (
	"context"
	"errors"
)

// NasSimHubNodeSummary exposes only operational fields. Public keys and
// certificates remain in the database until an explicit trust workflow needs
// them; they are never included in a general dashboard response.
type NasSimHubNodeSummary struct {
	ID           string `json:"node_id"`
	Platform     string `json:"platform"`
	DisplayName  string `json:"display_name"`
	PairState    string `json:"pair_state"`
	Transport    string `json:"transport"`
	USBEndpoint  string `json:"usb_endpoint"`
	WiFiEndpoint string `json:"wifi_endpoint"`
	LastSeenAt   string `json:"last_seen_at,omitempty"`
}

func (s *Store) NasSimHubNodes(ctx context.Context) ([]NasSimHubNodeSummary, error) {
	if s == nil || s.database == nil {
		return nil, ErrDatabaseRequired
	}
	rows, err := s.database.QueryContext(ctx, `
		SELECT node_id, platform, display_name, pair_state, transport,
		       usb_endpoint, wifi_endpoint, COALESCE(CAST(last_seen_at AS TEXT), '')
		FROM nassimhub_nodes ORDER BY node_id LIMIT 129`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]NasSimHubNodeSummary, 0)
	for rows.Next() {
		if len(result) == 128 {
			return nil, errors.New("more than 128 NasSimHub nodes")
		}
		var node NasSimHubNodeSummary
		if err := rows.Scan(&node.ID, &node.Platform, &node.DisplayName,
			&node.PairState, &node.Transport, &node.USBEndpoint,
			&node.WiFiEndpoint, &node.LastSeenAt); err != nil {
			return nil, err
		}
		result = append(result, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
