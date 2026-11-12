# 2026-10-25 审查报告复核与处理记录

基线：NasSimHub main e7128605b276ab0cad276f65c70cf29d9a20379c。
这是对当前代码和历史 PR 的复核，不能据此宣称线上设备已升级或真机已验收。
旧仓库 NasSimHub-old 与当前 NasSimHub 是不同仓库，历史清理状态不能混用。

原报告的“无死锁/无严重漏洞”等绝对结论超出静态审查能证明的范围。
下表将已实现修复、明确边界、迁移方案与外部待办分开；不因报告或 PR 合并而全项勾完成。

| 编号 | 当前处理结果 |
| --- | --- |
| P0-1 | 当前树已无所列交接文档，本轮加入索引快照凭据扫描和 CI 门禁。NAS 改密、历史 token 撤销需所有者确认，不伪造已完成。旧仓库历史不擅自 force-push。 |
| P0-2 | 当前树所列身份信息文档已移除；原始 SIM 标识不在本记录复现。历史清理与凭据撤销状态另计。 |
| P0-3 | 第一批已修复安装镜像命名空间；部署测试覆盖默认镜像与正式清单。 |
| P1-4 | 第二批已实现浏览器最长 90 天/闲置 30 天到期及会话列表 expires_at。iOS 配对的轮换是独立兼容迁移，见安全边界文档，未标完成。 |
| P1-5 | 原大二进制已不在跟踪树，hygiene 有大小门禁。历史体积与清理不是删除当前文件即可解决。 |
| P1-6 | 第二批已把 NKN 聊天读写/清空限制为管理员，前端入口同步限制。 |
| P1-7 | 已有 Go/agent/Web/部署 CI；本轮增加脱敏凭据扫描和前端生产构建。实际 Actions 是否成功以此次 PR 运行结果为准。 |
| P1-8 | api/web 已无 privileged；fnOS 保留 root 和明确能力是存储沙箱兼容需要。不能把“无 privileged”误写成“非 root”。转发器仍依赖 credentials.env，边界已记录。 |
| P1-9 | updater 的宿主级权限确实存在，已明确 TCB、socket proxy 局限和升级边界。完整权限架构迁移未实施。 |
| P2-10 | 已限制公开直连来源；本轮修复无法解析 RemoteAddr 时仍信任头的漏洞，并限制 nginx origin 元数据来自私有 tunnel 网络。默认不发布 API/origin 端口，信任 Compose 网络是部署前提。 |
| P2-11 | 本轮修复：重置必须有本地 256-bit 确认码加机器标识，远程 GET 不返回码。无效/缺失码不能写清空标记；健康安装仍不能重置。 |
| P2-12 | 第二批已使用参数化 viewer CTE，保留 hostile user_id 回归测试。原拼接在现有随机 ID 下并非已证实可利用 SQL 注入。 |
| P2-13 | 已给出发行/许可密钥分离和旧设备过渡方案；未接触私钥，未替换现有信任根。 |
| P2-14 | 已明确 Unix socket 组成员具有完整 SIM 权限。应用 token 需要 agent/API 协议同步升级，暂不强推。 |
| P2-15 | 本轮把 gitleaks 索引快照扫描接入 make check 与 CI；全历史扫描保留独立命令。日志强制脱敏。 |
| P2-16 | 已给出 iOS 每网关 SPKI pin、双 pin 轮换和可信配对方案，未冒称已完成原生实现/真机验收。 |
| P2-17 | 本轮把会话/JSON/超时/连接/读取/意外状态错误接入九种语言；重置本地确认也有九语言提示。 |
| P2-18 | 本轮 Vite 默认仅 127.0.0.1；LAN 预览通过 VITE_DEV_HOST 明确开启。 |
| P2-19 | 本轮六个代理 location 共享公共 include；Cloudflare scheme 白名单，公网直连不采信 origin 的 CF/IP 头，减少伪造及重定向环。JSON gzip 本身不是已成立的 BREACH 漏洞。 |
| P3-20/21/22/23 | 第一批已清理根目录一次性脚本、Claude outputs、init 和交接文档；保留 hygiene 防回归。 |
| P3-24 | 本轮旧构建/拉取/注入器改为 docs/archive/fnos-scripts 文本归档；新增 fail-fast、按仓库定位的 build.sh，仅构建不部署。rollback.sh 不再写死源码路径。 |
| P3-25 | SSH helper 原已要求环境变量；本轮移除 VoWiFi 探针默认 NAS/用户/网卡身份，要求显式配置；NKN 文档的本机地址改为文档示例。归档中历史镜像站地址不等同于家庭出口。 |
| P3-26 | 文档明确产品/镜像/兼容 module 与变量命名对应关系，暂不破坏已有部署进行全量改名。 |
| P3-27 | 清掉个人目录忽略条目；解释本地自签探活例外。正式 iOS bundle ID 依赖所有者 Apple 签名身份，未擅自选择。 |
| P1-28 | 第一批已给 AT 观察器每轮 30 秒期限；保持调用方 context 契约。超时不等同于健康面已覆盖全部 MM 卡死情形，真机故障注入未做。 |
| P1-29 | 第一批已增加订阅失败退避重试。本轮修复 ALSA 测试依赖宿主安装状态的问题，并覆盖存在/缺失两条路径。 |
| P2-30 | 第一批 updater 默认回环监听；容器覆盖显式指定内部监听地址。 |
| P2-31 | 安全文档明确 Go 1.26.5 正式链仅 Ed25519，ML-DSA 未启用；完整工具链迁移仍待实施。 |
| P3-32 | 文件较大是维护风险，不能据此认定逻辑错误。本轮新逻辑独立 reset_token.go/response.go；未为行数进行全项目重构。 |
| P3-33 | 锁文件 npm ci 和生产构建验证；文档说明 TS6/TS7 sidecar 分工、Node 24 要求和升级边界，保留已验证依赖。 |
| P3-34 | 本轮 AT 终止改为完整结果行、共享读循环，ERROR 作为失败；NKN 接收用一次总时限并处理消息通道关闭；panic 构造器移到测试文件。 |
| P3-35 | 本轮固定代理 runner 引用、串口零写入转 poll、NKN 重启等待及时响应 stop。 |

## 验证与剩余边界

本地验证（Linux Go 1.26.5、CGO_ENABLED=0；Node.js 24.22.1）：

- 主模块 `go test -p 2 -mod=readonly ./cmd/... ./internal/...` 全部通过；
  新增确认码/来源 IP/停止等待测试加入后，相关包再次通过；root/agent 的 vet 均通过。
- agent `go test -p 2 -mod=readonly ./...` 全部通过；
  测试构造器迁移后 cmd/modemdeck-agent、internal/modemmanager 再次通过。
- Web 契约测试 774 项全通过；vue-tsc、TS7 sidecar、eslint 通过；
  Vite 生产构建与 bundle budget 通过（安全更新后最大启动语言合计约 231.5 KiB gzip）。
- `deploy/tests/run.sh` 的五组测试全通过；使用真实 Compose 5.7.1 的配置解析，
  没有 Docker daemon，也未执行容器构建/启动。
- 真实 nginx 1.28.3 校验完整代理配置通过；五个回环 HTTP 用例验证公开来源伪造头
  被忽略、合法 tunnel scheme 生效、非法 scheme 回落。
- 仓库 hygiene、git diff --check、构建/回滚/扫描脚本语法检查通过。
- gitleaks 8.30.1 当前索引快照无告警；当前仓库历史扫描覆盖约 19.37 MB、
  623 个有差异提交。最初七项均核实为固定测试向量/公开示例，按具体文件与具体
  固定值白名单后无告警。未忽略整个测试目录；此结果不证明旧仓库或所有形式的
  SIM 身份信息均已清理，也不能代替密码/Token 撤销。

Windows 原生 Go 不能编译本项目的 Linux unix.Open/ALSA 路径，
因此后端验收使用 WSL Ubuntu，并非将 Windows 编译失败计作代码缺陷。

CI / PR 的实际运行与合并状态以对应 GitHub 页面为准，不以本地测试替代。
复核时 main 的 CI run 41486472653 三个 job 都没有执行步骤，GitHub 注释明确为
账户付款失败或支出限额不足；这不是测试执行后报出的代码失败，也没有修改账单设置。
本轮不向真实 SIM 拨号/发短信，不更换语音驱动，不清空 NAS 数据，不切换线上镜像。

## 推送时新增的依赖告警

GitHub 另报 36 条依赖告警（23 high、10 moderate、3 low），不是原报告的全部内容。
已按具体依赖补修：

- Vue 3.5.45、vue-i18n 11.4.18、Vite 8.3.8；更新锁文件中的 nanoid、
  source-map-js、postcss-selector-parser、brace-expansion 等安全版本。
- ESLint/TypeScript-ESLint 上游尚约束有漏洞的 minimatch 版本；只对开发工具显式
  override minimatch 11.0.8，保留 ESLint 10，避免 npm --force 降级整套 lint。
  Node 24 下 lint、双类型检查、774 契约、生产构建和预算全部通过。
- iOS 构建工具更新 xmldom 0.9.12、glob 14.0.5、minimatch 11.0.8、
  brace-expansion 5.0.13；保留 Capacitor 8.5.3。JavaScript 契约 45 项通过，
  不代表已完成 Mac/Xcode 打包或 iPhone 真机验收。
- Go gorilla/websocket 1.4.2 → 1.5.3；全部主模块测试再次通过。

Web/iOS 的 npm audit 均为 0 告警；GitHub Dependabot 的关闭状态需要合并后索引刷新，
不能把本地 npm 结果直接写成远端告警已经关闭。CI 补上 iOS 契约测试。

需要所有者确认的收尾：

1. NAS 密码是否已更换，曾暴露的 GitHub token 是否已撤销。只确认状态，不粘贴凭据。
2. 旧仓库的凭据/SIM 历史是否仍保留；若需重写，应先协调全部协作端并准备恢复副本。
3. 发行密钥分离、iOS 证书锁定/凭据轮换及正式签名身份，需要按方案独立迁移验收。

这些保留项未宣称修复完成。具体方案见 [安全边界](../SECURITY-BOUNDARIES.md)。
