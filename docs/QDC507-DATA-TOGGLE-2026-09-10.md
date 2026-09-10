# QDC507 流量开关慢 / 打不开 —— 根因定论（2026-09-10）

> 本文是 r67 的定论档案。此前 `QDC507-CALL-AUDIO-SILENT-2026-09-10.md` 里对流量开关的描述
> 只说"connect_data 稳定超时"，本文给出**为什么会超时**以及**数据其实早就通了**的完整证据链。

## 一、用户症状

- 打开流量（`connect_data`）非常慢，最终失败："流量打不开，服务暂时不可用（HTTP 502）"
- 关闭流量（`disconnect_data`）也非常慢
- 提示 `host agent is unavailable`

## 二、实测时间线（ModemManager 日志，戳为 unix 秒）

```
1789022219.77  registered -> connecting        # 用户点"开"
               …… 卡在 connecting 45 秒 ……
1789022264.61  connecting -> disconnecting     # 用户放弃，点"关"
               connection attempt #1 failed: Operation was cancelled
               …… 卡在 disconnecting 136 秒 ……
1789022400.38  disconnecting -> registered
```

手工复现：

```
$ mmcli -m 0 -b 2 --connect
error: couldn't connect the bearer: 'Timeout was reached'   # 约 25s
$ mmcli -m 0 | grep state
           |  state: connecting                              # 卡死，需 power-cycle 才恢复
```

试 `ip-type=ipv4`（换掉默认的 `ipv4v6`）**同样超时** → 排除 IPv6 PDP 问题。
`mmcli --create-bearer` 本身**瞬时成功** → 只有 `--connect` 会挂。

## 三、**决定性发现：数据其实早就是通的**

### 3.1 Bearer/4 已经连上了

```
$ mmcli -m 0 -b 4
  General    |  type: default-attach          ← 注意：attach 型，不是 default
  Status     |  connected: yes                 ← 已经连着！
  Properties |  apn: CTNET
```

`Bearer/4` 是 **`default-attach`** 类型（ModemManager 在 PDP attach 时自动建立的初始
bearer），**状态 `connected: yes`**。

而我们代码里的 `bearerTypeDefault = 1` 只匹配 **`default`（值 1）**；ModemManager 的
`MM_BEARER_TYPE_DEFAULT_ATTACH = 4`，**代码里从未处理过这个值**。

于是 `connect_data` 去创建**第二条独立的 default bearer**（Bearer/2、Bearer/3），
而运营商侧同一个 APN 的第二个 PDP context **永远激活不了** → 稳定超时。

**这不是模组故障，是操作选错了对象。**

### 3.2 ECM 网卡已经拿到地址并在跑流量

```
$ ip -br addr show enx8e88eec12eb4
enx8e88eec12eb4  UP  192.168.225.30/24  240e:471:ca0:c7:.../64

$ ip rule show
16:  from 192.168.225.30 lookup route_enx8e88eec12eb4 proto static   ← 策略路由已就位

$ ip route show table route_enx8e88eec12eb4
default via 192.168.225.1 dev enx8e88eec12eb4 proto dhcp src 192.168.225.30

$ cat /sys/class/net/enx8e88eec12eb4/statistics/{rx,tx}_bytes
10363
51482                                    ← 有真实流量
```

ECM（`cdc_ether`）网卡：**已 UP、已有 IPv4/IPv6 地址、已有策略路由、已在收发数据**。

### 3.3 但 ModemDeck 自己不认账

```
$ cat /run/modemdeck/network.json
{ "version": 1, "networks": [] }         ← ModemDeck 数据面 owned = 0

$ cat /run/modemdeck/bearers.json
{ "bearers": [ { "line_id": "line_I9mGO66zALFdGy2UIv-K3HoD",
                 "bearer_path": "/org/freedesktop/ModemManager1/Bearer/1",
                 "apn": "CTNET", "ip_family": 4 } ] }
                                         ← 记录的是 Bearer/1，早已不存在（陈旧）
```

**结论：数据面在"能用"的状态，但 ModemDeck 的数据面从未成功 `Configure` 过。**
界面于是显示"未连接"，用户点开关又必然超时。

## 四、慢的第二个原因：45 秒写超时 + 2 秒 API 超时

- `agent/internal/modemmanager/device_configuration.go:23`
  `deviceConfigurationWriteTimeout = 45 * time.Second`
- 整个写操作持 `p.configMu`（`:77-78`）→ **锁 45 秒**
- API 侧对 agent 的调用超时只有约 2 秒
  → 用户看到 **`host agent is unavailable`**（不是 agent 挂了，是被 45 秒锁挡住）

`disconnect_data` 慢 136 秒同理：`deactivateOwnedBearerData` 里
`bearerInterface.Disconnect` / `modemInterface.DeleteBearer` 对一条卡在
`connecting` 的 bearer 会各自阻塞到超时。

## 五、根因归纳

| # | 问题 | 位置 |
|---|---|---|
| R1 | `default-attach`（值 4）bearer 从未被识别，导致把"已连上"误判为"未连接"，进而去建第二条永远建不起来的 bearer | `device_configuration.go:38`（只有 `bearerTypeDefault = 1`）、`bearer_manager.go:575`、`device_configuration.go:927` |
| R2 | 无人从已 attach 的默认 bearer / ECM 网卡**接管**数据面，`network.json` 恒为 `[]` | `ecm_fallback.go` 只在 `IPv4.Method == "ppp"` 时才改写，attach 型不触发 |
| R3 | 写超时 45s 且持全局锁，撑爆 API 侧 2s 超时 → `host agent is unavailable` | `device_configuration.go:23,77` |
| R4 | 卡死的 `connecting`/`disconnecting` 状态无自愈，后续操作继续被拖死 | `deactivateOwnedBearerData` |

## 六、修复方向

1. **识别 attach 型 bearer**：新增 `bearerTypeDefaultAttach = 4`，在
   `dataConnectionIsDefault` / `matchingConnectedData` / `bearerConnectionReady` 中一并接受。
   `default-attach` 表示"调制解调器已附着、数据面可用"。
2. **`connect_data` 优先接管已附着的数据面**：若已存在 `connected: yes` 的 attach 型 bearer
   且其 APN/IP family 匹配，则跳过 `--connect`，直接走 `configureDataPlane`（ECM + DHCP），
   把 ECM 网卡纳入 ModemDeck 数据面（`network.json` 有记录）。
3. **收紧写超时**：`connect` 的 bearer 等待单独设短超时（≤ 15s），与整体写锁解耦；
   超时后立即进入清理，不再阻塞 `configMu`。
4. **卡死自愈**：写路径超时后，若 modem 停在 `connecting`/`disconnecting`，执行一次
   有界的 `set-power-state-low → on` 复位（实测可在 ~10s 内回到 `registered`），
   且必须幂等、有界、不阻断返回。

## 七、排障速查（给未来 AI）

1. 看到"流量打不开"**先别查模组固件**。先 `mmcli -m 0 -b <n>` 把**每一个** bearer 打完，
   看有没有 `type: default-attach` + `connected: yes`。有 → 数据面本来就是好的，问题在代码没接管。
2. `cat /run/modemdeck/network.json` 若是 `"networks": []`，说明 ModemDeck 数据面**从未成功的
   Configure** 过——这是"界面显示未连接"的直接原因。
3. 别用 `mmcli -m 0 --connect` 手工测试后**放着不管**：会把 modem 卡在 `connecting`，
   之后连 AT 命令都超时。**必须** power-cycle 复位。
4. 卡住时不要等它自己好——`connecting` 45s、`disconnecting` 136s，等不起。
5. `ip rule show` 里 `from 192.168.225.30 lookup route_enx...` 存在 = ECM 策略路由已就位，
   说明**至少有一次** DHCP 成功过（可能来自系统 DHCP 客户端，不是 ModemDeck）。
