package modemmanager

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// Module-side system metrics for the dashboard task-manager panel. The panel
// describes the QDC507 module itself, so every CPU, memory and flash value is
// collected inside the module's own Linux system over the ADB shell instead
// of from the NAS host. A single batched shell command feeds the whole
// snapshot and the result is cached briefly, mirroring the modem temperature
// cache. Everything is best-effort: a missing value stays zero.

const (
	moduleMetricsTTL   = 30 * time.Second
	moduleShellTimeout = 20 * time.Second
)

// moduleDetailsCommand gathers module CPU identity/frequency/load, memory,
// MTD flash layout, filesystem type, thermal zones and the usage of the
// mounted UBI volumes in one round trip. Only plain busybox ash features are
// used (no awk), and every value is tagged for line parsing.
const moduleDetailsCommand = "echo CORES:$(grep -c ^processor /proc/cpuinfo); " +
	"echo MODEL:$(grep -m1 '^model name' /proc/cpuinfo | cut -d: -f2-); " +
	"echo HW:$(grep -m1 ^Hardware /proc/cpuinfo | cut -d: -f2-); " +
	"echo CURF:$(cat /sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_cur_freq 2>/dev/null); " +
	"echo MAXF:$(cat /sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq 2>/dev/null); " +
	"echo LOAD:$(cat /proc/loadavg); " +
	"grep -E '^(MemTotal|MemFree|MemAvailable|Cached|Buffers):' /proc/meminfo; " +
	"echo PROCS:$(ls -d /proc/[0-9]* | wc -l); " +
	"s=0; for v in $(cat /sys/class/mtd/mtd*/size); do s=$((s+v)); done; echo MTDSZ:$s; " +
	"echo MTDTYPE:$(cat /sys/class/mtd/mtd0/type 2>/dev/null); " +
	"echo FSTYPE:$(grep ^ubi0:rootfs /proc/mounts | cut -d ' ' -f3); " +
	"for z in /sys/class/thermal/thermal_zone*; do echo TZ:$(cat $z/type 2>/dev/null):$(cat $z/temp 2>/dev/null); done; " +
	"df -k / /data /cache 2>/dev/null | tail -n +2"

type moduleSnapshot struct {
	Cores                int
	LoadRaw1             float64
	LoadRaw5             float64
	LoadRaw15            float64
	CPUModelName         string
	CPUCurrentMHz        float64
	CPUMaxMHz            float64
	CPUTempCelsius       *float64
	ProcessCount         int
	ThreadCount          int
	MemoryTotalBytes     uint64
	MemoryAvailableBytes uint64
	MemoryUsedPercent    float64
	MemoryCachedBytes    uint64
	MemoryBuffersBytes   uint64
	FlashTotalBytes      uint64
	FlashFreeBytes       uint64
	FlashUsedPercent     float64
	FlashPath            string
	FlashFsType          string
	FlashDeviceModel     string
	mtdTotalBytes        uint64
	mtdType              string
	fallbackTemp         *float64
}

type moduleSnapshotCache struct {
	mu        sync.Mutex
	value     *moduleSnapshot
	readAt    time.Time
	inFlight  bool
	succeeded bool
}

// Module channel health as reported in SystemMetrics.ModuleChannel.
const (
	moduleChannelOK          = "ok"
	moduleChannelStale       = "stale"
	moduleChannelUnavailable = "unavailable"
)

func (p *Provider) collectModuleDetails(ctx context.Context, metrics *domain.SystemMetrics) {
	if p.moduleShell == nil {
		metrics.ModuleChannel = moduleChannelUnavailable
		return
	}
	snapshot, stale := p.cachedModuleSnapshot(ctx)
	if snapshot == nil {
		metrics.ModuleChannel = moduleChannelUnavailable
		return
	}
	if stale {
		metrics.ModuleChannel = moduleChannelStale
	} else {
		metrics.ModuleChannel = moduleChannelOK
	}
	metrics.Cores = snapshot.Cores
	metrics.LoadRaw1 = snapshot.LoadRaw1
	metrics.LoadRaw5 = snapshot.LoadRaw5
	metrics.LoadRaw15 = snapshot.LoadRaw15
	if snapshot.Cores > 0 {
		metrics.LoadPercent1 = percentOfCapacity(snapshot.LoadRaw1, snapshot.Cores)
		metrics.LoadPercent5 = percentOfCapacity(snapshot.LoadRaw5, snapshot.Cores)
		metrics.LoadPercent = percentOfCapacity(snapshot.LoadRaw15, snapshot.Cores)
	}
	metrics.CPUModelName = snapshot.CPUModelName
	metrics.CPUCurrentMHz = snapshot.CPUCurrentMHz
	metrics.CPUMaxMHz = snapshot.CPUMaxMHz
	metrics.CPUTempCelsius = snapshot.CPUTempCelsius
	metrics.ProcessCount = snapshot.ProcessCount
	metrics.ThreadCount = snapshot.ThreadCount
	metrics.MemoryTotalBytes = snapshot.MemoryTotalBytes
	metrics.MemoryAvailableBytes = snapshot.MemoryAvailableBytes
	metrics.MemoryUsedPercent = snapshot.MemoryUsedPercent
	metrics.MemoryCachedBytes = snapshot.MemoryCachedBytes
	metrics.MemoryBuffersBytes = snapshot.MemoryBuffersBytes
	metrics.FlashTotalBytes = snapshot.FlashTotalBytes
	metrics.FlashFreeBytes = snapshot.FlashFreeBytes
	metrics.FlashUsedPercent = snapshot.FlashUsedPercent
	metrics.FlashPath = snapshot.FlashPath
	metrics.FlashFsType = snapshot.FlashFsType
	metrics.FlashDeviceModel = snapshot.FlashDeviceModel
}

// cachedModuleSnapshot returns the cached module observation plus whether it
// is stale. A fresh TTL hit or a successful probe returns (snapshot, false).
// When a refresh probe fails but a previous snapshot exists, the last known
// values are served with stale=true so the dashboard keeps rendering instead
// of dropping to empty boxes. Never blocks longer than the shell timeout.
func (p *Provider) cachedModuleSnapshot(ctx context.Context) (*moduleSnapshot, bool) {
	p.moduleSnapshot.mu.Lock()
	if p.moduleSnapshot.inFlight ||
		(p.moduleSnapshot.succeeded && time.Since(p.moduleSnapshot.readAt) < moduleMetricsTTL) {
		value := p.moduleSnapshot.value
		stale := p.moduleSnapshot.inFlight || time.Since(p.moduleSnapshot.readAt) >= moduleMetricsTTL
		p.moduleSnapshot.mu.Unlock()
		return value, value != nil && stale
	}
	p.moduleSnapshot.inFlight = true
	p.moduleSnapshot.mu.Unlock()

	snapshot := p.queryModuleSnapshot(ctx)

	p.moduleSnapshot.mu.Lock()
	p.moduleSnapshot.inFlight = false
	p.moduleSnapshot.readAt = time.Now()
	if snapshot != nil {
		p.moduleSnapshot.value = snapshot
		p.moduleSnapshot.succeeded = true
		p.moduleSnapshot.mu.Unlock()
		return snapshot, false
	}
	// Refresh failed: keep serving the last known snapshot, marked stale.
	if stale := p.moduleSnapshot.value; stale != nil {
		p.moduleSnapshot.mu.Unlock()
		return stale, true
	}
	p.moduleSnapshot.mu.Unlock()
	return nil, false
}

func (p *Provider) queryModuleSnapshot(ctx context.Context) *moduleSnapshot {
	if p.moduleShell == nil {
		return nil
	}
	output, err := p.moduleShell.ModuleShell(ctx, moduleDetailsCommand, moduleShellTimeout)
	if err != nil && strings.TrimSpace(output) == "" {
		return nil
	}
	return parseModuleSnapshot(output)
}

func parseModuleSnapshot(output string) *moduleSnapshot {
	snapshot := &moduleSnapshot{}
	meminfo := make(map[string]uint64)
	rootVolume := dfVolume{}
	dataVolume := dfVolume{}
	dataSeen := false
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "CORES:"):
			snapshot.Cores = atoiTrimmed(strings.TrimPrefix(line, "CORES:"))
		case strings.HasPrefix(line, "MODEL:"):
			if snapshot.CPUModelName == "" {
				snapshot.CPUModelName = cleanModuleText(strings.TrimPrefix(line, "MODEL:"))
			}
		case strings.HasPrefix(line, "HW:"):
			if value := cleanModuleText(strings.TrimPrefix(line, "HW:")); value != "" {
				snapshot.CPUModelName = strings.ReplaceAll(
					value, "Qualcomm Technologies, Inc ", "Qualcomm ")
			}
		case strings.HasPrefix(line, "CURF:"):
			snapshot.CPUCurrentMHz = float64(atoiTrimmed(strings.TrimPrefix(line, "CURF:"))) / 1000
		case strings.HasPrefix(line, "MAXF:"):
			snapshot.CPUMaxMHz = float64(atoiTrimmed(strings.TrimPrefix(line, "MAXF:"))) / 1000
		case strings.HasPrefix(line, "LOAD:"):
			parseModuleLoadavg(strings.TrimPrefix(line, "LOAD:"), snapshot)
		case isMeminfoLine(line):
			key, value := parseMeminfoLine(line)
			if key != "" {
				meminfo[key] = value
			}
		case strings.HasPrefix(line, "PROCS:"):
			snapshot.ProcessCount = atoiTrimmed(strings.TrimPrefix(line, "PROCS:"))
		case strings.HasPrefix(line, "MTDSZ:"),
			strings.HasPrefix(line, "MTDTYPE:"),
			strings.HasPrefix(line, "FSTYPE:"):
			parseModuleFlashTag(line, snapshot)
		case strings.HasPrefix(line, "TZ:"):
			parseModuleZoneTemp(strings.TrimPrefix(line, "TZ:"), snapshot)
		case isDfLine(line):
			volume := parseDfLine(line)
			if volume.device == "" {
				continue
			}
			if strings.HasPrefix(volume.device, "ubi0:") {
				rootVolume = volume
			} else if !dataSeen {
				dataVolume = volume
				dataSeen = true
			}
		}
	}
	applyModuleMemory(snapshot, meminfo)
	applyModuleFlash(snapshot, rootVolume, dataVolume)
	if snapshot.CPUTempCelsius == nil {
		snapshot.CPUTempCelsius = snapshot.fallbackTemp
	}
	return snapshot
}

func parseModuleLoadavg(value string, snapshot *moduleSnapshot) {
	fields := strings.Fields(value)
	if len(fields) >= 3 {
		load1, err1 := strconv.ParseFloat(fields[0], 64)
		load5, err5 := strconv.ParseFloat(fields[1], 64)
		load15, err15 := strconv.ParseFloat(fields[2], 64)
		if err1 == nil && err5 == nil && err15 == nil {
			snapshot.LoadRaw1 = load1
			snapshot.LoadRaw5 = load5
			snapshot.LoadRaw15 = load15
		}
	}
	// The fourth field is "running/total scheduling entities"; the total is
	// the module's thread count.
	if len(fields) >= 4 {
		if _, total, found := strings.Cut(fields[3], "/"); found {
			snapshot.ThreadCount = atoiTrimmed(total)
		}
	}
}

func isMeminfoLine(line string) bool {
	for _, key := range []string{"MemTotal:", "MemFree:", "MemAvailable:", "Cached:", "Buffers:"} {
		if strings.HasPrefix(line, key) {
			return true
		}
	}
	return false
}

func parseMeminfoLine(line string) (string, uint64) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", 0
	}
	fields := strings.Fields(parts[1])
	if len(fields) == 0 {
		return "", 0
	}
	value, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return "", 0
	}
	return parts[0], value * 1024
}

func applyModuleMemory(snapshot *moduleSnapshot, values map[string]uint64) {
	total := values["MemTotal"]
	if total == 0 {
		return
	}
	available := values["MemAvailable"]
	if available == 0 {
		available = values["MemFree"]
	}
	snapshot.MemoryTotalBytes = total
	snapshot.MemoryAvailableBytes = available
	snapshot.MemoryUsedPercent = float64(total-available) / float64(total) * 100
	snapshot.MemoryCachedBytes = values["Cached"]
	snapshot.MemoryBuffersBytes = values["Buffers"]
}

func parseModuleFlashTag(line string, snapshot *moduleSnapshot) {
	switch {
	case strings.HasPrefix(line, "MTDSZ:"):
		if size := uint64(atoiTrimmed(strings.TrimPrefix(line, "MTDSZ:"))); size > 0 {
			snapshot.mtdTotalBytes = size
		}
	case strings.HasPrefix(line, "MTDTYPE:"):
		if value := cleanModuleText(strings.TrimPrefix(line, "MTDTYPE:")); value != "" {
			snapshot.mtdType = value
		}
	case strings.HasPrefix(line, "FSTYPE:"):
		if value := cleanModuleText(strings.TrimPrefix(line, "FSTYPE:")); value != "" {
			snapshot.FlashFsType = value
		}
	}
}

func parseModuleZoneTemp(value string, snapshot *moduleSnapshot) {
	kind, temp, found := strings.Cut(value, ":")
	if !found {
		return
	}
	milli, err := strconv.ParseFloat(strings.TrimSpace(temp), 64)
	if err != nil {
		return
	}
	// The module exposes plain Celsius (for example 43); tolerate drivers
	// that report millidegrees instead.
	celsius := milli
	if celsius >= 1000 {
		celsius /= 1000
	}
	if celsius <= -30 || celsius >= 150 {
		return
	}
	isCPU := strings.Contains(kind, "tsens")
	if isCPU {
		if snapshot.CPUTempCelsius == nil || celsius > *snapshot.CPUTempCelsius {
			value := celsius
			snapshot.CPUTempCelsius = &value
		}
		return
	}
	// Fallback: remember the hottest non-CPU zone in case no tsens zone exists.
	if snapshot.fallbackTemp == nil || celsius > *snapshot.fallbackTemp {
		value := celsius
		snapshot.fallbackTemp = &value
	}
}

type dfVolume struct {
	device string
	total  uint64
	free   uint64
}

func isDfLine(line string) bool {
	return strings.Contains(line, "%") && len(strings.Fields(line)) >= 5
}

func parseDfLine(line string) dfVolume {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return dfVolume{}
	}
	total, errTotal := strconv.ParseUint(fields[1], 10, 64)
	free, errFree := strconv.ParseUint(fields[3], 10, 64)
	if errTotal != nil || errFree != nil {
		return dfVolume{}
	}
	return dfVolume{device: fields[0], total: total * 1024, free: free * 1024}
}

func applyModuleFlash(snapshot *moduleSnapshot, rootVolume, dataVolume dfVolume) {
	volumes := make([]dfVolume, 0, 2)
	if rootVolume.device != "" {
		volumes = append(volumes, rootVolume)
	}
	if dataVolume.device != "" {
		volumes = append(volumes, dataVolume)
	}
	if len(volumes) == 0 {
		return
	}
	var total, free uint64
	for _, volume := range volumes {
		total += volume.total
		free += volume.free
	}
	snapshot.FlashTotalBytes = total
	snapshot.FlashFreeBytes = free
	if total > 0 {
		snapshot.FlashUsedPercent = float64(total-free) / float64(total) * 100
	}
	snapshot.FlashPath = "ubi0:rootfs + /data"
	if snapshot.FlashDeviceModel == "" && snapshot.mtdType != "" {
		model := strings.ToUpper(snapshot.mtdType)
		if snapshot.mtdTotalBytes > 0 {
			model += " " + humanizeBytes(snapshot.mtdTotalBytes)
		}
		snapshot.FlashDeviceModel = model
	}
}

func humanizeBytes(value uint64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	result := float64(value)
	unit := 0
	for result >= 1024 && unit < len(units)-1 {
		result /= 1024
		unit++
	}
	return fmt.Sprintf("%.0f%s", result, units[unit])
}

func atoiTrimmed(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}

func cleanModuleText(value string) string {
	return strings.TrimSpace(strings.Trim(value, ": "))
}
