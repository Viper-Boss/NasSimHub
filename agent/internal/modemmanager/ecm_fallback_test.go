package modemmanager

import (
	"errors"
	"testing"

	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

func ecmTestConnection() domain.DataConnection {
	return domain.DataConnection{
		ID:        "bearer1",
		Connected: true,
		APN:       "cmnet",
		Interface: "ttyUSB0",
		IPv4: domain.IPConfiguration{
			Method: "ppp",
		},
		IPv6: domain.IPConfiguration{
			Method: "ppp",
		},
	}
}

func TestECMFallbackRewritesPPPontoECMNetdev(t *testing.T) {
	resolver := func(string) (string, error) { return "enx3209127b07e1", nil }
	connection := ecmTestConnection()
	rewritten, changed := ecmFallbackConnection(resolver, connection)
	if !changed {
		t.Fatal("expected the PPP connection to be rewritten")
	}
	if rewritten.Interface != "enx3209127b07e1" {
		t.Fatalf("interface = %q, want the ECM netdev", rewritten.Interface)
	}
	if rewritten.IPv4.Method != "dhcp" {
		t.Fatalf("IPv4 method = %q, want dhcp", rewritten.IPv4.Method)
	}
	if rewritten.IPv4.Address != "" || rewritten.IPv4.Gateway != "" ||
		len(rewritten.IPv4.DNS) != 0 || rewritten.IPv4.MTU != 0 {
		t.Fatalf("IPv4 lease fields not cleared: %+v", rewritten.IPv4)
	}
	if rewritten.IPv6.Method != "" {
		t.Fatalf("IPv6 method = %q, want empty (deferred)", rewritten.IPv6.Method)
	}
	if connection.Interface != "ttyUSB0" || connection.IPv4.Method != "ppp" {
		t.Fatalf("original connection was mutated: %+v", connection)
	}
}

func TestECMFallbackLeavesNonPPPConnectionsAlone(t *testing.T) {
	resolver := func(string) (string, error) {
		t.Fatal("resolver must not be called for non-PPP connections")
		return "", nil
	}
	connection := ecmTestConnection()
	connection.IPv4.Method = "dhcp"
	if _, changed := ecmFallbackConnection(resolver, connection); changed {
		t.Fatal("expected the dhcp connection to pass through unchanged")
	}
}

func TestECMFallbackKeepsConnectionWhenNoNetdev(t *testing.T) {
	resolver := func(string) (string, error) { return "", errors.New("no sibling netdev") }
	connection := ecmTestConnection()
	rewritten, changed := ecmFallbackConnection(resolver, connection)
	if changed {
		t.Fatal("expected the connection to pass through when no ECM netdev exists")
	}
	if rewritten.IPv4.Method != "ppp" {
		t.Fatalf("IPv4 method = %q, want ppp", rewritten.IPv4.Method)
	}
}

func TestECMFallbackKeepsNonPPPIPv6(t *testing.T) {
	resolver := func(string) (string, error) { return "usb0", nil }
	connection := ecmTestConnection()
	connection.IPv6 = domain.IPConfiguration{Method: "dhcp"}
	rewritten, changed := ecmFallbackConnection(resolver, connection)
	if !changed {
		t.Fatal("expected the PPP connection to be rewritten")
	}
	if rewritten.IPv6.Method != "dhcp" {
		t.Fatalf("IPv6 method = %q, want dhcp preserved", rewritten.IPv6.Method)
	}
}

func TestUSBDeviceDirResolvesSerialAndNetdevSymlinks(t *testing.T) {
	prefix := "/sys/devices/platform/fd000000.usb/xhci-hcd.2.auto/usb2/2-1"
	cases := map[string]string{
		prefix + "/2-1:1.2/ttyUSB2": prefix,
		prefix + "/2-1:1.4":         prefix,
	}
	for path, expected := range cases {
		actual, err := usbDeviceDir(path)
		if err != nil {
			t.Fatalf("usbDeviceDir(%s): %v", path, err)
		}
		if actual != expected {
			t.Fatalf("usbDeviceDir(%s) = %q, want %q", path, actual, expected)
		}
	}
	if _, err := usbDeviceDir("/sys/devices/platform/fd000000.usb/xhci-hcd.2.auto/usb2/2-1"); err == nil {
		t.Fatal("expected an error for a sysfs path without a USB interface component")
	}
}
