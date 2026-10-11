package networkmanager

import (
	"context"
	"errors"
	"github.com/human-agent65535/nassimhub-node/proto"
	"strings"
	"testing"
)

func TestRadioOffPreservesProfileAndSurvivesBackendRestart(t *testing.T) {
	enabled := true
	fail := false
	calls := []string{}
	run := func(_ context.Context, args ...string) (string, error) {
		v := strings.Join(args, " ")
		calls = append(calls, v)
		switch {
		case v == "radio wifi":
			if enabled {
				return "enabled", nil
			}
			return "disabled", nil
		case v == "radio wifi off" || v == "radio wifi on":
			if fail {
				return "", errors.New("denied")
			}
			enabled = v == "radio wifi on"
			return "", nil
		case strings.Contains(v, "802-11-wireless.ssid"):
			return "Example-Saved", nil
		case strings.Contains(v, "DEVICE,TYPE,STATE,CONNECTION"):
			return "wlan0:wifi:disconnected:--", nil
		default:
			return "", nil
		}
	}
	ctx := context.Background()
	backend := New(Options{Run: run, ProvisioningAP: ProvisioningAPAuto})
	st, err := backend.SetRadioEnabled(ctx, false)
	if err != nil || st.State != proto.WiFiDisabled || st.SavedSSID != "Example-Saved" {
		t.Fatal(st, err)
	}
	restarted := New(Options{Run: run, ProvisioningAP: ProvisioningAPAuto})
	restarted.Tick(ctx)
	st, err = restarted.Status(ctx)
	if err != nil || st.RadioEnabled == nil || *st.RadioEnabled {
		t.Fatal(st, err)
	}
	_, err = restarted.Connect(ctx, proto.WiFiConnectRequest{SSID: "Example-New", Security: proto.WiFiSecurityOpen})
	if proto.CodeOf(err) != proto.ErrorFailedPrecondition {
		t.Fatal(err)
	}
	for _, c := range calls {
		if strings.Contains(c, "connection add") || strings.Contains(c, "connection delete") || strings.Contains(c, "usb") || strings.Contains(c, "wwan") {
			t.Fatal("radio changed unrelated settings", c)
		}
	}
	fail = true
	_, err = restarted.SetRadioEnabled(ctx, true)
	if err == nil || enabled {
		t.Fatal("failure reported success")
	}
	fail = false
	st, err = restarted.SetRadioEnabled(ctx, true)
	if err != nil || st.RadioEnabled == nil || !*st.RadioEnabled {
		t.Fatal(st, err)
	}
}
