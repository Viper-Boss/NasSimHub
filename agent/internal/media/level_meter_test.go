package media

import (
	"encoding/binary"
	"math"
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
			samples, sumSquares, peak := measurePCM(test.pcm)
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
