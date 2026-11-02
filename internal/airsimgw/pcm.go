package airsimgw

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/airsimproto"
)

const (
	pcmHandshakeTimeout = 5 * time.Second
	pcmFrameDuration    = 20 * time.Millisecond
	// pcmWaitForCall is how long a connected phone may wait for its call to
	// become active (an outgoing call still ringing) before it is dropped.
	pcmWaitForCall = 90 * time.Second
	// pcmUplinkBuffer bounds the phone-to-call queue: Wi-Fi delivers audio in
	// bursts, and anything older than this is stale and dropped.
	pcmUplinkBuffer = 200 * time.Millisecond
)

func (g *Gateway) pcmActiveFor(pairingID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pcmPairing != "" && g.pcmPairing == pairingID
}

func (g *Gateway) servePCM(ctx context.Context, listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			var temporary interface{ Temporary() bool }
			if errors.As(err, &temporary) && temporary.Temporary() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		go g.handlePCM(ctx, conn)
	}
}

func (g *Gateway) handlePCM(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	if _, ok := g.peerAllowed(conn.RemoteAddr().String()); !ok {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(pcmHandshakeTimeout))
	reader := bufio.NewReaderSize(conn, 4096)
	line, err := readPrefaceLine(reader)
	if err != nil {
		return
	}
	preface, err := airsimproto.ParsePCMPreface(line)
	if err != nil || !airsimproto.FreshTimestamp(preface.Timestamp, g.now()) {
		return
	}
	canonical := airsimproto.PCMCanonical(preface)
	var pairing Pairing
	found := false
	for _, candidate := range g.store.withSecrets() {
		if airsimproto.VerifySignature(candidate.secret, preface.Signature, canonical) {
			pairing, found = candidate, true
			break
		}
	}
	if !found || !g.replay.Accept(preface.Nonce, g.now()) {
		g.logger.Info("AirSIM PCM rejected", "component", "airsim", "reason", "authentication")
		return
	}
	peer := remoteHost(conn.RemoteAddr().String())
	identity, err := g.identity(pairing, peer)
	if err != nil {
		return
	}
	backend := g.currentBackend()
	if backend == nil || backend.Authorize(ctx, identity) != nil {
		return
	}
	// One audio session at a time, as on the Android gateway.
	g.mu.Lock()
	if g.pcmPairing != "" {
		g.mu.Unlock()
		g.logger.Info("AirSIM PCM rejected", "component", "airsim", "reason", "busy")
		return
	}
	g.pcmPairing = pairing.ID
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.pcmPairing = ""
		g.mu.Unlock()
	}()
	if _, err := conn.Write([]byte(airsimproto.PCMReady)); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	g.store.touch(pairing.ID, peer, g.now())
	g.logger.Info("AirSIM PCM connected", "component", "airsim", "pairing_id", pairing.ID)

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	bridge := newPCMBridge(g, backend, identity, conn, reader)
	bridge.run(sessionCtx)
	g.logger.Info("AirSIM PCM closed", "component", "airsim", "pairing_id", pairing.ID,
		"uplink_frames", bridge.uplinkFrames, "downlink_frames", bridge.downlinkFrames,
		"dropped_frames", bridge.droppedFrames)
}

func readPrefaceLine(reader *bufio.Reader) (string, error) {
	var line []byte
	for len(line) <= 1024 {
		character, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		line = append(line, character)
		if character == '\n' {
			return string(line), nil
		}
	}
	return "", airsimproto.ErrInvalid
}

// pcmBridge moves audio between the phone's 8 kHz stream and the call's
// host endpoint for as long as the phone stays connected. It attaches to the
// call when the call turns active and keeps the phone fed with silence
// before that, and between calls never: when the call ends, so does the
// connection, which is what the app expects.
type pcmBridge struct {
	gateway  *Gateway
	backend  Backend
	identity Identity
	conn     net.Conn
	reader   io.Reader

	writeMu sync.Mutex

	uplinkMu sync.Mutex
	uplink   []byte // host-rate samples waiting to be played into the call

	uplinkFrames, downlinkFrames, droppedFrames int64
}

func newPCMBridge(gateway *Gateway, backend Backend, identity Identity, conn net.Conn, reader io.Reader) *pcmBridge {
	return &pcmBridge{gateway: gateway, backend: backend, identity: identity, conn: conn, reader: reader}
}

func (b *pcmBridge) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream := make(chan PCMStream, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer cancel()
		b.readPhone(ctx, stream)
	}()
	defer func() {
		_ = b.conn.Close()
		<-readerDone
	}()

	attached, err := b.waitForCall(ctx)
	if err != nil || attached == nil {
		return
	}
	defer attached.Close()
	stream <- attached
	b.gateway.store.touch(b.identity.PairingID, b.identity.Peer, b.gateway.now())

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer cancel()
		b.playUplink(ctx, attached)
	}()
	b.forwardDownlink(ctx, attached)
	cancel()
	<-writerDone
}

// waitForCall sends silence to the phone until the pairing's call is active
// and its media can be attached.
func (b *pcmBridge) waitForCall(ctx context.Context) (PCMStream, error) {
	silence := make([]byte, airsimproto.PCMFrameBytes)
	ticker := time.NewTicker(pcmFrameDuration)
	defer ticker.Stop()
	deadline := time.Now().Add(pcmWaitForCall)
	nextTry := time.Time{}
	for {
		if now := time.Now(); !now.Before(nextTry) {
			nextTry = now.Add(300 * time.Millisecond)
			callID, live := b.targetCall(ctx)
			if !live {
				return nil, nil
			}
			if callID != "" {
				attachCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				stream, err := b.backend.AttachPCM(attachCtx, b.identity, callID)
				cancel()
				if err == nil {
					return stream, nil
				}
				if errors.Is(err, ErrForbidden) {
					return nil, err
				}
			}
			if now.After(deadline) {
				return nil, errors.New("airsim: call did not become active")
			}
		}
		if err := b.writePhone(silence); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// targetCall returns the active call to attach to; live is false when the
// line has no call in progress at all, so there is nothing to wait for.
func (b *pcmBridge) targetCall(ctx context.Context) (callID string, live bool) {
	lookup, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	active, _, err := b.backend.Calls(lookup, b.identity, 0)
	if err != nil {
		return "", true
	}
	owned := b.gateway.ownedCall(b.identity.PairingID)
	for _, call := range active {
		if !liveCall(call) {
			continue
		}
		if owned != "" && call.ID != owned {
			continue
		}
		if call.Phase == "active" && call.MediaAvailable {
			return call.ID, true
		}
		return "", true
	}
	return "", false
}

// readPhone consumes the phone's 20 ms frames. Before the call is attached
// they are discarded; after, they are converted to the host rate and queued.
func (b *pcmBridge) readPhone(ctx context.Context, streams <-chan PCMStream) {
	frame := make([]byte, airsimproto.PCMFrameBytes)
	var stream PCMStream
	var upsampler resampler
	for {
		if _, err := io.ReadFull(b.reader, frame); err != nil {
			return
		}
		if stream == nil {
			select {
			case stream = <-streams:
				upsampler = resampler{from: airsimproto.PCMSampleRate, to: stream.SampleRate()}
			default:
				continue
			}
		}
		if ctx.Err() != nil {
			return
		}
		if b.gateway.isMuted(b.identity.PairingID) {
			clear(frame)
		}
		converted := upsampler.convert(frame)
		limit := bytesFor(stream.SampleRate(), pcmUplinkBuffer)
		b.uplinkMu.Lock()
		b.uplink = append(b.uplink, converted...)
		if extra := len(b.uplink) - limit; extra > 0 {
			extra += extra % 2
			b.uplink = append(b.uplink[:0], b.uplink[extra:]...)
			b.droppedFrames++
		}
		b.uplinkMu.Unlock()
	}
}

// playUplink paces queued phone audio into the call, one host frame per
// frame period. The hub plays silence for any period with no frame.
func (b *pcmBridge) playUplink(ctx context.Context, stream PCMStream) {
	frameBytes := stream.FrameBytes()
	period := time.Duration(frameBytes/2) * time.Second / time.Duration(stream.SampleRate())
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	frame := make([]byte, frameBytes)
	for {
		select {
		case <-ctx.Done():
			return
		case <-stream.Done():
			return
		case <-ticker.C:
		}
		b.uplinkMu.Lock()
		ready := len(b.uplink) >= frameBytes
		if ready {
			copy(frame, b.uplink[:frameBytes])
			b.uplink = append(b.uplink[:0], b.uplink[frameBytes:]...)
		}
		b.uplinkMu.Unlock()
		if !ready {
			continue
		}
		if err := stream.WriteUplink(ctx, frame); err != nil {
			if errors.Is(err, ErrUplinkDropped) {
				b.droppedFrames++
				continue
			}
			return
		}
		b.uplinkFrames++
	}
}

// ErrUplinkDropped is what a PCMStream returns for a frame it dropped
// because the call's playback queue was full; the bridge keeps going.
var ErrUplinkDropped = errors.New("airsim: uplink frame dropped")

func (b *pcmBridge) forwardDownlink(ctx context.Context, stream PCMStream) {
	downsampler := resampler{from: stream.SampleRate(), to: airsimproto.PCMSampleRate}
	var pending []byte
	for {
		frame, err := stream.ReadDownlink(ctx)
		if err != nil {
			return
		}
		pending = append(pending, downsampler.convert(frame)...)
		for len(pending) >= airsimproto.PCMFrameBytes {
			if err := b.writePhone(pending[:airsimproto.PCMFrameBytes]); err != nil {
				return
			}
			pending = pending[airsimproto.PCMFrameBytes:]
			b.downlinkFrames++
		}
		if len(pending) == 0 {
			pending = nil
		}
	}
}

func (b *pcmBridge) writePhone(frame []byte) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	_ = b.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err := b.conn.Write(frame)
	return err
}

func bytesFor(sampleRate int, duration time.Duration) int {
	samples := int(time.Duration(sampleRate) * duration / time.Second)
	return samples * 2
}

// resampler converts mono PCM16LE between 8 kHz and an integer multiple of
// it (16 kHz on every NasSimHub voice endpoint today). Down: average each
// group of samples, a crude low-pass that keeps speech intelligible. Up:
// linear interpolation from the previous frame's last sample.
type resampler struct {
	from, to int
	last     int16
}

func (r *resampler) convert(input []byte) []byte {
	samples := len(input) / 2
	switch {
	case r.from == r.to || r.from <= 0 || r.to <= 0:
		return append([]byte(nil), input[:samples*2]...)
	case r.from > r.to && r.from%r.to == 0:
		factor := r.from / r.to
		output := make([]byte, 0, samples/factor*2)
		for index := 0; index+factor <= samples; index += factor {
			sum := 0
			for offset := 0; offset < factor; offset++ {
				sum += int(int16(binary.LittleEndian.Uint16(input[(index+offset)*2:])))
			}
			output = binary.LittleEndian.AppendUint16(output, uint16(int16(sum/factor)))
		}
		return output
	case r.to > r.from && r.to%r.from == 0:
		factor := r.to / r.from
		output := make([]byte, 0, samples*factor*2)
		previous := int(r.last)
		for index := 0; index < samples; index++ {
			current := int(int16(binary.LittleEndian.Uint16(input[index*2:])))
			for step := 1; step <= factor; step++ {
				value := previous + (current-previous)*step/factor
				output = binary.LittleEndian.AppendUint16(output, uint16(int16(value)))
			}
			previous = current
		}
		r.last = int16(previous)
		return output
	default:
		// Not a rate any endpoint uses; nearest-sample conversion.
		outSamples := samples * r.to / r.from
		output := make([]byte, 0, outSamples*2)
		for index := 0; index < outSamples; index++ {
			source := index * r.from / r.to
			output = append(output, input[source*2], input[source*2+1])
		}
		return output
	}
}
