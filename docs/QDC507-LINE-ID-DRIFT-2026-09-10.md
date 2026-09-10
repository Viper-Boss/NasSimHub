# QDC507 line_id 哈希漂移导致 API 静默丢线（"打电话 502 / line was not found"）

- 日期：2026-09-10（r64）
- 症状报告者：绥哥
- 状态：**根因已定论**，修复方案已确定（见第 6 节），待实施

---

## 1. 用户可见症状（三条，实为同一根因）

1. **打电话连不上** —— 前端收到 nginx 返回的原始 HTML：`502 Bad Gateway`。
2. **流量（移动数据）开关打开有问题** —— 打开后立即失败或界面卡住。
3. **弹窗报错**：`重新读取服务端状态失败：read device configuration: line was not found`
   （i18n key `runtime.refreshAfterSaveFailed`，由 `web/src/state/deviceConfiguration.ts` 渲染）

## 2. 一句话根因

**硬件 agent 计算的 line_id 是一个哈希值，其输入包含只有在模组内部系统在线时才可读的字段。当这些字段从"不可读"变为"可读"时，哈希发生漂移，agent 换用了新的 line_id；而 API 侧的"agent 端点 ID → 稳定业务线路 ID"映射表里没有这个新端点的记录，于是 API 在投影阶段把这条线路静默丢弃，线路列表变成 0 条。**

线路列表为 0 条 → 任何需要线路的操作（拨号、读设备配置）都失败。**模组硬件本身是完全健康的**。

## 3. 证据链（NAS 实测，2026-09-10）

### 3.1 agent 侧：硬件健康，但 line_id 是新的

通过 agent 的 Unix socket 直接读它的真实快照：

```bash
docker exec nas-comm-hardware curl -s --max-time 15 \
  --unix-socket /run/modemdeck/agent.sock http://localhost/v1/snapshot
```

关键字段：

| 字段 | 值 |
|---|---|
| `id` | `line_I9mGO66zALFdGy2UIv-K3HoD` |
| `equipment_identifier` | `866069050000003` |
| `device_identifier` | `c0958f1474fe87e6c28f8e3e663cc8b306713d93` |
| `physical_device` | `/sys/devices/platform/fd000000.usb/xhci-hcd.2.auto/usb2/2-1` |
| `identity_source` | `physical_device+equipment_identifier+device_identifier` |
| `capabilities` | `dial:true`, `media:true`, `voice_interface:true`（全绿） |
| `voice_verification` | `usb_configuration:"enabled"`, `media_routing:"enabled"` |
| `sim_identifier` | `89861100000000000001` |
| `imsi` | `460110000000005` |

连采 3 次（间隔 5 秒）`identity_source` 与三要素完全一致 —— **identity 目前稳定**，即这是一次性的跃迁，不是持续抖动。

agent 日志中只出现过这一个 line_id：

```
line_id":"line_I9mGO66zALFdGy2UIv-K3HoD      ← 7 次
```

### 3.2 API 侧：线路数为 0

`nas-comm-api` 全量日志统计 `line_count`：

```
34 次  line_count":0
1  次  line_count":1      ← 仅历史早期
```

即 API 侧确实认为"没有线路"。日志里能看到失败序列：

```json
{"msg":"device configuration applied","line_id":"line_5c4e5d8ad18a04a7611c37a60473bc89","operation":"connect_data"}
{"level":"WARN","msg":"read device configuration","path":"/api/v1/devices/line_5c4e5d8ad18a04a7611c37a60473bc89/configuration",
 "error_class":"not_found","error":"read device configuration: line was not found"}
```

注意：**API 用的是旧 line_id `line_5c4e5d8...`，agent 用的是新 line_id `line_I9mGO...`** —— 两者不一致。

### 3.3 数据库侧：SIM 身份映射正确，端点映射表是空的

> 宿主没有 sqlite3，用一次性容器读：
> ```bash
> docker run --rm -v /mnt/docker-data/homesim/data:/d -v /tmp/q.sql:/q.sql alpine:3.20 \
>   sh -c "apk add -q sqlite >/dev/null 2>&1; sqlite3 /d/modemdeck.db < /q.sql"
> ```
> 表名是 `modemdeck_lines`，**不是** `lines`。

`modemdeck_lines`（稳定业务线路表）：

| line_id | phone_number | created_at |
|---|---|---|
| `line_5c4e5d8ad18a04a7611c37a60473bc89` | (空) | 2026-09-07T14:47:34Z |
| `line_41cb957f9b539b57de4ba49ddf149651` | +8616621285413 | 2026-09-09T18:42:27Z |

`sim_cards`：

| iccid | line_id | imsi | current_imei |
|---|---|---|---|
| `89861100000000000001` | **`line_5c4e5d8ad18a04a7611c37a60473bc89`** | `460110000000005` | `866069050000003` |
| `89860100000000000002` | `line_41cb957f9b539b57de4ba49ddf149651` | `460090000000004` | (空) |

`devices`：

| imei | endpoint_id | iccid | last_seen |
|---|---|---|---|
| `866069050000003` | **`line_I9mGO66zALFdGy2UIv-K3HoD`** | `89861100000000000001` | 2026-09-10T05:22:33Z |

`modemdeck_legacy_endpoint_lines`：

```
count(*) = 0        ← 空表
```

**读出来的结论**：
- 这张 SIM（ICCID/IMSI）**正确地**解析到稳定线路 `line_5c4e5d8...` —— SIM 身份侧没问题。
- `devices.endpoint_id` **已经被更新为新端点** `line_I9mGO...` —— 说明 store 接受了新端点。
- 但 **没有任何地方记录** "`line_I9mGO...` 这个端点对应稳定线路 `line_5c4e5d8...`"。

## 4. 断裂点（代码级，精确到行）

### 4.1 agent 为什么换 ID

`agent/internal/modemmanager/ids.go:80-107`，`stableLineIdentity`：

```go
sum := sha256.Sum256([]byte("line\x00" + strings.Join(components, "\x00")))
return lineIdentity{
    id:         "line_" + base64.RawURLEncoding.EncodeToString(sum[:18]),
    ...
}
```

`components` 依次由 `physical_device`、`equipment_identifier`、`device_identifier` 组成（同文件 :83-94）。

背景：此前 QDC507 的非串口 USB 接口被内核 `option` 串口驱动抢占，模组内部系统（ADB）离线，`equipment_identifier`/`device_identifier` 读不到；接口抢占修复后模组内部系统恢复，这两个字段变成可读 → **component 列表从 1 项变 3 项 → 哈希改变 → line_id 漂移**。

### 4.2 API 为什么丢线

1. `internal/store/hardware.go:33` `ApplyHardwareSnapshotWithResult`
   - :51-73 遍历快照线路，`upsertHardwareLine` 用 **SIM 身份**（ICCID/IMSI/phone）解析出**稳定的**内部 `lineID`（实现见 `internal/store/stable_lines.go:30` `resolveOrCreateStableLine`）。
   - :58 `endpointID := strings.TrimSpace(line.ID)` ← 这是 **agent 的哈希 ID**。
   - :71 `lineIDsByEndpoint[endpointID] = lineID` ← 映射**在这一帧内是正确的**，并随 `HardwareSnapshotResult` 返回。

2. `internal/communication/service.go:556`
   ```go
   lines = bindProjectedLines(lines, snapshotResult.LineIDsByEndpoint, stableLines)
   ```

3. `internal/communication/service.go:3130-3161` `bindProjectedLines` —— **丢线就在这里**：
   ```go
   endpointID := strings.TrimSpace(line.EndpointID)
   if endpointID == "" {
       endpointID = strings.TrimSpace(line.ID)
   }
   lineID := strings.TrimSpace(lineIDsByEndpoint[endpointID])
   if lineID == "" {
       continue          // ← :3150-3152  查不到就静默丢弃
   }
   ```
   其中 `line.EndpointID` 由 `projectLine`（:2875，具体 :2884-2885）设为 agent 的 line ID：
   ```go
   ID:         line.ID,
   EndpointID: line.ID,
   ```
   于是新端点 `line_I9mGO...` 在 `lineIDsByEndpoint` 里应该能查到……**除非该 map 在跨帧/跨重启场景下没能对上**。

### 4.3 真正缺失的一环：端点→线路映射没有被持久化

`modemdeck_legacy_endpoint_lines` 的读写点：

| 位置 | 操作 |
|---|---|
| `internal/platform/database/stable_line_migration.go:517` | 建表 |
| `internal/platform/database/stable_line_migration.go:611` | **仅迁移期**写入（且只写"provisional 且非合法稳定 ID"的端点） |
| `internal/store/stable_lines.go:875` | `resolveLegacyEndpointLines` **读** |
| `internal/store/stable_lines.go:740` | 线路合并时改绑 |
| `internal/store/hardware.go:404` | 删除已转正的 provisional 端点 |

**结论：这张表只在一次性迁移时被填充过，运行时没有任何代码把"当前 agent 端点 ID → 稳定线路 ID"写进去。** 一旦 agent 端点 ID 变化，映射就永久缺失。

另外 `resolveLegacyEndpointLines`（`internal/store/stable_lines.go:848`）的查询带 `AND lines.phone_number = ''` 过滤（:877），而生产库里这条线路的 `phone_number` 是空串，本来应该能命中 —— 但表里根本没有行，过滤条件无从生效。

### 4.4 为什么表现为 502

线路列表为空 → `internal/communication/service.go:3101` `resolveLine` 找不到选择器 → 返回 `"selected line is not attached"` 或 `"line was not found"` → 拨号接口链路失败，反代层给出 nginx 502。

## 5. 排障回退速查（下次同类问题）

```bash
# 1) 看 agent 真实线路身份（api 容器里没有 curl；agent 容器里有 /usr/bin/curl）
docker exec nas-comm-hardware curl -s --unix-socket /run/modemdeck/agent.sock http://localhost/v1/snapshot \
  | tr ',' '\n' | grep -E '"id"|identity_source|equipment_identifier|device_identifier'

# 2) 看 API 认为有几条线
docker logs nas-comm-api 2>&1 | grep -o 'line_count":[0-9]*' | sort | uniq -c

# 3) 对比 DB 里的稳定线路 / SIM 归属 / 端点映射
docker run --rm -v /mnt/docker-data/homesim/data:/d alpine:3.20 \
  sh -c "apk add -q sqlite >/dev/null 2>&1; sqlite3 -header -column /d/modemdeck.db \
    'select line_id,phone_number from modemdeck_lines;
     select iccid,line_id,imsi,current_imei from sim_cards;
     select imei,endpoint_id,iccid from devices;
     select count(*) as legacy_map_rows from modemdeck_legacy_endpoint_lines;'"
```

**判据**：若第 1 步的 `id` 与第 3 步 `sim_cards.line_id` 不同、且 `legacy_map_rows = 0`，即命中本故障。

## 6. 修复方案

**方向：在 API 的线路投影阶段补一个"端点 ID 失配 → 回退到 SIM 身份解析"的兜底，并把解析结果持久化到端点映射表。**

理由：agent 的哈希 line_id 从设计上就允许因硬件可读性变化而漂移（`ids.go` 的注释明确说 ModemManager 对象路径与内核端口名"永不作为持久身份"，但 `physical_device` 恰恰是内核 USB 槽位路径，本身就会变）。因此**稳定性必须由 API 侧的 SIM 身份层承担**，这正是 `modemdeck_lines` 表存在的意义。

具体改动点：

1. **`internal/communication/service.go` `bindProjectedLines`（:3130）**：当 `lineIDsByEndpoint[endpointID]` 查空时，**不要 `continue`**，而是回退用该线路的 SIM 身份（`ICCID`/`IMSI`，快照里已有）在 `stableLines` 里解析稳定线路 ID；解析成功则绑定并继续，解析失败才丢弃。

2. **`internal/store/hardware.go` `upsertHardwareLine`（:329）**：在确认 `lineID` 后，把 `(endpointID → lineID)` 写入 `modemdeck_legacy_endpoint_lines`（`INSERT ... ON CONFLICT(endpoint_id) DO UPDATE SET line_id = excluded.line_id`），使映射跨帧/跨重启存活。注意与 :404 的"转正后 DELETE"逻辑对齐，避免互相覆写。

3. **`internal/store/stable_lines.go` `resolveLegacyEndpointLines`（:848）**：重新审视 :877 的 `AND lines.phone_number = ''` 过滤 —— 该过滤会让"已有号码的线路"永远无法通过端点映射表被解析，是本故障的必要条件之一。需确认其原始意图（推测是怕 provisional 空号线路抢占正式线路），并在补齐写入路径后重新评估。

4. **防御性**：`bindProjectedLines` 丢弃线路时应至少 `slog.Warn` 一条带 `endpoint_id` 的日志 —— 本次故障中丢线是**完全静默**的，日志里只有下游的 `line was not found`，掩盖了真实原因。

5. **前端健壮性**（独立于本根因，见 `web/src/state/deviceConfiguration.ts`）：
   - 选中的 line_id 若已不在服务端线路列表中，应清理并重新选择，而不是持续打 404。
   - API 返回非 JSON（如 nginx 502 HTML）时应给出可读错误，而不是把 HTML 塞进错误文案。

## 7. 给未来 AI 的禁令

1. **看到 `line was not found` / `"selected line is not attached"`，先对比 agent 的 line_id 与 DB `sim_cards.line_id`，不要直接怀疑模组硬件。** 本次三条症状全都源于此，硬件侧 `dial`/`media` 全绿。
2. **不要"重新配对"或"重置线路"来绕过** —— 那会丢用户已配置的线路标签/策略，而正确做法是补齐端点映射。
3. **agent 的 line_id 不可当作持久身份。** `physical_device` 是 USB 槽位路径，插拔/重枚举即变；`equipment_identifier`/`device_identifier` 依赖模组内部系统在线。
4. **`modemdeck_legacy_endpoint_lines` 目前只有迁移期写入** —— 在补齐运行时写入之前，任何 agent 端点 ID 变化都会重现本故障。
5. 改 `bindProjectedLines` 时**别把 `continue` 当成"数据不完整跳过"的无害操作** —— 它在这条链路里等于"把一条健康的线路从用户界面抹掉"。

## 8. 未完成事项

- [ ] 按第 6 节实施修复 + 补回归测试（复现"同一 SIM、端点 ID 变化"场景）
- [ ] 修复后实测：线路列表恢复为 1 条、拨号可用、流量开关可用
- [ ] 语音双向通话仍未由用户实测验收（见 `docs/QDC507-VOICE-INTERFACE-CLAIM-2026-09-10.md`）
