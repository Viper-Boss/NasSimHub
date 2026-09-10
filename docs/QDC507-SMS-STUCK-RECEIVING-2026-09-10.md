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

### 3.1 把「在途来信」显式捞出来交给清理逻辑

`projectSnapshot()` 新增第三个返回值 `unsettledMessages []store.HardwareMessage`：来信中处于**在途状态**（`receiving` / `sending`）的消息，仍然**不落库**（保持原有语义），但会被单独收集出来传给清理逻辑。

注意这里放宽了 `Text` 的约束：清理只需要消息 ID 和 line，不需要正文，所以正文为空的在途消息也能被收集（正常落库路径仍然要求正文非空，行为不变）。

### 3.2 清理逻辑识别「卡住的在途消息」并清掉

`enqueueDeviceMessageCleanup()` 新增第二个入参 `unsettledMessages`，并做了两件事：

- 用 `messageTransitSince map[string]time.Time` 记录每条在途消息**第一次被观察到**的时间（这个 map 在消息转为终态或从快照消失时立即清理，不会泄漏）
- 在途时间超过 `deviceMessageTransitGrace`（**3 分钟**）仍没收完 → 判定为卡死 → 排进删除队列 + 打一条 `Warn` 日志，日志里带 `message_id` / `line_id` / `state` / `stuck_for`

新增辅助：

```go
func messageStateInTransit(state string) bool {
	return state == "receiving" || state == "sending"
}
```

**为什么用「观察时长」而不是短信自带的时间戳**：`projectMessage()` 在 ModemManager 的 `Timestamp` 解析失败时会**回退成观察时刻**，那种情况下时间戳每次刷新都会变，永远算不出「卡了多久」。用服务侧自己记录的时间更可靠，也完全不依赖 SMSC 的时钟。

**为什么是 3 分钟**：正常的长短信（多段合并）接收在数秒内完成。3 分钟足够宽松，不会误删正在接收的消息，又能自动释放槽位。

### 3.3 为什么不改前端

前端刷新完全由 SSE `/messages/events` 驱动（`web/src/state/messageRuntime.ts`），而事件只在消息以 `received` 落库时才发布。所以前端的刷新链路本身没问题——**根因是上游压根没产生「已收到」的消息**。修好槽位后事件自然就来了。

---

## 4. 验证

新增测试（`internal/communication/service_test.go`）：

| 测试 | 断言 |
|---|---|
| `TestMessageStateInTransit` | `receiving`/`sending` 为真；`received`/`sent`/`stored`/`unknown`/空串为假 |
| `TestRefreshClearsSMSWedgedMidReception` | 首次观察不删；宽限期内不删；超过宽限期**删除**；删除后不再重复删 |
| `TestRefreshRestartsTransitClockWhenMessageLeavesTheSnapshot` | 消息从快照消失后再出现，重新起算宽限期（证明记录不会漏） |
| `TestRefreshDeletesOnlyPersistedTerminalMessagesFromDevice`（既有） | 仍在跑：`stored` 消息不得被删，回归保护 |

结果：`internal/communication` 整包通过；`GOOS=linux` 交叉编译干净。

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
5. 修完之后卡死消息会在 **3 分钟**内被自动清掉，日志关键字：`SMS stuck mid-reception was cleared to free the modem slot`。
