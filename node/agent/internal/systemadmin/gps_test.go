package systemadmin

import (
	"fmt"
	"testing"
	"time"
)

func TestGPSRejectsMissingAndStaleFix(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cap := `{"modem":{"location":{"capabilities":["gps-raw"],"enabled":["gps-raw"],"gps":{"refresh-rate":"30"}}}}`
	for _, coords := range []string{`"latitude":"--","longitude":"--","utc":"--"`, `"latitude":"NaN","longitude":"3","utc":"2026-01-01T12:00:00Z"`, `"latitude":"40","longitude":"3","utc":"2026-01-01T11:00:00Z"`} {
		s, e := ParseGPS(cap, `{"modem":{"location":{"gps":{`+coords+`}}}}`, now)
		if e != nil || s.Fixed || s.Latitude != nil {
			t.Fatal(s, e)
		}
	}
	pos := fmt.Sprintf(`{"modem":{"location":{"gps":{"latitude":"40","longitude":"3","utc":"%s"}}}}`, now.Format(time.RFC3339))
	s, e := ParseGPS(cap, pos, now)
	if e != nil || !s.Fixed || !s.Supported || !s.Enabled {
		t.Fatal(s, e)
	}
}
