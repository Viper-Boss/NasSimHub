package systemadmin

import (
	"encoding/json"
	"math"
	"strconv"
	"time"
)

type GPSStatus struct {
	Supported bool     `json:"supported"`
	Enabled   bool     `json:"enabled"`
	Fixed     bool     `json:"fixed"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
	UTC       string   `json:"utc,omitempty"`
	Refresh   int      `json:"refresh"`
}

func ParseGPS(capabilities, position string, now time.Time) (GPSStatus, error) {
	var doc struct {
		Modem struct {
			Location struct {
				Capabilities []string
				Enabled      []string
				GPS          struct {
					Refresh string `json:"refresh-rate"`
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(capabilities), &doc); err != nil {
		return GPSStatus{}, err
	}
	l := doc.Modem.Location
	s := GPSStatus{}
	s.Refresh, _ = strconv.Atoi(l.GPS.Refresh)
	for _, v := range l.Capabilities {
		if v == "gps-raw" {
			s.Supported = true
		}
	}
	for _, v := range l.Enabled {
		if v == "gps-raw" {
			s.Enabled = true
		}
	}
	if !s.Enabled || position == "" {
		return s, nil
	}
	var pos struct {
		Modem struct {
			Location struct {
				GPS struct {
					Latitude  string
					Longitude string
					UTC       string
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(position), &pos); err != nil {
		return s, err
	}
	p := pos.Modem.Location.GPS
	lat, e1 := strconv.ParseFloat(p.Latitude, 64)
	lon, e2 := strconv.ParseFloat(p.Longitude, 64)
	stamp, e3 := time.Parse(time.RFC3339, p.UTC)
	window := time.Duration(s.Refresh*2) * time.Second
	if window < 60*time.Second {
		window = 60 * time.Second
	}
	if e1 == nil && e2 == nil && e3 == nil && !math.IsNaN(lat) && !math.IsInf(lat, 0) && !math.IsNaN(lon) && !math.IsInf(lon, 0) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 && now.Sub(stamp) <= window && stamp.Sub(now) < 10*time.Second {
		s.Fixed = true
		s.Latitude = &lat
		s.Longitude = &lon
		s.UTC = p.UTC
	}
	return s, nil
}
