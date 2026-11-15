# 统一转发（NAS 原生）

管理员进入 **设置 → 统一转发**，配置通道、规则、模板、免打扰和投递记录。原 Bark、短信转发菜单链接重定向到此页；旧 Bark/forwarders API 返回 `410 forwarding_replaced`，不会继续发送。Telegram 的机器人交互命令保留，生产入口关闭原自动通知调度，由此模块负责通知。iOS APNs/VoIP 推送、NKN 网络和聊天不变。

实现参考 [pppscn/SmsForwarder](https://github.com/pppscn/SmsForwarder) 的通道分类和规则思路（BSD-2-Clause）；Go/Vue 实现为本仓库独立编写，不包含其 Android 程序。Android URL Scheme、应用广播、通知读取、SIM 管理和设备系统动作明确不支持；这不是 Android 版的全量等价移植。

## 开始使用

1. 创建通道，填写参数和凭据引用，勾选“启用通道”。凭据优先用 **NAS 进程环境变量名称**：在容器的 `environment`/`env_file` 中配置对应值，重新创建容器后生效。不要把真实值写入仓库、模板或通道名称。
2. 也可以直接输入秘密值：只写入、使用现有 `SecretBox` 设置密钥加密保存。保存后只显示“已配置”，不会再返回值、nonce 或 ciphertext；留空保留，显式清除才删除。更换通道类型会清除旧秘密。
3. 创建规则，选择事件、来源线路、条件、通道顺序、策略和模板，勾选“启用规则”。空来源线路集只表示配置所有者**当前**获授权的线路，绝不是绕过线路权限。
4. 保存配置；总开关默认关闭，检查后显式启用并保存。关闭期间和旧配置版本收到的事件不补发；修改配置会取消尚未发送的旧任务。已经进入外部网络的请求不能撤回。
5. “发送合成测试通知”只测试已保存的通道，**即使总开关关闭也会实际发送**，不包含存储中的短信。测试接受不保证收件人收到。`preview` 不发送、不入队。

Fixture 页面仅供编辑和交互演示，拒绝保存、迁移、重试和发送；不得把 fixture 成功渲染说成已连通 NAS 或通知服务。

## 通道目录与参数

所有类型均有 `id/name/type/enabled/params/secrets`。`params` 中的服务器地址不能带 userinfo、查询凭据或 fragment；Webhook/机器人完整 URL 放到 write-only `secrets.url` 中。秘密项是 `{ "env": "VARIABLE_NAME" }`，或只写入的 `{ "value": "…" }`；读取时加密项只返回 `{ "set": true }`。

| type | params | secrets | 投递确认 |
|---|---|---|---|
| `dingtalk` | 无 | `url`，可选 `signing_key` | 机器人 `errcode=0`，支持原 HMAC 时间签名 |
| `dingtalk_app` | `agent_id`, `to`（userid_list） | `app_key`, `app_secret` | 取 token 后调用企业应用发送，检查 `errcode` |
| `email` | `host`, `port`（默认587）, `username`（可选）, `from`, `to`（逗号分隔）, `tls` | 登录时需 `password` | SMTP DATA 接受；TLS 必须 STARTTLS 或 implicit |
| `bark` | `server`（默认官方地址）, `group`, `sound`, `encryption` | `key`，加密时 `encryption_key` | `code=200`；加密支持 `aes-cbc`，AES key 为16/24/32字节 |
| `webhook` | 无 | `url`，可选 `headers`（JSON对象） | HTTP 2xx；不跟随重定向 |
| `wecom` | 无 | `url` | 企业微信群机器人 `errcode=0` |
| `wecom_app` | `agent_id`, `to`（touser） | `corp_id`, `corp_secret` | 取 token 后检查应用发送 `errcode` |
| `serverchan` | 无 | `key` | Server酱 Turbo `code=0` |
| `telegram` | `chat_id` | `token` | Bot API `ok=true` |
| `sms` | `line_id`（发送线路）, `to` | 无 | 复用原短信 handler 的201回执、线路权限和反滥发限制 |
| `feishu` | 无 | `url`，可选 `signing_key` | 飞书群机器人 `code=0`/`StatusCode=0` |
| `feishu_app` | `to`, `receive_id_type`（open_id/user_id/chat_id/email） | `app_id`, `app_secret` | tenant token + IM text，检查 `code` |
| `pushplus` | 可选 `topic` | `token` | `code=200` |
| `gotify` | `server`, `priority`（默认5，0–10） | `token` | HTTP 2xx且返回消息ID |
| `socket` | `protocol`（tcp/udp/mqtt）, `host`, `port`, `tls`（true/false）；MQTT另需 `topic`，可选 `username`, `client_id` | 可选 MQTT `password` | TCP/UDP仅本地传输接受；MQTT3.1.1 QoS1要求CONNACK/PUBACK |

SMTP不允许明文模式，不跳过证书验证；MQTT/TCP可显式启用TLS，UDP不支持TLS且载荷限1400字节。MQTT只实现有限的单次连接/发布/确认，不实现订阅、离线会话或代理配置。HTTP响应限32KiB、模板输出限64KiB，传输总预算20秒。HTTP沿用现有HomeSIM outbound transport；SMTP与Socket使用NAS自身网络路由，不将HTTP代理设置描述成这些协议的代理支持。

支持文本消息；不实现上游每个通道的全部高级选项（富文本卡片、附件、任意Webhook方法/脚本、Android功能、MQTT订阅等）。服务商接受与最终到达分开，`sent` 在页面显示“已接受”。未实际使用真实服务完成联调。

## 规则、模板与免打扰

- 事件：`incoming_sms`、`incoming_call`、`missed_call`。只消费原业务同事务提交的通知事件。
- 字段：`peer/body/line_id/event`。操作：`is/notis/contain/notcontain/regex`，以及显式嵌套 `and/or/not`；正则使用 Go RE2。
- 关键词以 `&&` 连接且、以 `||` 连接或，且优先于或。`notis/notcontain` 对**完整正向组合**取反：`notis "10086||10000"` 同时排除这两个号码；`notcontain "bank&&code||parcel"` 排除“bank code”和“parcel arrived”，允许“bank statement”。再套显式 NOT 会反转结果。需要匹配含这些分隔符的字面量时使用 RE2。
- 单规则最多128个条件节点、深度12；最多64条规则、100个通道和64个模板。无效配置整体拒绝，不把无效正则解释成允许。
- 策略：`all` 并行投递所有通道；`until_success` 按选择顺序到首个成功停止；`until_fail` 到首个最终失败/过期停止。重试中的任务仍阻挡后续通道，不将临时失败直接当最终失败。
- `stop=true` 表示匹配后不再处理后续规则，不改变本规则的通道策略。
- 模板仅支持 `{{.Peer}}`、`{{.Body}}`、`{{.LineID}}`、`{{.Event}}`、`{{.Time}}`、`{{.ResourceID}}`，支持标题和正文，不允许函数、脚本、循环或读取进程环境。
- 免打扰是延后到窗口结束，使用明确的 IANA 时区（例如 Asia/Shanghai），支持跨午夜；跨午夜归属开始日。星期0为周日，空集为每天。延后不延长24小时有效期。

## 持久队列与权限

新模块在同一SQLite库中建立自己的 config、AUTOINCREMENT journal、cursor 和 tasks 表。原通知事件的 AFTER INSERT trigger 在原短信/通话事务内记录 journal；回滚同时撤销 journal。初装不回填已有历史通知，游标和任务在一个事务内提交，任务按事件/规则/通道去重。事件还绑定接收时的配置 revision/开关，不能在一次启用或修改收件人后把旧事件套到新配置上。

最多4路并发；45秒租约、随机 attempt token、有限5次尝试和指数退避。批次等待后按实际认领/发送/完成时间重新计算租约、有效期、免打扰和退避，不复用批次开始时间。完成更新要求仍持有该token，旧进程的迟到回执不能覆盖新租约。串行任务在LIMIT100**之前**排除被前序 pending/sending 阻挡的候选；停止策略一次取消组内剩余pending任务，避免大批次饥饿。

每次发送重新读取配置，并在最终发送边界检查当前所有者账号仍启用/仍为管理员、来源线路权限、SMS发送线路权限及原设备OTP策略。私有载荷保留原 `endpoint_line_id`/`endpoint_message_id`，重启后不得从不完整序列化结果推断“不是受限设备”。SMS使用稳定的 `forwarding_<task-id>` 请求ID并进入原短信业务handler，不绕过反滥发或原请求重放控制。

明确撤权仍会取消任务；账号权限查询暂时失败则禁止发送，并以 `pending/authorization_unavailable` 延后30秒重查，保留私有载荷。此时尚未发生的发送不消耗5次尝试预算；仅退回当前worker自身刚领取且未发送的那一次，保留此前真实发送或中断租约消耗的次数。等待仍受原24小时有效期、当前配置和每次发送前的权限检查约束，重启后可以继续。

`expired` 与 `attempt_limit` 区分：时间无效、未来异常或超过24小时记 `expired`；租约中断后耗尽尝试记 `failed/attempt_limit`，使UntilFail正确停止。只有仍在24小时内、当前配置启用且revision相同的failed任务可显式重试；过期、取消、已成功或unknown任务不能重放。手动重试不复活已取消的同组通道。

HTTP/SMTP/MQTT/Socket采用**至少一次**语义：外部已经接受后进程中断或响应丢失可能重复，不能宣称恰好一次。SMS依赖原稳定请求ID去重；原业务返回Unknown时不会把它伪造成已送达。收件人/服务商不支持去重的场景需自行接受该限制后启用。

记录API只输出任务/事件/线路/规则/通道ID、状态、尝试次数和固定错误类别，不输出正文、号码、凭据、传输错误原文或响应正文。终态sent/cancelled/expired/unknown任务移除逻辑载荷；failed载荷只在有效重试窗口保留，终态元数据保留7天。SQLite/WAL历史页与原短信表不是安全擦除承诺，备份和文件系统访问仍按原NAS数据保护处理。

## 迁移

仅在新模块通道与规则均为空时，由管理员显式 `POST /api/v1/settings/forwarding/migrate`，带当前revision。导入旧 `forwarders.json`、`bark.json`，总开关保持关闭，原文件不改。旧明文Webhook URL/签名key使用现有SecretBox加密；不复制为公开字段。

NKN短信通道不导入。Telegram仅为当前管理员获授权的通知范围建立**禁用**的通道/规则；原机器人token不提供提取接口，需给新通道补充新的环境变量引用。其他所有者的机器人通知只报告需要人工复核，不扩大权限。机器人交互命令继续由原Telegram模块管理。

## API

统一前缀 `/api/v1/settings/forwarding`，所有资源都要求原管理员认证；写操作沿用原CSRF/Origin校验。

- `GET/PUT`：配置与revision CAS，GET附带15类通道目录及unsupported能力。
- `POST /preview`：`{config,rule_id,event}`，返回匹配与渲染结果，`sends=false`。
- `POST /test`：`{channel_id}`，只发送固定合成测试文本，不取短信数据库正文。
- `GET /deliveries?before=<task-id>`：按ID递减，每页100条，过滤当前管理员不可访问的线路。
- `POST /retry`：`{id}`，重新入队，发送时仍重新检查权限。
- `POST /migrate`：`{revision}`，显式导入并保持关闭。

配置修改会取消旧pending任务；修改后需要重新加载revision再保存。不要把密钥放在通道名字、公开参数或模板里，那里是操作者输入的普通文本，不具备“自动识别任意秘密”的能力。

## 离线验证

在Go工具链≥模块要求的1.26.5及有效libopus开发环境中：

```sh
go test -p 4 -count=1 -mod=readonly ./cmd/... ./internal/...
go test -race -count=1 -p 2 -mod=readonly ./internal/forwarding ./internal/airsimgw ./internal/communication ./internal/telegramruntime
go test -race -count=1 -mod=readonly -run 'Forwarding|AirSIM|Adversarial' ./internal/httpapi
go vet -p 4 -mod=readonly ./internal/forwarding ./internal/httpapi ./internal/communication ./internal/telegramruntime ./internal/airsimgw ./cmd/modemdeck
cd web
npm ci --include=dev --no-audit --no-fund
npm run test:contracts
npm run typecheck
npm run lint
npm run build
VITE_MODEMDECK_FIXTURE=1 npm run dev -- --host 127.0.0.1 --port 5174 --strictPort
```

用预装Playwright Python和Chromium运行 `scripts/test-forwarding-browser.py --base-url http://127.0.0.1:5174 --output <临时目录>`，检查条件编辑器、跨午夜免打扰、旧链接和窄屏。所有传输测试只使用本地HTTP/SMTP TLS/TCP/UDP/MQTT mocks和合成数据，不发送真实短信、不拨号、不联通真实服务。
