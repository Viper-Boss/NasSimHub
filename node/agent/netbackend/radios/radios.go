// Package radios runs bounded device controls as the existing service account.
package radios

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/human-agent65535/nassimhub-node/agent/internal/systemadmin"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const profile = "airsimhub-cellular"

type Request struct {
	Action   string `json:"action"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Address  string `json:"address,omitempty"`
	Alias    string `json:"alias,omitempty"`
	APN      string `json:"apn,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Refresh  int    `json:"refresh,omitempty"`
}
type Device struct {
	Address   string `json:"address"`
	Name      string `json:"name"`
	Paired    bool   `json:"paired"`
	Connected bool   `json:"connected"`
}
type Bluetooth struct {
	Supported bool     `json:"supported"`
	Powered   *bool    `json:"powered"`
	Alias     string   `json:"alias"`
	Devices   []Device `json:"devices"`
	Reason    string   `json:"reason,omitempty"`
}
type Cellular struct {
	Known         bool   `json:"known"`
	Enabled       bool   `json:"enabled"`
	Connected     bool   `json:"connected"`
	External      bool   `json:"external_connection"`
	APN           string `json:"apn"`
	Username      string `json:"username"`
	PasswordSaved bool   `json:"password_saved"`
	USBConnected  bool   `json:"usb_connected"`
	USBAddress    string `json:"usb_address"`
	Policy        string `json:"policy"`
}
type Status struct {
	Bluetooth  Bluetooth             `json:"bluetooth"`
	GPS        systemadmin.GPSStatus `json:"gps"`
	GPSControl bool                  `json:"gps_control"`
	Cellular   Cellular              `json:"cellular"`
	ObservedAt time.Time             `json:"observed_at"`
}
type settings struct {
	Bluetooth bool   `json:"bluetooth"`
	Alias     string `json:"alias"`
	GPS       bool   `json:"gps"`
	Refresh   int    `json:"gps_refresh"`
	Cellular  bool   `json:"cellular"`
	APN       string `json:"apn"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}
type Runner func(context.Context, string, ...string) (string, error)
type Backend struct {
	mu  sync.Mutex
	Dir string
	Run Runner
	GPS func(context.Context, Request) (systemadmin.GPSStatus, error)
}

var mac = regexp.MustCompile(`^(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$`)
var apn = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{0,99}$`)
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func Validate(r Request) error {
	if r.Username != "" || r.Password != "" {
		return errors.New("暂不支持需要用户名或密码的 APN")
	}
	if len(r.Alias) > 64 || len(r.Username) > 128 || len(r.Password) > 256 || strings.ContainsAny(r.Alias+r.Username+r.Password, "\x00\r\n") {
		return errors.New("invalid radio configuration")
	}
	common := func() bool {
		return r.Address == "" && r.Alias == "" && r.APN == "" && r.Username == "" && r.Password == "" && r.Refresh == 0
	}
	switch r.Action {
	case "status", "bluetooth_scan":
		if r.Enabled != nil || !common() {
			return errors.New("unexpected arguments")
		}
	case "bluetooth_power", "gps_power", "cellular_power":
		if r.Enabled == nil || !common() {
			return errors.New("enabled is required")
		}
	case "bluetooth_alias":
		if strings.TrimSpace(r.Alias) == "" || r.Enabled != nil || r.Address != "" || r.APN != "" || r.Username != "" || r.Password != "" || r.Refresh != 0 {
			return errors.New("invalid Bluetooth name")
		}
	case "bluetooth_pair", "bluetooth_connect", "bluetooth_disconnect", "bluetooth_remove":
		if !mac.MatchString(r.Address) || r.Enabled != nil || r.Alias != "" || r.APN != "" || r.Username != "" || r.Password != "" || r.Refresh != 0 {
			return errors.New("invalid Bluetooth address")
		}
	case "gps_config":
		if r.Refresh < 1 || r.Refresh > 120 || r.Enabled != nil || r.Address != "" || r.Alias != "" || r.APN != "" || r.Username != "" || r.Password != "" {
			return errors.New("GPS interval must be 1-120 seconds")
		}
	case "cellular_config":
		if !apn.MatchString(r.APN) || r.Enabled != nil || r.Address != "" || r.Alias != "" || r.Refresh != 0 {
			return errors.New("invalid APN")
		}
	default:
		return errors.New("unsupported radio operation")
	}
	return nil
}
func (b *Backend) run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if b.Run != nil {
		return b.Run(ctx, name, args...)
	}
	out, e := exec.CommandContext(ctx, name, args...).Output()
	if e != nil {
		return "", errors.New("device command failed")
	}
	if len(out) > 128<<10 {
		return "", errors.New("device response too large")
	}
	return ansi.ReplaceAllString(string(out), ""), nil
}
func (b *Backend) config() (settings, error) {
	var c settings
	c.Refresh = 30
	data, e := os.ReadFile(filepath.Join(b.Dir, "radios.json"))
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(data, &c)
	return c, e
}
func (b *Backend) save(c settings) error {
	if e := os.MkdirAll(b.Dir, 0700); e != nil {
		return e
	}
	data, _ := json.Marshal(c)
	f, e := os.CreateTemp(b.Dir, ".radios-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	e = f.Chmod(0600)
	if e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	return os.Rename(name, filepath.Join(b.Dir, "radios.json"))
}
func field(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		p := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(p) == 2 && p[0] == key {
			return strings.TrimSpace(p[1])
		}
	}
	return ""
}
func power(out string) *bool {
	v := field(out, "Powered")
	if v != "yes" && v != "no" {
		return nil
	}
	x := v == "yes"
	return &x
}
func (b *Backend) bluetooth(ctx context.Context) Bluetooth {
	out, e := b.run(ctx, "/usr/bin/bluetoothctl", "show")
	s := Bluetooth{Devices: []Device{}}
	if e != nil || !strings.Contains(out, "Controller ") {
		s.Reason = "蓝牙服务或控制器不可用"
		return s
	}
	s.Supported = true
	s.Powered = power(out)
	s.Alias = field(out, "Alias")
	rows, e := b.run(ctx, "/usr/bin/bluetoothctl", "devices")
	if e != nil {
		return s
	}
	flags := func(filter string) map[string]bool {
		set := map[string]bool{}
		v, e := b.run(ctx, "/usr/bin/bluetoothctl", "devices", filter)
		if e != nil {
			return set
		}
		for _, line := range strings.Split(v, "\n") {
			p := strings.Fields(line)
			if len(p) >= 2 && p[0] == "Device" && mac.MatchString(p[1]) {
				set[strings.ToUpper(p[1])] = true
			}
		}
		return set
	}
	paired, connected := flags("Paired"), flags("Connected")
	for _, line := range strings.Split(rows, "\n") {
		p := strings.SplitN(strings.TrimSpace(line), " ", 3)
		if len(p) != 3 || p[0] != "Device" || !mac.MatchString(p[1]) {
			continue
		}
		if len(s.Devices) >= 24 {
			break
		}
		s.Devices = append(s.Devices, Device{p[1], p[2], paired[strings.ToUpper(p[1])], connected[strings.ToUpper(p[1])]})
	}
	return s
}
func (b *Backend) cellular(ctx context.Context, c settings) Cellular {
	s := Cellular{Enabled: c.Cellular, APN: c.APN, Username: c.Username, PasswordSaved: c.Password != "", Policy: "usb_preferred_no_cellular_fallback"}
	out, e := b.run(ctx, "/usr/bin/nmcli", "-t", "--escape", "no", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device")
	if e == nil {
		s.Known = true
		for _, line := range strings.Split(out, "\n") {
			p := strings.SplitN(line, ":", 4)
			if len(p) != 4 {
				continue
			}
			if p[0] == "usb0" && p[2] == "connected" {
				s.USBConnected = true
			}
			if p[1] == "gsm" && p[2] == "connected" {
				if p[3] == profile {
					s.Connected = true
				} else {
					s.External = true
				}
			}
		}
	}
	if s.USBConnected {
		v, _ := b.run(ctx, "/usr/bin/nmcli", "-g", "IP4.ADDRESS", "device", "show", "usb0")
		s.USBAddress = strings.TrimSpace(v)
	}
	return s
}
func (b *Backend) status(ctx context.Context, c settings) Status {
	s := Status{Bluetooth: b.bluetooth(ctx), Cellular: b.cellular(ctx, c), ObservedAt: time.Now().UTC(), GPSControl: b.GPS != nil}
	if b.GPS != nil {
		s.GPS, _ = b.GPS(ctx, Request{Action: "status"})
	} else {
		v, e := b.run(ctx, "/usr/bin/mmcli", "-m", "any", "--location-status", "-J")
		if e == nil {
			s.GPS, _ = systemadmin.ParseGPS(v, "", time.Now())
		}
	}
	return s
}
func (b *Backend) Status(ctx context.Context) (Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, e := b.config()
	if e != nil {
		return Status{}, errors.New("wireless settings unavailable")
	}
	return b.status(ctx, c), nil
}
func (b *Backend) Execute(ctx context.Context, r Request) (Status, error) {
	if e := Validate(r); e != nil {
		return Status{}, e
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	c, e := b.config()
	if e != nil {
		return Status{}, errors.New("wireless settings unavailable")
	}
	var err error
	switch r.Action {
	case "status":
		return b.status(ctx, c), nil
	case "bluetooth_power":
		v := "off"
		if *r.Enabled {
			v = "on"
		}
		out, e := b.run(ctx, "/usr/bin/bluetoothctl", "--timeout", "5", "power", v)
		if e != nil || !strings.Contains(out, "succeeded") {
			return Status{}, errors.New("蓝牙开关操作失败，请刷新确认状态")
		}
		actual := b.bluetooth(ctx)
		if actual.Powered == nil || *actual.Powered != *r.Enabled {
			return Status{}, errors.New("蓝牙开关状态未确认，请刷新")
		}
		c.Bluetooth = *r.Enabled
	case "bluetooth_alias":
		out, e := b.run(ctx, "/usr/bin/bluetoothctl", "--timeout", "5", "system-alias", r.Alias)
		if e != nil || (!strings.Contains(out, "succeeded") && !strings.Contains(out, "Changing")) {
			return Status{}, errors.New("蓝牙名称修改失败")
		}
		c.Alias = r.Alias
	case "bluetooth_scan":
		s := b.bluetooth(ctx)
		if s.Powered == nil || !*s.Powered {
			return Status{}, errors.New("请先开启蓝牙")
		}
		_, err = b.run(ctx, "/usr/bin/bluetoothctl", "--timeout", "6", "scan", "on")
	case "bluetooth_pair", "bluetooth_connect", "bluetooth_disconnect", "bluetooth_remove":
		s := b.bluetooth(ctx)
		found := false
		paired := false
		for _, d := range s.Devices {
			if strings.EqualFold(d.Address, r.Address) {
				found = true
				paired = d.Paired
			}
		}
		if !found {
			return Status{}, errors.New("设备已不在当前列表，请重新扫描")
		}
		op := strings.TrimPrefix(r.Action, "bluetooth_")
		if op == "pair" && paired {
			return Status{}, errors.New("已配对，请直接连接")
		}
		if op == "connect" && !paired {
			return Status{}, errors.New("请先配对该设备")
		}
		args := []string{"--timeout", "8"}
		if op == "pair" {
			args = append(args, "--agent", "NoInputNoOutput")
		}
		args = append(args, op, r.Address)
		out, e := b.run(ctx, "/usr/bin/bluetoothctl", args...)
		if e != nil || strings.Contains(out, "Failed") || strings.Contains(out, "failed") {
			return Status{}, errors.New("蓝牙操作失败；本页配对支持免输入码设备，需 PIN/确认码的设备需后续适配")
		}
		actual, infoErr := b.run(ctx, "/usr/bin/bluetoothctl", "info", r.Address)
		confirmed := infoErr == nil && ((op == "pair" && field(actual, "Paired") == "yes") || (op == "connect" && field(actual, "Connected") == "yes") || (op == "disconnect" && field(actual, "Connected") == "no"))
		if op == "remove" {
			confirmed = strings.Contains(out, "has been removed")
		}
		if !confirmed {
			return Status{}, errors.New("蓝牙操作状态尚未确认，请刷新后再试")
		}
	case "gps_power", "gps_config":
		if b.GPS == nil {
			return Status{}, errors.New("GPS 控制尚未授权")
		}
		_, err = b.GPS(ctx, r)
		if err == nil {
			if r.Action == "gps_power" {
				c.GPS = *r.Enabled
			} else {
				c.Refresh = r.Refresh
			}
		}
	case "cellular_config":
		c.APN = r.APN
		c.Username = r.Username
		if r.Password != "" {
			c.Password = r.Password
		}
		if c.Cellular {
			return Status{}, errors.New("请先关闭移动数据再修改 APN")
		}
	case "cellular_power":
		if *r.Enabled {
			if c.APN == "" {
				return Status{}, errors.New("请先设置 APN")
			}
			link := b.cellular(ctx, c)
			if !link.Known {
				return Status{}, errors.New("无法确认 USB 和移动数据状态，暂不允许开启流量")
			}
			if link.USBConnected {
				return Status{}, errors.New("USB 已连接 NAS，移动数据保持关闭；请断开 USB 后按需开启")
			}
			if e := b.configureCellular(ctx, c); e != nil {
				return Status{}, e
			}
			if _, e := b.run(ctx, "/usr/bin/nmcli", "--wait", "8", "connection", "up", "id", profile); e != nil {
				return Status{}, errors.New("移动数据连接失败，请检查 APN 和运营商服务")
			}
			if !b.cellular(ctx, c).Connected {
				return Status{}, errors.New("移动数据尚未建立连接，请刷新确认")
			}
			c.Cellular = true
		} else {
			if b.cellular(ctx, c).Connected {
				if e := b.downCellular(ctx); e != nil {
					return Status{}, errors.New("关闭移动数据失败，请刷新确认实际状态")
				}
			}
			if b.cellular(ctx, c).Connected {
				return Status{}, errors.New("移动数据仍连接，请重试关闭")
			}
			c.Cellular = false
		}
	}
	if err != nil {
		return Status{}, errors.New("无线设备操作失败，请刷新确认实际状态")
	}
	if e = b.save(c); e != nil {
		return Status{}, errors.New("无线设置保存失败，请刷新状态")
	}
	return b.status(ctx, c), nil
}
func (b *Backend) configureCellular(ctx context.Context, c settings) error {
	if _, e := b.run(ctx, "/usr/bin/nmcli", "-g", "connection.id", "connection", "show", profile); e != nil {
		if _, e = b.run(ctx, "/usr/bin/nmcli", "connection", "add", "type", "gsm", "ifname", "*", "con-name", profile, "apn", c.APN, "connection.autoconnect", "no"); e != nil {
			return errors.New("无法创建移动数据配置")
		}
	}
	args := []string{"connection", "modify", profile, "gsm.apn", c.APN, "gsm.username", c.Username, "gsm.password", c.Password, "connection.autoconnect", "no", "ipv4.route-metric", "900", "ipv6.route-metric", "900"}
	if _, e := b.run(ctx, "/usr/bin/nmcli", args...); e != nil {
		return errors.New("移动数据配置失败")
	}
	return nil
}
func (b *Backend) downCellular(ctx context.Context) error {
	_, e := b.run(ctx, "/usr/bin/nmcli", "--wait", "3", "connection", "down", "id", profile)
	return e
}

// No auto-enable: cellular is off by default and suspended whenever USB appears.
func (b *Backend) Supervise(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, e := b.config()
	if e != nil {
		return
	}
	s := b.cellular(ctx, c)
	if s.Connected && (!c.Cellular || s.USBConnected) {
		if b.downCellular(ctx) != nil {
			return
		}
		c.Cellular = false
		_ = b.save(c)
	}
}
func (b *Backend) RunLoop(ctx context.Context) {
	b.Supervise(ctx)
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			b.Supervise(ctx)
		}
	}
}

// Restore only previously selected Bluetooth state. GPS/cellular never auto-start.
func (b *Backend) Restore(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, e := b.config()
	if e != nil {
		return
	}
	if b.cellular(ctx, c).Connected {
		if b.downCellular(ctx) != nil {
			return
		}
	}
	c.Cellular = false
	_ = b.save(c)
	v := "off"
	if c.Bluetooth {
		v = "on"
	}
	_, _ = b.run(ctx, "/usr/bin/bluetoothctl", "--timeout", "3", "power", v)
	if c.Alias != "" {
		_, _ = b.run(ctx, "/usr/bin/bluetoothctl", "--timeout", "3", "system-alias", c.Alias)
	}
	_, _ = b.run(ctx, "/usr/bin/bluetoothctl", "--timeout", "3", "discoverable", "off")
	_, _ = b.run(ctx, "/usr/bin/bluetoothctl", "--timeout", "3", "pairable", "off")
}
