# 账号权限查询故障补修验证（2026-11-15）

按用户“处理下1”的要求，仅修复临时账号权限查询失败被误判为撤权的问题，继续提交到 PR #4，不自行合并。

## 行为

- `Users` 查询失败（含取消、超时）返回固定 `forwarding_unavailable`，不泄露原始错误；账号不存在、禁用、降为成员或线路撤权仍然拒绝。
- 领取前、领取后的最终检查及短信业务入口的账号查询故障，都禁止发送并保留任务，以 `pending/authorization_unavailable` 延后30秒重查。
- 尚未发送的当前领取不消耗发送预算；计数回退更新匹配任务次数、配置 revision 和当前 token。以前中断的租约保留计数，旧 worker 不能修改新租约。
- 保留原24小时有效期、配置变更取消、每次发送前重新鉴权和短信业务防滥发边界。等待可跨重启恢复。

## 源码与构建

- 补修前 HEAD：`424513f07ffdf9e3118f01926b457a5afecbfd30`。
- 本轮产品提交：`7b7ffcd3ed084ec3190bd3139d36541b98d8d19e`。
- 该提交 `git archive --format=tar` SHA256：`ef1cd0827faacb3b0e76bc3029477537ecf462b18c23110d4c103b60fb106972`。
- Go 1.27.3 / Linux amd64 原生生产入口构建通过，大小44308976字节，SHA256：`34558d7eb2d2a8a235947f414e4f883d0e057c19467754e4aeb7c40ad93743f4`。构建信息确认该提交及 `vcs.modified=false`；未运行服务或部署。
- 后续报告提交只增加验证记录和历史报告链接，不将旧版本前端或二进制检查冒充本轮执行。

## 实际检查

```sh
go test -p 2 -count=1 -mod=readonly ./cmd/... ./internal/...
go test -race -p 2 -count=1 -mod=readonly ./internal/forwarding
go test -race -p 2 -count=1 -mod=readonly -run 'Forwarding|AdversarialPlantedSecrets' ./internal/httpapi
go vet -p 2 -mod=readonly ./internal/forwarding ./internal/httpapi
go build -p 2 -mod=readonly -o /cache/bin/modemdeck ./cmd/modemdeck
sh scripts/check-repository-hygiene.sh
git diff --check
```

以上全部通过；根模块49个测试包通过，另5个包无测试。转发 race 12.785秒，相关API race 16.357秒。隔离测试使用合成数据和本地发送替身，无设备或真实密钥。

新回归覆盖三个检查点连续超过5次查询故障后恢复并重启，仅实际发送一次；明确拒绝在两次鉴权检查均取消；故障等待仍会过期；旧租约不可释放新租约；此前中断次数保留；等待后撤权或配置变更仍阻断发送。API另覆盖查询故障/取消/超时的503与脱敏，以及短信发送服务未被调用。

补修前源码加上新断言实际失败：转发故障/过期用例在首轮错误分类断言失败，API断言记录 `lookup failure treated as revoked forwarding_denied`。日志分别保存在工作区验证目录的 `authorization-negative.log`、`authorization-api-negative.log`，不将旧源码测试记为通过。

首次环境运行还遇到临时目录禁止执行及Opus挂载路径错误，两次均未进入测试；修正后才执行上述负对照。API race 首次链接因512MiB临时目录不足失败，改用较大的独立临时目录重跑通过。三次环境失败日志保留为 `authorization-environment-noexec.log`、`authorization-api-environment-opus.log`、`authorization-api-race-environment-space.log`，未修改断言、依赖锁或CI预算以规避。

本轮没有重跑前端/浏览器检查，没有真实NAS、手机、服务商联调、ARM构建或生产Docker发行构建。此前前端结果与CI计费阻塞见历史报告；计费设置未由本任务修改，CI通过与否须单独核实。

推送后已核实 [CI run 43677925002](https://github.com/Viper-Boss/NasSimHub/actions/runs/43677925002)（HEAD `5d736f9c0e09b101416c63ad01ad647069dbd066`）：Go、Web、Deployment三个任务均未启动、steps为空，检查注释均为 “The job was not started because recent account payments have failed or your spending limit needs to be increased”。这是平台计费阻塞，本轮CI未执行，不能计为通过；未修改计费、工作流或手动重跑。
