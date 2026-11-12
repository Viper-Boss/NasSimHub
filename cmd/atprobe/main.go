// Command atprobe 是 QDC507 / EG25-G 模块的 AT 自检工具（无副作用，只发查询命令）。
//
// 用法：
//
//	atprobe                     # 自动扫描所有串口，找到能响应 AT 的口并跑自检
//	atprobe -port /dev/ttyUSB6  # 指定串口（Windows：COM3）
//	atprobe -list               # 只列出串口
//	atprobe -quectel-id         # 额外把 USB ID 从 2ca3:4006 改成 2c7c:0125（需 -port，改完自动重启模块）
//	atprobe -reset              # 自检后重启模块（AT+CFUN=1,1）
//
// 危险操作（-quectel-id / -reset / -dial / -sms）都默认关闭，必须显式打开。
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.bug.st/serial"
)

// 常见 AT 查询命令（只读，安全）。
var probeCommands = []struct {
	label    string
	command  string
	optional bool
}{
	{"连通性", "AT", false},
	{"关回显", "ATE0", false},
	{"固件信息", "ATI", false},
	{"制造商", "AT+CGMI", true},
	{"型号", "AT+CGMM", true},
	{"固件版本", "AT+CGMR", true},
	{"IMEI", "AT+GSN", false},
	{"SIM 状态", `AT+CPIN?`, false},
	{"ICCID", "AT+QCCID", true},
	{"IMSI", "AT+CIMI", true},
	{"信号强度", "AT+CSQ", false},
	{"CS 注册", "AT+CREG?", false},
	{"PS 注册", "AT+CEREG?", false},
	{"运营商", "AT+COPS?", false},
	{"网络详情", "AT+QNWINFO", true},
	{"服务小区", "AT+QENG=\"servingcell\"", true},
	{"USB 模式", `AT+QCFG="usbnet"`, true},
	{"USB ID", `AT+QCFG="usbid"`, true},
	{"VoLTE 开关", `AT+QCFG="ims"`, true},
	{"短信格式", "AT+CMGF=1", false},
	{"短信存储", "AT+CPMS?", true},
	{"短信条数", `AT+CPBS?`, true},
	{"蜂窝功能", "AT+CFUN?", true},
}

// voiceCommands 是通话与音频通路的自检项。
//
// 这批命令不是猜出来的，是从模块官方固件 QDC507GLEFM21_01.001.02.004 的
// system 分区里逆出来的：atfwd_daemon 的字符串表里有完整的 +QCFG 子键清单，
// quectel_daemon 里有音频通路的建立顺序，/etc/snd_soc_msm/snd_soc_msm_9x07_Tomtom_I2S
// 是那条通路真正读的 UCM 配置。每条为什么值得问，见下面的注释。
//
// 全部是查询，没有副作用。
var voiceCommands = []struct {
	label    string
	command  string
	optional bool
}{
	// 自动应答寄存器。固件的 Linux 用户态里**没有**任何自动接听逻辑
	// （整个 rootfs 的二进制都扫过了），所以「模块自己接电话」只可能来自
	// modem 侧 AT 栈的 S0 寄存器。非 0 就是它。
	{"自动应答 S0", "ATS0?", false},
	{"ATH 是否挂断语音", "AT+CVHU?", true},
	{"来电号码显示", "AT+CLIP?", true},
	{"来电扩展结果码", "AT+CRC?", true},

	// USB 组合。最后一个标志位是 UAC（USB 声卡）。内核里 UAC2 gadget
	// （snd_uac2 / f_audio_source）是编进去的，libql_usb.so 也有 audio 这个
	// function，所以这一位是真能开的——开了通话音频就能走 USB 声卡，
	// 不用再靠 ADB 进模块里掰 mixer。
	{"USB 组合", `AT+QCFG="usbcfg"`, false},

	// 次级 AUX PCM 的时钟开关。atfwd_daemon 用它写
	// /sys/devices/soc:qcom,msm-sec-auxpcm/enable_clk。时钟没开的话，
	// mixer 开关全对也不会有声音，而且这个状态在别处完全看不出来。
	{"AUX PCM 时钟", `AT+QCFG="pcmclk"`, true},

	// 数字音频接口与 PCM 语音通道。"=?" 问的是取值范围而不是当前值：
	// 这是唯一能区分「这个固件不支持」和「支持但没开」的办法。
	{"数字音频接口", "AT+QDAI?", true},
	{"数字音频接口范围", "AT+QDAI=?", true},
	{"PCM 语音通道", "AT+QPCMV?", true},
	{"PCM 语音通道范围", "AT+QPCMV=?", true},
	{"音频模式", "AT+QAUDMOD?", true},
	{"音频回环", "AT+QAUDLOOP?", true},
	{"侧音", "AT+QSIDET?", true},
	{"送话增益", "AT+QMIC?", true},
	{"收话增益", "AT+QRXGAIN?", true},

	// 来电提示音与忙音：厂商 daemon 会在这两个状态下抢音频通路。
	{"来电提示音", `AT+QCFG="tone/incoming"`, true},

	// VoLTE。CS 语音和 VoLTE 走的是两条完全不同的 mixer 通路，
	// 厂商 daemon 只建 CS 那条（见 HANDOFF 里的分析），所以这里必须问清楚
	// 当前通话到底会走哪条。
	{"VoLTE 开关", `AT+QCFG="ims"`, true},

	// 这个固件支持的全部厂商配置项，用来核对上面每一条是不是真的存在。
	{"全部 QCFG 配置项", "AT+QCFG=?", true},
}

func main() {
	port := flag.String("port", "", "串口（默认自动扫描）；Windows 例：COM3，Linux 例：/dev/ttyUSB6")
	baud := flag.Int("baud", 115200, "波特率")
	list := flag.Bool("list", false, "只列出系统串口")
	timeout := flag.Duration("timeout", 2500*time.Millisecond, "每条 AT 命令的等待时间")
	cmds := flag.String("cmd", "", "只执行指定的 AT 命令，多条用 | 分隔；例：'AT+CPINC?|AT+CSQ'")
	sms := flag.String("sms", "", "发送短信，格式 '号码|内容'（文本模式、GSM7 英文；中文用 UCS2 时改 -cmd）")
	enableIMS := flag.Bool("ims", false, "尝试开 VoLTE：AT+QCFG=\"ims\",1（需要 AT+CFUN=1,1 重启模块）")
	flagQuectelID := flag.Bool("quectel-id", false, "危险：把 USB ID 改成 2c7c:0125 并重启模块（让内核/chan_quectel 自然识别）")
	flagReset := flag.Bool("reset", false, "危险：自检后重启模块（AT+CFUN=1,1）")
	flag.Parse()

	ports, err := serial.GetPortsList()
	if err != nil {
		fmt.Fprintln(os.Stderr, "枚举串口失败:", err)
		os.Exit(1)
	}
	if *list {
		if len(ports) == 0 {
			fmt.Println("系统里没有任何串口设备（模块驱动装了吗？）")
			return
		}
		for _, p := range ports {
			fmt.Println(p)
		}
		return
	}

	candidates := ports
	if *port != "" {
		candidates = []string{*port}
	}
	if len(candidates) == 0 {
		fmt.Println("没有找到串口。Windows 上需要先装模块的 USB 串口驱动；Linux(NAS) 上把模块插上后应出现 /dev/ttyUSB*。")
		return
	}

	fmt.Printf("扫描 %d 个串口（波特率 %d）...\n", len(candidates), *baud)
	for _, candidate := range candidates {
		port, err := openAT(candidate, *baud, *timeout)
		if err != nil {
			fmt.Printf("  %s: %s\n", candidate, shortErr(err))
			continue
		}
		fmt.Printf("\n=== 在 %s 上找到 AT 口 ===\n", candidate)
		if *sms != "" {
			parts := strings.SplitN(*sms, "|", 2)
			if len(parts) != 2 {
				fmt.Fprintln(os.Stderr, "-sms 格式错误，应为 '号码|内容'")
				_ = port.Close()
				os.Exit(1)
			}
			resp, err := sendSMS(port, parts[0], parts[1], *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "发短信失败: %s\n", err)
			} else {
				fmt.Println("短信结果:", flatten(resp))
			}
		}
		if *enableIMS {
			resp, err := exchange(port, `AT+QCFG="ims",1`, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "开 ims 失败: %s\n", err)
			} else {
				fmt.Printf("ims: %s（需要 AT+CFUN=1,1 重启模块生效）\n", flatten(resp))
			}
		}
		if *cmds != "" {
			for _, one := range strings.Split(*cmds, "|") {
				one = strings.TrimSpace(one)
				if one == "" {
					continue
				}
				response, err := exchange(port, one, *timeout)
				if err != nil {
					fmt.Printf("%-24s -> 错误: %s\n", one, shortErr(err))
					continue
				}
				fmt.Printf("%-24s -> %s\n", one, flatten(response))
			}
			_ = port.Close()
			return
		}
		report(port, *timeout)

		if *flagQuectelID && applyQuectelID(port, *timeout) {
			fmt.Println("已下发 AT+QCFG=\"usbid\",11388,293（2c7c:0125），模块重启中；Linux 上还需 unbind/bind USB 才会重新枚举。")
		}
		if *flagReset {
			_ = send(port, "AT+CFUN=1,1", *timeout)
			fmt.Println("已下发重启命令 AT+CFUN=1,1。")
		}
		_ = port.Close()
		return
	}
	fmt.Println("\n没有任何串口响应 AT。请确认：1) 驱动已装；2) 波特率正确；3) 模块已开机（不是只供电）。")
}

func openAT(name string, baud int, timeout time.Duration) (serial.Port, error) {
	p, err := serial.Open(name, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, err
	}
	if err := p.SetReadTimeout(timeout); err != nil {
		_ = p.Close()
		return nil, err
	}
	response, err := exchange(p, "AT", timeout)
	if err != nil {
		_ = p.Close()
		return nil, err
	}
	if !strings.Contains(strings.ToUpper(response), "OK") {
		_ = p.Close()
		return nil, fmt.Errorf("响应不含 OK: %q", response)
	}
	return p, nil
}

func report(p serial.Port, timeout time.Duration) {
	runProbeSet(p, timeout, probeCommands)
	fmt.Println("\n--- 通话与音频通路 ---")
	runProbeSet(p, timeout, voiceCommands)
}

func runProbeSet(p serial.Port, timeout time.Duration, set []struct {
	label    string
	command  string
	optional bool
}) {
	for _, item := range set {
		response, err := exchange(p, item.command, timeout)
		if err != nil {
			fmt.Printf("%-14s %-26s -> 错误: %s\n", item.label, item.command, shortErr(err))
			continue
		}
		flat := flatten(response)
		if flat == "" {
			flat = "(无返回)"
		}
		fmt.Printf("%-14s %-26s -> %s\n", item.label, item.command, flat)
	}
}

// applyQuectelID 把模块的 USB ID 从大疆定制的 2ca3:4006 改成通用 Quectel
// EC25 的 2c7c:0125（十进制 11388,293），让 Linux 内核与 chan_quectel 能自然识别。
func applyQuectelID(p serial.Port, timeout time.Duration) bool {
	if _, err := exchange(p, `AT+QCFG="usbnet",1`, timeout); err == nil {
		fmt.Println(`已设置 AT+QCFG="usbnet",1`)
	}
	_, err := exchange(p, `AT+QCFG="usbid",11388,293`, timeout)
	_, _ = exchange(p, "AT+CFUN=1,1", timeout)
	return err == nil
}

func exchange(p serial.Port, command string, timeout time.Duration) (string, error) {
	return sendAndExpect(p, command, "", timeout)
}

func send(p serial.Port, command string, timeout time.Duration) error {
	_, err := exchange(p, command, timeout)
	return err
}

func flatten(s string) string {
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.EqualFold(line, "OK") || line == "AT" {
			continue
		}
		if strings.HasSuffix(line, "\u0000") {
			line = strings.TrimSuffix(line, "\u0000")
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, " | ")
}

func shortErr(err error) string {
	msg := err.Error()
	if idx := strings.Index(msg, ":"); idx > 0 && idx < 40 {
		return strings.TrimSpace(msg[idx+1:])
	}
	return msg
}

// sendAndExpect writes a command then reads until the response contains the
// expected substring, OK/ERROR, or the timeout. Pass an empty expect to wait
// only for OK/ERROR.
func sendAndExpect(p serial.Port, command, expect string, timeout time.Duration) (string, error) {
	if command != "" {
		if _, err := p.Write([]byte(command + "\r\n")); err != nil {
			return "", err
		}
	}
	deadline := time.Now().Add(timeout)
	var buf strings.Builder
	for time.Now().Before(deadline) {
		chunk := make([]byte, 512)
		n, err := p.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
			text := buf.String()
			if expect != "" && strings.Contains(text, expect) {
				return text, nil
			}
			if complete, failed := terminalATResponse(text); complete {
				if failed {
					return text, fmt.Errorf("AT command rejected")
				}
				return text, nil
			}
		}
		if err != nil {
			if buf.Len() > 0 {
				return buf.String(), nil
			}
			return "", err
		}
	}
	if buf.Len() == 0 {
		return "", fmt.Errorf("超时无响应")
	}
	return buf.String(), nil
}

// sendSMS sends one PDU-free text-mode message and waits for the network
// confirmation. The body is taken as-is (GSM7 / ASCII only). For Chinese use
// AT+CSCS="UCS2" first and encode the body to UCS2 big-endian hex.
func sendSMS(p serial.Port, number, body string, timeout time.Duration) (string, error) {
	if _, err := sendAndExpect(p, "AT+CMGF=1", "", 3*time.Second); err != nil {
		return "", fmt.Errorf("CMGF: %w", err)
	}
	prompt, err := sendAndExpect(p, fmt.Sprintf("AT+CMGS=\"%s\"", number), ">", 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("CMGS 提示: %w (收到: %q)", err, prompt)
	}
	return sendAndExpect(p, body+"\x1a", "", timeout)
}
