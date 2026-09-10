# QDC507 短信收不到 / 必须切飞行模式才能收 —— 故障档案

- 日期：2026-09-10
- 现象：绥哥报告「短信刷新不及时，想收个短信还要手动开关飞行模式才能收到」
- 结论：**ModemManager 有一条短信永远卡在 `receiving` 状态，占住模组短信槽位，导致后续短信也收不完**。ModemDeck 两层代码都没有处理这种「半途卡住」的消息——一层把它过滤掉了，另一层只清理正常终态的消息。
- 与硬件无关，与固件无关，**与飞行模式无关**（切飞行模式只是重启了射频，把卡住的槽位冲掉，所以「看起来有效」）。

---

## 1. 现场证据（NAS 实测）

ModemManager 里当时只剩一条短信对象，永远停在 `receiving`：

```
$ mmcli -m 0 --messaging-list-sms
    /org/freedesktop/ModemManager1/SMS/10 (receiving)

$ mmcli -m 0 -s 10
  Content    |    number: 95555
  Properties |  pdu type: deliver
             |     state: receiving          ← 卡死，永远到不了 received
             |   storage: me
             | timestamp: 2026-09-10T11:02:55+08    ← 当时已过去 6 小时以上
```

agent 的快照里**确实带着**这条消息（`"state":"receiving"`），所以 agent 侧没问题。

API 日志里能看到正常短信是能走通的：

```
processing user request to list SMS messages...
reported 2 SMS messages available
processing user request to delete SMS message '/SMS/11'...
deleted SMS message '/SMS/11'
reported 1 SMS messages available
```

即：**正常消息 list → 落库 → 删模组对象**，这条链路是好的。卡住的那条从来没被 list 进来删除。

同时日志显示每一条 `SMS received` 前面 1 秒内都有一对 `set_radio_enabled`：

```
07:45:27.578  set_radio_enabled     ← 绥哥开飞行模式
07:45:29.208  SMS received 22
07:45:29.257  set_radio_enabled     ← 绥哥关飞行模式
```

**这解释了「必须切飞行模式」**：射频重启会冲掉卡死的短信会话，模组短信槽位被释放，后续短信才收得完。不是飞行模式有什么特殊作用。

---

## 2. 根因：两层代码各有一个洞，合起来导致卡死无人清理

### 洞 1 —— 投影层把「未收到」的来信直接丢掉

`internal/communication/service.go` 的 `projectSnapshot()`（原 2811-2817 行）：

```go
line, found := lineIndex[message.LineID]
if !found || strings.TrimSpace(message.Number) == "" || strings.TrimSpace(message.Text) == "" {
    continue
}
if normalizeMessageDirection(message.Direction) == "incoming" &&
    strings.ToLower(strings.TrimSpace(message.State)) != "received" {
    continue     // ← 来信只要不是 received，就被整条丢弃
}
```

这个过滤本身**动机是正确的**：内容还没收完的短信不该落库、更不该通知用户。但它顺手把消息从 `hardwareSnapshot.Messages` 里摘掉了，于是后面负责清理的函数**根本看不到它**。

### 洞 2 —— 清理层只处理终态消息

`internal/communication/service.go` 的 `enqueueDeviceMessageCleanup()`（原 624-628 行）：

```go
state := strings.ToLower(strings.TrimSpace(message.State))
if state != "received" && state != "sent" {
    continue     // ← receiving / sending 一律跳过：既不落库，也不删模组对象
}
```

就算消息能走到这里，`receiving` 也不在清理范围内。

### 合起来的后果

卡住的那条短信：

1. 不落库（洞 1 让它到不了落库逻辑；即便到了，洞 2 也会跳过）
2. **不删模组对象**（洞 2 跳过）→ 永远占着模组的短信槽位
3. 后续每一条新短信都收不完 → 用户什么都看不到
4. 只有射频重启（切飞行模式 / 重启模组）才能冲掉 → 这就是「必须手动开关飞行模式」

补充说明：`internal/store/hardware.go:118-127` 对快照里的**所有**消息都做 upsert，只有 `Direction=="incoming" && State=="received"` 才作为事件发布。也就是说卡住的消息**本来就不会**产生用户可见的通知——这是对的，但也意味着它一旦卡住就完全是个沉默的黑洞。

---

## 3. 修复

### 3.1 放在哪一层：**agent**，不是 API

第一版我把修复放在 API 层（`internal/communication`），后来**推翻了**，原因是一个硬约束：

API 的 `Refresh()` 是**纯事件驱动**的——`Run()` 的维护循环里，对支持 Events 的 agent 只刷 telemetry，不重读完整快照（`internal/communication/service.go:1542-1551`，并且有 `TestRunSamplesTelemetryWithoutApplyingHardwareSnapshot` 专门守着这个设计）。而卡死的短信**再也不会产生任何 ModemManager 信号**，所以：

- API 根本不会再次观察到它 → API 侧的「观察到两次」逻辑永远凑不齐
- 就算偶尔因为别的事件碰巧刷到，也只是碰运气

agent 侧则**已经有为这类问题准备的成熟模式**：`cmd/modemdeck-agent/main.go` 里有两个 3 分钟周期的 reconciler（QDC507 语音 adbd、射频状态），注释写得明明白白——「模块卡住时不会有 lifecycle 事件，所以必须有周期性 tick」。短信卡死是同一类问题，理应放在同一层，并且不依赖 API 是否在跑。

所以 API 层的改动已整体回退（`git checkout` 回 r67），最终只保留 agent 侧修复。

### 3.2 修复实现

新增 `agent/internal/modemmanager/message_hygiene.go`：

- `messageStateInTransit(state)` —— `receiving` / `sending` 视为在途
- `(*Provider).ReconcileStuckMessages(ctx)` —— 读一次快照，把「上一轮也在途、这一轮还在途」的消息删掉

判定规则用的是**两轮对比**，而不是时间戳：

```go
// 本轮在途的记下来；上一轮也在途的 → 卡死
inTransit[messageID] = struct{}{}
if _, seen := p.transitMessages[messageID]; seen {
    stuck = append(stuck, message)
}
p.transitMessages = inTransit   // 整张替换，离开快照的消息自动遗忘
```

**为什么不看时间戳**：`Receiving` 那条的 `Timestamp` 来自 SMSC，ModemManager 可能不给（`parseMessage` 里是空字符串），而且 SMSC 时钟未必可信。而 reconciler 的周期本身（3 分钟）就是天然的度量——「连续两轮都在途」= 卡了至少一个周期，正常的多段短信合并接收在数秒内完成，不可能跨两轮。

**开箱即用**：`transitMessages` 一直是整张赋值，所以 nil map 也安全，无需改构造函数。

### 3.3 接入周期

`agent/cmd/modemdeck-agent/main.go` 新增 `runMessageReconciler`（与既有两个 reconciler 同构）：

- 启动时先跑一轮（只做记录，不会删任何东西）
- 之后每 **3 分钟**一轮，每轮 60 秒超时
- 删除单条消息超时 10 秒（`stuckMessageDeleteTimeout`）
- 删除成功/失败都打 `Warn` 日志，关键字见第 5 节

也就是说：一条短信卡住后，最长约 **3～6 分钟**内会被自动清掉，槽位释放，后续短信恢复正常。而且**不依赖前端是否打开、不依赖 API 有没有收到事件**。

### 3.4 为什么不改前端

前端刷新完全由 SSE `/messages/events` 驱动（`web/src/state/messageRuntime.ts`），而事件只在消息以 `received` 落库时才发布。所以前端的刷新链路本身没问题——**根因是上游压根没产生「已收到」的消息**。槽位修好后事件自然就来了。

---

## 4. 验证

新增测试（`agent/internal/modemmanager/message_hygiene_test.go`，该包含 Linux-only 文件，需在 NAS 的 golang 容器里跑）：

| 测试 | 断言 |
|---|---|
| `TestMessageStateInTransit` | `receiving`/`sending`/`RECEIVING` 为真；`received`/`sent`/`stored`/`unknown`/空串为假 |
| `TestStuckTransitMessagesOnlyReportsSecondObservation` | 第一轮不报；第二轮只报「一直在途」的那条；离开快照后再出现重新起算 |
| `TestReconcileStuckMessagesClearsWedgedReception` | 第一轮不删；第二轮删掉 `receiving` 的对象；第三轮无重复删除 |
| `TestReconcileStuckMessagesLeavesCompletedReceptionAlone` | 在途中的消息不删；两轮之间转为 `sent` 的消息不删（回归保护） |

同时 `internal/communication` 保持原样（已回退），整包测试通过。

---

## 5. 排障速查（给未来的 AI）

1. **「短信收不到 / 要切飞行模式才收得到」先查模组里有没有卡在 `receiving` 的短信对象**：
   ```
   docker exec nas-comm-hardware mmcli -m 0 --messaging-list-sms
   docker exec nas-comm-hardware mmcli -m 0 -s <N>      # 看 state:
   ```
   `receiving` 且时间戳明显是过去很久 → 就是这个故障。
2. **不要**先怀疑前端刷新。前端刷新由 SSE 事件驱动，事件来自「已 `received` 且落库」的消息；没有新事件说明上游没有完成接收。
3. **不要**把「切飞行模式能收到」当作飞行模式的功劳——那只是射频重启顺带冲掉了卡死的短信会话。真正的修法是让 ModemDeck 主动清掉卡住的对象。
4. `set_radio_enabled` 紧跟着 `SMS received` 的日志配对模式，是「用户被迫手动重启射频」的典型指纹，见到就按本档案查。
5. **修完之后**卡死消息最长 3～6 分钟自动清掉，日志关键字：`SMS stuck mid-reception was cleared to free the modem SMS slot`。
6. **这一层不要放回 API**：API 的 `Refresh()` 是事件驱动的（见 3.1），卡死的消息不产生事件，放 API 永远不会触发。要加这类「静静卡住、不再产生事件」的兜底，都放 agent 的周期 reconciler——`main.go` 里已经有两个先例。

