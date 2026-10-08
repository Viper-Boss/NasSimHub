package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/human-agent65535/modemdeck/internal/auth"
	"github.com/human-agent65535/modemdeck/internal/phone"
)

var ErrOwnNumberSIMChanged = errors.New("own number SIM changed or detached")
var ErrOwnNumberInvalid = errors.New("invalid own number")
var ErrOwnNumberForbidden = errors.New("own number requires administrator")

// UpdateOwnNumber supplements an unreadable number for the currently attached
// SIM. Stable line identity follows the SIM, never the temporary modem index.
func (s *Store) UpdateOwnNumber(ctx context.Context, lineID, expectedICCID, number string) (string, error) {
	if p, ok := auth.PrincipalFromContext(ctx); ok && (!p.IsAdmin() || !p.CanAccessLine(lineID)) {
		return "", ErrOwnNumberForbidden
	}
	lineID, expectedICCID, number = strings.TrimSpace(lineID), strings.TrimSpace(expectedICCID), strings.TrimSpace(number)
	if lineID == "" || expectedICCID == "" {
		return "", ErrOwnNumberSIMChanged
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var region string
	err = tx.QueryRowContext(ctx, `SELECT l.home_country_iso FROM modemdeck_lines l
 JOIN sim_cards sc ON sc.line_id = l.line_id
 JOIN devices d ON d.imei = sc.current_imei AND d.iccid = sc.iccid
 WHERE l.line_id = ? AND sc.iccid = ? AND d.sim_inserted = 1
 AND d.last_seen = (SELECT observed_at FROM modemdeck_hardware_sync WHERE singleton = 1)`, lineID, expectedICCID).Scan(&region)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrOwnNumberSIMChanged
	}
	if err != nil {
		return "", err
	}
	normalized := ""
	if number != "" {
		parsed, parseErr := phone.ParseSubscriber(number, region)
		if parseErr != nil || parsed.E164 == "" {
			return "", ErrOwnNumberInvalid
		}
		normalized = parsed.E164
	}
	// A duplicate number must not merge these SIMs or touch another line.
	var conflict int
	if normalized != "" {
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM modemdeck_lines WHERE phone_number = ? AND line_id <> ?`, normalized, lineID).Scan(&conflict)
		if err != nil {
			return "", err
		}
		if conflict != 0 {
			return "", ErrOwnNumberInvalid
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE modemdeck_lines SET phone_number = ?, updated_at = CURRENT_TIMESTAMP WHERE line_id = ?`, normalized, lineID); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sim_subscriptions SET phone_number = ?, updated_at = CURRENT_TIMESTAMP WHERE line_id = ? AND current_iccid = ?`, normalized, lineID, expectedICCID); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return normalized, nil
}
