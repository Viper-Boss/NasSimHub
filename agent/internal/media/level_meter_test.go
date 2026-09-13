package media

import (
	"bytes"
	"encoding/binary"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"
)

func encodePCM(samples ...int16) []byte {
	pcm := make([]byte, 0, len(samples)*2)
	for _, sample := range samples {
		pcm = binary.LittleEndian.AppendUint16(pcm, uint16(sample))
	}
	return pcm
}

func TestMeasurePCM(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		pcm         []byte
		wantSamples int64
		wantPeak    int
		wantSum     float64
	}{
		"digital silence": {
			pcm:         encodePCM(0, 0, 0, 0),
			wantSamples: 4,
			wantPeak:    0,
			wantSum:     0,
		},
		"negative samples are measured as magnitudes": {
			pcm:         encodePCM(-100, 100, -300),
			wantSamples: 3,
			wantPeak:    300,
			wantSum:     100*100 + 100*100 + 300*300,
		},
		"the minimum sample does not overflow when negated": {
			pcm:         encodePCM(math.MinInt16),
			wantSamples: 1,
			wantPeak:    32767,
			wantSum:     32767 * 32767,
		},
		"a trailing partial sample is ignored": {
			pcm:         append(encodePCM(7), 0x01),
			wantSamples: 1,
			wantPeak:    7,
			wantSum:     49,
		},
		"an empty block measures nothing": {
			pcm:         nil,
			wantSamples: 0,
			wantPeak:    0,
			wantSum:     0,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			samples, sumSquares, peak, _ := measurePCM(test.pcm)
			if samples != test.wantSamples {
				t.Errorf("samples = %d, want %d", samples, test.wantSamples)
			}
			if peak != test.wantPeak {
				t.Errorf("peak = %d, want %d", peak, test.wantPeak)
			}
			if sumSquares != test.wantSum {
				t.Errorf("sum of squares = %v, want %v", sumSquares, test.wantSum)
			}
		})
	}
}

func TestDBFSOf(t *testing.T) {
	t.Parallel()

	if got := dbfsOf(0); got != silentFloorDBFS {
		t.Errorf("dbfsOf(0) = %v, want the silence floor %v", got, silentFloorDBFS)
	}
	// Full scale is 32768, which is 0 dBFS.
	if got := dbfsOf(32768); math.Abs(got) > 1e-9 {
		t.Errorf("dbfsOf(full scale) = %v, want 0", got)
	}
	// Half scale is approximately -6 dBFS.
	if got := dbfsOf(16384); math.Abs(got-(-6.0206)) > 0.01 {
		t.Errorf("dbfsOf(half scale) = %v, want about -6.02", got)
	}
	// Sub-unit magnitudes stay finite and negative rather than diverging.
	if got := dbfsOf(0.5); got >= 0 {
		t.Errorf("dbfsOf(0.5) = %v, want a negative value", got)
	}
}

// TestLevelMeterReportsAndResetsEachWindow pins the window bookkeeping without
// capturing logs: an open window accumulates, and a closed one is reported and
// then reset.
func TestLevelMeterReportsAndResetsEachWindow(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.September, 10, 9, 0, 0, 0, time.UTC)
	meter := newLevelMeter("capture", base)
	meter.clock = func() time.Time { return base }

	meter.observe(encodePCM(1000, -1000))
	if meter.samples != 2 || meter.peak != 1000 {
		t.Fatalf(
			"open window = {samples: %d, peak: %d}, want {2, 1000}",
			meter.samples,
			meter.peak,
		)
	}

	// Half an interval in, the window is still open and keeps accumulating.
	meter.clock = func() time.Time { return base.Add(levelMeterInterval / 2) }
	meter.observe(encodePCM(500))
	if meter.samples != 3 {
		t.Fatalf("window at half interval = %d samples, want 3", meter.samples)
	}

	// Past the interval the window is reported — the block that closed it
	// belongs to the reported window — and the next window starts empty.
	meter.clock = func() time.Time { return base.Add(levelMeterInterval) }
	meter.observe(encodePCM(0))
	if meter.samples != 0 {
		t.Fatalf(
			"reported window = %d samples, want a fresh window holding none",
			meter.samples,
		)
	}
	if meter.peak != 0 {
		t.Fatalf("reset window peak = %d, want 0", meter.peak)
	}
	if meter.sumSquares != 0 {
		t.Fatalf("reset window sum of squares = %v, want 0", meter.sumSquares)
	}
}

// TestLevelMeterIgnoresEmptyAndPartialBlocks keeps the meter from reporting a
// window with no samples in it.
func TestLevelMeterIgnoresEmptyAndPartialBlocks(t *testing.T) {
	t.Parallel()

	meter := newLevelMeter("playback", time.Now())
	meter.observe(nil)
	meter.observe([]byte{0x01})
	if meter.samples != 0 {
		t.Fatalf("samples = %d, want none", meter.samples)
	}
}

// Clipping introduced by the gain stage must reach the log.
//
// This is the measurement the gain constants were missing. The playback
// amplification was tuned against a microphone delivering -60 dBFS; at 50x
// everything above -34 dBFS clips, and the primary measurement is taken BEFORE
// the gain, so a pre-gain level reads perfectly healthy while the module
// receives a square wave. Without a clamp count in the log the only symptom is
// somebody saying it sounds bad.
func TestTheLevelLogReportsWhatTheModuleSideReceived(t *testing.T) {
	base := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	meter := newLevelMeter("playback", base)
	now := base
	meter.clock = func() time.Time { return now }

	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	defer slog.SetDefault(previous)

	// A browser block at about -30 dBFS -- a normal quiet speech level, and
	// well below the -60 dBFS the 50x amplification was tuned against.
	// Amplified, it clips: 1000 * 50 is 50000 against a ceiling of 32767.
	browser := pcmBlock(400, 1000)
	module := append([]byte(nil), browser...)
	clamped := applyGainS16LE(module, 50, 1)
	if clamped == 0 {
		t.Fatal("the fixture did not clip; it cannot exercise the report")
	}
	meter.observeModuleSide(module, clamped)
	now = now.Add(levelMeterInterval)
	meter.observe(browser)

	line := logged.String()
	for _, field := range []string{"module_peak=", "module_rms_dbfs=", "clipped=", "clipped_pct="} {
		if !strings.Contains(line, field) {
			t.Fatalf("the level log has no %s field, so gain clipping is invisible: %s", field, line)
		}
	}
	if strings.Contains(line, "clipped=0 ") {
		t.Fatalf("a clipping window reported no clamped samples: %s", line)
	}
}

// And a direction with no gain stage must not grow empty fields, or every log
// line carries four numbers that mean nothing.
func TestADirectionWithNoGainStageReportsNoModuleSide(t *testing.T) {
	base := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	meter := newLevelMeter("capture", base)
	now := base
	meter.clock = func() time.Time { return now }

	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	defer slog.SetDefault(previous)

	now = now.Add(levelMeterInterval)
	meter.observe(pcmBlock(400, 1000))
	if strings.Contains(logged.String(), "module_peak") {
		t.Fatalf("a direction with no gain reported a module side: %s", logged.String())
	}
}

// pcmBlock builds a block of little-endian S16 samples alternating around zero
// at the given magnitude.
func pcmBlock(samples int, magnitude int16) []byte {
	block := make([]byte, samples*2)
	for index := 0; index < samples; index++ {
		value := magnitude
		if index%2 == 1 {
			value = -magnitude
		}
		binary.LittleEndian.PutUint16(block[index*2:], uint16(value))
	}
	return block
}

// A hot signal and a clipped one must be distinguishable in the log.
//
// module_peak=32767 on its own says nothing: one sample touching full scale in
// a two second window is ordinary for speech, and a two second peak is pinned
// by exactly one such sample. The question that decides whether anything on
// this side of the module can help is how MANY samples sit there -- clipping
// flattens the tops of the waveform before the audio reaches us, and no amount
// of attenuation afterwards puts them back.
//
// Written because the module's capture was being recorded as a firmware
// limitation on the strength of the peak alone, while the crest factor in the
// same measurements looked like ordinary speech.
func TestPinnedSamplesSeparateAHotSignalFromAClippedOne(t *testing.T) {
	// Speech-like: one sample at full scale, the rest well below it.
	hot := make([]byte, 0, 200)
	binary.LittleEndian.AppendUint16(hot, uint16(32767))
	hot = append(hot, byte(32767&0xff), byte(32767>>8))
	for range 99 {
		hot = append(hot, byte(4000&0xff), byte(4000>>8))
	}
	_, _, hotPeak, hotPinned := measurePCM(hot)
	if hotPeak != 32767 {
		t.Fatalf("hot peak = %d", hotPeak)
	}
	if hotPinned != 1 {
		t.Fatalf("a single full-scale sample counted as %d pinned", hotPinned)
	}

	// Clipped: the tops are flat.
	clipped := make([]byte, 0, 200)
	for range 100 {
		clipped = append(clipped, byte(32767&0xff), byte(32767>>8))
	}
	_, _, clippedPeak, clippedPinned := measurePCM(clipped)
	if clippedPeak != hotPeak {
		t.Fatal("peak cannot tell the two apart, which is the whole point")
	}
	if clippedPinned != 100 {
		t.Fatalf("a fully clipped block counted as %d pinned", clippedPinned)
	}

	// Both negative rails count, or a clipped waveform reports half its damage
	// and a symmetric square wave looks half clean. A clipper may stop at
	// -32767 or at -32768 depending on how it rounds, so both are pinned.
	for name, negative := range map[string][]byte{
		"-32768": {0x00, 0x80, 0x00, 0x80},
		"-32767": {0x01, 0x80, 0x01, 0x80},
	} {
		_, _, _, negativePinned := measurePCM(negative)
		if negativePinned != 2 {
			t.Fatalf("samples at %s counted as %d pinned", name, negativePinned)
		}
	}
}

// And the count has to reach the log, with its share of the window.
func TestTheLevelLogReportsPinnedSamples(t *testing.T) {
	base := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	meter := newLevelMeter("capture", base)
	now := base
	meter.clock = func() time.Time { return now }

	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	defer slog.SetDefault(previous)

	meter.observeModuleSide(pcmBlock(100, 32767), 0)
	now = now.Add(levelMeterInterval)
	meter.observe(pcmBlock(100, 1000))

	line := logged.String()
	if fieldOf(line, "module_pinned") != "100" {
		t.Fatalf("module_pinned missing or wrong: %s", line)
	}
	if fieldOf(line, "module_pinned_pct") != "100" {
		t.Fatalf("module_pinned_pct missing or wrong: %s", line)
	}
}
