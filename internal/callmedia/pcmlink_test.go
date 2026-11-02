package callmedia

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestPCMLinkOwnsCallMediaAndBridgesBothDirections(t *testing.T) {
	format := testFormat(8000)
	core, opener, _ := testCore(t, format)
	authorizeCall(t, core, "call-pcm")
	active := ActiveCall{ID: "call-pcm", State: CallStateActive}

	link, err := core.AttachPCM(context.Background(), active, "airsim-device-1")
	if err != nil {
		t.Fatal(err)
	}
	if link.Format() != format {
		t.Fatalf("link format = %+v, want %+v", link.Format(), format)
	}

	// A browser cannot take the call while the PCM client owns it.
	browser := newTestBrowser(t)
	offer := testOffer("call-pcm", browser.offer(t))
	offer.OwnerToken = "browser-token"
	if _, err := core.Exchange(context.Background(), offer); !errors.Is(err, ErrCallInUse) {
		t.Fatalf("browser exchange error = %v, want ErrCallInUse", err)
	}
	if _, err := core.AttachPCM(context.Background(), active, "airsim-device-2"); !errors.Is(err, ErrCallInUse) {
		t.Fatalf("second PCM client error = %v, want ErrCallInUse", err)
	}

	remote := bytes.Repeat([]byte{0x11, 0x22}, format.FrameBytes()/2)
	opener.endpoint.read <- remote
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	for {
		frame, err := link.ReadDownlink(ctx)
		if err != nil {
			t.Fatalf("read downlink: %v", err)
		}
		if bytes.Equal(frame, remote) {
			break
		}
	}

	local := bytes.Repeat([]byte{0x33, 0x44}, format.FrameBytes()/2)
	if queued, err := link.WriteUplink(local); err != nil || !queued {
		t.Fatal(err)
	}
	for {
		frame := receive(t, opener.endpoint.writes)
		if bytes.Equal(frame, local) {
			break
		}
	}

	// Closing the link frees the owner slot without ending the call media.
	link.Close()
	receive(t, link.Done())
	if _, err := link.ReadDownlink(ctx); err == nil {
		t.Fatal("a closed link still read audio")
	}
	eventually(t, func() bool {
		result, err := core.Exchange(context.Background(), offer)
		if err != nil {
			return false
		}
		browser.applyAnswer(t, result.AnswerSDP)
		return true
	})
	if got := opener.endpoint.closeCalls.Load(); got != 0 {
		t.Fatalf("closing the PCM link closed the endpoint %d times", got)
	}
	if got := opener.opens.Load(); got != 1 {
		t.Fatalf("endpoint opens = %d, want 1", got)
	}
}

func TestPCMLinkSameTokenReplacesItsOwnLink(t *testing.T) {
	core, _, _ := testCore(t, testFormat(8000))
	authorizeCall(t, core, "call-pcm-replace")
	active := ActiveCall{ID: "call-pcm-replace", State: CallStateActive}

	first, err := core.AttachPCM(context.Background(), active, "airsim-device-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := core.AttachPCM(context.Background(), active, "airsim-device-1")
	if err != nil {
		t.Fatalf("same-token reattach: %v", err)
	}
	receive(t, first.Done())
	select {
	case <-second.Done():
		t.Fatal("the replacement link stopped")
	default:
	}
	second.Close()
}

func TestPCMLinkEndsWhenTheCallEnds(t *testing.T) {
	core, _, _ := testCore(t, testFormat(8000))
	authorizeCall(t, core, "call-pcm-end")
	link, err := core.AttachPCM(context.Background(), ActiveCall{ID: "call-pcm-end", State: CallStateActive}, "airsim-device-1")
	if err != nil {
		t.Fatal(err)
	}
	authorizeCall(t, core)
	receive(t, link.Done())
	if _, err := core.AttachPCM(context.Background(), ActiveCall{ID: "call-pcm-end", State: CallStateActive}, "airsim-device-1"); err == nil {
		t.Fatal("attached to a call that is no longer active")
	}
}
