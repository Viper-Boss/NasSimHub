# AirSIM iPhone App 兼容

[AirSIM](https://github.com/kai-wu-cortex/AirSIM) 是一个 iPhone App，原本配合一台装了 AirSIM 安卓网关的手机使用：iPhone 通过局域网（App 里叫 VoWLAN）控制那台手机打电话、收短信，通话声音走 iPhone。

NasSimHub 冒充这台安卓网关，**不修改 AirSIM App 的任何代码**：App 配对、签名、音频格式全部按它自己的协议来。配对后 iPhone 用 NasSimHub 的一条线路拨打和接听电话、收发短信。

## 使用

1. 启动时加上端口覆盖文件（三个端口在 NAS 上发布，宿主机端口必须与容器端口相同）：

   ```
   docker compose -f docker-compose.yml [-f deploy/fnos/compose.fnos.yml ...] -f docker-compose.airsim.yml up -d
   ```

2. 管理员登录 → 设置 → **AirSIM 手机**：打开开关，选择线路，保存。
3. 点“开始配对”，页面显示 6 位验证码（120 秒有效）。
4. iPhone 上 AirSIM → 配对 → 手动输入：地址填 NAS 的局域网 IP（或 Tailscale IP），端口填页面上显示的配对端口（默认 7594），再输入验证码。
5. 配对成功后 App 自动验证控制端口（7592）。

“已配对的 iPhone”里可以随时解除配对，解除后它的签名立即失效，它控制中的通话会被结束。

## 端口与环境变量

| 变量 | 默认 | 用途 |
| --- | --- | --- |
| `MODEMDECK_AIRSIM` | 开 | `off` 时完全不启动监听 |
| `MODEMDECK_AIRSIM_CONTROL_PORT` | 7592 | 签名控制请求（HTTP） |
| `MODEMDECK_AIRSIM_PCM_PORT` | 7593 | 通话音频（TCP，8 kHz PCM16） |
| `MODEMDECK_AIRSIM_PAIRING_PORT` | 7594 | 配对，只在管理员打开配对窗口时应答 |
| `MODEMDECK_AIRSIM_BIND_ADDRESS` | 0.0.0.0 | 覆盖文件里发布端口时绑定的宿主机地址 |

7592/7593 与 AirSIM 安卓网关一致。监听在 api 容器内；不加 `docker-compose.airsim.yml` 时外部访问不到，设置里没有打开时所有请求都被拒绝。

## 安全边界

- 只接受私有网络来源：127/8、10/8、172.16/12、192.168/16、链路本地、IPv6 ULA、100.64/10（Tailscale）。**不要把这三个端口映射到公网**：这是 App 的本地模式协议，没有 TLS。
- 配对：X25519 + HKDF-SHA256（盐 = 会话 ID，info = `AirSIMPair/v1:` + 验证码）+ AES-256-GCM。验证码只显示在管理员页面；窗口单次有效，120 秒过期；错误验证码不消耗窗口。
- 每台 iPhone 有自己的 32 字节 VoWLAN 密钥，用设置加密密钥（与 SIM PIN 相同的 secretbox）加密后存在 `数据目录/homesim/airsim.json`（0600）。每个请求 HMAC-SHA256 签名，时间戳 ±30 秒，nonce 防重放。
- 身份：iPhone 以**配对它的管理员账号**行事，但权限降为普通成员、只能访问网关选定的那一条线路。每个请求都重新读取账号：账号被停用、删除或失去管理员角色，下一个请求就被拒绝并清除它的配对；账号没有被分配所选线路时请求被拒绝，但配对保留。
- 所有命令走与浏览器相同的处理函数：线路权限、通话所有权（call lease）、防骚扰拨号限制都照常生效。
- 通话所有权：每台 iPhone 是独立的持有者，和浏览器会话互斥；网关在 iPhone 保持连接期间续租，iPhone 离线约 45 秒后停止续租，租约随之到期。
- 音频：通话的媒体所有者槽同一时间只能有一个——浏览器 WebRTC 或 iPhone PCM。

## 支持的功能

| AirSIM 功能 | 状态 |
| --- | --- |
| 手动配对（IP + 端口 + 验证码） | 支持 |
| Bonjour 自动发现 | 未实现（Docker 桥接网络里 mDNS 出不去，用手动配对） |
| 拨号（含 owned dial 回执与 operation_proof） | 支持 |
| 接听 / 拒接 / 挂断 / DTMF / 静音 | 支持 |
| 通话音频（PCM 双向，8k↔16k 重采样） | 支持 |
| 来电提醒 | App 在前台或后台保活时通过长轮询收到；**推送唤醒不支持**（需要 AirSIM 自己的云端中继和 APNs 证书） |
| 收短信 / 已读回执 / 验证码提取 | 支持（配对之后、最近 7 天的短信） |
| 发短信 | 支持 |
| 模组状态、信号 | 支持（信号由百分比换算为 dBm，仅供参考） |
| 调试页 `/api/debug`、录音、通话记录回执 | 返回 404，与 App 访问旧网关时一致 |

## 协议实现位置

- `internal/airsimproto`：线格式（规范请求串、签名、证明、配对加密、PCM 握手），测试向量来自 AirSIM 安卓网关源码（原样编译）和独立实现的 iOS 端加密。
- `internal/airsimgw`：三个监听、配对窗口、事件长轮询、PCM 桥。
- `internal/httpapi/airsim.go`：NasSimHub 一侧（Backend 实现、管理员设置接口）。
- `internal/callmedia/pcmlink.go`：原始 PCM 媒体所有者。

协议依据 AirSIM 仓库 commit `93db925`。App 升级若改了线格式，`internal/airsimproto` 的向量测试会先失败。
