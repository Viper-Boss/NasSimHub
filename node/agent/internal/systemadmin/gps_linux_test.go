//go:build linux

package systemadmin

import (
	"context"
	"strings"
	"testing"
)

func TestGPSOnlyUsesFixedCommandsAndChecksActualState(t *testing.T) {
	enabled := false
	calls := []string{}
	m := &Manager{Run: func(_ context.Context, input, name string, args ...string) (string, error) {
		v := strings.Join(args, " ")
		calls = append(calls, name+" "+v)
		if input != "" || name != "/usr/bin/mmcli" {
			t.Fatal("unbounded GPS command")
		}
		switch {
		case strings.Contains(v, "--location-status"):
			set := `[]`
			if enabled {
				set = `["gps-raw","gps-nmea"]`
			}
			return `{"modem":{"location":{"capabilities":["gps-raw"],"enabled":` + set + `,"gps":{"refresh-rate":"30"}}}}`, nil
		case strings.Contains(v, "--location-enable-gps-raw"):
			enabled = true
		case strings.Contains(v, "--location-disable-gps-raw"):
			enabled = false
		}
		return `{}`, nil
	}}
	if _, e := m.Execute(context.Background(), Request{Action: "gps_power", User: "root", Enabled: true}); e == nil || len(calls) != 0 {
		t.Fatal("accepted account arguments")
	}
	s, e := m.Execute(context.Background(), Request{Action: "gps_power", Enabled: true})
	if e != nil || s.GPS == nil || !s.GPS.Enabled {
		t.Fatal(s, e)
	}
	s, e = m.Execute(context.Background(), Request{Action: "gps_power", Enabled: false})
	if e != nil || s.GPS.Enabled {
		t.Fatal(s, e)
	}
	for _, r := range []Request{{Action: "gps_interval", GPSRefresh: 0}, {Action: "gps_interval", GPSRefresh: 121}, {Action: "status", GPSRefresh: 1}, {Action: "gps_power", Password: "x"}} {
		if Validate(r) == nil {
			t.Fatal("accepted invalid request", r)
		}
	}
}
