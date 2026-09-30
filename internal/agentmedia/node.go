package agentmedia

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/human-agent65535/modemdeck/internal/callmedia"
	"github.com/human-agent65535/modemdeck/internal/store"
)

// NodeMediaSource opens an authenticated, pinned Node media connection.
type NodeMediaSource interface {
	OpenMedia(context.Context, string, string) (io.ReadWriteCloser, error)
}

// NodeMux keeps the existing host endpoint path while routing Node calls to
// the same MDM2 PCM interface over the paired Node's pinned TLS connection.
type NodeMux struct {
	host  callmedia.MediaEndpointOpener
	nodes NodeMediaSource
	calls CallStore
}

func NewNodeMux(host callmedia.MediaEndpointOpener, nodes NodeMediaSource, calls CallStore) (*NodeMux, error) {
	if host == nil || nodes == nil || calls == nil {
		return nil, errors.New("Node media mux requires host, Node and call store")
	}
	return &NodeMux{host: host, nodes: nodes, calls: calls}, nil
}

func (m *NodeMux) Open(ctx context.Context, active callmedia.ActiveCall) (callmedia.MediaEndpoint, error) {
	if active.State != callmedia.CallStateActive || strings.TrimSpace(active.ID) == "" {
		return nil, callmedia.ErrCallNotActive
	}
	openCtx, cancel := context.WithTimeout(normalizeContext(ctx), defaultOpenTimeout)
	defer cancel()
	call, err := m.calls.CallByID(openCtx, active.ID)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(call.EndpointLineID, store.NasSimHubNodeEndpointPrefix) {
		return m.host.Open(ctx, active)
	}
	if call.ID != active.ID || call.Phase != "active" || !call.MediaAvailable ||
		strings.TrimSpace(call.EndpointCallID) == "" || strings.TrimSpace(call.AudioPort) == "" {
		return nil, callmedia.ErrEndpointUnavailable
	}
	format, err := callFormat(call)
	if err != nil {
		return nil, err
	}
	if format.SampleRate != 16000 || format.FrameDuration != 20*time.Millisecond {
		return nil, callmedia.ErrUnsupported
	}
	stream, err := m.nodes.OpenMedia(openCtx, call.EndpointLineID, call.EndpointCallID)
	if err != nil {
		return nil, err
	}
	connection := bridgeNodeStream(stream)
	return &endpoint{
		connection: connection, reader: bufio.NewReader(connection),
		format: format, frameBytes: format.FrameBytes(),
		startTimeout: defaultStartTimeout, ioTimeout: defaultIOTimeout,
	}, nil
}

// net.Pipe supplies independent read/write deadlines to the existing MDM2
// endpoint. Closing either side also closes the underlying TLS upgrade.
func bridgeNodeStream(stream io.ReadWriteCloser) net.Conn {
	local, peer := net.Pipe()
	connection := &nodeBridge{Conn: local, peer: peer, stream: stream}
	go func() {
		_, _ = io.Copy(peer, stream)
		_ = connection.Close()
	}()
	go func() {
		_, _ = io.Copy(stream, peer)
		_ = connection.Close()
	}()
	return connection
}

type nodeBridge struct {
	net.Conn
	peer   net.Conn
	stream io.ReadWriteCloser
	once   sync.Once
}

func (c *nodeBridge) Close() error {
	var closeErr error
	c.once.Do(func() {
		_ = c.peer.Close()
		_ = c.Conn.Close()
		closeErr = c.stream.Close()
	})
	return closeErr
}
