# 故障档案：打电话 502 + "line was not found" + 流量开关异常

- 日期：2026-09-10（r64）
- 报告者：绥哥
- 状态：**已定位，当前已自愈**；不需要硬件或数据修复
- ⚠️ **本文档纠正了同日早先的一个错误判断**：最初怀疑"agent line_id 哈希漂移 → API 静默丢线"，经实测**证伪**（见第 4 节）。留此记录以防后人重走弯路。

---

## 1. 用户可见症状

1. **打电话连不上** —— 前端弹出 nginx 原始 HTML：`502 Bad Gateway`
2. **流量（移动数据）开关打开有问题**
3. 提示：`重新读取服务端状态失败：read device configuration: line was not found`

三张证据：截图时间戳 `2026-09-10T05:31:36Z`（= 北京时间 **13:31:36**）。

## 2. 结论（先给答案）

**这不是硬件故障，也不是数据损坏，而是"部署重启窗口 + 前端陈旧错误态"叠加产生的假象。故障窗口已经过去，系统当前完全正常。**

| 症状 | 真实原因 |
|---|---|
| `502 Bad Gateway`（nginx HTML） | **api 容器在部署时重启**，nginx `upstream modemdeck_api` 短暂不可达。另外 `proxy_read_timeout 1h` 意味着一个挂住的通话请求在 upstream 断开时也会以 502 收场。 |
| `line was not found` | 线路在**部署重启期间瞬时 detach**（`cellular line detached` → `attached`），此时任何读配置请求都会 404。 |
| 流量开关"打开有问题" | 开关的写操作会**先**读一次设备配置拿 revision（`applyHardwareUpdate` 在写之前先 GET）；GET 撞上 detach 窗口即失败，**写请求根本没发出去**，界面因此停在错误态。 |
| 13:31 截图里仍有报错 | 13:21:54 部署完成后 API **零错误**（见第 3.4 节）。截图里的是**浏览器残留的陈旧错误态**，不是当时新发生的失败。 |

## 3. 证据链

### 3.1 当前状态：全绿

```
nas-comm-hardware | Up 25 minutes (healthy)
nas-comm-web      | Up 25 minutes (healthy)
nas-comm-api      | Up 25 minutes (healthy)
```

```
GET https://127.0.0.1:7577/api/v1/health
{"status":"ok","kind":"readiness","checks":{
  "application":{"status":"ok"},"database":{"status":"ok"},
  "host_agent":{"status":"ok"},"modem_manager":{"status":"ok"}}}
```

### 3.2 模组硬件健康

agent 的 line 一直以**稳定业务 ID** 出现，能力全开：

```json
{"id":"line_I9mGO66zALFdGy2UIv-K3HoD",
 "equipment_identifier":"866069050000003",
 "device_identifier":"c0958f1474fe87e6c28f8e3e663cc8b306713d93",
 "identity_source":"physical_device+equipment_identifier+device_identifier",
 "capabilities":{"dial":true,"media":true,"voice_interface":true,...},
 "voice_verification":{"usb_configuration":"enabled","media_routing":"enabled"}}
```

`QDC507 module voice route ready` 每 3 分钟一条，`runtime_version: qdc507-3.18.44-voice-20260712.5`，**近 30 分钟无任何 ERROR/WARN**。

### 3.3 模组只在今天被创建过一次

```
docker logs nas-comm-hardware | grep -c 'successfully created'   →  1
```

```
[base-manager] modem for device '.../usb2/2-1' successfully created     ← 05:22:33 UTC
[modem0] state changed (unknown -> disabled)
[modem0] power state updated: on
[modem0] 3GPP registration state changed (registering -> home)
[modem0] state changed (enabled -> registered)
```

即：**模组自 05:22:33 起一次都没掉过**。近一小时内核无 USB 事件（`journalctl -k | grep usb 2-1` 为空）。

### 3.4 故障时间线（关键）

api 容器启动时间 `2026-09-10 05:54:29 +0800`（= 2026-09-09T21:54:29Z），日志：

```
2026-09-09T21:54:31Z  ModemDeck HTTP origin started     ← 上一轮容器
2026-09-10T05:21:53Z  ERROR  ModemDeck stopped: shut down HTTP server: context deadline exceeded
2026-09-10T05:22:03Z  ModemDeck HTTP origin started     ← 本次部署后重启
2026-09-10T05:22:32Z  cellular line attached  line_id: line_5c4e5d8ad18a04a7611c37a60473bc89
```

**05:22 之后（覆盖用户 05:31 截图时刻）API 日志里一个 ERROR/WARN 都没有**：

```
docker logs --since 25m nas-comm-api | grep -E '"level":"(ERROR|WARN)'   →  空
```

而在此之前（部署前的反复重启期）确实有一串 detach/attached 循环：

```
03:24:39 hardware connection recovered  line_count:0
03:24:39 cellular line detached   line_5c4e5d8ad18a04a7611c37a60473bc89
03:25:29 cellular line attached   line_5c4e5d8ad18a04a7611c37a60473bc89
...（约每 12 分钟一轮，共 8 轮）
04:45:44 cellular line detached   ← 紧接着
04:45:43 read device configuration: line was not found
04:46:44 read device configuration: select line: selected line is not attached
```

每轮 detach 都紧跟一次 `line was not found` —— **这就是用户看到的报错，发生在部署重启窗口内**。

## 4. ⚠️ 被证伪的早期假设（务必阅读，防止重走弯路）

**早期假设**：agent 的 line_id 是 `sha256(physical_device + equipment_identifier + device_identifier)`（`agent/internal/modemmanager/ids.go:80-107`）。接口抢占修复后模组内部系统恢复、原本读不到的字段变成可读，怀疑哈希漂移 → 新端点 ID 查不到映射 → `bindProjectedLines`（`internal/communication/service.go:3150-3152`）静默丢线。

**证伪依据（两条，独立且确凿）**：

1. **单元测试反证**。`internal/store/hardware_endpoint_change_test.go` 的
   `TestHardwareSnapshotEndpointIDChangePreservesStableLine` 复现了"同一 SIM、agent 端点 ID 从 `line_OLD` 变为 `line_NEW`"场景，断言 `LineIDsByEndpoint[line_NEW]` 仍映射到同一个稳定线路。**实测 PASS**：
   ```
   --- PASS: TestHardwareSnapshotEndpointIDChangePreservesStableLine (0.03s)
   ok  github.com/human-agent65535/modemdeck/internal/store
   ```
   即 store 层**完全能正确处理**端点 ID 变化，不会丢线。

2. **生产日志反证**。api 侧**始终**使用稳定 ID `line_5c4e5d8ad18a04a7611c37a60473bc89`，从未把 agent 的哈希 ID 当作对外线路 ID（`cellular line attached` 的 `line_id` 一律是前者）。`bindProjectedLines` 的重绑一直正常工作。

**顺便记录两个与之无关但真实存在的观察**（不是本次故障原因）：
- `modemdeck_legacy_endpoint_lines` 在生产库中是空表；该表只在一次性 stable-line 迁移时写入（`internal/platform/database/stable_line_migration.go:611`），运行时不写。目前不影响功能（因为 SIM 身份解析足够），但属于脆弱的隐式依赖。
- `devices.endpoint_id` 存的是 agent 的哈希端点 ID（`line_I9mGO...`），而 `sim_cards.line_id` 存的是稳定 ID（`line_5c4e5d8...`）—— 两者语义不同、混用易误判，排障时务必区分（**这正是我最初误判的原因**）。

## 5. 排障速查（下次直接用）

```bash
# 1) 线路当前是 attached 还是 detached？—— 这一条基本就定性了
docker logs nas-comm-api 2>&1 | grep -E 'cellular line (attached|detached)' | tail -10

# 2) API 当前健康度
curl -sk https://127.0.0.1:7577/api/v1/health

# 3) 部署重启造成的 detach 窗口：比对容器启动时间与报错时间
docker ps --format '{{.Names}}|{{.CreatedAt}}'
docker logs nas-comm-api 2>&1 | grep -E 'ModemDeck (stopped|HTTP origin started)' | tail -6

# 4) 模组硬件是否健康（agent 容器里有 curl，api 容器里没有）
docker exec nas-comm-hardware curl -s --unix-socket /run/modemdeck/agent.sock \
  http://localhost/v1/snapshot | tr ',' '\n' | grep -E '"id"|capabilities|identity_source'
```

**判据**：
- 第 1 步若近期有 `detached` 且**没有**对应的 `attached` → 线路当前确实掉了。
- 若 `detached` 紧跟 `attached`（成对出现）且时间与容器重启吻合 → **部署窗口的正常抖动，不必处理**。
- 第 4 步 `capabilities.dial/media` 为 true 且 `identity_source` 三项齐全 → 硬件健康，问题不在模组。

## 6. 真正需要修的（健壮性，与本次根因无关但会放大影响）

后端数据面没问题，**前端把一次瞬时故障变成了持久错觉**，这才是应该修的：

1. **非 JSON 响应被原样当报错文案**：`web/src/api/client.ts:985-1000` 读 body 后直接赋给错误 message，于是 nginx 的 502 HTML 整页显示给用户。应在 body 不是预期错误信封（如以 `<` 开头 / 解析不出 message）时，替换为简洁的 HTTP 状态文案，并且绝不让原始 HTML 进入 UI。
2. **失效 line_id 没有恢复路径**：`web/src/state/deviceConfiguration.ts` 的 `selectedLineID` 只由 `selectDeviceConfiguration`（:251）写入，从不与最新线路列表校验；线路消失时既不清理也不改选，导致错误态一直留着、请求持续失败。
3. **写操作前的读会被瞬时故障拖垮**：`applyHardwareUpdate`（:372-404，尤其 :380 的 revision 读取）在写之前先 GET；GET 失败即整体失败，开关看着像"坏了"。应区分"读不到"与"写失败"，并对瞬时不可用做有限重试。
4. **`restoreDeviceConfiguration`（:309-333）** 在任何保存错误后都尝试刷新，刷新也失败时会拼出"保存失败；重新读取服务端状态失败"的双重报错，用户难理解。

## 7. 给未来 AI 的禁令

1. **看到 `line was not found` / `"selected line is not attached"`，先查 `cellular line attached/detached` 泳道**，判断是"线路真的掉了"还是"请求撞上了重启窗口"。别一上来就怀疑数据损坏或哈希漂移。
2. **区分两套 ID**：`sim_cards.line_id` = 稳定业务 ID（对用户可见、前端使用）；`devices.endpoint_id` = agent 的哈希端点 ID（内部）。**两者不相等是正常的**，不要据此推断丢线。
3. **不要在 detach/attached 成对出现且时间与容器重启吻合时"修数据"** —— 那是把正常抖动当成故障修。
4. **部署会短暂 502**。给用户交代预期，别把它当 bug 查。
5. 若今后真要动 `bindProjectedLines` 的 `continue` 分支，先跑 `internal/store/hardware_endpoint_change_test.go` —— 它能证明端点 ID 变化本身是安全的。

## 8. 未完成事项

- [ ] 需要绥哥**复测**：确认现在（重启后）拨号、流量开关均正常。若仍复现，说明还有第二个原因，需带具体时间点再查。
- [ ] 前端健壮性三项（第 6 节）待实施。
- [ ] 语音双向通话仍未由用户实测验收（见 `docs/QDC507-VOICE-INTERFACE-CLAIM-2026-09-10.md`）。
