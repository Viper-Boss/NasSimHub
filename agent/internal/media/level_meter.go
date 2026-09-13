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

	// The same audio measured on the MODULE side of the digital gain stage,
	// plus the samples that gain had to clamp.
	//
	// Why both sides are needed. The gain stage sits between the browser and
	// the module (see alsa_command.go), and each direction's primary
	// measurement above is taken on the BROWSER side: capture is measured after
	// it is attenuated, playback before it is amplified. That is the right
	// primary -- it is what the person hears and what the person's microphone
	// delivered -- but it blinds the log to the two questions the gain constants
	// exist to answer:
	//
	//   is the module still delivering peaks pinned at full scale (the
	//   distortion the capture attenuation was added for), and
	//
	//   is the playback amplification clipping what it sends to the module?
	//
	// The second one matters most. A fixed amplification tuned against a very
	// quiet microphone clips everything once the microphone delivers a normal
	// level, and nothing in the old log would have shown it: the pre-gain
	// measurement looks perfect right up to the point the far end hears a
	// square wave.
	moduleSamples    int64
	moduleSumSquares float64
	modulePeak       int
	clamped          int64
	hasModuleSide    bool
}

// observeModuleSide records the same block as measured on the module side of
// the gain stage, and how many samples that gain clamped.
//
// Called before observe for the same block, so a window that closes reports
// both sides of the same audio rather than splitting them across two windows.
func (m *levelMeter) observeModuleSide(pcm []byte, clamped int64) {
	if m == nil || len(pcm) < 2 {
		return
	}
	samples, sumSquares, peak := measurePCM(pcm)
	m.noteModuleSide(samples, sumSquares, peak, clamped)
}

// noteModuleSide records module-side statistics that were measured elsewhere.
//
// It exists for the capture direction, where the gain is applied IN PLACE to
// the transfer buffer: by the time that buffer could be handed to
// observeModuleSide it has already been attenuated, and the module's own
// full-scale peaks -- the distortion the attenuation was added for -- have been
// halved out of the log. Measuring before the gain and reporting the numbers
// afterwards keeps that visible without copying every block.
func (m *levelMeter) noteModuleSide(samples int64, sumSquares float64, peak int, clamped int64) {
	if m == nil || samples == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hasModuleSide = true
	m.moduleSamples += samples
	m.moduleSumSquares += sumSquares
	if peak > m.modulePeak {
		m.modulePeak = peak
	}
	m.clamped += clamped
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
			// Negating the minimum sample overflows, so clamp it to the
			// largest magnitude it can represent.
			if sample == math.MinInt16 {
				sample = math.MaxInt16
			} else {
				sample = -sample
			}
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
	moduleSide := m.hasModuleSide
	moduleRMS := 0.0
	if m.moduleSamples > 0 {
		moduleRMS = math.Sqrt(m.moduleSumSquares / float64(m.moduleSamples))
	}
	modulePeak := m.modulePeak
	clamped := m.clamped
	clampedShare := 0.0
	if m.moduleSamples > 0 {
		clampedShare = 100 * float64(clamped) / float64(m.moduleSamples)
	}
	m.since = now
	m.samples = 0
	m.sumSquares = 0
	m.peak = 0
	m.moduleSamples = 0
	m.moduleSumSquares = 0
	m.modulePeak = 0
	m.clamped = 0
	m.mu.Unlock()

	fields := []any{
		"component", "media",
		"direction", m.label,
		"window_seconds", math.Round(elapsed.Seconds()*10) / 10,
		"samples", samples,
		"peak", peak,
		"rms_dbfs", math.Round(dbfsOf(rms)*10) / 10,
		"silent", rms == 0,
	}
	if moduleSide {
		fields = append(
			fields,
			"module_peak", modulePeak,
			"module_rms_dbfs", math.Round(dbfsOf(moduleRMS)*10)/10,
			"clipped", clamped,
			"clipped_pct", math.Round(clampedShare*10)/10,
		)
	}
	slog.Info("modem call audio level", fields...)
}
