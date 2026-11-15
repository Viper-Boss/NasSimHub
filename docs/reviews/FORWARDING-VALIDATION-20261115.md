# 统一转发重写交付与验证（2026-11-15）

后续已补修账号权限查询故障误取消任务的问题；最新产品提交与本轮实测见 [权限查询故障补修验证](FORWARDING-AUTHORIZATION-RETRY-20261115.md)。以下保留原交付阶段的源码、制品和验证记录。

已按用户“忽略原源码、从头做”的决定，从远端main重写统一转发及交接要求的NAS侧AirSIM多线路支持。原环境未提交源码没有恢复，历史774项等检查点未冒充新代码验证。此次有实际代码、生产接线、迁移、关键回归、浏览器交互及构建；真实NAS/手机/通知服务联调另列未运行。

## 源码与制品

- 仓库 `Viper-Boss/NasSimHub`；基线main `b14f7e0bc5288ab3186589a6b35a777c80e93cad`。
- 独立分支 `codex/forwarding-resume-20261115`，本地 `/workspace/NasSimHub`。
- 第一阶段实现 `8d34a6ac8135d7b53ea0d024c7e73c355803f303`；最终租约时间补修后产品源码 **`d6ee9b8405b00966d27f6ab4a95c15e4568f70cf`**。
- 最终 `git archive --format=tar` SHA256 **`d99d94fc0c86475a5268801c0a98c36debd7177a7ffd80eb23a0562bbd6ec42a`**。
- 原生Linux/amd64 `cmd/modemdeck` 实际构建44295352字节，SHA256 `d36d44fb5881cfdb9f8b4f3decf34bd9a153a21754b5b48041bb9eb0609b4331`；`go version -m` 确认vcs.revision=d6ee9b8405b00966d27f6ab4a95c15e4568f70cf、vcs.modified=false。只构建，未作为真实服务运行或部署。
- 本报告及JSON为后续仅文档提交，不声称报告HEAD重新构建了二进制。机器证据见同目录 `FORWARDING-VALIDATION-20261115.json`。

## 本次实际实现

1. `internal/forwarding`：15类NAS通道、递归AND/OR/NOT及RE2规则、受限模板、时区/跨午夜免打扰、SQLite事件journal/游标/任务、4路并发、随机token租约、有限重试、分页脱敏历史和显式重试。凭据引用环境变量，直接输入/旧密钥使用原SecretBox加密；GET不返回秘密、nonce或ciphertext。
2. 新API统一 `/api/v1/settings/forwarding`，包含配置、preview、test、deliveries、retry、migrate。全部经过原认证/管理员门和写入CSRF检查。发送前重读当前账号、线路权限、总开关及原设备OTP策略；私有队列保留原endpoint ID和endpoint message ID。
3. `cmd/modemdeck/main.go` 实际初始化队列表和原通知事件的同事务trigger、接入发送者并启动worker，关闭时等待worker退出。SMS进入原短信handler，保留反滥发、权限和稳定request ID处理；不是另一个直接调用设备的入口。
4. 移除原Bark执行器、旧短信内存广播发送器及旧面板/注入脚本；旧设置API明确410。NKN短信不迁移，NKN网络/聊天保留。Telegram只在生产入口关闭旧自动通知调度，机器人命令保留。
5. 显式迁移旧Bark/forwarders文件，保留原文件、总开关关闭、秘密加密；Telegram只为当前管理员原授权范围生成禁用配置，需补环境引用，不提取原token或扩大其他账号权限。
6. Vue统一页提供通道、规则、模板、免打扰、预览和历史；旧链接跳转，使用原SelectControl、表单label及按钮样式，中英文完整文案按需加载。Fixture拒绝真实保存/发送，预览不伪称调用过NAS规则引擎。
7. AirSIM：不改手机App；NAS侧选择最多32条线路、配对范围冻结、聚合短信保留来源/独立ACK、按sim_account路由写操作。新增/重排全局线路不扩大旧配对，撤权/重启/无效SIM账户均有回归；通话仍受原call lease/媒体所有权和单PCM会话限制。

完整使用、各通道参数、非等价选项、隐私/至少一次边界与重跑命令见 `docs/FORWARDING.md`；手机兼容范围见 `docs/AIRSIM-COMPAT.md`。未修改Go/npm锁文件、提高包体预算、变更Git作者配置或改手机源码。

## 实际验证结果

- **最终产品根模块**：`go test -p 4 -count=1 -mod=readonly ./cmd/... ./internal/...`，49个测试包通过，另5个包无测试，失败0。
- **最终转发race**：`go test -race -count=1 -mod=readonly ./internal/forwarding` 通过；包括下列四项指定回归及租约时间补修。
- **最终相关API race**：`-run 'Forwarding|AdversarialPlantedSecrets' ./internal/httpapi` 通过，覆盖原管理员/CSRF、迁移、OTP/线路撤销、原短信反滥发、日志和错误响应脱敏。
- 第一阶段完整执行 `-race ./internal/forwarding ./internal/airsimgw ./internal/communication ./internal/telegramruntime` 与 `-race -run 'Forwarding|AirSIM|Adversarial' ./internal/httpapi` 均通过。最终补修只改转发engine/sql/专用测试和文档；AirSIM/communication/Telegram/API源码与8d34a6a相同，已用Git逐路径核对，不将不同文件冒充同源。
- **最终受影响包go vet**、原生生产cmd构建、仓库hygiene和diff检查通过。
- **前端779/779契约检查**，包含原774项和新增5项真实API客户端/目录/权限契约；typecheck、lint、生产build、原包体预算均通过。最坏首屏231.3KiB gzip，最大JS97.7KiB gzip（原上限240/100KiB保持）。
- **实际Chromium154.0.8037.57**：英文/中文 × 320/390/1280，共6个上下文、84项检查通过；条件输入有原生label、否定关键词可编辑、嵌套条件、跨午夜免打扰、旧链接、AirSIM多选、fixture保存/预览拒绝、无真实API写入、无测量阶段运行错误/横向溢出。脚本 `scripts/test-forwarding-browser.py` 可重跑。测量使用明确预热的DEV fixture，不是生产浏览器/NAS端到端或冷启动验收。
- 前端与浏览器按8d34a6a执行；最终d6ee9b8对 `web/`、`scripts/` 无差异，JSON明确记对应源码。Go原生制品和最终Go检查按d6ee9b8执行。

### 四项指定回归

- 两通道UntilFail，首项连续租约中断达到5次：首项 `failed/attempt_limit`，第二项取消，HTTP发送0。
- 25小时前短信：`expired`，HTTP发送0，显式Retry拒绝。
- 两短信事件/100通道/UntilSuccess：200任务，Due先返回两个position0链首，最终**2次发送、198条取消**。
- `notis "10086||10000"` 同时排除两个号码；`notcontain "bank&&code||parcel"` 排除前述组合、允许部分词或普通消息；外层NOT正确反转。

另覆盖journal与原SMS事务回滚、重启后endpoint元数据、并发Claim唯一胜者、旧token迟到回执拒绝、OFF/旧revision事件不补发、时区与免打扰、无效时间不阻塞后续journal、凭据SecretBox关联数据绑定、HTTP应用层失败/重定向/有界响应、Bark CBC解密、SMTP TLS、TCP/UDP/MQTT QoS1本地协议交互。

### 实际发现的批次时间缺陷及负对照

最终复核发现：8d34a6a将批次开始时间传给等待并发槽的后续任务，可能以过早时间认领租约/判断有效期，也把发送耗时计入了退避。实际从该提交归档构造独立旧源码，确认engine文件SHA256 `979b7cd96aaf9fbbf99870d24a8fcaf9e830d8f7adebe7e2db828f43d10197c9`，运行同一个“等待后已过期”断言：**exit1，sends=1/status=sent**，是真实预期失败负对照。

d6ee9b8改为实际认领/发送/完成时间，发送前再检查有效期与免打扰，重排任务保留token/revision保护。新回归证明过期后发送0、认领时租约45秒、发送完成后才开始退避、等待跨入免打扰则延后且不消耗尝试；转发race及根模块再次通过。旧版本结果没有写成最终成功。

## 工具与实际失败记录

使用可用的Go1.27.3和libopus1.5.2，原go.mod最低要求1.26.5保持；不是交接中历史Go1.26.5/libopus1.3.1环境。自有禁用特权容器中只读挂载现有工具链/native库，私有tmpfs保存模块/构建缓存，测试输出在忽略目录外的 `/workspace/.nassimhub-validation/`；未借用真实NAS数据、Owner或密钥。

实施期间实际出现并修正：缺libopus头搜索路径；测试fixture重复endpoint message ID；已移除广播的旧日志断言需改为验证新队列及脱敏历史；新增客户端mock漏Content-Type；前端原生select/未翻译文案/残余未用import不符合现有契约；模板和样式编译错误；首屏JS曾超100KiB约0.4KiB，改为按需加载完整文案，未放宽上限；页面渲染可选secret曾导致假dirty，已修并进入84项浏览器检查。

开发首次Vite依赖优化曾造成模块重载异常，稳定依赖后才进行明确预热的测量；不声称冷启动已验收。负对照Docker cp无法访问运行时tmpfs，改用exec stdin；最初无新增断言的“no tests to run”未作为负对照，只有随后实际执行并失败的断言用于上述证明。

## 未运行与交付边界

未部署/连接真实NAS；未用原版AirSIM实体手机验证配对、音频或后台行为；未发送真实通知/短信或拨号；未使用真实凭据；未构建ARM或完整生产Docker发行镜像；未完成生产构建页面到真实NAS的端到端验收。15类通道的“通过”指本地协议/HTTP mock与业务权限路径，不是对应服务商实网到达。

重试是至少一次，不承诺外部恰好一次或收件人已读。Android专属功能、SMTP明文、MQTT订阅及全部上游高级通道选项不实现；这些是明确边界，不留自动执行的假壳。整体仓库双轨许可、原有签名/授权及NKN功能保持。

代码交独立PR审核，不自行合并。详细公开机器证据不含原始短信、凭据、临时DB或私钥。


## PR 与 GitHub CI 平台状态

非草稿 [PR #4](https://github.com/Viper-Boss/NasSimHub/pull/4) 已提交main，未合并。首次报告头336f29be630d8cff90bc0d66d787d9ed6ae772a4触发的 [CI run43667743090](https://github.com/Viper-Boss/NasSimHub/actions/runs/43667743090) 三个任务均显示failure，但steps为空。逐任务读取annotations，三者都明确：

> The job was not started because recent account payments have failed or your spending limit needs to be increased.

因此GitHub Actions未运行，不能记通过，也不是该次CI执行代码后发现的测试失败。本地实际执行结果如上；未修改CI、计费设置或支出额度，未手动重跑。此补充仅文档，不改变产品源码与归档。
