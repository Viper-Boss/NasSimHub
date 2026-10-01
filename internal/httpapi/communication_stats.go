package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/human-agent65535/modemdeck/internal/store"
)

// communicationStatsWindow is the dashboard summary's window.
const communicationStatsWindow = 24

// communicationStatsSource is the one repository method this endpoint needs.
// It is asserted rather than added to Repository, so a repository without it --
// every test double in this package -- answers "not here" instead of failing
// to compile, and the card reads that as "not wired" exactly as it did before.
type communicationStatsSource interface {
	CommunicationStats(context.Context, time.Time, int) (store.CommunicationStats, error)
}

type communicationStatsResponse struct {
	WindowHours int                        `json:"window_hours"`
	SMS         communicationStatsSMS      `json:"sms"`
	Calls       communicationStatsCalls    `json:"calls"`
	Hourly      []communicationStatsBucket `json:"hourly"`
}

type communicationStatsSMS struct {
	Received     int `json:"received"`
	Sent         int `json:"sent"`
	Failed       int `json:"failed"`
	Verification int `json:"verification"`
}

type communicationStatsCalls struct {
	Incoming        int   `json:"incoming"`
	Outgoing        int   `json:"outgoing"`
	Missed          int   `json:"missed"`
	DurationSeconds int64 `json:"duration_seconds"`
}

type communicationStatsBucket struct {
	Hour     int `json:"hour"`
	Messages int `json:"messages"`
	Calls    int `json:"calls"`
}

// communicationStats answers GET /api/v1/stats/communication.
//
// The zeros in this response are real: they are counts of stored records, and
// a line that handled nothing today did handle nothing. That is the difference
// from the card's own "not wired" state, which is what it shows when this
// endpoint does not answer at all.
//
// Scoped like the call and message lists: a member sees only the lines they
// may open, through the principal on the request context.
func (api *API) communicationStats(response http.ResponseWriter, request *http.Request) {
	source, ok := api.repository.(communicationStatsSource)
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "Communication statistics are not available", "")
		return
	}
	stats, err := source.CommunicationStats(request.Context(), time.Now(), communicationStatsWindow)
	if lineID := request.URL.Query().Get("line_id"); lineID != "" {
		scoped, ok := api.repository.(interface {
			CommunicationStatsForLine(context.Context, time.Time, int, string) (store.CommunicationStats, error)
		})
		if !ok {
			writeError(response, http.StatusServiceUnavailable, "stats_unavailable", "Per-line statistics are unavailable", "")
			return
		}
		stats, err = scoped.CommunicationStatsForLine(request.Context(), time.Now(), communicationStatsWindow, lineID)
	}
	if err != nil {
		api.logger.Error("communication stats failed", "error", err)
		writeError(response, http.StatusInternalServerError, "communication_stats_failed", "Communication statistics could not be read", "")
		return
	}
	hourly := make([]communicationStatsBucket, len(stats.Hourly))
	for index, bucket := range stats.Hourly {
		hourly[index] = communicationStatsBucket{Hour: index, Messages: bucket.Messages, Calls: bucket.Calls}
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, communicationStatsResponse{
		WindowHours: stats.WindowHours,
		SMS: communicationStatsSMS{
			Received:     stats.SMSReceived,
			Sent:         stats.SMSSent,
			Failed:       stats.SMSFailed,
			Verification: stats.SMSVerification,
		},
		Calls: communicationStatsCalls{
			Incoming:        stats.CallsIncoming,
			Outgoing:        stats.CallsOutgoing,
			Missed:          stats.CallsMissed,
			DurationSeconds: stats.CallDurationSeconds,
		},
		Hourly: hourly,
	})
}
