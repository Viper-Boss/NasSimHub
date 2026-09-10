package qdc507usb

import "testing"

// TestDriverHijackedTruthTable covers the descriptor-independent driver-ownership
// check. A non-serial QDC507 interface is hijacked only when it exists and is
// bound to a non-functional driver; an unbound interface (empty driver) is a
// different fault and must not be reported as a hijack.
func TestDriverHijackedTruthTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		iface    USBInterfaces
		hijacked bool
	}{
		{
			name:     "ADB bound to option is hijacked",
			iface:    USBInterfaces{ADB: true, ADBDriver: "option"},
			hijacked: true,
		},
		{
			name:     "ADB bound to snd-usb-audio is hijacked",
			iface:    USBInterfaces{ADB: true, ADBDriver: "snd-usb-audio"},
			hijacked: true,
		},
		{
			name:     "ADB bound to usbfs is correct",
			iface:    USBInterfaces{ADB: true, ADBDriver: "usbfs"},
			hijacked: false,
		},
		{
			name:     "ADB bound to usb is correct",
			iface:    USBInterfaces{ADB: true, ADBDriver: "usb"},
			hijacked: false,
		},
		{
			name:     "ADB present but unbound is not a hijack",
			iface:    USBInterfaces{ADB: true, ADBDriver: ""},
			hijacked: false,
		},
		{
			name:     "UAC bound to option is hijacked",
			iface:    USBInterfaces{UACControl: true, UACStreaming: true, UACDriver: "option"},
			hijacked: true,
		},
		{
			name:     "UAC bound to snd-usb-audio is correct",
			iface:    USBInterfaces{UACControl: true, UACStreaming: true, UACDriver: "snd-usb-audio"},
			hijacked: false,
		},
		{
			name:     "UAC bound to usb is correct",
			iface:    USBInterfaces{UACControl: true, UACStreaming: true, UACDriver: "usb"},
			hijacked: false,
		},
		{
			name:     "UAC present but unbound is not a hijack",
			iface:    USBInterfaces{UACControl: true, UACStreaming: true, UACDriver: ""},
			hijacked: false,
		},
		{
			name:     "clean descriptors are not hijacked",
			iface:    USBInterfaces{ADB: true, ADBDriver: "usbfs", UACControl: true, UACStreaming: true, UACDriver: "snd-usb-audio"},
			hijacked: false,
		},
		{
			// The option driver can claim only the streaming interfaces while
			// leaving the UAC control interface correctly bound. That must still
			// be detected, otherwise the device looks ready yet stays silent.
			name: "streaming-only hijack behind a healthy control interface is caught",
			iface: USBInterfaces{
				ADB: true, ADBDriver: "usbfs",
				UACControl: true, UACDriver: "snd-usb-audio",
				UACStreaming: true, UACStreamingDriver: "option",
			},
			hijacked: true,
		},
		{
			name: "streaming bound to snd-usb-audio is correct",
			iface: USBInterfaces{
				ADB: true, ADBDriver: "usbfs",
				UACControl: true, UACDriver: "snd-usb-audio",
				UACStreaming: true, UACStreamingDriver: "snd-usb-audio",
			},
			hijacked: false,
		},
		{
			name: "streaming present but unbound is not a hijack",
			iface: USBInterfaces{
				ADB: true, ADBDriver: "usbfs",
				UACControl: true, UACDriver: "snd-usb-audio",
				UACStreaming: true, UACStreamingDriver: "",
			},
			hijacked: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.iface.driverHijacked(); got != tc.hijacked {
				t.Fatalf("driverHijacked() = %t, want %t for %+v", got, tc.hijacked, tc.iface)
			}
		})
	}
}
