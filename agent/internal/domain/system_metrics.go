package domain

import (
	"context"
	"time"
)

// SystemMetricSample is a single host/modem observation for the dashboard
// modem-status panel: busy state of the NAS (CPU load, memory, flash), the
// 4G data port counters, and the module temperature when the modem reports
// one. All fields are best-effort; a zero/nil field means "not available on
// this platform" rather than an error.
type SystemMetrics struct {
	ObservedAt time.Time `json:"observed_at"`

	// 1/5/15-minute load average expressed both ways: raw (Linux style) and
	// percent of total CPU capacity (load / cores * 100), which is what the
	// dashboard shows.
	LoadRaw1     float64 `json:"load_raw_1"`
	LoadRaw5     float64 `json:"load_raw_5"`
	LoadRaw15    float64 `json:"load_raw_15"`
	LoadPercent1 float64 `json:"load_percent_1"`
	LoadPercent5 float64 `json:"load_percent_5"`
	LoadPercent  float64 `json:"load_percent_15"`
	Cores        int     `json:"cores"`

	MemoryTotalBytes     uint64   `json:"memory_total_bytes"`
	MemoryAvailableBytes uint64   `json:"memory_available_bytes"`
	MemoryUsedPercent    float64  `json:"memory_used_percent"`
	FlashPath            string   `json:"flash_path"`
	FlashTotalBytes      uint64   `json:"flash_total_bytes"`
	FlashFreeBytes       uint64   `json:"flash_free_bytes"`
	FlashUsedPercent     float64  `json:"flash_used_percent"`
	ModemTempCelsius     *float64 `json:"modem_temp_celsius"`

	Interfaces []NetworkInterfaceCounters `json:"interfaces"`
}

type NetworkInterfaceCounters struct {
	Name    string `json:"name"`
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

// SystemMetricsProvider is implemented by providers that can observe host
// and modem health. It is optional: the HTTP API degrades gracefully when a
// provider does not implement it.
type SystemMetricsProvider interface {
	SystemMetrics(ctx context.Context) (SystemMetrics, error)
}
