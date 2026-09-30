package agentmedia

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/human-agent65535/modemdeck/internal/callmedia"
	"github.com/human-agent65535/modemdeck/internal/store"
)

type testNodeStream struct {
	stream io.ReadWriteCloser
	lineID string
	callID string
}

func (s *testNodeStream) OpenMedia(_ context.Context, lineID, callID string) (io.ReadWriteCloser, error) {
	s.lineID, s.callID = lineID, callID
	return s.stream, nil
}

type rejectingHostOpener struct{}

func (rejectingHostOpener) Open(context.Context, callmedia.ActiveCall) (callmedia.MediaEndpoint, error) {
	return nil, errors.New("host opener must not be used for Node calls")
}

func TestNodeMuxStreamsDuplexPCMThroughExistingEndpoint(t *testing.T) {
	nodeSide, serverSide := net.Pipe()
	defer serverSide.Close()
	serverResult := make(chan error, 1)
	go func() {
		start, err := readFrame(serverSide, maxControlBytes)
		if err != nil {
			serverResult <- err
			return
		}
		if start.Type != frameStart {
			serverResult <- errors.New("missing START frame")
			return
		}
		if err := writeFrame(serverSide, frame{Type: frameStarted}); err != nil {
			serverResult <- err
			return
		}
		playback, err := readFrame(serverSide, 640)
		if err != nil {
			serverResult <- err
			return
		}
		if playback.Type != framePlayback || playback.Sequence != 0 || !bytes.Equal(playback.Payload, bytes.Repeat([]byte{0x19}, 640)) {
			serverResult <- errors.New("invalid playback frame")
			return
		}
		serverResult <- writeFrame(serverSide, frame{Type: frameCapture, Payload: bytes.Repeat([]byte{0x27}, 640)})
	}()
	call := store.Call{
		ID: "call-1", EndpointLineID: "nsh:node-1:0", EndpointCallID: "mm-4",
		Phase: "active", AudioPort: "nsh:q6voice", AudioEncoding: "pcm",
		AudioResolution: "s16le", AudioRate: 16000, MediaAvailable: true,
	}
	source := &testNodeStream{stream: nodeSide}
	mux, err := NewNodeMux(rejectingHostOpener{}, source, fixedCallStore{call: call})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := mux.Open(context.Background(), callmedia.ActiveCall{ID: call.ID, State: callmedia.CallStateActive})
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	if source.lineID != call.EndpointLineID || source.callID != call.EndpointCallID || endpoint.Format().FrameBytes() != 640 {
		t.Fatalf("wrong Node binding or format: %q %q %+v", source.lineID, source.callID, endpoint.Format())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := endpoint.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := endpoint.WritePCM(ctx, bytes.Repeat([]byte{0x19}, 640)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 640)
	if err := endpoint.ReadPCM(ctx, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bytes.Repeat([]byte{0x27}, 640)) {
		t.Fatal("capture frame changed across Node bridge")
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func TestNodeMuxRejectsUnverifiedMediaBeforeOpeningStream(t *testing.T) {
	call := store.Call{ID: "call-1", EndpointLineID: "nsh:node-1:0", EndpointCallID: "mm-4", Phase: "active"}
	source := &testNodeStream{}
	mux, err := NewNodeMux(rejectingHostOpener{}, source, fixedCallStore{call: call})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mux.Open(context.Background(), callmedia.ActiveCall{ID: call.ID, State: callmedia.CallStateActive})
	if !errors.Is(err, callmedia.ErrEndpointUnavailable) || source.callID != "" {
		t.Fatalf("unverified media was opened: %v", err)
	}
}
