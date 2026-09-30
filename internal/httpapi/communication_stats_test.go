package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/store"
)

// The real store must be what the endpoint finds; this fails to compile if
// the method's signature drifts from the one the handler asserts.
var _ communicationStatsSource = (*store.Store)(nil)

type statsRepository struct {
	fakeRepository
	stats  store.CommunicationStats
	window int
}

func (repository *statsRepository) CommunicationStats(
	_ context.Context,
	_ time.Time,
	hours int,
) (store.CommunicationStats, error) {
	repository.window = hours
	return repository.stats, nil
}

func TestCommunicationStatsAreServedInTheCardsShape(t *testing.T) {
	hourly := make([]store.CommunicationHour, 24)
	hourly[23] = store.CommunicationHour{Messages: 2, Calls: 1}
	repository := &statsRepository{stats: store.CommunicationStats{
		WindowHours:         24,
		SMSReceived:         23,
		SMSSent:             4,
		SMSFailed:           0,
		SMSVerification:     17,
		CallsIncoming:       6,
		CallsOutgoing:       3,
		CallsMissed:         1,
		CallDurationSeconds: 2520,
		Hourly:              hourly,
	}}
	api, err := New(repository, Options{disableAuthentication: true, disableLicenseEnforcement: true})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/stats/communication", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("stats answered %d: %s", recorder.Code, recorder.Body.String())
	}
	if repository.window != 24 {
		t.Fatalf("asked the store for %d hours", repository.window)
	}
	var body struct {
		WindowHours int            `json:"window_hours"`
		SMS         map[string]int `json:"sms"`
		Calls       map[string]int `json:"calls"`
		Hourly      []struct {
			Hour     int `json:"hour"`
			Messages int `json:"messages"`
			Calls    int `json:"calls"`
		} `json:"hourly"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.WindowHours != 24 || body.SMS["received"] != 23 || body.SMS["verification"] != 17 ||
		body.Calls["missed"] != 1 || body.Calls["duration_seconds"] != 2520 {
		t.Fatalf("body = %s", recorder.Body.String())
	}
	// A real zero is sent as a zero: it is a count, not a missing reading.
	if failed, present := body.SMS["failed"]; !present || failed != 0 {
		t.Fatalf("sms.failed = %v, %v", failed, present)
	}
	if len(body.Hourly) != 24 || body.Hourly[23].Hour != 23 || body.Hourly[23].Messages != 2 {
		t.Fatalf("hourly = %+v", body.Hourly)
	}
}

func TestWithoutACountingStoreTheStatsAreNotServed(t *testing.T) {
	api, err := New(&fakeRepository{}, Options{disableAuthentication: true, disableLicenseEnforcement: true})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/stats/communication", nil))
	// Anything but 200 is "not wired" to the card, which then shows dashes --
	// never a row of zeros nothing counted.
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("stats answered %d without a store that counts", recorder.Code)
	}
}
