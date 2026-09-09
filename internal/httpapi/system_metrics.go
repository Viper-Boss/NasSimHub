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

	MemoryTotalBytes   uint64   `json:"memory_total_bytes"`
	MemoryAvailBytes   uint64   `json:"memory_available_bytes"`
	MemoryUsedPercent  float64  `json:"memory_used_percent"`
	MemoryCachedBytes  uint64   `json:"memory_cached_bytes"`
	MemoryBuffersBytes uint64   `json:"memory_buffers_bytes"`
	FlashPath          string   `json:"flash_path"`
	FlashTotalBytes    uint64   `json:"flash_total_bytes"`
	FlashFreeBytes     uint64   `json:"flash_free_bytes"`
	FlashUsedPercent   float64  `json:"flash_used_percent"`
	FlashFsType        string   `json:"flash_fs_type"`
	FlashDeviceModel   string   `json:"flash_device_model"`
	FlashDevicePath    string   `json:"flash_device_path"`
	ModemTempCelsius   *float64 `json:"modem_temp_celsius"`

	CPUModelName   string   `json:"cpu_model_name"`
	CPUCurrentMHz  float64  `json:"cpu_current_mhz"`
	CPUMaxMHz      float64  `json:"cpu_max_mhz"`
	CPUL1DataKB    uint64   `json:"cpu_l1_data_kb"`
	CPUL1InstKB    uint64   `json:"cpu_l1_inst_kb"`
	CPUL2KB        uint64   `json:"cpu_l2_kb"`
	CPUL3KB        uint64   `json:"cpu_l3_kb"`
	CPUTempCelsius *float64 `json:"cpu_temp_celsius"`
	ProcessCount   int      `json:"process_count"`
	ThreadCount    int      `json:"thread_count"`

	Cellular      *systemMetricsCellular   `json:"cellular"`
	ModuleChannel string                   `json:"module_channel,omitempty"`
	Interfaces    []systemMetricsInterface `json:"interfaces"`
	Available     bool                     `json:"available"`
	Error         string                   `json:"error,omitempty"`
}

type systemMetricsCellular struct {
	State                string  `json:"state"`
	RAT                  string  `json:"rat"`
	Duplex               string  `json:"duplex"`
	PLMN                 string  `json:"plmn"`
	Band                 string  `json:"band"`
	EARFCN               uint32  `json:"earfcn"`
	FrequencyMHz         float64 `json:"frequency_mhz"`
	UplinkBandwidthMHz   float64 `json:"uplink_bandwidth_mhz"`
	DownlinkBandwidthMHz float64 `json:"downlink_bandwidth_mhz"`
	PCI                  uint16  `json:"pci"`
	CellIDHex            string  `json:"cell_id_hex"`
	TACHex               string  `json:"tac_hex"`
	RSRP                 int     `json:"rsrp"`
	RSRQ                 int     `json:"rsrq"`
	RSSI                 int     `json:"rssi"`
	SINR                 int     `json:"sinr"`
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
	result.MemoryCachedBytes = metrics.MemoryCachedBytes
	result.MemoryBuffersBytes = metrics.MemoryBuffersBytes
	result.FlashPath = metrics.FlashPath
	result.FlashTotalBytes = metrics.FlashTotalBytes
	result.FlashFreeBytes = metrics.FlashFreeBytes
	result.FlashUsedPercent = metrics.FlashUsedPercent
	result.FlashFsType = metrics.FlashFsType
	result.FlashDeviceModel = metrics.FlashDeviceModel
	result.FlashDevicePath = metrics.FlashDevicePath
	result.ModemTempCelsius = metrics.ModemTempCelsius
	result.CPUModelName = metrics.CPUModelName
	result.CPUCurrentMHz = metrics.CPUCurrentMHz
	result.CPUMaxMHz = metrics.CPUMaxMHz
	result.CPUL1DataKB = metrics.CPUL1DataKB
	result.CPUL1InstKB = metrics.CPUL1InstKB
	result.CPUL2KB = metrics.CPUL2KB
	result.CPUL3KB = metrics.CPUL3KB
	result.CPUTempCelsius = metrics.CPUTempCelsius
	result.ProcessCount = metrics.ProcessCount
	result.ThreadCount = metrics.ThreadCount
	if metrics.Cellular != nil {
		result.Cellular = &systemMetricsCellular{
			State:                metrics.Cellular.State,
			RAT:                  metrics.Cellular.RAT,
			Duplex:               metrics.Cellular.Duplex,
			PLMN:                 metrics.Cellular.PLMN,
			Band:                 metrics.Cellular.Band,
			EARFCN:               metrics.Cellular.EARFCN,
			FrequencyMHz:         metrics.Cellular.FrequencyMHz,
			UplinkBandwidthMHz:   metrics.Cellular.UplinkBandwidthMHz,
			DownlinkBandwidthMHz: metrics.Cellular.DownlinkBandwidthMHz,
			PCI:                  metrics.Cellular.PCI,
			CellIDHex:            metrics.Cellular.CellIDHex,
			TACHex:               metrics.Cellular.TACHex,
			RSRP:                 metrics.Cellular.RSRP,
			RSRQ:                 metrics.Cellular.RSRQ,
			RSSI:                 metrics.Cellular.RSSI,
			SINR:                 metrics.Cellular.SINR,
		}
	}
	for _, item := range metrics.Interfaces {
		result.Interfaces = append(result.Interfaces, systemMetricsInterface{
			Name:    item.Name,
			RxBytes: item.RxBytes,
			TxBytes: item.TxBytes,
		})
	}
	result.ModuleChannel = metrics.ModuleChannel
	result.Available = true
	writeJSON(response, http.StatusOK, result)
}
