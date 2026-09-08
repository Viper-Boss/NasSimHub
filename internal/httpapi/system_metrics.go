package httpapi

import (
	"net/http"
	"time"
)

type systemMetricsResponse struct {
	ObservedAt string  `json:"observed_at"`
	LoadRaw1   float64 `json:"load_raw_1"`
	LoadRaw5   float64 `json:"load_raw_5"`
	LoadRaw15  float64 `json:"load_raw_15"`
	LoadPct1   float64 `json:"load_percent_1"`
	LoadPct5   float64 `json:"load_percent_5"`
	LoadPct15  float64 `json:"load_percent_15"`
	Cores      int     `json:"cores"`

	MemoryTotalBytes  uint64   `json:"memory_total_bytes"`
	MemoryAvailBytes  uint64   `json:"memory_available_bytes"`
	MemoryUsedPercent float64  `json:"memory_used_percent"`
	FlashPath         string   `json:"flash_path"`
	FlashTotalBytes   uint64   `json:"flash_total_bytes"`
	FlashFreeBytes    uint64   `json:"flash_free_bytes"`
	FlashUsedPercent  float64  `json:"flash_used_percent"`
	ModemTempCelsius  *float64 `json:"modem_temp_celsius"`

	Interfaces []systemMetricsInterface `json:"interfaces"`
	Available  bool                     `json:"available"`
	Error      string                   `json:"error,omitempty"`
}

type systemMetricsInterface struct {
	Name    string `json:"name"`
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

func (api *API) systemMetrics(response http.ResponseWriter, request *http.Request) {
	result := systemMetricsResponse{
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Interfaces: []systemMetricsInterface{},
	}
	if api.deviceConfigurations == nil {
		result.Error = "device configuration service is not configured"
		writeJSON(response, http.StatusOK, result)
		return
	}
	metrics, err := api.deviceConfigurations.SystemMetrics(request.Context())
	if err != nil {
		result.Error = err.Error()
		writeJSON(response, http.StatusOK, result)
		return
	}
	result.ObservedAt = metrics.ObservedAt.UTC().Format(time.RFC3339Nano)
	result.LoadRaw1 = metrics.LoadRaw1
	result.LoadRaw5 = metrics.LoadRaw5
	result.LoadRaw15 = metrics.LoadRaw15
	result.LoadPct1 = metrics.LoadPercent1
	result.LoadPct5 = metrics.LoadPercent5
	result.LoadPct15 = metrics.LoadPercent
	result.Cores = metrics.Cores
	result.MemoryTotalBytes = metrics.MemoryTotalBytes
	result.MemoryAvailBytes = metrics.MemoryAvailableBytes
	result.MemoryUsedPercent = metrics.MemoryUsedPercent
	result.FlashPath = metrics.FlashPath
	result.FlashTotalBytes = metrics.FlashTotalBytes
	result.FlashFreeBytes = metrics.FlashFreeBytes
	result.FlashUsedPercent = metrics.FlashUsedPercent
	result.ModemTempCelsius = metrics.ModemTempCelsius
	for _, item := range metrics.Interfaces {
		result.Interfaces = append(result.Interfaces, systemMetricsInterface{
			Name:    item.Name,
			RxBytes: item.RxBytes,
			TxBytes: item.TxBytes,
		})
	}
	result.Available = true
	writeJSON(response, http.StatusOK, result)
}
