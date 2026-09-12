package media

import (
	"bytes"
	"context"
	"io"
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
