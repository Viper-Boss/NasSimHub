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

	// Task-manager style CPU details. Cache sizes are per-core L1 slices and
	// the shared L2/L3 as exposed by sysfs; 0 means "not exposed".
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

	// Real CPU utilization from /proc/stat deltas (0-100). Load averages on a
	// single-core module overstate usage, so the dashboard uses this.
	CPUUsagePercent float64 `json:"cpu_usage_percent"`

	// HostUptimeSeconds is the kernel uptime of the machine the agent runs
	// on (containers share the host kernel, so /proc/uptime is the real
	// host uptime). 0 when unavailable.
	HostUptimeSeconds int64 `json:"host_uptime_seconds"`

	// Serving-cell radio details parsed from AT+QENG="servingcell"; nil when
	// the modem does not answer.
	Cellular *CellularServingCell `json:"cellular"`

	// ModuleChannel reports the health of the ADB channel into the module's
	// own system: "ok" for fresh data, "stale" when the last known snapshot
	// is served because a refresh failed, "unavailable" when no module data
	// exists at all. Empty on platforms without a module shell.
	ModuleChannel string `json:"module_channel,omitempty"`

	Interfaces []NetworkInterfaceCounters `json:"interfaces"`
}

// CellularServingCell is one observation of the cell the modem is camped on.
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

// SystemMetricsProvider is implemented by providers that can observe host
// and modem health. It is optional: the HTTP API degrades gracefully when a
// provider does not implement it.
type SystemMetricsProvider interface {
	SystemMetrics(ctx context.Context) (SystemMetrics, error)
}
