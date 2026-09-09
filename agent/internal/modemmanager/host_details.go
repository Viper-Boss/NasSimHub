package modemmanager

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

// Host details for the task-manager style dashboard: CPU identity/frequency/
// cache/temperature, process and thread counts, and flash device identity.
// Everything is best-effort sysfs/procfs scraping; missing pieces stay zero.

// armCpuPartNames maps the ARM "CPU part" value from /proc/cpuinfo to a human
// readable core name for the common parts seen on consumer ARM boards.
var armCpuPartNames = map[string]string{
	"0xd01": "Cortex-A32",
	"0xd03": "Cortex-A53",
	"0xd04": "Cortex-A35",
	"0xd05": "Cortex-A55",
	"0xd06": "Cortex-A65",
	"0xd07": "Cortex-A57",
	"0xd08": "Cortex-A72",
	"0xd09": "Cortex-A73",
	"0xd0a": "Cortex-A75",
	"0xd0b": "Cortex-A76",
	"0xd0c": "Neoverse-N1",
	"0xd0d": "Cortex-A77",
	"0xd0e": "Cortex-A76AE",
	"0xd40": "Neoverse-V1",
	"0xd41": "Cortex-A78",
	"0xd42": "Cortex-X1",
	"0xd44": "Cortex-X2",
	"0xd46": "Cortex-A510",
	"0xd47": "Cortex-A710",
	"0xd48": "Cortex-X3",
	"0x00f": "Brahmar-B13",
	"0x002": "Brahmar-B15",
}

func (p *Provider) collectCPUDetails(metrics *domain.SystemMetrics) {
	cpuInfo := readFileString("/proc/cpuinfo")
	model := cpuModelFromCPUInfo(cpuInfo)
	if model == "" {
		model = strings.TrimSpace(readFileString("/proc/device-tree/model"))
	}
	metrics.CPUModelName = model

	cur, max := cpuFrequenciesMHz(metrics.Cores)
	metrics.CPUCurrentMHz = cur
	metrics.CPUMaxMHz = max

	collectCPUCache(metrics)
	if temp := cpuThermalCelsius(); temp != nil {
		metrics.CPUTempCelsius = temp
	}
	metrics.ProcessCount = countNumericDirs("/proc")
	metrics.ThreadCount = countThreads("/proc")
}

func cpuModelFromCPUInfo(cpuInfo string) string {
	for _, line := range strings.Split(cpuInfo, "\n") {
		if strings.HasPrefix(line, "model name") {
			return strings.TrimSpace(strings.TrimPrefix(strings.SplitN(line, ":", 2)[1], " "))
		}
	}
	// ARM kernels: derive from the first "CPU part" hex code.
	for _, line := range strings.Split(cpuInfo, "\n") {
		if strings.HasPrefix(line, "CPU part") {
			part := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(line, ":", 2)[1], " "))
			if name, found := armCpuPartNames[part]; found {
				return name
			}
			if part != "" {
				return "ARM " + part
			}
		}
	}
	return ""
}

func cpuFrequenciesMHz(cores int) (float64, float64) {
	var current, max float64
	for index := 0; index < cores || index == 0; index++ {
		cur := parseKiloHertz(readFileString(fmt.Sprintf(
			"/sys/devices/system/cpu/cpu%d/cpufreq/cpuinfo_cur_freq", index)))
		maxFreq := parseKiloHertz(readFileString(fmt.Sprintf(
			"/sys/devices/system/cpu/cpu%d/cpufreq/cpuinfo_max_freq", index)))
		if cur > current {
			current = cur
		}
		if maxFreq > max {
			max = maxFreq
		}
		if index == 0 && cores == 0 {
			break
		}
	}
	return current / 1000, max / 1000
}

func parseKiloHertz(value string) float64 {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return 0
	}
	parsed, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return parsed
}

func collectCPUCache(metrics *domain.SystemMetrics) {
	base := "/sys/devices/system/cpu/cpu0/cache"
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, entry := range entries {
		index := filepath.Join(base, entry.Name())
		level := strings.TrimSpace(readFileString(filepath.Join(index, "level")))
		kind := strings.TrimSpace(readFileString(filepath.Join(index, "type")))
		size := parseCacheSizeKB(readFileString(filepath.Join(index, "size")))
		switch level + "/" + kind {
		case "1/Data":
			metrics.CPUL1DataKB += size
		case "1/Instruction":
			metrics.CPUL1InstKB += size
		case "2/Unified":
			metrics.CPUL2KB += size
		case "3/Unified":
			metrics.CPUL3KB += size
		}
	}
}

// parseCacheSizeKB accepts sysfs forms like "32K", "512K", "1024K" or "2M".
func parseCacheSizeKB(value string) uint64 {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return 0
	}
	multiplier := uint64(1)
	switch value[len(value)-1] {
	case 'K':
		value = value[:len(value)-1]
	case 'M':
		value = value[:len(value)-1]
		multiplier = 1024
	case 'G':
		value = value[:len(value)-1]
		multiplier = 1024 * 1024
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed * multiplier
}

// cpuThermalCelsius returns the hottest thermal zone whose type mentions cpu,
// falling back to the hottest zone of any type.
func cpuThermalCelsius() *float64 {
	entries, err := os.ReadDir("/sys/class/thermal")
	if err != nil {
		return nil
	}
	bestCPU := 0.0
	bestAny := 0.0
	foundCPU := false
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "thermal_zone") {
			continue
		}
		base := filepath.Join("/sys/class/thermal", entry.Name())
		milli, err := strconv.ParseFloat(strings.TrimSpace(readFileString(filepath.Join(base, "temp"))), 64)
		if err != nil || milli <= -30000 || milli >= 150000 {
			continue
		}
		celsius := milli / 1000
		if celsius > bestAny {
			bestAny = celsius
		}
		kind := strings.ToLower(readFileString(filepath.Join(base, "type")))
		if strings.Contains(kind, "cpu") && celsius > bestCPU {
			bestCPU = celsius
			foundCPU = true
		}
	}
	value := bestCPU
	if !foundCPU {
		if bestAny == 0 {
			return nil
		}
		value = bestAny
	}
	return &value
}

func countNumericDirs(base string) int {
	entries, err := os.ReadDir(base)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "" || name[0] < '0' || name[0] > '9' {
			continue
		}
		count++
	}
	return count
}

func countThreads(base string) int {
	entries, err := os.ReadDir(base)
	if err != nil {
		return 0
	}
	threads := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "" || name[0] < '0' || name[0] > '9' {
			continue
		}
		status := readFileString(filepath.Join(base, name, "status"))
		for _, line := range strings.Split(status, "\n") {
			if strings.HasPrefix(line, "Threads:") {
				value, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Threads:")))
				if err == nil {
					threads += value
				}
				break
			}
		}
	}
	return threads
}

// collectFlashDetails resolves the filesystem type and the backing block
// device for the flash mount already measured by collectFlash.
func collectFlashDetails(metrics *domain.SystemMetrics) {
	if metrics.FlashPath == "" {
		return
	}
	device, fsType := mountForPath(metrics.FlashPath)
	metrics.FlashFsType = fsType
	if device == "" {
		return
	}
	metrics.FlashDevicePath = device
	blockName := blockDeviceOf(device)
	if blockName == "" {
		return
	}
	model := strings.TrimSpace(readFileString(fmt.Sprintf("/sys/block/%s/device/model", blockName)))
	metrics.FlashDeviceModel = model
}

// mountForPath finds the /proc/mounts entry for a path, preferring the exact
// match and otherwise the longest mounted prefix.
func mountForPath(path string) (string, string) {
	mounts := readFileString("/proc/mounts")
	best := ""
	bestDevice := ""
	bestType := ""
	for _, line := range strings.Split(mounts, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		mountPoint, err := unescapeMountPath(fields[1])
		if err != nil {
			continue
		}
		if mountPoint != path && !strings.HasPrefix(path, mountPoint+"/") {
			continue
		}
		if len(mountPoint) > len(best) {
			best = mountPoint
			bestDevice = fields[0]
			bestType = fields[2]
		}
	}
	return bestDevice, bestType
}

func unescapeMountPath(value string) (string, error) {
	value = strings.ReplaceAll(value, "\\040", " ")
	value = strings.ReplaceAll(value, "\\011", "\t")
	return strings.ReplaceAll(value, "\\134", "\\"), nil
}

// blockDeviceOf maps "/dev/sda1" or "/dev/mmcblk0p2" to the sysfs block name.
func blockDeviceOf(device string) string {
	name := strings.TrimPrefix(device, "/dev/")
	if name == device || name == "" {
		return ""
	}
	candidates := []string{name}
	// Partition style "mmcblk0p2" -> parent "mmcblk0".
	if index := strings.LastIndex(name, "p"); index > 0 && isAllDigits(name[index+1:]) {
		candidates = append(candidates, name[:index])
	}
	// Partition style "sda1" -> parent "sda".
	trimmed := strings.TrimRight(name, "0123456789")
	if trimmed != name && trimmed != "" {
		candidates = append(candidates, trimmed)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat("/sys/block/" + candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func isAllDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func readFileString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(data), "\x00\n\r\t ")
}
