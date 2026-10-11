package localadmin

import (
	"context"
	"errors"
	"github.com/human-agent65535/nassimhub-node/proto"
	"testing"
	"time"
)

type powerStub struct {
	calls int
	err   error
}

func (s *powerStub) Status(context.Context) (proto.WiFiStatus, error) { return proto.WiFiStatus{}, nil }
func (s *powerStub) SetRadioEnabled(_ context.Context, e bool) (proto.WiFiStatus, error) {
	s.calls++
	return proto.WiFiStatus{RadioEnabled: &e}, s.err
}
func TestWiFiPowerRequiresSessionAndCSRFAndPreservesErrors(t *testing.T) {
	a, h, cookie, csrf := configured(t, &clock{now: time.Unix(1_800_000_000, 0)}, &spy{})
	s := &powerStub{}
	a.Attach(Sources{WiFi: s})
	path := "/admin/api/v1/wifi/radio"
	if w := call(t, h, path, "POST", map[string]bool{"enabled": false}, nil, csrf, origin); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(t, h, path, "POST", map[string]bool{"enabled": false}, cookie, "", origin); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call(t, h, path, "POST", map[string]bool{"enabled": false}, cookie, csrf, "https://other.test"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if s.calls != 0 {
		t.Fatal("unauthorized power operation")
	}
	if w := call(t, h, path, "POST", map[string]bool{}, cookie, csrf, origin); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call(t, h, path, "POST", map[string]bool{"enabled": false}, cookie, csrf, origin); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	s.err = proto.Conflict("wifi", "busy")
	if w := call(t, h, path, "POST", map[string]bool{"enabled": true}, cookie, csrf, origin); w.Code != 409 {
		t.Fatal(w.Code)
	}
	s.err = errors.New("private backend detail")
	if w := call(t, h, path, "POST", map[string]bool{"enabled": true}, cookie, csrf, origin); w.Code != 503 {
		t.Fatal(w.Code)
	}
}
