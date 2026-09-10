# 通话音频连接失败（双向无声）根因定论 —— WebRTC ICE 走不通（2026-09-10 15:10 CST）

> 绥哥报告：电话能打通、能听到嘟嘟声，但**双方说话都没声音**，界面提示
> **「通话音频连接失败」**。

**结论：不是模组问题，不是 ALSA 问题，不是固件问题。是 WebRTC 媒体通道从架构上就不通。**

---

## 0. 一句话结论

`pion/webrtc` 跑在 **Docker bridge 网络**的 `api` 容器里（IP `172.18.0.2`），
且 **api 容器没有任何端口映射**（`PortBindings = {}`），pion 也**没有配置
`SettingEngine` / `NAT1To1IPs`**。于是：

- pion 报出的 ICE candidate 全是 **`172.18.0.x` 容器内网地址** → 浏览器的包
  无法路由到；
- 即便地址对了，**UDP 也没有任何端口转发**能进入该容器。

结果：**ICE 永远停在 `connecting`** → 前端 `MEDIA_RECOVERY_TIMEOUT_MS = 15s`
超时 → `failConnection()` → 界面报 **`通话音频连接失败`**（`runtime.callAudioConnectionFailed`）
→ 双向无音频。

信令（拨号、SDP 交换）走 HTTP 经 nginx 反代，**所以看起来"打通了"**——
但媒体是 **P2P UDP**，与信令完全不同的通路，它没通。

---

## 1. 证据链（NAS 现场实测）

### 1.1 api 容器网络（关键）

```
$ docker inspect nas-comm-api --format '{{json .HostConfig.PortBindings}}'
{}                                     ← ★ 零端口映射

$ docker inspect nas-comm-api   # 网络
networks=nas-comm-gateway_default  ip=172.18.0.2

$ docker exec nas-comm-api cat /proc/net/fib_trie   # 容器只看到自己
   |-- 172.18.0.0/16 ... /32 host LOCAL  → 172.18.0.2
```

宿主机地址是 `192.168.120.190/24`；api 容器的 `172.18.0.2` **对浏览器不可路由**。

### 1.2 pion 完全没有 NAT/端口配置

```
internal/callmedia/pion.go:32  func newWebRTCAPI()
  → engine := &webrtc.MediaEngine{}          # 只有编解码
  → webrtc.NewAPI(WithMediaEngine, WithInterceptorRegistry)
  ✗ 没有 webrtc.WithSettingEngine(...)
  ✗ 没有 SettingEngine.SetNAT1To1IPs(...)
  ✗ 没有 SetICEUDPMux / 固定 UDP 端口范围
```

`internal/callmedia/session.go:91-110` 只在**前端下发了 ICE 服务器**时才覆盖：
```go
if len(configuration.ICEServers) > 0 { peerConfiguration.ICEServers = ... }
if configuration.RelayOnly { peerConfiguration.ICETransportPolicy = Relay }
```
容器网络下没有 STUN/TURN，就只能靠 host candidate —— 而 host candidate 是
容器内网地址。

### 1.3 局域网直连时后端**故意返回空 ICE 列表**

`internal/httpapi/call_media.go:171-179`：
```go
relayRequired := api.callMediaRequiresRelay(request)
if !relayRequired {
    writeJSON(..., callMediaICEConfigurationResponse{
        ICEServers:         []rtcconfig.ICEServer{},   // ★ 空
        ICETransportPolicy: "all",
    })
    return
}
```

`callMediaRequiresRelay()`（`:220-236`）只在**手机端请求**、**Web 入口域名匹配**、
或带 **Cloudflare 头**（`CF-Ray`/`CF-Connecting-IP`）时返回 true。
从内网 `http://192.168.120.190:7577` 直接访问 → **false** → 空 ICE + policy `all`
→ 纯 host candidate → **必挂**。

> 反过来说：**如果从外网经 Cloudflare 访问，反而可能通**——那时会下发 TURN 中继。

### 1.4 agent 侧其实全都正常（排除法）

| 检查项 | 结果 |
|---|---|
| 通话信令 | `outgoing call start accepted` → `active`，`bearer=volte` ✅ |
| agent 媒体流 | `host call media stream opened` / `closed`，**零 ERROR** ✅ |
| WebRTC SDP 交换 | `browser call media exchange established` ✅ |
| USB 驱动归属 | `1.7-1.9 → snd-usb-audio`、`1.6 → usbfs`、`1.4-1.5 → cdc_ether` ✅ |
| 声卡 | `/proc/asound/cards` → `0 [Baiwang]` ✅ |
| ALSA 采集 | 格式 8000Hz/S16_LE/mono，**无错误**，时长字节数正确 ✅ |
| ALSA 播放 | `aplay` 写噪声 `rc=0`，**无错误** ✅ |
| 音量控件 | `amixer scontrols` → **无任何控件**（裸 UAC，软件不可调音量） |

**所以：主机侧音频设备健康，模组侧桥接进程活着。唯一断的是 ICE。**

### 1.5 "能听到嘟嘟声"是个陷阱

那声回铃音是**浏览器本地播放的 ogg 素材**
（`web/src/state/browserSounds.ts` 的 `ringtoneCatalog`，20 个 `.ogg`），
**不经过任何音频链路**。所以"能听到嘟嘟声"**不能**证明音频通路正常——
恰恰相反，它与"双向无声"同时出现，正说明**链路完全没通**。

---

## 2. 修复方案（两条必须都做）

### 修复 A：让 api 容器的媒体端口可达（compose）

两种路线，**推荐 A2**：

- **A1（简单粗暴）**：`api` 服务加 `network_mode: host`。
  - 优点：pion 直接拿到宿主机的 `192.168.120.190`，host candidate 立刻可用。
  - 缺点：`api` 原本靠 `extra_hosts: host.docker.internal:host-gateway` 访问
    NKN 节点 RPC（`HOMESIM_NKN_NODE_RPC: http://host.docker.internal:30003`），
    改成 host 网络后这个地址要改为 `127.0.0.1:30003`；且与 `web` 容器的
    nginx 反代方式（`api:8080`）冲突，需要一并改 nginx upstream 为 `127.0.0.1:8080`。

- **A2（推荐，改动小、不破坏现有网络拓扑）**：
  在 `api` 服务上固定 UDP 端口范围并映射到宿主机，再让 pion 只用这个范围：
  ```yaml
  api:
    ports:
      - "7881:7881/udp"        # 单端口复用（ICE UDPMux）
  ```
  配合修复 B 的 `SetICEUDPMux`，**所有 ICE 媒体流量复用一个 UDP 端口**，
  只需映射这一个端口，无需开大范围。

### 修复 B：让 pion 知道自己的公网/宿主地址（代码）

`internal/callmedia/pion.go` 的 `newWebRTCAPI()` 注入 `SettingEngine`：

```go
func newWebRTCAPI(hostIPs []string, udpPort int) (*webrtc.API, error) {
    engine := &webrtc.MediaEngine{}
    // ... 现有 codec/interceptor 注册 ...

    settings := webrtc.SettingEngine{}
    if len(hostIPs) > 0 {
        // 把容器内网 candidate 改写为宿主机地址
        ips := make([]net.IP, 0, len(hostIPs))
        for _, raw := range hostIPs {
            ip := net.ParseIP(raw)
            if ip == nil { return nil, fmt.Errorf(...) }
            ips = append(ips, ip)
        }
        settings.SetNAT1To1IPs(ips, webrtc.ICECandidateTypeHost)
    }
    if udpPort > 0 {
        mux := ice.NewMultiUDPMuxDefault(
            ice.UDPMuxParams{UDPConn: mustListenUDP(udpPort)},
        )
        settings.SetICEUDPMux(mux)
    }
    return webrtc.NewAPI(
        webrtc.WithMediaEngine(engine),
        webrtc.WithInterceptorRegistry(interceptors),
        webrtc.WithSettingEngine(settings),
    ), nil
}
```

配置来源（新增环境变量，默认关闭以保持向后兼容）：
- `MODEMDECK_CALL_MEDIA_PUBLIC_IPS`（如 `192.168.120.190`，逗号分隔）
- `MODEMDECK_CALL_MEDIA_UDP_PORT`（如 `7881`）

**注意**：`Core.Options` 需要新增字段把这两个值传进 `New()`。

### 修复 C（可选增强）：局域网直连也下发 STUN

若不想改端口映射，另一条路是**局域网也下发 STUN**（如 `stun:stun.l.google.com:19302`
或自建 coturn），让浏览器发现自己的 srflx 地址。但这**救不了容器内的 candidate
不可达问题**，仍须配合 A2/B。所以 A+B 是主线，C 只作为跨 NAT 场景增强。

---

## 3. 排障速查（下次"没声音"先跑这几条）

```bash
# 1. 容器有端口映射吗？
docker inspect nas-comm-api --format '{{json .HostConfig.PortBindings}}'
#    {} → 媒体必然不通

# 2. api 容器 IP 与宿主机是否同网段？
docker inspect nas-comm-api --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}'
ip -br addr show | grep -v docker

# 3. pion 有没有 SettingEngine？
grep -rn "WithSettingEngine\|SetNAT1To1IPs\|SetICEUDPMux" internal/callmedia/

# 4. 界面报的是哪个错？
#    runtime.callAudioConnectionFailed → ICE 15s 超时（本文）
#    runtime.microphoneHTTPSRequired   → 浏览器没给麦克风权限/非 HTTPS
#    runtime.audioOfferMissing        → createOffer 失败

# 5. 回铃音 ≠ 音频通！那是浏览器本地 ogg 素材。
```

---

## 4. 给未来 AI 的禁令

1. **看到"通话音频连接失败"先查 `HostConfig.PortBindings` 和容器 IP 网段**，
   不要一上来就怀疑模组、固件、ALSA。
2. **"能听到嘟嘟声"不是音频链路通的证据**——那是浏览器本地素材。
3. **信令通 ≠ 媒体通。** 信令走 HTTP（nginx 反代），媒体走 P2P UDP，
   两者是完全不同的通路，必须分别验证。
4. **容器里的 WebRTC 必须显式处理 candidate 地址与 UDP 端口**，
   pion 默认行为在 bridge 网络下 100% 失败。
5. 上一轮 `docs/QDC507-CALL-AUDIO-SILENT-2026-09-10.md` 里
   "模组侧 PCM 未搬运语音"的**推测已作废**——那个"采集全零"是**无通话时**测的，
   静音本就正常。真实原因是本文的 ICE 不通。

---

## 5. 未完成事项

- [ ] 实施修复 A（compose 端口/网络）+ 修复 B（pion SettingEngine）
- [ ] 部署后在局域网实测**双向通话**
- [ ] 顺带处理 `流量开关` 的 `connect_data` 25s 超时（见
      `docs/QDC507-CALL-AUDIO-SILENT-2026-09-10.md` 第 2 节，
      那是**另一个独立问题**）
