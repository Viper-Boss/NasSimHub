# QDC507 通话无声音 + 流量开关挂起 —— 现场诊断（2026-09-10 14:40 CST）

> ⚠️ **第 1 节（通话无声音）结论已作废，请看
> [`QDC507-CALL-NO-AUDIO-ICE-2026-09-10.md`](./QDC507-CALL-NO-AUDIO-ICE-2026-09-10.md)。**
> 真实原因是 **WebRTC ICE 走不通**（api 容器零端口映射 + pion 无 NAT 配置），
> 不是模组侧 PCM 没搬运。本文第 1.3 节那个"采集全零"是**无通话时**测的，
> 静音本就正常，不能作为证据。**第 2 节（流量开关）仍然有效。**

> 绥哥报告：① 电话能拨通、能听到嘟嘟声，但双方说话都没声音，提示「通话音频连接失败」；
> ② 流量打不开，报「服务暂时不可用（HTTP 502）」+「重新读取服务端状态失败：read device
> configuration: host agent is unavailable」；③ 打开/关闭流量都特别慢。

所有结论均来自 NAS 现场实测，不含猜测。**未修改任何生产配置。**

---

## 0. 一句话结论

| 症状 | 真实原因 | 归属 |
|---|---|---|
| **通话无声音** | 主机侧 ALSA 正常、模组侧 bridge 自检通过，但 `hw:CARD=Baiwang,DEV=0` 采集**全零（纯静音）**——模组内部 PCM→UAC 通道没有真正搬运语音 | **待进一步定位（模组侧/UAC 路由）** |
| **流量打不开** | 数据面 ECM 网卡其实**已经通了**（有 IP）；但 `connect_data` 走 ModemManager 的 **3GPP bearer**，`--connect` 稳定 25s 超时（`Timeout was reached`），modem 卡在 `connecting` | **真 bug：modem 的 PDP 激活挂起** |
| **开/关流量特别慢** | 就是上面的超时叠加：`connecting` 卡 45s，`disconnecting` 卡 **136s** | 同上 |
| `host agent is unavailable` / 502 | **瞬时现象**：当时 agent 正被 `disconnecting` 长阻塞拖住，API 的 2s 超时打到空档。现测 healthz 四项全 ok | 已自愈 |

---

## 1. 通话：能通、有媒体、但音频是静音

### 1.1 通话链路本身完全正常（agent + api 双端日志互证）

```
06:36:04.28  agent  outgoing call start accepted       call_path=/ModemManager1/Call/0
06:36:04.40  api    call observed                      direction=outgoing state=dialing
06:36:07.11  api    call state changed                 dialing -> ringing-out     ← 此时听到嘟嘟声
06:36:09.10  api    call state changed                 ringing-out -> active  bearer=volte media_available=true
06:36:09.67  api    call state changed                 active -> active       media_active=true
06:36:09.71  api    browser call media exchange established      ← WebRTC SDP 交换成功
06:36:09.15  agent  host call media stream opened      call_id=call_k2OR16q3GQapV0vAbyeLTJgz
06:36:28.92  agent  host call media stream closed      ← 用户挂断（通话时长 24s）
```

**关键：`bearer=volte`、`media_available=true`、`media_active=true`，
且 `browser call media exchange established` —— WebRTC 通道和模组媒体流都开了，
全程零 ERROR。** 所以问题不在信令、不在 WebRTC 建链、不在 agent 媒体流开关。

### 1.2 主机侧 USB / ALSA 完全健康（接口抢占修复仍生效）

```
/proc/asound/cards:
 0 [Baiwang]: USB-Audio - Baiwang   BAIWANG Baiwang at usb-xhci-hcd.2.auto-1

/sys/bus/usb/devices/2-1:1.*/driver:
  1.0-1.3 -> option          （真串口，正确）
  1.4-1.5 -> cdc_ether       （ECM 数据网卡，正确）
  1.6     -> usbfs           （ADB，正确）
  1.7-1.9 -> snd-usb-audio   （UAC 音频，正确）
```

对比 `docs/QDC507-VOICE-INTERFACE-CLAIM-2026-09-10.md` 的故障态（1.4–1.9 全被 option 抢），
**这次驱动归属完全正确，接口抢占没有复发。**

### 1.3 决定性证据：采集到的是纯静音

```
$ arecord -D hw:CARD=Baiwang,DEV=0 --dump-hw-params ...
  FORMAT: S16_LE     CHANNELS: 1     RATE: 8000     （硬件只支持 8kHz 单声道）

$ arecord -D hw:CARD=Baiwang,DEV=0 -d 1 -f S16_LE -r 8000 -c 1 /tmp/cap.wav
  Recording WAVE: Signed 16 bit Little Endian, Rate 8000 Hz, Mono
  -rw-r--r-- 16044 bytes        ← 数据量正确（1s × 8000 × 2B + 44B 头）

$ 统计非零样本数：1 / 8000+
  → 全部为 0，数字静音
```

采集时接口状态也正确：`1.8 bAlternateSetting=1`（流式接口已被主机打开），
`1.7` 无 endpoint（控制接口，符合 UAC 规范）。

**结论：主机 → 模组的"取音"方向拿到的是一路静音。**

### 1.4 模组侧自检**声称**通过（但不覆盖音频数据流）

`routeReady()`（`agent/internal/qdc507voice/manager.go:577-593`）检查 6 项：
bridge 进程存活（PID+starttime 防 PID 复用）、cmdline 带 `--voice-route-session`、
日志有 `VoLTE route session active on hw:0,4`、`/sys/class/android_usb/f_audio/audio_enable = 1`、
`pcm4p` 和 `pcm4c` 都是 `state: RUNNING`。

**这 6 项全过（每 3 分钟一条 `voice route ready`），但它们只证明"桥接进程活着、ALSA 句柄开着"，
并不证明 PCM 里真有语音数据。** 采集全零与"自检全过"并不矛盾——这正是盲区所在。

### 1.5 待办（音频）

1. **捕捉通话中的模组侧 PCM 统计**：在通话进行时通过模组 ADB 读
   `/proc/asound/card0/pcm4p/sub0/status` 的 `hw_ptr`/`avail` 推进速率，
   判断模组 I2S 侧是否真有数据流动（本机 `adb` 直连受限，需经 agent 的
   `ModuleShell()` 通道，即 `/v1/system/metrics` 那条路）。
2. **确认 UAC 方向语义**：`1.7/1.8/1.9` 中哪一组承载 uplink、哪一组 downlink，
   与 `mavo-pcm-bridge` 的 hw:0,4 映射是否相符。
3. 先做 1，再决定是模组侧 bridge 需要重启（`reprobe_voice` / 一键修复第 5 步），
   还是 UAC 路由本身要修。

---

## 2. 流量开关：`connect_data` 稳定超时（真 bug）

### 2.1 数据面其实**已经通**（重要反直觉发现）

```
$ ip -br addr show | grep enx
enx8e88eec12eb4  UP  192.168.225.30/24  240e:471:ca0:c7:5805:d159:b905:9d68/64 ...
```

ECM 网卡 **UP、有 IPv4 + 公网 IPv6 地址** —— 模组的 USB ECM 数据通道是好的。

### 2.2 但 `connect_data` 走的不是 ECM，是 ModemManager 的 3GPP bearer

`agent/internal/modemmanager/device_configuration.go:172` → `DeviceConfigurationConnectData`
→ 走 ModemManager 的 `bearer --connect`。**实测稳定复现失败：**

```
$ mmcli -m 0 -b 2 --connect
error: couldn't connect the bearer: 'Timeout was reached'     ← 25 秒后
$ mmcli -m 0 | grep state
           |  state: connecting                                ← 卡在 connecting
```

换 `ip-type=ipv4` 单独试 → **同样 25s 超时**，排除 IPv6 PDP 的问题。

### 2.3 现场时间线（绥哥那次操作）

```
1789022219.77  registered -> connecting        ← 点"开流量"
1789022264.61  connecting -> disconnecting     ← 45s 后用户放弃、点"关"
               connection attempt #1 failed: Operation was cancelled
1789022400.38  disconnecting -> registered     ← 又过 136s 才恢复
```

**开：卡 45s；关：卡 136s。** 这就是"特别慢"的量化答案。

### 2.4 为什么会 `host agent is unavailable` + 502

ModemManager 在 `disconnecting` 里阻塞 136s 期间，agent 的请求处理被拖住；
API 对 agent 的请求超时只有 **2 秒**（`internal/agentclient/client.go:29`
`defaultRequestTimeout = 2 * time.Second`），必然打空 →

```
06:37:49  PATCH .../configuration  error: apply device configuration: host agent is unavailable
06:37:54  GET   .../configuration  error: read device configuration: host agent is unavailable
06:38:04  同上
```

502 则是 nginx（`web/nginx.conf` 的 `upstream modemdeck_api`）在这些长阻塞请求上
超时/upstream 断开时的表层表现。**这是瞬时现象，现已自愈**（healthz 四项全 ok）。

### 2.5 待办（流量）

1. **查 PDP 激活为何挂起**：`AT+CGACT?` / `AT+CGDCONT?` 在 modem 卡死时也超时（连不上 AT 口）。
   需要在下一次复现时**立即**抓 `AT+CEER`（获取最后错误原因）+ ModemManager `--debug` 日志。
   常见嫌疑：APN `CTNET` 在新卡/新套餐下需要改 APN、或电信侧 PDP 未放行。
2. **ECM vs bearer 双路径问题（建议长期修）**：既然 ECM 已经能用，
   而 `connect_data` 却去连一个会挂的 3GPP bearer —— agent 的
   `noopDataPlane`（`data_plane.go:53`）说明数据面还没真正接上。**这是架构性缺口：**
   要么 `connect_data` 直接管 ECM 网卡的 up/down，要么把 bearer 连接改成
   非阻塞 + 超时后回落到 ECM。
3. **超时策略重审**：agent 的 2s 超时 vs ModemManager 25s+ 的 connect/disconnect，
   量级差 10 倍以上。长操作应该异步化（返回 operation id + 轮询），
   而不是让 API 请求同步等 2s 就报 unavailable。

---

## 3. 排障速查（给未来的 AI）

1. **"能打通没声音" ≠ 信令问题**。先看 api 日志的
   `call state changed ... bearer=volte media_active=true` 和
   `browser call media exchange established`；这两条都有就是**媒体数据面**问题。
2. **先跑这句话确认是不是纯静音**（只在**通话进行中**跑才有意义）：
   ```
   docker exec nas-comm-hardware arecord -D hw:CARD=Baiwang,DEV=0 -d 2 \
     -f S16_LE -r 8000 -c 1 /tmp/cap.wav
   # 用 od 数非零样本；全零 = 主机拿到的是数字静音
   ```
   **⚠️ 硬件是 8kHz 单声道，别加 `-c 2`（会报 `Channels count non available`）。**
3. **`voice route ready` 不能当作"音频通"的证据**。它只验证桥接进程与 ALSA 句柄存活
   （`manager.go:577`），不验证 PCM 里有数据。
4. **流量慢/打不开先看 `connecting`/`disconnecting` 各卡了多久**：
   ```
   docker logs nas-comm-hardware | grep -E 'state changed.*(connecting|disconnecting|registered)'
   ```
5. **手动测 bearer 连接（可安全复现，记得收尾）**：
   ```
   mmcli -m 0 --create-bearer="apn=CTNET,ip-type=ipv4v6"     # 秒级成功
   mmcli -m 0 -b <N> --connect                                # 25s 后 Timeout was reached
   mmcli -m 0 -b <N> --disconnect                             # 可能再卡
   ```
   **收尾**：卡在 `connecting` 时 AT 口会一起超时；等它自己回到 `registered`
   （实测约 2 分钟）即可，**不要**反复 --disconnect 叠加阻塞。
6. **`host agent is unavailable` 先怀疑 agent 被长操作阻塞**（ModemManager 卡住），
   而不是网络/DNS。两个容器不在同一 Docker 网络（hardware 用 `network_mode: host`，
   api 在 `nas-comm-gateway_default`），**它们靠共享 Unix socket
   `/run/modemdeck/agent.sock` 通信，不是靠主机名**。所以在 api 容器里
   `wget http://modemdeck-agent/...` 报 `bad address` 是**正常的**，不是故障。

---

## 4. 本次现场状态记录

- 三容器 healthy；healthz（application/database/host_agent/modem_manager）四项全 ok
- 模组：`state=registered`、`access tech=lte`、`signal 80%`、`packet service=attached`、
  `registration=home`、PLMN `46011`（中国电信）、Band B3、RSRP -98
- 接口驱动归属正确（见 1.2），接口抢占未复发
- ECM 网卡 `enx8e88eec12eb4` UP，`192.168.225.30/24` + 公网 IPv6
- 模组固件 `QDC507GLEFM21_01.001.02.004_BETA0611`
- 语音运行时 `qdc507-3.18.44-voice-20260712.5`
- 未改动任何生产配置（bearer 2/3 是我为复现而建，modem 已自行回到 registered）
