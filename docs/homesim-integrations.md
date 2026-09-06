# HomeSIM 当前接口与后续接入边界

主项目为 `nas-comm-gateway`；不修改 `.zcode/workspace/default/frontend`。本页描述实现状态，不是部署完成证明。

## 已实现的新增服务端接口

继续使用原有会话 Cookie、CSRF 校验与账号权限，不另建匿名或 API Key 直连接口。

| 接口 | 行为 | 权限 |
| --- | --- | --- |
| `POST /api/v1/contacts/backup` | 不接收客户端联系人或文件路径；分页读取账户联系人，写入独立 JSON，返回 201 与 filename/count/created_at | 已登录，保留联系人 owner scope |
| `GET /api/v1/settings/homesim` | 读取接入草稿，返回 `draft`、`state: draft_only`、`runtime_ready: false` | 管理员 |
| `PUT /api/v1/settings/homesim` | 保存完整草稿及当前 revision；旧版本返回 409；不启动任何执行器 | 管理员 + CSRF |

联系人备份位置为 SQLite 所在目录的 `backups/contacts/`；fnOS overlay 中对应 `MODEMDECK_DATA_DIR/backups/contacts/`。文件采用独立随机名称、0600 权限、临时文件 + 同目录 rename；写入失败移除本次临时文件，不改旧备份。单任务执行，超过 10000 联系人或 32 MiB 明确失败。读取沿用分页接口语义，不是跨并发编辑的数据库事务快照；建议备份期间不要编辑联系人。本次没有实现恢复或自动清理旧备份。

接入草稿位于同一数据目录的 `homesim/integration-draft.json`，0600 权限。只允许保存密钥环境变量名称，不保存 API Key、PIN 或 PUK；严禁在 URL 中夹带认证信息、查询串或片段。保存不会测试或调用第三方。

## 前端入口

- 消息：SIM 状态按钮、最近 3 个验证码、验证码历史扫描、签名聚合筛选、气泡号码时间、未读红点。
- 通话：每行右侧录音入口，展开后带进度与拖动；“通话守护与 AI 助理”进入语音设置。
- 联系人：立即备份；演示模式不会宣称已备份。
- 系统 → 模块 → SIM：解锁 PIN、启用/关闭 PIN 保护、更改 PIN、用运营商 PUK 解锁并设置新 PIN。

## 号码标记与归属查询候选

- 聚合数据来电号码显示：https://www.juhe.cn/docs/api/id/511 。提供诈骗、骚扰、推销等号码标签；账号申请、授权范围、费用、覆盖率及缓存权利需按服务商约定确认。当前没有接入调用。
- 聚合数据手机号归属地：https://www.juhe.cn/docs/api/id/11 。用于地区及号段运营商信息，不是实时位置，携号转网可能导致差异。
- 开源参考 https://github.com/SysAdminDoc/CallShield 是 Android 项目及其号码库工作流；未验证其中国号码覆盖，未复制数据库或承诺具备小米同等识别率。

短信企业归类当前采用本地【签名】规则和明确别名，不需要把短信原文发送给第三方。既不能把签名当作银行认证，也不应把任意未知号码合并到真实银行联系人。

## AI 代接适配器待实现

当前只提供配置数据结构，**没有可用的 AI 通话执行端点**。下阶段需实现以下服务端契约，不能只将文字聊天接口拼到网页上：

1. `startSession(call_id, line_id, provider, model, audio_format, disclosure)`：由 NAS 取得通话及媒体租约，人工接听优先。音频编码/采样率须由适配器明确协商。
2. 双向流：模块输入音频 → 实时语音服务；返回音频 → 模块。实现打断、超时、最长时长、取消和断线回收。
3. 转写事件：`call_id, segment_id, speaker, start_ms, end_ms, text, final`。按段去重，只有最终文本进入摘要。
4. 完成结果：`call_id, transcript_ref, summary, action_items, recording_refs, status`；通过现有权限控制读取，原录音保留在 NAS。
5. 推送：按 `call_id` 幂等，失败重试限次；默认只推摘要，号码和敏感文本脱敏，不传验证码、密码或支付授权。

可以参考火山引擎实时语音方案的官方文档：https://www.volcengine.com/docs/6348/1902994?lang=zh 。服务商尚未确定，当前也没有调用其服务。

## 自动拒接与 SIM 自动解锁待实现

号码策略须运行在 NAS：白名单优先 → 明确黑名单 → 查询成功且符合拒接标签及阈值才拒接；查询失败放行。每个 call_id 只执行一次，确认仍在响铃，不能挂断已被用户接起的电话。

自动 PIN 解锁须在后台实施：仅保存已通过实卡验证的 PIN，使用服务器密钥加密并绑定完整 ICCID；每次卡插入/启动周期只允许一次尝试，且剩余次数未知、卡标识变化、PUK 锁定、上次结果不确定或上次失败时停止并通知。PUK 永远不能自动重试。当前 UI 不采集自动解锁 PIN，也没有自动解锁服务。

## 验证边界

前端 493 项测试、TypeScript、ESLint 与构建通过；新增 Go HTTP 测试和 API 入口已编译为 Linux amd64，但未在 Linux/NAS 执行。没有实卡、NAS、云服务 Key，未做真实通话、短信或第三方联调。演示数据明确标注，不能作为部署或模块兼容性的证据。
