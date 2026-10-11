package radios

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestUSBBlocksCellularAndSupervisionNeverEnablesFallback(t *testing.T) {
	usb := true
	connected := false
	calls := []string{}
	b := &Backend{Dir: t.TempDir(), Run: func(_ context.Context, name string, args ...string) (string, error) {
		v := strings.Join(args, " ")
		calls = append(calls, name+" "+v)
		if strings.Contains(v, "DEVICE,TYPE") {
			s := ""
			if usb {
				s = "usb0:ethernet:connected:nassimhub-usb\n"
			}
			if connected {
				s += "wwan0:gsm:connected:airsimhub-cellular"
			}
			return s, nil
		}
		if strings.Contains(v, "connection down") {
			connected = false
		}
		return "", nil
	}}
	ctx := context.Background()
	if _, e := b.Execute(ctx, Request{Action: "cellular_config", APN: "3gnet"}); e != nil {
		t.Fatal(e)
	}
	on := true
	if _, e := b.Execute(ctx, Request{Action: "cellular_power", Enabled: &on}); e == nil {
		t.Fatal("USB permitted cellular")
	}
	b.Supervise(ctx)
	for _, c := range calls {
		if strings.Contains(c, "connection up") || strings.Contains(c, "radio wwan") || strings.Contains(c, "simple-connect") {
			t.Fatal("fallback or modem shutdown", c)
		}
	}
	connected = true
	_ = b.save(settings{Cellular: true, APN: "3gnet"})
	b.Supervise(ctx)
	if connected {
		t.Fatal("USB did not stop owned data connection")
	}
	c, _ := b.config()
	if c.Cellular {
		t.Fatal("remembered automatic data permission")
	}
	usb = false
	b.Restore(ctx)
	for _, c := range calls {
		if strings.Contains(c, "connection up") {
			t.Fatal("startup enables data")
		}
	}
}
func TestRadioRejectsUnboundedInputsAndSecrets(t *testing.T) {
	for _, q := range []Request{{Action: "bluetooth_pair", Address: "--help"}, {Action: "bluetooth_alias", Alias: "x\ncommand"}, {Action: "cellular_config", APN: "a;command"}, {Action: "cellular_config", APN: "a", Password: "secret"}, {Action: "cellular_power"}, {Action: "gps_config", Refresh: 121}} {
		if Validate(q) == nil {
			t.Fatal("invalid input accepted", q.Action)
		}
	}
	b := &Backend{Dir: t.TempDir(), Run: func(context.Context, string, ...string) (string, error) {
		return "", errors.New("private device detail")
	}}
	off := false
	if _, e := b.Execute(context.Background(), Request{Action: "bluetooth_power", Enabled: &off}); e == nil || strings.Contains(e.Error(), "private") {
		t.Fatal("reported success or leaked device detail")
	}
}

func TestBluetoothSuccessNeedsObservedState(t *testing.T) {
	b := &Backend{Dir: t.TempDir(), Run: func(_ context.Context, _ string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "show":
			return "Controller 00:11:22:33:44:55\n Powered: yes", nil
		case "devices", "devices Paired":
			return "Device 11:22:33:44:55:66 Test", nil
		case "--timeout 2 info 11:22:33:44:55:66", "info 11:22:33:44:55:66":
			return "Paired: yes\nConnected: no", nil
		default:
			return "Attempting to connect", nil
		}
	}}
	if _, err := b.Execute(context.Background(), Request{Action: "bluetooth_connect", Address: "11:22:33:44:55:66"}); err == nil {
		t.Fatal("reported unobserved connection as success")
	}
}

func TestUnknownUSBStateCannotEnablePaidData(t *testing.T) {
	b := &Backend{Dir: t.TempDir(), Run: func(_ context.Context, _ string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "connection up") {
			t.Fatal("enabled mobile data with unknown USB state")
		}
		return "", errors.New("network query failed")
	}}
	if err := b.save(settings{APN: "3gnet"}); err != nil {
		t.Fatal(err)
	}
	on := true
	if _, err := b.Execute(context.Background(), Request{Action: "cellular_power", Enabled: &on}); err == nil {
		t.Fatal("permitted data with unknown state")
	}
}
