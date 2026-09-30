package dht

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ReleaseProtocol hands over the signed release manifest this gateway holds.
//
// WHY THIS IS ALLOWED ON A HOST THAT "CARRIES NOTHING": the rule the rest of
// this package keeps is that the node is never a transport for somebody
// else's traffic. This protocol is not that. It serves one document this
// gateway itself verified -- the publisher's signed manifest, at most
// maxReleaseBytes -- and reads the same document from peers it is already
// connected to. Nothing is forwarded, nothing is stored for a stranger, and
// nothing a peer sends is believed here: the api verifies every document
// against the compiled-in key before it can influence an update. The operator
// decided on 2026-09-25 that releases travel over the DHT and NKN as well as
// over the HTTPS mirrors, and that gateways share them by default.
const ReleaseProtocol = ProtocolPrefix + "/release/1.0.0"

const (
	// maxReleaseBytes is the signed manifest's own size limit
	// (internal/release.MaxManifestBytes in the main module). A peer that
	// sends more is not sending a manifest.
	maxReleaseBytes = 64 << 10
	// releaseFetchPeers bounds how many peers one fetch asks.
	releaseFetchPeers = 8
	releaseTimeout    = 8 * time.Second
	// releaseServeInterval is how often one peer may be served. A manifest
	// changes a few times a year; a peer asking faster than this is not
	// checking for updates.
	releaseServeInterval = 30 * time.Second
)

// ErrReleaseTooLarge refuses a document bigger than a manifest can be.
var ErrReleaseTooLarge = errors.New("dht: release document exceeds the manifest size limit")

// PeerRelease is one peer's answer, unverified.
type PeerRelease struct {
	Peer   string
	Signed []byte
}

// releaseShelf is the document on offer and who was served when.
type releaseShelf struct {
	mu     sync.Mutex
	signed []byte
	served map[peer.ID]time.Time
	now    func() time.Time
}

func (shelf *releaseShelf) set(signed []byte) error {
	if len(signed) > maxReleaseBytes {
		return ErrReleaseTooLarge
	}
	shelf.mu.Lock()
	defer shelf.mu.Unlock()
	shelf.signed = append([]byte(nil), signed...)
	return nil
}

// take returns the document for one peer, or nil when there is none or the
// peer asked too recently.
func (shelf *releaseShelf) take(id peer.ID) []byte {
	shelf.mu.Lock()
	defer shelf.mu.Unlock()
	if len(shelf.signed) == 0 {
		return nil
	}
	now := time.Now()
	if shelf.now != nil {
		now = shelf.now()
	}
	if shelf.served == nil {
		shelf.served = make(map[peer.ID]time.Time)
	}
	if last, ok := shelf.served[id]; ok && now.Sub(last) < releaseServeInterval {
		return nil
	}
	// Forget peers served long ago, so the map cannot grow without bound.
	for other, at := range shelf.served {
		if now.Sub(at) >= releaseServeInterval {
			delete(shelf.served, other)
		}
	}
	shelf.served[id] = now
	return append([]byte(nil), shelf.signed...)
}

// SetRelease sets the signed document this node shares. Empty stops sharing.
func (s *Service) SetRelease(signed []byte) error {
	return s.release.set(signed)
}

// serveRelease writes the document and closes. A peer with nothing to be
// given gets a stream reset, which reads the same as "no answer".
func (s *Service) serveRelease(stream network.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(releaseTimeout))
	signed := s.release.take(stream.Conn().RemotePeer())
	if len(signed) == 0 {
		_ = stream.Reset()
		return
	}
	if _, err := stream.Write(signed); err != nil {
		_ = stream.Reset()
	}
}

// FetchReleases asks the peers this node is connected to for their signed
// release documents. Unverified: the caller checks every one.
func (s *Service) FetchReleases(ctx context.Context) ([]PeerRelease, error) {
	s.mu.Lock()
	h := s.host
	s.mu.Unlock()
	if h == nil {
		return nil, errors.New("dht: node is not running")
	}
	peers := h.Network().Peers()
	if len(peers) > releaseFetchPeers {
		peers = peers[:releaseFetchPeers]
	}
	var (
		wait    sync.WaitGroup
		mu      sync.Mutex
		results []PeerRelease
	)
	for _, id := range peers {
		wait.Add(1)
		go func(id peer.ID) {
			defer wait.Done()
			signed, err := readRelease(ctx, func(ctx context.Context) (network.Stream, error) {
				return h.NewStream(ctx, id, ReleaseProtocol)
			})
			if err != nil || len(signed) == 0 {
				return
			}
			mu.Lock()
			results = append(results, PeerRelease{Peer: id.String(), Signed: signed})
			mu.Unlock()
		}(id)
	}
	wait.Wait()
	return results, nil
}

func readRelease(ctx context.Context, open func(context.Context) (network.Stream, error)) ([]byte, error) {
	streamCtx, cancel := context.WithTimeout(ctx, releaseTimeout)
	defer cancel()
	stream, err := open(streamCtx)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(releaseTimeout))
	var body bytes.Buffer
	if _, err := io.Copy(&body, io.LimitReader(stream, maxReleaseBytes+1)); err != nil {
		return nil, err
	}
	if body.Len() > maxReleaseBytes {
		_ = stream.Reset()
		return nil, ErrReleaseTooLarge
	}
	return body.Bytes(), nil
}
