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
	MemoryCachedBytes    uint64   `json:"memory_cached_bytes"`
	MemoryBuffersBytes   uint64   `json:"memory_buffers_bytes"`
	FlashPath            string   `json:"flash_path"`
	FlashTotalBytes      uint64   `json:"flash_total_bytes"`
	FlashFreeBytes       uint64   `json:"flash_free_bytes"`
	FlashUsedPercent     float64  `json:"flash_used_percent"`
	FlashFsType          string   `json:"flash_fs_type"`
	FlashDeviceModel     string   `json:"flash_device_model"`
	FlashDevicePath      string   `json:"flash_device_path"`
	ModemTempCelsius     *float64 `json:"modem_temp_celsius"`

	CPUModelName    string   `json:"cpu_model_name"`
	CPUUsagePercent float64  `json:"cpu_usage_percent"`
	CPUCurrentMHz   float64  `json:"cpu_current_mhz"`
	CPUMaxMHz       float64  `json:"cpu_max_mhz"`
	CPUL1DataKB     uint64   `json:"cpu_l1_data_kb"`
	CPUL1InstKB     uint64   `json:"cpu_l1_inst_kb"`
	CPUL2KB         uint64   `json:"cpu_l2_kb"`
	CPUL3KB         uint64   `json:"cpu_l3_kb"`
	CPUTempCelsius  *float64 `json:"cpu_temp_celsius"`
	ProcessCount    int      `json:"process_count"`
	ThreadCount     int      `json:"thread_count"`

	Cellular *CellularServingCell `json:"cellular"`

	// Health of the ADB channel into the module's own system: ok/stale/
	// unavailable (see the agent's domain.SystemMetrics).
	ModuleChannel string `json:"module_channel,omitempty"`

	Interfaces []NetworkInterfaceCounters `json:"interfaces"`
}

type CellularServingCell struct {
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
