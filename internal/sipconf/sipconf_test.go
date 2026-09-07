package sipconf

import (
	"strings"
	"testing"
)

func validOptions() Options {
	return Options{
		Extension: "6001",
		Password:  "sip-secret-42",
		Domain:    "192.168.1.9",
	}
}

func TestApplyDefaults(t *testing.T) {
	o := validOptions()
	o.ApplyDefaults()
	if o.Port != DefaultPort || o.Transport != DefaultTransport {
		t.Fatalf("port/transport defaults: got %d/%s", o.Port, o.Transport)
	}
	if o.RTPStart != DefaultRTPStart || o.RTPEnd != DefaultRTPEnd {
		t.Fatalf("rtp defaults: got %d-%d", o.RTPStart, o.RTPEnd)
	}
	if o.AudioTTY != DefaultAudioTTY || o.DataTTY != DefaultDataTTY {
		t.Fatalf("tty defaults: got %s/%s", o.AudioTTY, o.DataTTY)
	}
	if o.DeviceName != DefaultDevice || o.Context != DefaultContext || o.InboundContext != DefaultInbound {
		t.Fatalf("asterisk defaults: got %s/%s/%s", o.DeviceName, o.Context, o.InboundContext)
	}
	if o.USBVendor != DefaultVendor || o.USBProduct != DefaultProduct {
		t.Fatalf("usb defaults: got %s:%s", o.USBVendor, o.USBProduct)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Options)
		wantErr string
	}{
		{"extension empty", func(o *Options) { o.Extension = "" }, "3-8 digits"},
		{"extension letters", func(o *Options) { o.Extension = "60a1" }, "3-8 digits"},
		{"extension too long", func(o *Options) { o.Extension = "123456789" }, "3-8 digits"},
		{"short password", func(o *Options) { o.Password = "abcdefg" }, "at least 8"},
		{"empty domain", func(o *Options) { o.Domain = "  " }, "domain is required"},
		{"port low", func(o *Options) { o.Port = 80 }, "out of range"},
		{"port high", func(o *Options) { o.Port = 70000 }, "out of range"},
		{"bad transport", func(o *Options) { o.Transport = "sctp" }, "tcp, udp or tls"},
		{"rtp inverted", func(o *Options) { o.RTPStart, o.RTPEnd = 10010, 10000 }, "invalid"},
		{"rtp single port", func(o *Options) { o.RTPStart, o.RTPEnd = 10000, 10000 }, "at least 2 ports"},
		{"tty not absolute", func(o *Options) { o.AudioTTY = "ttyUSB3" }, "/dev paths"},
		{"tty duplicated", func(o *Options) { o.AudioTTY = o.DataTTY }, "must differ"},
		{"bad usb id", func(o *Options) { o.USBVendor = "zzzz" }, "4 hex digits"},
		{"iface duplicated", func(o *Options) { o.AudioIface = o.DataIface }, "must differ"},
	}
	for _, item := range cases {
		o := validOptions()
		o.ApplyDefaults()
		item.mutate(&o)
		if err := o.Validate(); err == nil {
			t.Errorf("%s: expected error", item.name)
		} else if !strings.Contains(err.Error(), item.wantErr) {
			t.Errorf("%s: error %q does not contain %q", item.name, err, item.wantErr)
		}
	}
}

func TestBuildFiles(t *testing.T) {
	files, err := Build(validOptions())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []string{FileUdevRules, FileExtensions, FileGroundwire, FilePJSIP, FileQuectel, FileRTP}
	got := files.Names()
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v", got, want)
		}
	}

	quectel := files[FileQuectel]
	for _, want := range []string{"txgain=-5", "audio=" + DefaultAudioTTY, "data=" + DefaultDataTTY, "context=" + DefaultInbound} {
		if !strings.Contains(quectel, want) {
			t.Errorf("quectel.conf missing %q:\n%s", want, quectel)
		}
	}

	pjsip := files[FilePJSIP]
	for _, want := range []string{"protocol=tcp", "bind=0.0.0.0:5160", "[6001]", "username=6001", "password=sip-secret-42", "ice_support=yes"} {
		if !strings.Contains(pjsip, want) {
			t.Errorf("pjsip_ec20.conf missing %q:\n%s", want, pjsip)
		}
	}

	dialplan := files[FileExtensions]
	for _, want := range []string{"Quectel/quectel0/${EXTEN}", "Dial(PJSIP/6001,60,rtT)", "[from-internal]", "[incoming-quectel]"} {
		if !strings.Contains(dialplan, want) {
			t.Errorf("extensions_ec20.conf missing %q:\n%s", want, dialplan)
		}
	}

	rtp := files[FileRTP]
	if !strings.Contains(rtp, "rtpstart=10000") || !strings.Contains(rtp, "rtpend=10010") {
		t.Errorf("rtp_ec20.conf wrong range:\n%s", rtp)
	}

	udev := files[FileUdevRules]
	for _, want := range []string{`ATTRS{idVendor}=="2c7c"`, `ATTRS{idProduct}=="0125"`, `SYMLINK+="ttyEC20-audio"`, `SYMLINK+="ttyEC20-data"`} {
		if !strings.Contains(udev, want) {
			t.Errorf("udev rule missing %q:\n%s", want, udev)
		}
	}

	credentials := files[FileGroundwire]
	for _, want := range []string{"6001", "sip-secret-42", "192.168.1.9", "5160", "TCP", "10000-10010"} {
		if !strings.Contains(credentials, want) {
			t.Errorf("groundwire.txt missing %q:\n%s", want, credentials)
		}
	}
}

func TestBuildCustomOptions(t *testing.T) {
	files, err := Build(Options{
		Extension:  "7002",
		Password:   "another-secret",
		Domain:     "nas.example.com",
		Port:       5062,
		Transport:  "UDP",
		RTPStart:   20000,
		RTPEnd:     20020,
		AudioTTY:   "/dev/ttyUSB4",
		DataTTY:    "/dev/ttyUSB2",
		DeviceName: "quectel1",
		AudioIface: "04",
		DataIface:  "02",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(files[FilePJSIP], "bind=0.0.0.0:5062") || !strings.Contains(files[FilePJSIP], "protocol=udp") {
		t.Errorf("custom transport not applied:\n%s", files[FilePJSIP])
	}
	if !strings.Contains(files[FileExtensions], "Quectel/quectel1/${EXTEN}") {
		t.Errorf("custom device not applied:\n%s", files[FileExtensions])
	}
	if !strings.Contains(files[FileQuectel], "audio=/dev/ttyUSB4") {
		t.Errorf("custom audio tty not applied:\n%s", files[FileQuectel])
	}
	if !strings.Contains(files[FileRTP], "rtpstart=20000") || !strings.Contains(files[FileRTP], "rtpend=20020") {
		t.Errorf("custom rtp range not applied:\n%s", files[FileRTP])
	}
}

func TestBuildRejectsInvalidOptions(t *testing.T) {
	files, err := Build(Options{Extension: "1", Password: "short"})
	if err == nil {
		t.Fatal("expected Build to fail")
	}
	if files != nil {
		t.Fatalf("files must be nil on error, got %v", files.Names())
	}
}

func TestListenAddress(t *testing.T) {
	o := validOptions()
	o.ApplyDefaults()
	if got := o.ListenAddress(); got != "0.0.0.0:5160" {
		t.Fatalf("ListenAddress = %q", got)
	}
}
