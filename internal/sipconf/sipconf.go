// Package sipconf generates the Asterisk + chan_quectel configuration for the
// second call route (see HANDOFF-CN.md section 6f):
//
//	EG25-G / EC20 module -> Asterisk (chan_quectel, serial PCM audio)
//	                     -> PJSIP extension -> Groundwire on iPhone
//
// The generator only renders configuration text. Writing files, reloading
// Asterisk and protecting the rendered secrets are the caller's job: the
// rendered PJSIP file contains the SIP password in clear text because
// Asterisk and the SIP client both need it, so the caller must store it with
// restrictive permissions (0600) and never log it.
package sipconf

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
)

// File names produced by Build.
const (
	FileQuectel     = "quectel.conf"
	FilePJSIP       = "pjsip_ec20.conf"
	FileExtensions  = "extensions_ec20.conf"
	FileRTP         = "rtp_ec20.conf"
	FileUdevRules   = "99-quectel-ec20.rules"
	FileGroundwire  = "groundwire.txt"
	quectelTemplate = "Quectel/%s/${EXTEN}"
)

// Defaults applied by ApplyDefaults.
const (
	DefaultPort       = 5160
	DefaultTransport  = "tcp"
	DefaultRTPStart   = 10000
	DefaultRTPEnd     = 10010
	DefaultDevice     = "quectel0"
	DefaultContext    = "from-internal"
	DefaultInbound    = "incoming-quectel"
	DefaultVendor     = "2c7c" // Quectel
	DefaultProduct    = "0125" // EG25-G / EC25
	DefaultAudioIface = "03"
	DefaultDataIface  = "02"
	DefaultAudioTTY   = "/dev/ttyEC20-audio"
	DefaultDataTTY    = "/dev/ttyEC20-data"
	DefaultDomain     = "CHANGE-ME.local"
)

var (
	extensionPattern = regexp.MustCompile(`^[0-9]{3,8}$`)
	usbIDPattern     = regexp.MustCompile(`^[0-9a-fA-F]{4}$`)
)

// Options describes one Asterisk + module deployment.
type Options struct {
	Extension  string // SIP extension number, for example "6001".
	Password   string // SIP password; rendered in clear text, never logged.
	Domain     string // NAS IP or DDNS name used by the iPhone client.
	Port       int    // SIP listen port (TCP by default).
	Transport  string // tcp, udp or tls.
	RTPStart   int    // First RTP port.
	RTPEnd     int    // Last RTP port (inclusive).
	AudioTTY   string // chan_quectel audio device (serial PCM, not UAC).
	DataTTY    string // chan_quectel data device (AT commands).
	DeviceName string // chan_quectel device section name.
	Context    string // Dialplan context for outbound calls.
	// InboundContext is the dialplan context chan_quectel sends calls to.
	InboundContext string
	USBVendor      string // USB idVendor used by the udev rule.
	USBProduct     string // USB idProduct used by the udev rule.
	AudioIface     string // USB interface number of the audio (PCM) port.
	DataIface      string // USB interface number of the data (AT) port.
}

// Files maps a rendered file name to its content.
type Files map[string]string

// ApplyDefaults fills in the documented defaults for empty fields.
func (o *Options) ApplyDefaults() {
	if o.Port == 0 {
		o.Port = DefaultPort
	}
	if strings.TrimSpace(o.Transport) == "" {
		o.Transport = DefaultTransport
	}
	if o.RTPStart == 0 {
		o.RTPStart = DefaultRTPStart
	}
	if o.RTPEnd == 0 {
		o.RTPEnd = DefaultRTPEnd
	}
	if strings.TrimSpace(o.AudioTTY) == "" {
		o.AudioTTY = DefaultAudioTTY
	}
	if strings.TrimSpace(o.DataTTY) == "" {
		o.DataTTY = DefaultDataTTY
	}
	if strings.TrimSpace(o.DeviceName) == "" {
		o.DeviceName = DefaultDevice
	}
	if strings.TrimSpace(o.Context) == "" {
		o.Context = DefaultContext
	}
	if strings.TrimSpace(o.InboundContext) == "" {
		o.InboundContext = DefaultInbound
	}
	if strings.TrimSpace(o.USBVendor) == "" {
		o.USBVendor = DefaultVendor
	}
	if strings.TrimSpace(o.USBProduct) == "" {
		o.USBProduct = DefaultProduct
	}
	if strings.TrimSpace(o.AudioIface) == "" {
		o.AudioIface = DefaultAudioIface
	}
	if strings.TrimSpace(o.DataIface) == "" {
		o.DataIface = DefaultDataIface
	}
	if strings.TrimSpace(o.Domain) == "" {
		o.Domain = DefaultDomain
	}
	o.Transport = strings.ToLower(strings.TrimSpace(o.Transport))
	o.Extension = strings.TrimSpace(o.Extension)
}

// Validate reports the first problem it finds with the options.
func (o Options) Validate() error {
	if !extensionPattern.MatchString(o.Extension) {
		return fmt.Errorf("extension %q must be 3-8 digits", o.Extension)
	}
	if len(o.Password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	if strings.TrimSpace(o.Domain) == "" {
		return fmt.Errorf("domain is required")
	}
	if o.Port < 1024 || o.Port > 65535 {
		return fmt.Errorf("port %d out of range 1024-65535", o.Port)
	}
	switch o.Transport {
	case "tcp", "udp", "tls":
	default:
		return fmt.Errorf("transport %q must be tcp, udp or tls", o.Transport)
	}
	if o.RTPStart < 1024 || o.RTPEnd > 65535 || o.RTPStart > o.RTPEnd {
		return fmt.Errorf("rtp range %d-%d is invalid", o.RTPStart, o.RTPEnd)
	}
	if o.RTPEnd-o.RTPStart < 1 {
		return fmt.Errorf("rtp range %d-%d must contain at least 2 ports", o.RTPStart, o.RTPEnd)
	}
	if !strings.HasPrefix(o.AudioTTY, "/dev/") || !strings.HasPrefix(o.DataTTY, "/dev/") {
		return fmt.Errorf("audio/data tty must be absolute /dev paths")
	}
	if o.AudioTTY == o.DataTTY {
		return fmt.Errorf("audio and data tty must differ")
	}
	if !usbIDPattern.MatchString(o.USBVendor) || !usbIDPattern.MatchString(o.USBProduct) {
		return fmt.Errorf("usb vendor/product must be 4 hex digits")
	}
	if o.USBVendor == "" || o.AudioIface == "" || o.DataIface == "" {
		return fmt.Errorf("usb interface numbers are required")
	}
	if o.AudioIface == o.DataIface {
		return fmt.Errorf("audio and data usb interface must differ")
	}
	return nil
}

// Build renders every Asterisk file for the deployment. Options are defaulted
// and validated first, so a successful Build always returns all six files.
func Build(options Options) (Files, error) {
	options.ApplyDefaults()
	if err := options.Validate(); err != nil {
		return nil, err
	}
	files := Files{
		FileQuectel:    renderQuectel(options),
		FilePJSIP:      renderPJSIP(options),
		FileExtensions: renderExtensions(options),
		FileRTP:        renderRTP(options),
		FileUdevRules:  renderUdev(options),
		FileGroundwire: renderGroundwire(options),
	}
	return files, nil
}

// Names returns the rendered file names in a stable order.
func (f Files) Names() []string {
	names := make([]string, 0, len(f))
	for name := range f {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func header(note string) string {
	return fmt.Sprintf("; Generated by NasSimHub internal/sipconf - DO NOT EDIT BY HAND\n; %s\n", note)
}

func renderQuectel(o Options) string {
	return header("chan_quectel: serial PCM audio is preferred over UAC (UAC 实机有爆音/漂移)") +
		fmt.Sprintf(`
[general]
interval=15

[%s]
context=%s
audio=%s
data=%s
txgain=-5
rxgain=0
; 串口 PCM 音频（USB 接口 %s）以实机 AT+QCFG="usbcfg" 输出为准；txgain=-5 防爆音
`, o.DeviceName, o.InboundContext, o.AudioTTY, o.DataTTY, o.AudioIface)
}

func renderPJSIP(o Options) string {
	transport := fmt.Sprintf("[transport-%s]\ntype=transport\nprotocol=%s\nbind=0.0.0.0:%d\n\n", o.Transport, o.Transport, o.Port)
	return header("PJSIP endpoint for the iPhone SIP client (Groundwire)") + "\n" +
		transport +
		fmt.Sprintf(`[%s]
type=endpoint
transport=transport-%s
context=%s
disallow=all
allow=alaw,ulaw
auth=%s
aors=%s
direct_media=no
force_rport=yes
rewrite_contact=yes
rtp_symmetric=yes
ice_support=yes

[%s]
type=auth
auth_type=userpass
username=%s
password=%s

[%s]
type=aor
max_contacts=1
remove_existing=yes
`, o.Extension, o.Transport, o.Context, o.Extension, o.Extension,
			o.Extension, o.Extension, o.Password,
			o.Extension)
}

func renderExtensions(o Options) string {
	return header("Dialplan: extension -> module outbound, module -> extension inbound") +
		fmt.Sprintf(`
[%s]
; 呼出：iPhone 拨号 -> 模块
exten => _X.,1,NoOp(Outbound via %s)
 same => n,Dial(%s,60,T)
 same => n,Hangup()

[%s]
; 呼入：模块来电 -> iPhone；通话中收短信可能使模块异常（chan_quectel 已知风险）
exten => s,1,NoOp(Inbound from %s)
 same => n,Wait(1)
 same => n,Dial(PJSIP/%s,60,rtT)
 same => n,Hangup()
`, o.Context, o.DeviceName, fmt.Sprintf(quectelTemplate, o.DeviceName),
			o.InboundContext, o.DeviceName, o.Extension)
}

func renderRTP(o Options) string {
	return header("RTP range must be forwarded/allowed on the NAS firewall") +
		fmt.Sprintf(`
[general]
rtpstart=%d
rtpend=%d
icesupport=yes
stunaddr=stun.l.google.com:19302
`, o.RTPStart, o.RTPEnd)
}

func renderUdev(o Options) string {
	return fmt.Sprintf(`# Generated by NasSimHub internal/sipconf
# 固定模块串口名，避免重启后 ttyUSB 编号漂移（编号以实机 lsusb -t 为准）
SUBSYSTEM=="tty", ATTRS{idVendor}=="%s", ATTRS{idProduct}=="%s", ENV{ID_USB_INTERFACE_NUM}=="%s", SYMLINK+="ttyEC20-audio"
SUBSYSTEM=="tty", ATTRS{idVendor}=="%s", ATTRS{idProduct}=="%s", ENV{ID_USB_INTERFACE_NUM}=="%s", SYMLINK+="ttyEC20-data"

# 应用：
#   sudo cp 99-quectel-ec20.rules /etc/udev/rules.d/
#   sudo udevadm control --reload-rules && sudo udevadm trigger
`, o.USBVendor, o.USBProduct, o.AudioIface, o.USBVendor, o.USBProduct, o.DataIface)
}

func renderGroundwire(o Options) string {
	domainNote := ""
	if o.Domain == DefaultDomain {
		domainNote = "  <-- 改成 NAS 的内网 IP 或 DDNS 域名"
	}
	return fmt.Sprintf(`NasSimHub SIP 凭据（Groundwire / iPhone）
-----------------------------------------
账号 / Username : %s
密码 / Password : %s
域名 / Domain   : %s%s
端口 / Port     : %d
传输 / Transport: %s

iPhone 端（Groundwire）设置要点：
1. 新建 SIP 账号，按上面六项填写
2. 高级里打开 TCP 与 ICE
3. 允许后台刷新 + 通知权限（锁屏靠 Push 唤醒来电）
4. 保持账号注册；RTP %d-%d/UDP 与 SIP %d/%s 需要在 NAS 防火墙放行

部署提示：
- quectel.conf / pjsip_ec20.conf / extensions_ec20.conf / rtp_ec20.conf -> /etc/asterisk/
- udev 规则 -> /etc/udev/rules.d/
- 配置里密码是明文（Asterisk 需要），文件权限设 0600，别提交进仓库
- 生效：asterisk -rx "module reload chan_quectel.so" && asterisk -rx "pjsip reload"
`, o.Extension, o.Password, o.Domain, domainNote, o.Port, strings.ToUpper(o.Transport),
		o.RTPStart, o.RTPEnd, o.Port, strings.ToUpper(o.Transport))
}

// ListenAddress renders the bind address used by the PJSIP transport.
func (o Options) ListenAddress() string {
	return net.JoinHostPort("0.0.0.0", fmt.Sprint(o.Port))
}
