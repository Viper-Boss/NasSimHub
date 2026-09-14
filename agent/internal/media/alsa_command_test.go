package media

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestALSACommandUsesQuectelUACTransferPeriods(t *testing.T) {
	type invocation struct {
		name string
		args []string
	}
	var invocations []invocation
	opener := &ALSACommandOpener{
		arecord: "arecord",
		aplay:   "aplay",
		command: func(name string, args ...string) *exec.Cmd {
			invocations = append(invocations, invocation{
				name: name,
				args: append([]string(nil), args...),
			})
			return exec.Command("sh", "-c", "cat")
		},
	}
	device, err := opener.OpenDuplexPCM(context.Background(), "hw:2,0", mustFormat(t, 8000))
	if err != nil {
		t.Fatalf("OpenDuplexPCM() error = %v", err)
	}
	t.Cleanup(func() { _ = device.Close() })

	if len(invocations) != 2 {
		t.Fatalf("command count = %d, want 2", len(invocations))
	}
	if invocations[0].name != "arecord" ||
		!containsArgumentPair(invocations[0].args, "--period-size", "320") ||
		!containsArgumentPair(invocations[0].args, "--buffer-size", "640") {
		t.Fatalf("arecord invocation = %#v", invocations[0])
	}
	if invocations[1].name != "aplay" ||
		!containsArgumentPair(invocations[1].args, "--period-size", "800") ||
		!containsArgumentPair(invocations[1].args, "--buffer-size", "1600") ||
		!containsArgument(invocations[1].args, "--start-delay=-100000") {
		t.Fatalf("aplay invocation = %#v", invocations[1])
	}
}

func containsArgumentPair(args []string, name, value string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == name && args[index+1] == value {
			return true
		}
	}
	return false
}

func containsArgument(args []string, value string) bool {
	for _, argument := range args {
		if argument == value {
			return true
		}
	}
	return false
}

func TestALSACommandDeviceUsesQuectelUACTransferBlocks(t *testing.T) {
	format := mustFormat(t, 8000)
	capture := bytes.Repeat([]byte{0x5a}, transferBytes(format, quectelUACCaptureDuration))
	playback := &writeBufferCloser{}
	device := &alsaCommandDevice{
		capture:          io.NopCloser(bytes.NewReader(capture)),
		playback:         playback,
		captureTransfer:  make([]byte, len(capture)),
		captureOffset:    len(capture),
		playbackTransfer: make([]byte, transferBytes(format, quectelUACPlaybackDuration)),
	}

	// The device applies the route gains on the way through, so the frames
	// that come out are the inputs with the configured transform applied --
	// the assertions below compare against that same transform.
	captureGained := append([]byte(nil), capture...)
	applyGainS16LE(captureGained, captureGainNum, captureGainDen)
	playbackGained := applyGainS16LECopy(bytes.Repeat([]byte{0x31}, format.FrameBytes))

	for frameIndex := 0; frameIndex < 2; frameIndex++ {
		frame := make([]byte, format.FrameBytes)
		read, err := device.Read(frame)
		if err != nil {
			t.Fatalf("Read(frame %d) error = %v", frameIndex, err)
		}
		if read != len(frame) || !bytes.Equal(frame, captureGained[frameIndex*len(frame):(frameIndex+1)*len(frame)]) {
			t.Fatalf("Read(frame %d) = %d bytes", frameIndex, read)
		}
	}

	frame := bytes.Repeat([]byte{0x31}, format.FrameBytes)
	for frameIndex := 0; frameIndex < 5; frameIndex++ {
		written, err := device.Write(frame)
		if err != nil {
			t.Fatalf("Write(frame %d) error = %v", frameIndex, err)
		}
		if written != len(frame) {
			t.Fatalf("Write(frame %d) = %d bytes, want %d", frameIndex, written, len(frame))
		}
		if frameIndex < 4 && playback.Len() != 0 {
			t.Fatalf("playback flushed after %d internal frames", frameIndex+1)
		}
	}
	if playback.Len() != transferBytes(format, quectelUACPlaybackDuration) {
		t.Fatalf("playback block = %d bytes", playback.Len())
	}
	if !bytes.Equal(playback.Bytes(), bytes.Repeat(playbackGained, 5)) {
		t.Fatalf("playback content is not the browser input with the playback gain applied")
	}
}

// applyGainS16LECopy is the out-of-place form of applyGainS16LE, for tests
// that want the expected transform of an input.
func TestDefaultPlaybackGainPreservesBrowserSpeech(t *testing.T) {
	pcm := []byte{0xee, 0x46, 0x12, 0xb9} // +/-18158, measured healthy browser speech.
	original := append([]byte(nil), pcm...)
	clipped := applyGainS16LE(pcm, playbackGainNum, playbackGainDen)
	if clipped != 0 || !bytes.Equal(pcm, original) {
		t.Fatalf("default uplink gain distorted normal speech: clipped=%d pcm=%x", clipped, pcm)
	}
}

func applyGainS16LECopy(buffer []byte) []byte {
	gained := append([]byte(nil), buffer...)
	applyGainS16LE(gained, playbackGainNum, playbackGainDen)
	return gained
}

func TestALSACommandRejectsNonNativeQuectelUACRate(t *testing.T) {
	opener := &ALSACommandOpener{
		arecord: "arecord",
		aplay:   "aplay",
		command: exec.Command,
	}
	_, err := opener.OpenDuplexPCM(context.Background(), "hw:2,0", mustFormat(t, 16000))
	if !IsCode(err, ErrorUnsupportedFormat) {
		t.Fatalf("OpenDuplexPCM() error = %v, want unsupported format", err)
	}
}

func TestALSACommandDeviceDoesNotStartProcessesUntilStart(t *testing.T) {
	opener := &ALSACommandOpener{
		arecord: "arecord",
		aplay:   "aplay",
		command: func(string, ...string) *exec.Cmd {
			return exec.Command("sh", "-c", "cat")
		},
	}
	device, err := opener.OpenDuplexPCM(context.Background(), "hw:2,0", mustFormat(t, 8000))
	if err != nil {
		t.Fatalf("OpenDuplexPCM() error = %v", err)
	}
	t.Cleanup(func() { _ = device.Close() })

	commandDevice, ok := device.(*alsaCommandDevice)
	if !ok {
		t.Fatalf("device type = %T", device)
	}
	if commandDevice.captureCommand.Process != nil || commandDevice.playbackCommand.Process != nil {
		t.Fatal("ALSA processes started during OPEN")
	}
	startContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := commandDevice.Start(startContext); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if commandDevice.captureCommand.Process == nil || commandDevice.playbackCommand.Process == nil {
		t.Fatal("ALSA processes did not start after START")
	}
	if err := commandDevice.Start(startContext); !IsCode(err, ErrorConflict) {
		t.Fatalf("second Start() error = %v, want conflict", err)
	}
}

type writeBufferCloser struct {
	bytes.Buffer
}

func (*writeBufferCloser) Close() error {
	return nil
}

func TestALSACommandStartFailureIsTypedAndClosesCleanly(t *testing.T) {
	commandIndex := 0
	opener := &ALSACommandOpener{
		arecord: "arecord",
		aplay:   "aplay",
		command: func(string, ...string) *exec.Cmd {
			commandIndex++
			if commandIndex == 1 {
				return exec.Command("sh", "-c", "cat")
			}
			return exec.Command("/modemdeck-test-command-does-not-exist")
		},
	}
	device, err := opener.OpenDuplexPCM(context.Background(), "hw:2,0", mustFormat(t, 8000))
	if err != nil {
		t.Fatalf("OpenDuplexPCM() error = %v", err)
	}
	commandDevice := device.(*alsaCommandDevice)
	if err := commandDevice.Start(context.Background()); !IsCode(err, ErrorBackendUnavailable) {
		t.Fatalf("Start() error = %v, want backend unavailable", err)
	}
	if err := commandDevice.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestALSACommandCaptureErrorIncludesBoundedToolOutput(t *testing.T) {
	commandIndex := 0
	opener := &ALSACommandOpener{
		arecord: "arecord",
		aplay:   "aplay",
		command: func(string, ...string) *exec.Cmd {
			commandIndex++
			if commandIndex == 1 {
				return exec.Command("sh", "-c", "printf 'UAC capture failed' >&2; exit 1")
			}
			return exec.Command("sh", "-c", "cat >/dev/null")
		},
	}
	device, err := opener.OpenDuplexPCM(context.Background(), "hw:2,0", mustFormat(t, 8000))
	if err != nil {
		t.Fatalf("OpenDuplexPCM() error = %v", err)
	}
	commandDevice := device.(*alsaCommandDevice)
	t.Cleanup(func() { _ = commandDevice.Close() })
	if err := commandDevice.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	_, err = commandDevice.Read(make([]byte, 320))
	if err == nil || !strings.Contains(err.Error(), "arecord: UAC capture failed") {
		t.Fatalf("Read() error = %v", err)
	}
	if err := commandDevice.Close(); err != nil {
		t.Fatalf("Close() after capture failure = %v", err)
	}
}

func TestApplyGainS16LEScalesAndClamps(t *testing.T) {
	t.Parallel()

	// 16000, 1600, -1600, -16000 with a quarter gain: 4000, 400, -400, -4000.
	buffer := []byte{
		0x80, 0x3e, // 16000 little-endian
		0x40, 0x06, // 1600
		0xc0, 0xf9, // -1600
		0x80, 0xc1, // -16000
	}
	applyGainS16LE(buffer, 1, 4)
	want := []byte{0xa0, 0x0f, 0x90, 0x01, 0x70, 0xfe, 0x60, 0xf0}
	if string(buffer) != string(want) {
		t.Fatalf("quarter gain = % x; want % x", buffer, want)
	}
}

func TestApplyGainS16LEClampsAtFullScale(t *testing.T) {
	t.Parallel()

	// 20000 amplified 50x would be 1,000,000: clamp to 32767 / -32768.
	positive := []byte{0x20, 0x4e} // 20000
	applyGainS16LE(positive, 50, 1)
	if got := int16(uint16(positive[0]) | uint16(positive[1])<<8); got != 32767 {
		t.Fatalf("positive clamp = %d; want 32767", got)
	}
	negative := []byte{0x00, 0xb1} // -20000
	applyGainS16LE(negative, 50, 1)
	if got := int16(uint16(negative[0]) | uint16(negative[1])<<8); got != -32768 {
		t.Fatalf("negative clamp = %d; want -32768", got)
	}
}

func TestApplyGainS16LEIdentityWhenUnity(t *testing.T) {
	t.Parallel()

	buffer := []byte{0x80, 0x3e, 0xc0, 0xf9, 0x00, 0x80} // includes -32768
	original := append([]byte(nil), buffer...)
	applyGainS16LE(buffer, 7, 7)
	if string(buffer) != string(original) {
		t.Fatalf("unity gain mutated the buffer: % x; want % x", buffer, original)
	}
}

// The device must report BOTH sides of the gain stage to the level log.
//
// A test on the meter alone is not enough, and that gap is the one this
// project has been caught by before: a helper that works perfectly while
// nothing calls it fails as a feature that is simply absent, with every other
// signal looking healthy. So this exercises the real Read and Write paths and
// reads the log they produce.
//
// What each side is for: the module-side capture level is the only place the
// module's own clipping is still visible once the attenuation has halved it,
// and the module-side playback level is the only place the 50x amplification's
// clipping is visible at all -- the browser-side measurement is taken before
// the gain and looks perfect right up to the point the far end hears a square
// wave.
func TestTheDeviceReportsBothSidesOfTheGainStage(t *testing.T) {
	format := mustFormat(t, 8000)
	// Loud capture: the module pinning full scale, which is the fault the
	// capture attenuation was added for.
	capture := pcmBlock(transferBytes(format, quectelUACCaptureDuration)/2, 32767)
	playback := &writeBufferCloser{}
	device := &alsaCommandDevice{
		capture:          io.NopCloser(bytes.NewReader(capture)),
		playback:         playback,
		captureTransfer:  make([]byte, len(capture)),
		captureOffset:    len(capture),
		playbackTransfer: make([]byte, transferBytes(format, quectelUACPlaybackDuration)),
		captureLevel:     newLevelMeter("capture", time.Now()),
		playbackLevel:    newLevelMeter("playback", time.Now()),
	}
	// Windows that close on the first block, so one read and one write are
	// enough to produce both log lines.
	elapsed := time.Now().Add(levelMeterInterval)
	device.captureLevel.clock = func() time.Time { return elapsed }
	device.playbackLevel.clock = func() time.Time { return elapsed }

	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	defer slog.SetDefault(previous)

	if _, err := device.Read(make([]byte, format.FrameBytes)); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	// A browser level of about -30 dBFS: normal quiet speech, and far above
	// the -60 dBFS the amplification was tuned against, so it clips.
	if _, err := device.Write(pcmBlock(format.FrameBytes/2, 1000)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	lines := logged.String()
	if !strings.Contains(lines, "direction=capture") || !strings.Contains(lines, "direction=playback") {
		t.Fatalf("both directions did not report: %s", lines)
	}
	for _, field := range []string{"module_peak=", "clipped="} {
		if strings.Count(lines, field) < 2 {
			t.Fatalf(
				"%s is missing from one direction, so that side of the gain stage is "+
					"unmeasured: %s",
				field,
				lines,
			)
		}
	}
	// A clipped module output has to be distinguishable from a merely hot one.
	// The fixture above is a square wave at full scale, so every sample is
	// pinned; a peak of 32767 alone would look the same for one stray sample.
	if pct := fieldOf(logLine(lines, "direction=capture"), "module_pinned_pct"); pct != "100" {
		t.Fatalf(
			"the capture line reports module_pinned_pct=%s for a fully clipped block; "+
				"without it a hot signal and a clipped one read identically: %s",
			pct,
			lines,
		)
	}

	// The module's own clipping has to survive the attenuation in the log,
	// asserted on the CAPTURE line specifically. Searching the whole log for
	// module_peak=32767 passed even with the measurement taken after the gain,
	// because the playback line is fully clipped and carries the same number --
	// found by sabotaging the order and watching this test not fail.
	if peak := fieldOf(logLine(lines, "direction=capture"), "module_peak"); peak != "32767" {
		t.Fatalf(
			"the capture line reports module_peak=%s; the attenuation has hidden the "+
				"module's full-scale peaks, which is the distortion it was added for: %s",
			peak,
			lines,
		)
	}
	// And the amplification's clipping has to be counted rather than inferred.
	// Checked on the playback line specifically: the capture line legitimately
	// reports clipped=0, because attenuation cannot clamp.
	if playbackGainNum > playbackGainDen {
		playbackLine := logLine(lines, "direction=playback")
		if playbackLine == "" {
			t.Fatalf("no playback line: %s", lines)
		}
		if fieldOf(playbackLine, "clipped") == "0" {
			t.Fatalf("an amplified clipping block reported no clamped samples: %s", playbackLine)
		}
	}
}

// logLine returns the last logged line containing marker, so an assertion can
// be made against one direction rather than against the whole log. Searching
// the whole log lets one direction satisfy an assertion meant for the other.
func logLine(lines, marker string) string {
	found := ""
	for _, line := range strings.Split(lines, "\n") {
		if strings.Contains(line, marker) {
			found = line
		}
	}
	return found
}

// fieldOf reads one key=value field out of a slog text line.
func fieldOf(line, key string) string {
	for _, field := range strings.Fields(line) {
		name, value, found := strings.Cut(field, "=")
		if found && name == key {
			return value
		}
	}
	return ""
}
