# QDC507 语音通话反复下线故障档案

一句话摘要：语音通话反复下线（界面显示"不可用于语音通话"）的真正根因是 NAS 主机侧 Linux 内核 USB 驱动中 option 串口驱动抢占了 QDC507 的非串口接口，而非模组固件损坏；主机侧定时接口释放补丁已修复，无需刷固件。

日期：2026-09-10

---

## 1. 症状

- 界面文案：在 Web 控制台显示 `QDC507 · 不可用于语音通话` / `没有可用的语音线路`。
- 主机侧 `adb devices` 输出为空，找不到 `usb:2-1` 对应的 ADB 设备。
- NAS 主机执行 `aplay -l` / `cat /proc/asound/cards` 显示 `no soundcards`，USB 音频声卡未出现。
- 容器 `nas-comm-hardware` 的模组插件结构化 JSON 日志连续报：

```
{"level":"WARN","msg":"reconcile QDC507 module voice route","error":"line_...: wait for QDC507 ADB device: expected one ADB transport for usb:2-1, found 0"}
```

连续 3 次后触发 `QDC507 voice route keeps failing; forcing QADBKEY re-authorization and reboot`，但重授权并未使语音恢复。

---

## 2. 根因

根因不在模组固件，而在 **NAS 主机侧 Linux 内核 USB 驱动的 option 串口驱动抢占了 QDC507（USB VID:PID = 2CA3:4006，移远 EG25 内核 / 高通 MDM9207）的非串口接口**。

QDC507 的 USB 接口布局（10 个接口，2-1 设备）：

| 接口 | class:sub:proto | 正确驱动 | 功能 |
|---|---|---|---|
| 1.0-1.3 | ff:ff:ff / ff:00:00 | option | 真 AT 串口（保留，正确） |
| 1.4 | 02:06:00 | cdc_ether | CDC 以太网控制 |
| 1.5 | 0a:00:00 | cdc_ether | CDC 数据 |
| 1.6 | ff:42:01 | usbfs | ADB |
| 1.7 | 01:01:00 | snd-usb-audio | USB 音频控制 |
| 1.8 / 1.9 | 01:02:00 | snd-usb-audio | USB 音频流 |

故障时 `lsusb -t` 显示 2ca3:4006 的接口 0-9 **全部**被 `option` 驱动占用。具体被错误抢占的接口为：接口 1.4 / 1.5（应属 cdc_ether）、接口 1.6（应属 usbfs / ADB）、接口 1.7 / 1.8 / 1.9（应属 snd-usb-audio）。

连锁后果：

- 接口 1.6 被 option 抢占 → `adb devices` 为空；
- 接口 1.7/1.8/1.9 被 option 抢占 → NAS `no soundcards`；
- agent 的语音运行时等待 ADB 设备超时（`found 0`）→ 语音路由无法建立；
- 最终界面显示"不可用于语音通话"。

---

## 3. 关键证据链

- 手动解除错误占用后：**声卡立即出现**（接口 1.7-1.9 恢复为 snd-usb-audio），同时 **ADB 先变为 offline**（说明此前确实是驱动归属问题，而非硬件/固件损坏）。
- 随后一次 USB 重新枚举（Device36 → Device37），主机侧新增的定时修复服务再次释放错误绑定，**ADB 成为 device**，应用自行恢复语音路由与驻网。
- **全程未刷固件**。模组内部内核版本 3.18.44 与音频声卡在恢复后均可读取，且未替换镜像或重启业务容器。
- 结论：**此前在 r56/r57 阶段将"语音不上线"判断为"固件损坏需要重刷"是错误的**。重刷并不能解决驱动抢占型故障，本次未刷机即恢复，足以证伪"固件损坏"的旧结论。

---

## 4. 为什么反复发作

错误的 option 动态匹配（内核 option 驱动的 `new_id` / 动态匹配机制）会在 **USB 重连 / 重枚举后再次生效**，重新把 QDC507 的非串口接口（1.4-1.9）绑定到 option 驱动。

因此只做一次性的 USB reset 是治标不治本：reset 之后若发生重枚举，option 动态匹配会再次抢占接口，故障即复发。这也是历史上"USB reset → 短暂恢复 → 又下线"循环的根本原因。

---

## 5. 持久修复方案

主机侧已安装三个文件（非内核驱动永久匹配表修复，属于主机侧恢复补丁）：

1. `/usr/local/sbin/qdc507-interfaces.sh` — 按 VID/PID + 接口 class/subclass/protocol 共同筛选，仅解除 QDC507 非串口功能的 option 绑定；ADB 保留给 usbfs，AudioControl 绑 snd-usb-audio，CDC 控制绑 cdc_ether；已有正确绑定不动；flock 防并发；不执行 AT、不 reset。
2. `/etc/systemd/system/homesim-usb-interfaces.service` — Type=oneshot, TimeoutStartSec=15。
3. `/etc/systemd/system/homesim-usb-interfaces.timer` — OnBootSec=15, OnUnitInactiveSec=10（enabled + active）。

脚本筛选逻辑要点：

- 仅匹配 `idVendor=2ca3` 且 `idProduct=4006` 的设备；
- 白名单 `class:sub:proto` 为 `ff:42:01|01:*|02:06:00|0a:00:00`（即 ADB、USB 音频、CDC 以太网/数据）；
- 仅当该接口的当前 `driver` 为 `option` 时，才对其执行 `/sys/bus/usb/drivers/option/unbind`；
- 对未绑定驱动的目标接口（AudioControl 01:01、CDC 控制 02:06）补绑到 snd-usb-audio / cdc_ether。

timer 约每 10 秒兜底一次的原因：覆盖 USB reset 可能未触发完整 device add 事件、导致一次性处理漏掉接口的情况；OnBootSec=15 保证开机后稍作等待再首次运行。

---

## 6. 验证状态

实测（NAS 北京时间 2026-09-10 12:28 前后）：

- timer：`systemctl status homesim-usb-interfaces.timer` 显示 active + enabled；
- service：最近一次运行 exit 0；
- 接口绑定当前全部正确：`1.0-1.3 = option`，`1.4/1.5 = cdc_ether`，`1.6 = usbfs`，`1.7-1.9 = snd-usb-audio`；
- 容器内 `adb -L localfilesystem:/tmp/qdc507-adb/server.sock fork-server` 进程在运行；
- 模组插件日志出现 `QDC507 module voice route ready`，state = registered，ADB = device（usb:2-1），NAS 声卡 Baiwang 已出现；
- 注意：能力标记与运行时就绪不等于已实测双向通话；未拨打真实电话、未发短信，双向声音需用户试拨验收。

---

## 7. 排障与回退

查看状态 / 日志：

- `systemctl status homesim-usb-interfaces.timer`
- `journalctl -u homesim-usb-interfaces.service`
- `journalctl -t homesim-voice`（脚本用它写日志，记录释放了哪些接口，如释放接口 4-9）

回退方式（如需撤销守护）：

- `systemctl disable --now homesim-usb-interfaces.timer` — 先停用即可，不必删除文件；
- 停用不会主动把接口重新绑回错误的 option 驱动；
- 原 udev 规则 / 旧脚本未覆盖或删除，无需恢复备份；原业务版本完全保留。

---

## 8. 教训 / 给未来 AI 的明确指令

- 不要把声音 / ADB 故障一概判为固件损坏。本次明确证伪了"固件损坏"的旧结论，恢复全程未刷固件。
- 不要只反复 USB reset。option 动态匹配会在重连 / 重枚举后再次抢占接口，一次性 reset 治标不治本。
- 看到 `found 0`（即 `wait for QDC507 ADB device: expected one ADB transport for usb:2-1, found 0`）时，**先查 `lsusb -t` 看接口驱动归属**，确认接口 1.6 是否被 option 抢走，再决定是否走 QADBKEY 重授权路径。
- QADBKEY 重授权只解决授权类故障，解决不了驱动抢占；对驱动抢占型故障重授权无效，且会误导排查方向。
- 不要为验证恢复擅自拨打真实号码；由用户主动试拨以确认接通与双向声音。

---

## 9. 未完成事项

- agent 侧 voice reconciler 目前只走 QADBKEY 重授权 / 受控重启路径，**需要增强为能识别"接口被 option 抢走"这类故障**，并在确认后触发主机侧接口释放（调用 `/usr/local/sbin/qdc507-interfaces.sh` 或等效机制），而非无脑走 QADBKEY 路径。
- 本文档记录时该 agent 侧识别逻辑仍在处理中；当前恢复依赖主机侧 timer 兜底，而非 agent 主动识别。

---

## 数据来源

- 另一 AI 的修复交接记录：`C:\Users\wzs11\Documents\ChatGPT\大疆4G模块\voice-repair\修复交接.md`
- 同目录三个补丁源文件（与 NAS 主机上已安装文件同名）：
  - `C:\Users\wzs11\Documents\ChatGPT\大疆4G模块\voice-repair\qdc507-interfaces.sh`
  - `C:\Users\wzs11\Documents\ChatGPT\大疆4G模块\voice-repair\homesim-usb-interfaces.service`
  - `C:\Users\wzs11\Documents\ChatGPT\大疆4G模块\voice-repair\homesim-usb-interfaces.timer`
