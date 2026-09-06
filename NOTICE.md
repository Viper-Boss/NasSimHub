# NasSimHub（NAS猫窝）声明

## 许可（双轨）

NasSimHub 是家庭 4G/5G SIM 卡网关。本仓库包含两类代码，许可分别如下：

1. **ModemDeck 衍生部分**（基线 commit `0437f29` 继承的网关与前端骨架代码）：
   继承上游 [ModemDeck](https://github.com/human-agent65535/ModemDeck) 的
   **PolyForm Noncommercial 1.0.0** 协议（见 [LICENSE](LICENSE)）——仅限非商业用途。
2. **NasSimHub 原创新增部分**（2026-09 之后新增的设备适配与家卡功能代码，主要包括
   `internal/devicef50/`、`internal/phonegeo/`、`internal/barkpush/`、
   `internal/httpapi/homesim_*.go` 中新增处理器、`web/src/components/LineStatusStrip.vue`、
   `web/src/components/settings/{BarkSettingsPanel,F50DevicePanel,NetworkSwitchesPanel}.vue`、
   `web/src/utils/phoneRegion.ts`、`web/src/state/phoneGeo.ts`、`web/src/i18n/homesimData.ts`
   中的新增键等）：以 **GNU Affero General Public License v3.0**
   （[LICENSE-AGPL.txt](LICENSE-AGPL.txt)）授权——以网络服务形式使用同样必须开源。

合并分发整个仓库时，两类许可各自约束对应部分。

## 使用的开源模块与致谢

| 模块/数据 | 用途 | 来源与协议 |
| --- | --- | --- |
| ModemDeck | 网关与前端基线（上游） | github.com/human-agent65535/ModemDeck — PolyForm Noncommercial 1.0.0 |
| MacCellular | DJI 模块语音/短信拓扑参考 | github.com/yuexiazhuojiu-byte/MacCellular — PolyForm Noncommercial 1.0.0 |
| f50-monitor | ZTE F50 goform 协议参考（仅协议行为，未复制代码） | github.com/koldllc/f50-monitor — MIT |
| phone.dat 归属地数据 | internal/phonegeo 离线号码归属地 | github.com/xluohome/phonedata — MIT |
| Vue 3 / Vue Router / Vue I18n | 前端框架 | MIT |
| Vite | 前端构建 | MIT |
| libphonenumber-js | 号码格式化 | MIT |
| Lucide 图标 | 图标库 | ISC |
| things-go/go-socks5 | 线路代理 | MIT |
| modernc.org/sqlite | 存储（纯 Go SQLite） | MIT |
| pion WebRTC 栈 | WebRTC 通话媒体 | MIT |

## 铃声与提示音（Android Open Source Project）

`web/src/assets/ringtones/` 中的铃声来自 Android Open Source Project。
其中 `classic.ogg`、`digital.ogg`、`chime.ogg` 和 `soft.ogg` 分别对应
`Ring_Classic_02.ogg`、`Ring_Digital_02.ogg`、`Ring_Synth_02.ogg` 和
`Ring_Synth_04.ogg`；其余文件来自 `ringtones/ogg/` 中的同名文件。这些文件
由 Android Open Source Project 按 Apache License 2.0 授权：

https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/data/sounds/

https://www.apache.org/licenses/LICENSE-2.0

`web/src/assets/notifications/` 中的短信提示音同样来自上述 Android Open
Source Project 目录下的 `notifications/` 与 `newwavelabs/`，并采用相同的
Apache License 2.0。

---

# Notices (English)

## NasSimHub licensing (dual-track)

NasSimHub is a home 4G/5G SIM gateway. This repository contains two classes
of code, licensed separately:

1. **ModemDeck-derived portions** (gateway and frontend skeleton code
   inherited from baseline commit `0437f29`): licensed under the upstream
   [ModemDeck](https://github.com/human-agent65535/ModemDeck)
   **PolyForm Noncommercial 1.0.0** license (see [LICENSE](LICENSE)) —
   noncommercial use only.
2. **NasSimHub-original additions** (device adapters and HomeSIM feature
   code added after 2026-09, including `internal/devicef50/`,
   `internal/phonegeo/`, `internal/barkpush/`, new handlers under
   `internal/httpapi/homesim_*.go`, `web/src/components/LineStatusStrip.vue`,
   `web/src/components/settings/{BarkSettingsPanel,F50DevicePanel,NetworkSwitchesPanel}.vue`,
   `web/src/utils/phoneRegion.ts`, `web/src/state/phoneGeo.ts`, and new keys in
   `web/src/i18n/homesimData.ts`): licensed under the
   **GNU Affero General Public License v3.0**
   ([LICENSE-AGPL.txt](LICENSE-AGPL.txt)) — network-service use must open
   source accordingly.

When distributing the repository as a whole, each license governs its own
portions.

## Open-source modules and acknowledgements

| Module / data | Purpose | Source & license |
| --- | --- | --- |
| ModemDeck | Gateway & frontend baseline (upstream) | github.com/human-agent65535/ModemDeck — PolyForm Noncommercial 1.0.0 |
| MacCellular | DJI module voice/SMS topology reference | github.com/yuexiazhuojiu-byte/MacCellular — PolyForm Noncommercial 1.0.0 |
| f50-monitor | ZTE F50 goform protocol reference (behaviour only, no code copied) | github.com/koldllc/f50-monitor — MIT |
| phone.dat region dataset | Offline number region lookup in internal/phonegeo | github.com/xluohome/phonedata — MIT |
| Vue 3 / Vue Router / Vue I18n | Frontend framework | MIT |
| Vite | Frontend build | MIT |
| libphonenumber-js | Phone number formatting | MIT |
| Lucide icons | Icon library | ISC |
| things-go/go-socks5 | Line proxies | MIT |
| modernc.org/sqlite | Storage (pure-Go SQLite) | MIT |
| pion WebRTC stack | WebRTC call media | MIT |

## Android Open Source Project ringtones

The ringtones under `web/src/assets/ringtones/` come from the Android Open
Source Project. `classic.ogg`, `digital.ogg`, `chime.ogg`, and `soft.ogg`
correspond respectively to `Ring_Classic_02.ogg`, `Ring_Digital_02.ogg`,
`Ring_Synth_02.ogg`, and `Ring_Synth_04.ogg`; the remaining files correspond
to the same-named files under `ringtones/ogg/`. These files are licensed by
the Android Open Source Project under the Apache License 2.0:

https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/data/sounds/

https://www.apache.org/licenses/LICENSE-2.0

The SMS alert sounds under `web/src/assets/notifications/` likewise come from
the `notifications/` and `newwavelabs/` directories in the Android Open Source
Project source tree above and are licensed under the same Apache License 2.0.

For a summary of other bundled components and their licenses, see
`THIRD_PARTY_NOTICES.md`.
