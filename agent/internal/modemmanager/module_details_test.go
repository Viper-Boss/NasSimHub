package modemmanager

import (
	"testing"
)

// The fixture mirrors the real QDC507 module output for the batched metrics
// shell command: MDM9207 single-core ARMv7, 42 MB RAM, 128 MB NAND with UBI
// volumes for / and /data.
const moduleSnapshotFixture = `CORES:1
MODEL:ARMv7 Processor rev 5 (v7l)
HW:Qualcomm Technologies, Inc MDM9207
CURF:998400
MAXF:1305600
LOAD:1.01 1.03 1.04 3/224 12345
MemTotal:          43460 kB
MemFree:            3128 kB
MemAvailable:       9520 kB
Buffers:               0 kB
Cached:             8572 kB
PROCS:142
MTDSZ:134217728
MTDTYPE:nand
FSTYPE:ubifs
TZ:tsens_tz_sensor0:43
TZ:tsens_tz_sensor1:42
TZ:tsens_tz_sensor2:42
TZ:tsens_tz_sensor3:43
TZ:tsens_tz_sensor4:42
TZ:pa_therm0:39
TZ:pa_therm1:47
ubi0:rootfs              58948     50272      8676  85% /
/dev/ubi2_0              15712       228     15484   1% /cache
/dev/ubi2_0              15712       228     15484   1% /data
`

func TestParseModuleSnapshotUsesModuleValues(t *testing.T) {
	snapshot := parseModuleSnapshot(moduleSnapshotFixture)
	if snapshot.Cores != 1 {
		t.Fatalf("cores = %d, want 1", snapshot.Cores)
	}
	if snapshot.CPUModelName != "Qualcomm MDM9207" {
		t.Fatalf("cpu model = %q, want Qualcomm MDM9207", snapshot.CPUModelName)
	}
	if snapshot.CPUCurrentMHz != 998.4 {
		t.Fatalf("current mhz = %v, want 998.4", snapshot.CPUCurrentMHz)
	}
	if snapshot.CPUMaxMHz != 1305.6 {
		t.Fatalf("max mhz = %v, want 1305.6", snapshot.CPUMaxMHz)
	}
	if snapshot.LoadRaw1 != 1.01 || snapshot.LoadRaw5 != 1.03 || snapshot.LoadRaw15 != 1.04 {
		t.Fatalf("load = %v/%v/%v, want 1.01/1.03/1.04", snapshot.LoadRaw1, snapshot.LoadRaw5, snapshot.LoadRaw15)
	}
	if snapshot.ThreadCount != 224 {
		t.Fatalf("threads = %d, want 224", snapshot.ThreadCount)
	}
	if snapshot.ProcessCount != 142 {
		t.Fatalf("processes = %d, want 142", snapshot.ProcessCount)
	}
	if snapshot.CPUTempCelsius == nil || *snapshot.CPUTempCelsius != 43 {
		t.Fatalf("cpu temp = %v, want 43", snapshot.CPUTempCelsius)
	}
	if snapshot.MemoryTotalBytes != 43460*1024 {
		t.Fatalf("memory total = %d, want %d", snapshot.MemoryTotalBytes, 43460*1024)
	}
	if snapshot.MemoryUsedPercent <= 0 || snapshot.MemoryUsedPercent >= 100 {
		t.Fatalf("memory used percent = %v, want between 0 and 100", snapshot.MemoryUsedPercent)
	}
	if snapshot.MemoryCachedBytes != 8572*1024 {
		t.Fatalf("memory cached = %d, want %d", snapshot.MemoryCachedBytes, 8572*1024)
	}
	// The two /data rows share one device and must be counted once.
	if snapshot.FlashTotalBytes != (58948+15712)*1024 {
		t.Fatalf("flash total = %d, want %d", snapshot.FlashTotalBytes, (58948+15712)*1024)
	}
	if snapshot.FlashFreeBytes != (8676+15484)*1024 {
		t.Fatalf("flash free = %d, want %d", snapshot.FlashFreeBytes, (8676+15484)*1024)
	}
	if snapshot.FlashFsType != "ubifs" {
		t.Fatalf("flash fs = %q, want ubifs", snapshot.FlashFsType)
	}
	if snapshot.FlashDeviceModel != "NAND 128MB" {
		t.Fatalf("flash model = %q, want NAND 128MB", snapshot.FlashDeviceModel)
	}
	if snapshot.FlashUsedPercent <= 0 || snapshot.FlashUsedPercent >= 100 {
		t.Fatalf("flash used percent = %v, want between 0 and 100", snapshot.FlashUsedPercent)
	}
}

func TestParseModuleSnapshotToleratesMissingValues(t *testing.T) {
	snapshot := parseModuleSnapshot("")
	if snapshot.Cores != 0 || snapshot.CPUModelName != "" || snapshot.FlashTotalBytes != 0 {
		t.Fatalf("empty output should yield an empty snapshot: %+v", snapshot)
	}
	if snapshot.CPUTempCelsius != nil {
		t.Fatalf("empty output should yield no cpu temp")
	}
}

func TestParseModuleSnapshotFallsBackToAnyZone(t *testing.T) {
	snapshot := parseModuleSnapshot("TZ:pa_therm0:39\n")
	if snapshot.CPUTempCelsius == nil || *snapshot.CPUTempCelsius != 39 {
		t.Fatalf("cpu temp = %v, want 39 from the non-tsens fallback", snapshot.CPUTempCelsius)
	}
}

func TestParseModuleSnapshotNormalizesMillidegrees(t *testing.T) {
	snapshot := parseModuleSnapshot("TZ:tsens_tz_sensor0:43000\n")
	if snapshot.CPUTempCelsius == nil || *snapshot.CPUTempCelsius != 43 {
		t.Fatalf("cpu temp = %v, want 43 from millidegrees", snapshot.CPUTempCelsius)
	}
}
