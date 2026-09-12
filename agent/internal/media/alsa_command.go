package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type commandFactory func(string, ...string) *exec.Cmd

const (
	maxALSAErrorBytes = 4 << 10

	quectelUACCaptureDuration  = 40 * time.Millisecond
	quectelUACPlaybackDuration = 100 * time.Millisecond

	// The module's DSP delivers its own direction roughly 12 dB too hot -- a
	// live call measured speech at -8 dBFS RMS with peaks already at digital
	// full scale, which the far side hears as loud distortion. The capture
	// stream is therefore attenuated a half (-6 dB), landing speech around
	// -14 dBFS: above the noise with peak headroom. The first attempt at a
	// quarter (-12 dB) measured -22 dBFS RMS and the operator called it too
	// quiet.
	//
	// The opposite direction arrives some 50 dB too quiet: the browser's
	// microphone measured -60 dBFS RMS on a live call while the speaker
	// talked at a normal level, which is inaudible on a phone. The playback
	// stream is amplified 50x before it reaches the module's AFE TX path, and
	// hard clipping is the only peak protection -- a shout into the browser
	// microphone distorts rather than explodes.
	//
	// Both numbers come from one live QDC507 on one firmware; if a firmware
	// update changes the DSP calibration these want re-measuring (the level
	// logs around "modem call audio level" are the instrument).
	captureGainNum  = 1
	captureGainDen  = 2
	playbackGainNum = 50
	playbackGainDen = 1
)

// ALSACommandOpener uses the established alsa-utils PCM tools as an explicit
// full-duplex backend. It never discovers a device: the configured ALSA name
// is passed verbatim to both processes.
type ALSACommandOpener struct {
	arecord string
	aplay   string
	command commandFactory
}

func NewALSACommandOpener() (*ALSACommandOpener, error) {
	arecord, err := exec.LookPath("arecord")
	if err != nil {
		return nil, NewError(
			ErrorBackendUnavailable,
			"configure_alsa_pcm",
			"arecord is not installed",
			err,
		)
	}
	aplay, err := exec.LookPath("aplay")
	if err != nil {
		return nil, NewError(
			ErrorBackendUnavailable,
			"configure_alsa_pcm",
			"aplay is not installed",
			err,
		)
	}
	return &ALSACommandOpener{
		arecord: arecord,
		aplay:   aplay,
		command: exec.Command,
	}, nil
}

func (o *ALSACommandOpener) OpenDuplexPCM(
	ctx context.Context,
	endpoint string,
	format Format,
) (Device, error) {
	if o == nil || o.command == nil || o.arecord == "" || o.aplay == "" {
		return nil, NewError(
			ErrorBackendUnavailable,
			"open_alsa_pcm",
			"alsa-utils command backend is not configured",
			nil,
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, NewError(ErrorBackendUnavailable, "open_alsa_pcm", "media open canceled", err)
	}
	if format.Rate != 8000 {
		return nil, NewError(
			ErrorUnsupportedFormat,
			"open_alsa_pcm",
			"Quectel UAC requires an 8000 Hz PCM stream",
			nil,
		)
	}
	baseArgs := []string{
		"--quiet",
		"--device", endpoint,
		"--file-type", "raw",
		"--format", "S16_LE",
		"--channels", strconv.Itoa(int(format.Channels)),
		"--rate", strconv.FormatUint(uint64(format.Rate), 10),
	}
	captureBytes := transferBytes(format, quectelUACCaptureDuration)
	playbackBytes := transferBytes(format, quectelUACPlaybackDuration)
	captureArgs := append(
		append([]string(nil), baseArgs...),
		"--period-size", strconv.Itoa(transferFrames(format, quectelUACCaptureDuration)),
		"--buffer-size", strconv.Itoa(2*transferFrames(format, quectelUACCaptureDuration)),
	)
	playbackArgs := append(
		append([]string(nil), baseArgs...),
		"--period-size", strconv.Itoa(transferFrames(format, quectelUACPlaybackDuration)),
		"--buffer-size", strconv.Itoa(2*transferFrames(format, quectelUACPlaybackDuration)),
		"--start-delay=-"+strconv.FormatInt(
			int64(quectelUACPlaybackDuration/time.Microsecond),
			10,
		),
	)
	captureCommand := o.command(o.arecord, captureArgs...)
	captureErrors := &boundedCommandOutput{limit: maxALSAErrorBytes}
	captureCommand.Stderr = captureErrors
	capture, err := captureCommand.StdoutPipe()
	if err != nil {
		return nil, NewError(ErrorBackendUnavailable, "open_alsa_pcm", "create arecord output", err)
	}
	playbackCommand := o.command(o.aplay, playbackArgs...)
	playbackErrors := &boundedCommandOutput{limit: maxALSAErrorBytes}
	playbackCommand.Stderr = playbackErrors
	playback, err := playbackCommand.StdinPipe()
	if err != nil {
		_ = capture.Close()
		return nil, NewError(ErrorBackendUnavailable, "open_alsa_pcm", "create aplay input", err)
	}
	return &alsaCommandDevice{
		capture:          capture,
		playback:         playback,
		captureCommand:   captureCommand,
		playbackCommand:  playbackCommand,
		captureProcess:   commandProcess{command: captureCommand},
		playbackProcess:  commandProcess{command: playbackCommand},
		captureErrors:    captureErrors,
		playbackErrors:   playbackErrors,
		captureTransfer:  make([]byte, captureBytes),
		captureOffset:    captureBytes,
		playbackTransfer: make([]byte, playbackBytes),
		captureLevel:     newLevelMeter("capture", time.Now()),
		playbackLevel:    newLevelMeter("playback", time.Now()),
	}, nil
}

func transferFrames(format Format, duration time.Duration) int {
	return int(uint64(format.Rate) * uint64(duration/time.Millisecond) / 1000)
}

func transferBytes(format Format, duration time.Duration) int {
	return transferFrames(format, duration) * int(format.Channels) * 2
}

type alsaCommandDevice struct {
	capture         io.ReadCloser
	playback        io.WriteCloser
	captureCommand  *exec.Cmd
	playbackCommand *exec.Cmd
	captureProcess  commandProcess
	playbackProcess commandProcess
	captureErrors   *boundedCommandOutput
	playbackErrors  *boundedCommandOutput

	captureMu       sync.Mutex
	captureTransfer []byte
	captureOffset   int
	// captureLevel measures what the module actually delivers during a call.
	captureLevel *levelMeter

	playbackMu       sync.Mutex
	playbackTransfer []byte
	playbackOffset   int
	// playbackLevel measures what the browser actually sends during a call.
	playbackLevel *levelMeter

	closeOnce sync.Once
	closeErr  error
	startMu   sync.Mutex
	started   bool
	closed    bool
}

func (d *alsaCommandDevice) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	d.captureMu.Lock()
	defer d.captureMu.Unlock()

	if d.captureOffset >= len(d.captureTransfer) {
		if _, err := io.ReadFull(d.capture, d.captureTransfer); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				_ = d.captureProcess.wait()
			}
			return 0, commandIOError("arecord", d.captureErrors, err)
		}
		applyGainS16LE(d.captureTransfer, captureGainNum, captureGainDen)
		d.captureOffset = 0
	}
	read := copy(destination, d.captureTransfer[d.captureOffset:])
	d.captureOffset += read
	d.captureLevel.observe(destination[:read])
	return read, nil
}

func (d *alsaCommandDevice) Write(source []byte) (int, error) {
	if len(source) == 0 {
		return 0, nil
	}
	d.playbackMu.Lock()
	defer d.playbackMu.Unlock()

	d.playbackLevel.observe(source)
	// The level meter above reports what the browser delivered; what the
	// module receives is this buffer with the playback gain applied. The
	// caller's slice is left untouched.
	if playbackGainNum != playbackGainDen {
		gained := make([]byte, len(source))
		copy(gained, source)
		applyGainS16LE(gained, playbackGainNum, playbackGainDen)
		source = gained
	}
	consumed := 0
	for consumed < len(source) {
		copied := copy(d.playbackTransfer[d.playbackOffset:], source[consumed:])
		d.playbackOffset += copied
		consumed += copied
		if d.playbackOffset != len(d.playbackTransfer) {
			continue
		}
		if err := writeFull(d.playback, d.playbackTransfer); err != nil {
			return consumed, commandIOError("aplay", d.playbackErrors, err)
		}
		d.playbackOffset = 0
	}
	return consumed, nil
}

func (d *alsaCommandDevice) SetReadDeadline(deadline time.Time) error {
	deadlineReader, ok := d.capture.(interface {
		SetReadDeadline(time.Time) error
	})
	if !ok {
		return NewError(
			ErrorBackendUnavailable,
			"configure_alsa_pcm",
			"arecord pipe does not support deadlines",
			nil,
		)
	}
	return deadlineReader.SetReadDeadline(deadline)
}

func (d *alsaCommandDevice) SetWriteDeadline(deadline time.Time) error {
	deadlineWriter, ok := d.playback.(interface {
		SetWriteDeadline(time.Time) error
	})
	if !ok {
		return NewError(
			ErrorBackendUnavailable,
			"configure_alsa_pcm",
			"aplay pipe does not support deadlines",
			nil,
		)
	}
	return deadlineWriter.SetWriteDeadline(deadline)
}

func (d *alsaCommandDevice) Start(ctx context.Context) error {
	const operation = "start_alsa_pcm"

	d.startMu.Lock()
	defer d.startMu.Unlock()
	if d.closed {
		return NewError(ErrorConflict, operation, "alsa-pcm device is closed", nil)
	}
	if d.started {
		return NewError(ErrorConflict, operation, "alsa-pcm device is already started", nil)
	}
	if err := ctx.Err(); err != nil {
		return NewError(ErrorTimeout, operation, "alsa-pcm start canceled", err)
	}
	if err := d.captureCommand.Start(); err != nil {
		return NewError(ErrorBackendUnavailable, operation, "start arecord", err)
	}
	if err := ctx.Err(); err != nil {
		_ = d.captureProcess.stop()
		d.captureCommand = nil
		return NewError(ErrorTimeout, operation, "alsa-pcm start canceled", err)
	}
	if err := d.playbackCommand.Start(); err != nil {
		_ = d.captureProcess.stop()
		d.captureCommand = nil
		return NewError(ErrorBackendUnavailable, operation, "start aplay", err)
	}
	d.started = true
	return nil
}

func (d *alsaCommandDevice) Close() error {
	d.closeOnce.Do(func() {
		d.startMu.Lock()
		d.closed = true
		d.startMu.Unlock()
		d.closeErr = errors.Join(
			closePipe(d.capture),
			closePipe(d.playback),
			d.captureProcess.stop(),
			d.playbackProcess.stop(),
		)
	})
	return d.closeErr
}

func closePipe(closer io.Closer) error {
	if closer == nil {
		return nil
	}
	err := closer.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

type commandProcess struct {
	command  *exec.Cmd
	waitOnce sync.Once
	waitErr  error
}

func (process *commandProcess) wait() error {
	if process == nil || process.command == nil || process.command.Process == nil {
		return nil
	}
	process.waitOnce.Do(func() {
		process.waitErr = process.command.Wait()
	})
	return process.waitErr
}

func (process *commandProcess) stop() error {
	if process == nil || process.command == nil || process.command.Process == nil {
		return nil
	}
	killErr := process.command.Process.Kill()
	waitErr := process.wait()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	var exitError *exec.ExitError
	if errors.As(waitErr, &exitError) {
		waitErr = nil
	}
	return errors.Join(killErr, waitErr)
}

type boundedCommandOutput struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (output *boundedCommandOutput) Write(payload []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if remaining := output.limit - len(output.data); remaining > 0 {
		if len(payload) < remaining {
			remaining = len(payload)
		}
		output.data = append(output.data, payload[:remaining]...)
	}
	return len(payload), nil
}

func (output *boundedCommandOutput) String() string {
	if output == nil {
		return ""
	}
	output.mu.Lock()
	defer output.mu.Unlock()
	return strings.TrimSpace(string(output.data))
}

func commandIOError(command string, output *boundedCommandOutput, err error) error {
	if detail := output.String(); detail != "" {
		return fmt.Errorf("%s: %s: %w", command, detail, err)
	}
	return err
}

// applyGainS16LE scales a little-endian S16 sample buffer by
// numerator/denominator in place, clamping to full scale. Speech is roughly
// sinusoidal, so the truncation is toward zero and the clamp is symmetric;
// anything fancier (soft knee, AGC) belongs in a follow-up with its own
// listening test.
func applyGainS16LE(buffer []byte, numerator, denominator int) {
	if numerator == denominator || denominator == 0 {
		return
	}
	for index := 0; index+1 < len(buffer); index += 2 {
		sample := int16(uint16(buffer[index]) | uint16(buffer[index+1])<<8)
		scaled := int32(sample) * int32(numerator) / int32(denominator)
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		buffer[index] = byte(scaled)
		buffer[index+1] = byte(uint16(scaled) >> 8)
	}
}
