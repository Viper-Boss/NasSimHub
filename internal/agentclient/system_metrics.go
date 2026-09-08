package agentclient

import (
	"context"
	"net/http"
	"time"
)

// SystemMetrics mirrors the agent's host/modem health snapshot used by the
// dashboard modem-status panel.
type SystemMetrics struct {
	ObservedAt time.Time `json:"observed_at"`

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

func (client *Client) SystemMetrics(ctx context.Context) (SystemMetrics, error) {
	var metrics SystemMetrics
	if err := client.doJSON(
		ctx,
		http.MethodGet,
		"/v1/system/metrics",
		nil,
		http.StatusOK,
		&metrics,
	); err != nil {
		return SystemMetrics{}, err
	}
	if metrics.Interfaces == nil {
		metrics.Interfaces = []NetworkInterfaceCounters{}
	}
	return metrics, nil
}
