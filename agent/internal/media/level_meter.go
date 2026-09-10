package media

import (
	"encoding/binary"
	"log/slog"
	"math"
	"sync"
	"time"
)

// levelMeterInterval bounds how often one direction of the modem call audio is
// summarised.
const levelMeterInterval = 2 * time.Second

// silentFloorDBFS is reported for a window whose samples are all exactly zero.
// Real digital silence has no dBFS value (log of zero), so it is reported as
// this floor plus the explicit silent flag.
const silentFloorDBFS = -120.0

// levelMeter summarises the PCM crossing one direction of the modem audio
// boundary.
//
// It exists because "the call connected but neither side can hear anything" is
// indistinguishable from a healthy call in every other signal ModemDeck emits:
// signalling, the SDP exchange and the host media stream all report success
// while the samples on the wire are digital silence. Measuring the audio itself
// is what tells the two legs apart — at the modem boundary a silent capture
// means the module delivered nothing, whereas silent playback means the browser
// never sent anything.
//
// Samples are little-endian signed 16-bit mono, matching Format.
type levelMeter struct {
	label string
	// clock is a seam so tests can close a window without sleeping.
	clock func() time.Time
	mu    sync.Mutex
	since time.Time

	samples    int64
	sumSquares float64
	peak       int
}

func newLevelMeter(label string, now time.Time) *levelMeter {
	return &levelMeter{label: label, clock: time.Now, since: now}
}

// measurePCM summarises a block of little-endian signed 16-bit mono PCM: how
// many samples it holds, the sum of their squares (for RMS) and the largest
// magnitude seen. Partial trailing bytes are ignored.
func measurePCM(pcm []byte) (int64, float64, int) {
	var sumSquares float64
	var peak int
	samples := int64(0)
	for index := 0; index+1 < len(pcm); index += 2 {
		sample := int(int16(binary.LittleEndian.Uint16(pcm[index:])))
		if sample < 0 {
			// Negating the minimum value overflows, and it is an outlier
			// anyway, so clamp it.
			if sample == math.MinInt16 {
				sample = -math.MinInt16 - 1
			}
			sample = -sample
		}
		if sample > peak {
			peak = sample
		}
		sumSquares += float64(sample) * float64(sample)
		samples++
	}
	return samples, sumSquares, peak
}

// dbfsOf converts a root-mean-square sample magnitude to dBFS, clamping digital
// silence to silentFloorDBFS (the logarithm of zero is unbounded).
func dbfsOf(rms float64) float64 {
	if rms <= 0 {
		return silentFloorDBFS
	}
	return 20 * math.Log10(rms/32768)
}

// observe adds a block of PCM to the current window and, once the window has
// lasted levelMeterInterval, emits its summary and starts a new one.
func (m *levelMeter) observe(pcm []byte) {
	if m == nil || len(pcm) < 2 {
		return
	}
	windowSamples, windowSumSquares, windowPeak := measurePCM(pcm)
	if windowSamples == 0 {
		return
	}

	now := m.clock()
	m.mu.Lock()
	m.samples += windowSamples
	m.sumSquares += windowSumSquares
	if windowPeak > m.peak {
		m.peak = windowPeak
	}
	elapsed := now.Sub(m.since)
	if elapsed < levelMeterInterval {
		m.mu.Unlock()
		return
	}

	samples := m.samples
	rms := math.Sqrt(m.sumSquares / float64(samples))
	peak := m.peak
	m.since = now
	m.samples = 0
	m.sumSquares = 0
	m.peak = 0
	m.mu.Unlock()

	slog.Info(
		"modem call audio level",
		"component", "media",
		"direction", m.label,
		"window_seconds", math.Round(elapsed.Seconds()*10) / 10,
		"samples", samples,
		"peak", peak,
		"rms_dbfs", math.Round(dbfsOf(rms)*10) / 10,
		"silent", rms == 0,
	)
}
