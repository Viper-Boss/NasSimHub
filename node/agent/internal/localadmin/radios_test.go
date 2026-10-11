package localadmin

import (
	"context"
	"github.com/human-agent65535/nassimhub-node/agent/netbackend/radios"
	"testing"
	"time"
)

type radioSpy struct{ calls int }

func (s *radioSpy) Status(context.Context) (radios.Status, error) { return radios.Status{}, nil }
func (s *radioSpy) Execute(context.Context, radios.Request) (radios.Status, error) {
	s.calls++
	return radios.Status{}, nil
}
func TestRadioControlsRequireSessionOriginAndCSRF(t *testing.T) {
	a, h, cookie, csrf := configured(t, &clock{now: time.Unix(1_800_000_000, 0)}, &spy{})
	s := &radioSpy{}
	a.Attach(Sources{Radios: s})
	path := "/admin/api/v1/radios/change"
	body := map[string]any{"action": "bluetooth_power", "enabled": false}
	if w := call(t, h, path, "POST", body, nil, csrf, origin); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(t, h, path, "POST", body, cookie, "", origin); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call(t, h, path, "POST", body, cookie, csrf, "https://evil.test"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if s.calls != 0 {
		t.Fatal("unauthorized control")
	}
	if w := call(t, h, path, "POST", body, cookie, csrf, origin); w.Code != 200 || s.calls != 1 {
		t.Fatal(w.Code, s.calls)
	}
}
