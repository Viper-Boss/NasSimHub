package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/human-agent65535/modemdeck/internal/smsclass"
)

// CommunicationStats is the dashboard's traffic summary over a window that
// ends now. Every figure is a count of stored records, scoped to the lines the
// caller may see.
type CommunicationStats struct {
	WindowHours int

	SMSReceived     int
	SMSSent         int
	SMSFailed       int
	SMSVerification int

	CallsIncoming       int
	CallsOutgoing       int
	CallsMissed         int
	CallDurationSeconds int64

	// Hourly has WindowHours buckets, oldest first: bucket 0 is the hour that
	// began WindowHours ago, the last one is the hour ending now.
	Hourly []CommunicationHour
}

type CommunicationHour struct {
	Messages int
	Calls    int
}

// futureTolerance is how far past "now" a record's time may be and still be
// counted, in the newest bucket. A received message carries the time its
// SMSC stamped on it, and that clock and this gateway's are not the same
// clock; a message a few minutes "from the future" arrived just now.
const futureTolerance = time.Hour

// CommunicationStats counts messages and calls over the `hours` ending at
// `now`.
//
// This is done here, over the whole table, because the browser cannot do it:
// the call list is paginated and messages load per conversation, so a count
// taken there is a count of one page presented as a day.
//
// Record times are stored in more than one textual form (RFC 3339 now, a
// space-separated form in older rows), so SQL only narrows to rows dated a
// couple of days either side, by the date prefix all the forms share, and the
// exact window is applied here after parsing.
//
// What each figure means:
//
//	received      incoming messages
//	sent          outgoing messages that did not fail
//	failed        outgoing messages whose delivery failed
//	verification  incoming messages the push classifier calls a code (smsclass)
//	incoming      incoming calls, answered or not
//	outgoing      outgoing calls
//	missed        incoming calls never answered and not rejected -- the call
//	              list's own "missed"
//	duration      answered-to-ended time of calls that have ended
//
// Messages are placed by their own time, calls by when the gateway first saw
// them. Deleted messages are not counted, as they are not listed either.
func (s *Store) CommunicationStats(
	ctx context.Context,
	now time.Time,
	hours int,
) (CommunicationStats, error) {
	if hours <= 0 {
		return CommunicationStats{}, fmt.Errorf("communication stats: window must be positive")
	}
	stats := CommunicationStats{WindowHours: hours, Hourly: make([]CommunicationHour, hours)}
	start := now.Add(-time.Duration(hours) * time.Hour)
	// Two days either side of the window covers any UTC offset a legacy row
	// was written with.
	earliest := start.UTC().AddDate(0, 0, -2).Format("2006-01-02")

	bucket := func(at time.Time) (int, bool) {
		if at.Before(start) || at.After(now.Add(futureTolerance)) {
			return 0, false
		}
		index := int(at.Sub(start) / time.Hour)
		if index >= hours {
			index = hours - 1
		}
		return index, true
	}

	if err := s.countMessages(ctx, earliest, bucket, &stats); err != nil {
		return CommunicationStats{}, err
	}
	if err := s.countCalls(ctx, earliest, bucket, &stats); err != nil {
		return CommunicationStats{}, err
	}
	return stats, nil
}

func (s *Store) countMessages(
	ctx context.Context,
	earliest string,
	bucket func(time.Time) (int, bool),
	stats *CommunicationStats,
) error {
	statement := `SELECT type, delivery_status, content,
		COALESCE(CAST(timestamp AS TEXT), ''), COALESCE(CAST(created_at AS TEXT), '')
		FROM sms
		WHERE deleted_at IS NULL
		AND (
			COALESCE(CAST(timestamp AS TEXT), '') >= ? OR
			COALESCE(CAST(created_at AS TEXT), '') >= ?
		)`
	arguments := []any{earliest, earliest}
	if condition, values, scoped := principalLineScope(ctx, "line_id"); scoped {
		statement += " AND " + condition
		arguments = append(arguments, values...)
	}
	rows, err := s.database.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return fmt.Errorf("query message stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			messageType             sql.NullInt64
			deliveryStatus, content sql.NullString
			timestamp, createdAt    string
		)
		if err := rows.Scan(&messageType, &deliveryStatus, &content, &timestamp, &createdAt); err != nil {
			return fmt.Errorf("scan message stats: %w", err)
		}
		// A message's own time, unless it has none: a zero time is written as
		// year 1, and is no more a time than an empty one.
		at, ok := parseDatabaseTime(timestamp)
		if !ok || at.Year() < 2000 {
			at, ok = parseDatabaseTime(createdAt)
		}
		if !ok {
			continue
		}
		index, inside := bucket(at)
		if !inside {
			continue
		}
		switch intValue(messageType) {
		case 1:
			stats.SMSReceived++
			if smsclass.Classify(stringValue(content)) == smsclass.CategoryOTP {
				stats.SMSVerification++
			}
		case 2:
			if MessageDeliveryStatus(stringValue(deliveryStatus)) == MessageDeliveryFailed {
				stats.SMSFailed++
			} else {
				stats.SMSSent++
			}
		default:
			// Neither direction: not a message anyone sent or received.
			continue
		}
		stats.Hourly[index].Messages++
	}
	return rowsError("read message stats", rows.Err())
}

func (s *Store) countCalls(
	ctx context.Context,
	earliest string,
	bucket func(time.Time) (int, bool),
	stats *CommunicationStats,
) error {
	statement := `SELECT direction, phase,
		COALESCE(CAST(created_at AS TEXT), ''), COALESCE(CAST(active_at AS TEXT), ''),
		COALESCE(CAST(ended_at AS TEXT), ''), COALESCE(end_reason, ''), COALESCE(failure_code, '')
		FROM call_history
		WHERE COALESCE(CAST(created_at AS TEXT), '') >= ?`
	arguments := []any{earliest}
	if condition, values, scoped := principalLineScope(ctx, "line_id"); scoped {
		statement += " AND " + condition
		arguments = append(arguments, values...)
	}
	rows, err := s.database.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return fmt.Errorf("query call stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			direction, phase             sql.NullString
			createdAt, activeAt, endedAt string
			endReason, failureCode       string
		)
		if err := rows.Scan(
			&direction, &phase, &createdAt, &activeAt, &endedAt, &endReason, &failureCode,
		); err != nil {
			return fmt.Errorf("scan call stats: %w", err)
		}
		at, ok := parseDatabaseTime(createdAt)
		if !ok {
			continue
		}
		index, inside := bucket(at)
		if !inside {
			continue
		}
		answered := strings.TrimSpace(activeAt) != ""
		ended := strings.TrimSpace(endedAt) != ""
		switch stringValue(direction) {
		case string(CallKindIncoming):
			stats.CallsIncoming++
			// The same rule the call list uses for its "missed" filter.
			finished := stringValue(phase) == "ended" || stringValue(phase) == "failed" || ended
			if finished && !answered && endReason != "rejected" && failureCode != "rejected" {
				stats.CallsMissed++
			}
		case string(CallKindOutgoing):
			stats.CallsOutgoing++
		default:
			continue
		}
		if answered && ended {
			stats.CallDurationSeconds += durationSeconds(activeAt, endedAt)
		}
		stats.Hourly[index].Calls++
	}
	return rowsError("read call stats", rows.Err())
}
