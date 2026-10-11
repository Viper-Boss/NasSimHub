//go:build linux

package systemadmin

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Fixed GPS-only commands; no paths, shell fragments or modem selection accepted.
func (m *Manager) gps(ctx context.Context, r Request) (Status, error) {
	cap, e := m.exec(ctx, "", "/usr/bin/mmcli", "-m", "any", "--location-status", "-J")
	if e != nil {
		return Status{}, errors.New("GPS status unavailable")
	}
	before, e := ParseGPS(cap, "", time.Now())
	if e != nil || !before.Supported {
		return Status{}, errors.New("GPS not supported by this modem")
	}
	if r.Action == "gps_power" {
		args := []string{"-m", "any", "--location-disable-gps-raw", "--location-disable-gps-nmea"}
		if r.Enabled {
			args = []string{"-m", "any", "--location-enable-gps-raw", "--location-enable-gps-nmea"}
		}
		if _, e = m.exec(ctx, "", "/usr/bin/mmcli", args...); e != nil {
			return Status{}, errors.New("GPS power change failed")
		}
	}
	if r.Action == "gps_interval" {
		if _, e = m.exec(ctx, "", "/usr/bin/mmcli", "-m", "any", fmt.Sprintf("--location-set-gps-refresh-rate=%d", r.GPSRefresh)); e != nil {
			return Status{}, errors.New("GPS interval change failed")
		}
	}
	if r.Action != "gps_status" {
		cap, e = m.exec(ctx, "", "/usr/bin/mmcli", "-m", "any", "--location-status", "-J")
		if e != nil {
			return Status{}, errors.New("GPS status unavailable")
		}
	}
	status, e := ParseGPS(cap, "", time.Now())
	if e != nil {
		return Status{}, errors.New("GPS status invalid")
	}
	if r.Action == "gps_power" && status.Enabled != r.Enabled {
		return Status{}, errors.New("GPS power change not confirmed")
	}
	if status.Enabled {
		pos, e := m.exec(ctx, "", "/usr/bin/mmcli", "-m", "any", "--location-get", "-J")
		if e == nil {
			if actual, e := ParseGPS(cap, pos, time.Now()); e == nil {
				status = actual
			}
		}
	}
	return Status{GPS: &status}, nil
}
