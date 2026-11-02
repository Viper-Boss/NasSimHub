package callmedia

import (
	"context"
	"fmt"
	"sync"
)

// PCMLink is a raw-PCM media owner for one active call: the remote party's
// audio is read frame by frame and the local talker's audio is written back,
// both in the host endpoint's own PCMFormat. It takes the same single owner
// slot a browser WebRTC session does, so a call is either on the browser or on
// a PCM client (the AirSIM app's VoWLAN audio), never both at once.
type PCMLink struct {
	ctx       context.Context
	cancel    context.CancelFunc
	hub       *mediaHub
	sub       *DuplexSubscription
	done      chan struct{}
	connected chan struct{}
	once      sync.Once
}

// AttachPCM claims the media owner slot of an active call for a raw PCM
// client. ownerToken follows the same rules as a WebRTC offer: the same token
// replaces its own earlier link, any other token gets ErrCallInUse.
func (c *Core) AttachPCM(ctx context.Context, active ActiveCall, ownerToken string) (*PCMLink, error) {
	if c == nil {
		return nil, ErrCoreClosed
	}
	ctx = normalizeContext(ctx)
	call, err := normalizeActiveCall(active)
	if err != nil {
		return nil, fmt.Errorf("attach PCM: %w", err)
	}
	ownerToken, err = normalizeOwnerToken(ownerToken)
	if err != nil {
		return nil, fmt.Errorf("attach PCM: owner token: %w", err)
	}
	lifetime, owner, err := c.reserve(call.ID, ownerToken)
	if err != nil {
		return nil, fmt.Errorf("attach PCM: %w", err)
	}
	defer c.prepares.Done()
	committed := false
	defer func() {
		if !committed {
			c.releaseReservation(call.ID, owner)
		}
	}()
	hub, err := c.acquireHub(ctx, call, lifetime)
	if err != nil {
		return nil, fmt.Errorf("attach PCM: %w", err)
	}
	sub, err := hub.Subscribe(defaultBrowserQueueCapacity)
	if err != nil {
		return nil, fmt.Errorf("attach PCM: subscribe PCM hub: %w", err)
	}
	if err := sub.Start(ctx); err != nil {
		_ = sub.Close()
		return nil, fmt.Errorf("attach PCM: start PCM hub: %w", err)
	}
	linkCtx, cancel := context.WithCancel(owner.ctx)
	link := &PCMLink{
		ctx:       linkCtx,
		cancel:    cancel,
		hub:       hub,
		sub:       sub,
		done:      make(chan struct{}),
		connected: make(chan struct{}),
	}
	close(link.connected)
	go func() {
		select {
		case <-linkCtx.Done():
		case <-hub.Done():
		}
		link.stop(nil)
	}()
	if err := c.commit(call.ID, lifetime, owner, link); err != nil {
		link.stop(nil)
		return nil, fmt.Errorf("attach PCM: %w", err)
	}
	committed = true
	go c.releaseWhenDone(call.ID, owner, link)
	return link, nil
}

// Format is the host endpoint's PCM format; frames must be exactly
// Format().FrameBytes() long.
func (l *PCMLink) Format() PCMFormat { return l.sub.Format() }

// ReadDownlink returns the next frame of the remote party's audio.
func (l *PCMLink) ReadDownlink(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithCancel(normalizeContext(ctx))
	defer cancel()
	stop := context.AfterFunc(l.ctx, cancel)
	defer stop()
	frame, err := l.sub.Next(ctx)
	if err != nil {
		return nil, err
	}
	return frame.DownlinkPCM, nil
}

// WriteUplink queues one frame of the local talker's audio. Callers should
// pace writes at the frame rate; a frame that finds the playback queue full
// is dropped (reported as false) rather than ending the call's media.
func (l *PCMLink) WriteUplink(frame []byte) (bool, error) {
	if l.ctx.Err() != nil {
		return false, ErrCanceled
	}
	return l.hub.offerPCM(frame)
}

// Close releases the owner slot; the call itself continues.
func (l *PCMLink) Close() { l.stop(nil) }

func (l *PCMLink) Done() <-chan struct{} { return l.done }

func (l *PCMLink) stop(error) {
	l.once.Do(func() {
		l.cancel()
		_ = l.sub.Close()
		close(l.done)
	})
}

func (l *PCMLink) shutdown(ctx context.Context) error {
	l.stop(nil)
	select {
	case <-l.done:
		return nil
	case <-normalizeContext(ctx).Done():
		return ErrCanceled
	}
}

func (l *PCMLink) connectedSignal() <-chan struct{} { return l.connected }
