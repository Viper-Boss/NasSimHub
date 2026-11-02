# NAS 网关 Web 认证/会话安全审计（r128）

范围：`internal/httpapi`、`internal/auth`，以及它们依赖的 `internal/store`（会话/用户）、`internal/diagnostics`（日志脱敏）。
对象是 **NAS 网关自己的 Web 后台**，不是 UFI003 棒子上的本地后台。

方法：先读完整请求链路，再写反向测试驱动真实的 `http.Handler`（`API.ServeHTTP`）＋真实 SQLite 库＋真实 `auth.Service`。
所有结论都是**离线测试**（offline-tested）得出，没有任何硬件验证，也没有浏览器验证。

状态用语：已修复 = 本次改了代码并有“去掉修复就失败”的测试；未改 = 属于设计取舍，只记录并给建议。

---

## 1. 各项控制：在哪里实现、保证什么、不保证什么

### 1.1 路由与鉴权闸门
- 实现：`api.go: API.ServeHTTP`。先按字面路径放行**无需登录**的路由，然后 `auth.go: authorizeAPI`，然后 `users.go: adminOnlyAPIPath` + `requireAdmin`，最后进入各 handler。
- 无需登录即可到达：`/health*`、`/version`、`/mobile/tunnel/verify`、`/session`（GET/POST）、`/setup`、`/provision`、`/provision/reset`、`/mobile/enrollment/exchange`。
- 保证：除上述以外的所有路径（包括不存在的路径）无会话一律 401 —— `TestAdversarialEveryProtectedRouteNeedsASession`。
- 不保证：`adminOnlyAPIPath` 是**按路径前缀的名单**，新路由默认是“成员可达”。为此加了路由表测试：`api.go` 里每出现一个新的字面路由，必须在测试表 `advStaticRoutes` 里声明受众，否则 `TestAdversarialEveryStaticRouteHasADeclaredAudience` 失败。

### 1.2 会话
- 实现：`auth/service.go: Login/loginUser`（生成 32 字节随机会话令牌与 CSRF 令牌）、`Authenticate`、`Logout`；存储在 `store/auth.go`（表 `modemdeck_auth_sessions`，**只存 SHA-256 摘要**）。
- Cookie（`auth.go: setAuthCookieValues`）：`modemdeck_session`（HttpOnly）与 `modemdeck_csrf`（JS 可读）；两者 `SameSite=Strict`、`Path=/`、无 Domain、`Max-Age` 400 天；`Secure` 取决于 `Options.SecureCookies`。—— `TestAdversarialCookieAttributes`
- 保证：
  - 令牌格式在查库之前校验（`auth/token.go: validOpaqueToken`，严格 base64url、规范编码）；畸形/篡改的 Cookie 一律 401，不触库、不 500。—— `TestAdversarialTamperedAndMalformedSessionCookies`、`TestAdversarialMalformedSessionTokensNeverReachTheStore`
  - 每个请求都从库里**重新读取** principal（角色、线路权限）（`store/auth.go: UserSessionByTokenDigest`，JOIN `enabled = 1` 的用户），不缓存在会话里。—— `TestAdversarialRoleDowngradeTakesEffectOnTheNextRequest`、`TestAdversarialLineAccessRemovalTakesEffectOnTheNextRequest`、`TestAdversarialPrincipalIsReadFreshOnEveryAuthentication`
  - 登录不会沿用请求里带来的 Cookie 值（防会话固定）。—— `TestAdversarialLoginNeverAdoptsAPresentedSessionToken`
  - 每个用户最多 8 个会话，第 9 次登录淘汰该用户最旧的一个（`store/auth.go: maxSessionsPerUser`）。—— `TestAdversarialNinthLoginEvictsOnlyTheSameUsersOldestSession`
- 会话有服务端有效期：登录后最多 90 天、最后一次使用后 30 天，先到为准（见 A-07，2026-11 修复）；会话不绑定 IP/UA（`auth/principal.go` 注释明确说明元数据“不得用作认证绑定”）。

### 1.3 会话吊销
| 触发 | 实现 | 效果 | 测试 |
|---|---|---|---|
| 退出登录 | `auth.go: logout` → `Service.Logout` | 仅当前会话 | `TestAdversarialSessionDiesOnLogout` |
| 自己改密码 | `auth.go: accountPassword` → `store: ReplaceUserPasswordHashIfCurrentAndRevokeSessions` | 该用户全部 Web 会话＋全部 iOS 配对凭据，同一事务 | `TestAdversarialOwnPasswordChangeEndsEveryOwnSessionAndOnlyThose` |
| 管理员重置成员密码 | `users.go: userResource` → `store: SetMemberPassword` | 同上 | `TestAdversarialAdministratorPasswordResetEndsTheMembersSessions`、`TestAdversarialUpdateWithNewPasswordEndsTheMembersSessions` |
| 管理员停用成员 | `store/users.go: UpdateMember` | 会话行被删除；重新启用后旧 Cookie 仍然无效 | `TestAdversarialDisablingAMemberEndsItsSessionsForGood` |
| 吊销单个会话 | `account_sessions.go: accountSessionResource` → `Service.RevokeWebSession` | 只能吊销自己的、且不能是当前会话 | `TestAdversarialRevokingOneSessionEndsExactlyThatOne` |
| “退出其他设备” | `account_sessions.go: revokeOtherAccountSessions` | 自己的其他 Web 会话＋iOS 凭据 | `TestAdversarialRevokingOthersKeepsOnlyTheCaller` |
| 用户行被删除 | 表上级联删除＋认证时 JOIN 用户 | 会话与 Bearer 凭据失效 | `TestAdversarialDeletedUserSessionIsDead` |
| 并发吊销与使用 | — | 只出现 200/401，吊销后不再成功 | `TestAdversarialConcurrentRevocationAndUse` |
| 并发改密码 | 条件更新（比较旧哈希） | 恰好一个成功 | `TestAdversarialConcurrentPasswordChangeHasOneWinner` |

说明（事实，不是缺陷）：API **没有**修改角色的接口（唯一索引 `ux_modemdeck_single_admin` 保证只有一个管理员），也**没有**删除用户的接口。“降级”“删除用户”两项因此只能直接改库来验证，测试里就是这么做的。

SSE 流：`event_stream_auth.go: authorizeEventStream` 建流时认证，之后每 15 秒（`eventStreamAuthenticationInterval`）用 `currentStreamAccess` 重新认证；`event_stream_limiter.go` 限制每会话 6 条、全局 128 条。会话被吊销后流会结束 —— `TestAdversarialLogStreamEndsWhenItsSessionIsRevoked`。`/diagnostics/logs/stream` 额外要求管理员。

### 1.4 CSRF
- 实现：`auth.go: authorizeAPI`。非 GET/HEAD/OPTIONS 请求必须带请求头 `X-ModemDeck-CSRF`，其 SHA-256 必须等于**该会话行里保存的 CSRF 摘要**（`auth/service.go: Authentication.VerifyCSRF`，常数时间比较）。
- 性质：这是**绑定会话的同步令牌**，不是单纯的“双重提交”。服务器从不比较 Cookie 与请求头；`modemdeck_csrf` Cookie 只是让前端拿到令牌。因此“只有 Cookie 没有请求头”被拒绝，“有正确请求头没有 Cookie”被接受，“伪造一对相同的 Cookie+请求头”被拒绝。—— `TestAdversarialCSRFHeaderIsCheckedAgainstTheSessionNotTheCookie`
- 保证：所有受保护路由 × POST/PUT/PATCH/DELETE × 8 种伪造方式（无头、空头、畸形、同用户另一会话的令牌、他人令牌、把会话令牌当 CSRF、只放查询串……）全部 403 `csrf_failed`，且无副作用。—— `TestAdversarialEveryWriteOnEveryRouteNeedsTheSessionsOwnCSRFToken`、`TestAdversarialForgedWriteHasNoEffect`
- **全部豁免项**：
  1. GET/HEAD/OPTIONS（不改状态；不支持 method override）—— `TestAdversarialReadsAreNotWritesInDisguise`
  2. 带 `Authorization` 头的请求走 Bearer 路径（`mobile_auth.go: authorizeMobileAPI`），不查 CSRF；但此时 **Cookie 完全不参与认证**，所以不能用“Cookie＋假 Authorization”绕过 —— `TestAdversarialMobileBearerIsScopedLikeItsUser`
  3. 登录前的写接口：`POST /session`、`POST /setup`、`POST /provision/reset`、`POST /mobile/enrollment/exchange`，各有自己的前置条件 —— `TestAdversarialUnauthenticatedWritesAreInert`
- 辅助防线：请求体必须是 `application/json`（`query.go: decodeJSONBodyWithLimit`），跨站表单发不出 —— `TestAdversarialNonJSONBodiesAreRefusedEvenWithAValidToken`；不提供任何 CORS 响应头 —— `TestAdversarialNoCrossOriginResourceSharingIsOffered`。

### 1.5 Origin / Referer / Host
- 事实：网关**不检查** `Origin`、`Referer`、`Host`、`X-Forwarded-Host`、`Sec-Fetch-Site`。带有效令牌的写请求无论这些头是什么都被接受；不带令牌的无论这些头是什么都被拒绝。—— `TestAdversarialOriginRefererAndHostAreNotInputsToAuthorization`（覆盖直连、自带 nginx、Cloudflare、Tailscale Serve 四种访问形态及伪造 Origin、`Origin: null`、Host 不一致）
- 唯一读取转发头的地方与授权无关：`call_media.go: callMediaRequiresRelay` 用 `X-Forwarded-Host`（缺省时用 `Host`）以及 `CF-Ray`/`CF-Connecting-IP` 判断通话媒体是否必须走 TURN 中继。它只在**已通过认证**的请求上影响 ICE 策略，伪造这些头只能让自己的通话多走一次中继，不能获得任何权限。
- 评估：现有三层（会话绑定令牌＋`SameSite=Strict`＋仅 JSON）对浏览器跨站请求是足够的。**本次没有添加 Origin 检查**：`main.go` 直接把 `API` 作为 `http.Server.Handler`，网关没有“可信代理”名单，无法区分应当相信的 `X-Forwarded-Host` 与直连客户端自己写的；按某一种访问方式写死的检查会把通过另一种方式访问的机主锁在外面。见 A-09。

### 1.6 登录限流
- 实现：`login_limiter.go: loginFailureLimiter`，调用点 `auth.go: login`。
- **按账号名**限流（去空白、ASCII 转小写后取哈希），另有一个**全局**计数；**不按客户端地址**（代码注释说明：无法验证转发头）。
- 参数：5 分钟窗口；单账号 5 次失败后指数退避 1s→60s；全局 20 次失败后同样退避；账号表上限 1024 项；另有 `login_history.go` 的 1440 个分钟桶（固定内存）。
- 保证：
  - 锁定后正确密码与错误密码得到完全相同的 429 —— `TestAdversarialLoginLockoutEngagesPerAccountAndHidesThePasswordVerdict`
  - 改大小写、加首尾空白、换 `X-Forwarded-For`/`X-Real-IP`/`X-ModemDeck-Client-IP`/`CF-Connecting-IP`/UA 都绕不过 —— `TestAdversarialLoginLockoutCannotBeSidestepped`
  - 用别的账号登录成功不会清零 —— `TestAdversarialAnotherAccountsSuccessDoesNotResetTheCounter`
  - 不存在的账号同样被限流、同样的 401（不泄露用户名是否存在）—— `TestAdversarialUnknownAccountsAreThrottledLikeRealOnes`、`TestAdversarialLoginRefusalsAreIndistinguishable`
  - **会恢复**：退避到期放行一次、窗口过后清零、成功登录清零本账号 —— `TestAdversarialLoginLockoutRecovers`（用已有的时钟接缝 `loginFailureLimiter.now` 注入时钟，未改产品代码）
  - 内存有界 —— `TestAdversarialLimiterMemoryIsBoundedUnderAFlood`
  - 并发闸门（`loginSlots`，2 个）返回的 `login_busy` 不计为失败 —— `TestAdversarialBusyLoginGateIsNotCountedAsAFailure`
- 不保证：见 A-05、A-06、A-13、A-14。

### 1.7 角色与数据范围
- 模型：`auth/principal.go`（`RoleAdmin`/`RoleMember`、`AllowedLineIDs`、`CanAccessLine`）。
- 线路范围：handler 侧 `access.go: requireLineAccess/requireCallAccess`，存储侧 `store/access.go: principalLineScope`。联系人按**所有者**隔离（`store/access.go: contactOwnerSQL`），管理员也看不到成员的通讯录。
- 低权限：成员（包括一条线路都没有的成员）访问每一条管理员路由、每一种方法都是 403 `admin_required` —— `TestAdversarialMemberIsRefusedOnEveryAdministratorRoute`（含 r127 新增的 `/nodes/**`、`/security/trust/**`、`/security/journal/**`、`/stats/communication?node_id=`）。
- 跨用户：成员 A 的任何列表里都不出现成员 B 的数据 —— `TestAdversarialMemberCannotSeeAnotherMembersData`；36 条带标识符的路由上，“别人的资源”与“不存在的资源”返回**逐字节相同**的 404 —— `TestAdversarialForeignIdentifiersAreIndistinguishableFromMissingOnes`；批量接口不影响他人数据 —— `TestAdversarialBatchRoutesIgnoreForeignRows`；账号设置互不影响 —— `TestAdversarialAccountSettingsAreNotShared`。
- 手机 Bearer 凭据继承其用户的角色与线路范围 —— `TestAdversarialMobileBearerIsScopedLikeItsUser`。

### 1.8 重复提交
- 拨号：`communications.go: startCall` 先由 `calllease.Manager.ReserveOutgoingFor` 按（request id、线路、会话）占位，只有**创建**占位的那个请求可以下发；并发重试得到 409 `call_request_in_progress`。—— `TestAdversarialConcurrentDuplicateCallStartDialsOnce`（真实 `calllease.Manager`，12 个并发重试，拨号恰好 1 次）
- 短信/通话命令：HTTP 层不去重，只负责把**同一个** request id 原样交给 `communication.Service`；去重在 `communication/service.go: beginCommand` → `store/hardware_commands.go: BeginHardwareCommand`（原子插入）。—— `TestAdversarialDuplicateMessageSubmissionsCarryOneRequestIdentity`（HTTP 层）＋ `TestAdversarialConcurrentDuplicateCommandIsClaimedExactlyOnce`（真实库，32 个并发，恰好 1 个拿到命令）
- 说明：HTTP 层的短信测试用的是计数用的假服务，因此“只发一次”的结论来自存储层那条测试，而不是来自一次端到端的发送。通话/短信代码**没有改动**。
- 并发相同登录：各自得到独立会话，无共享令牌 —— `TestAdversarialConcurrentIdenticalLoginsAreIndependentSessions`

### 1.9 日志脱敏
- 实现：`diagnostics/logs.go`。按**键名**脱敏（`sensitiveKey`）＋ r128 新增按**值的形状**脱敏（`ScrubSecrets`）。
- 登录失败不记录任何内容（`auth.go: login` 对 `ErrInvalidCredentials` 不打日志）；内部错误只记录 `method`、`path`（不含查询串）、`error`（`api.go: writeInternalError`）。
- 端到端：把假密码、会话令牌、CSRF 令牌、Bearer 凭据、Bark key、Webhook 路径密钥与 access_token、钉钉签名密钥、F50 密码、Telegram bot token、假手机号、短信验证码经登录失败、被拒请求、设置保存、不可达的推送通道、来短信转发送进去，然后在进程日志、`/diagnostics/logs`、`/logs/download`、`/logs/stream` 以及全部错误响应里查找，均不出现 —— `TestAdversarialPlantedSecretsNeverReachALogOrAnErrorResponse`
- 不保证（`TestAdversarialScrubberLimitIsDocumented`）：值里没有 URL/令牌/请求头形状的秘密识别不了；URL 路径里短于 16 个字符的段会保留。真正的防线是“不要把它交给日志”。
- NAS 没有 Wi-Fi 设置项，因此不存在携带 PSK 的请求；PSK 形状的值是在日志层用键名 `psk`/`passphrase` 植入验证的（`TestAdversarialBothLogSinksAreRedacted`）。

---

## 2. 发现

| 编号 | 级别 | 位置 | 状态 | 复现测试 |
|---|---|---|---|---|
| A-01 | 高 | `httpapi/contact_backup.go` | 已修复 | `TestAdversarialMemberCannotSeeAnotherMembersData`、`TestAdversarialUnownedLegacyBackupIsAdministratorOnly` |
| A-02 | 高 | `httpapi/users.go: adminOnlyAPIPath`、`homesim_f50.go` | 已修复 | `TestAdversarialMemberIsRefusedOnEveryAdministratorRoute` |
| A-03 | 高 | `httpapi/auth.go: authorizeAPI`、`provisioning.go` | 已修复 | `TestAdversarialCloneRefusesInheritedSessionsAndBearerTokens` |
| A-04 | 中 | `diagnostics/logs.go`、`barkpush`、`homesim_forward.go`、`homesim_bark.go` | 已修复 | `TestAdversarialBothLogSinksAreRedacted`、`TestAdversarialPlantedSecretsNeverReachALogOrAnErrorResponse` |
| A-05 | 中 | `httpapi/login_limiter.go`（全局计数） | 未改（设计） | `TestAdversarialGlobalThrottleLocksEveryAccountAndRecovers` |
| A-07 | 中 | `auth/service.go: Authenticate` | 已修（2026-11，P1-4） | `TestAdversarialSessionExpiresOnTheServer`、`TestAdversarialMultiUserSessionExpires` |
| A-11 | 中 | `httpapi/homesim_chat_nkn.go` | 已修（2026-11，P1-6） | `TestAdversarialEveryStaticRouteHasADeclaredAudience`、`TestNKNChatIsAdministratorOnly` |
| A-06 | 低 | `login_limiter.go: evictOldestAccount` | 未改 | `TestAdversarialEvictingAVictimsCounterCostsMoreThanItBuys` |
| A-08 | 低 | `auth.go: getSession` | 未改（设计） | `TestAdversarialSessionProbeWithoutTheCSRFCookieRevokesTheSession` |
| A-09 | 信息 | `auth.go: authorizeAPI` | 未改（设计） | `TestAdversarialOriginRefererAndHostAreNotInputsToAuthorization` |
| A-10 | 低 | `auth.go: requestClientIP` | 已修（2026-11，P2-10） | `TestAdversarialSessionAccessIPIsClientSuppliedAndNeverAuthoritative`、`TestRequestSessionClientIgnoresRelayHeadersFromAPublicPeer` |
| A-12 | 低 | `system_metrics.go`、`vowifi_diagnostics.go`、`contact_backup.go` | 未改 | `TestAdversarialMemberRoutesAreNotMistakenForAdministratorRoutes` |
| A-13 | 低 | `login_limiter.go: activeState` | 未改 | `TestAdversarialWindowExpiryEndsAnActiveBlockEarly` |
| A-14 | 低 | `auth.go: accountPassword` | 未改 | `TestAdversarialPasswordChangeHasNoFailureLimiter` |
| B-01 | — | `httpapi/homesim_tunnel_lifecycle_test.go`（测试替身） | 已修复 | `TestAnIsolatedDHTIsNotRestartedInALoop`（`-race`） |

### A-01（高，已修复）联系人备份跨用户可读
- 问题：联系人按所有者隔离，但备份文件写在共享目录；`listContactBackups` 与 `readContactBackup` 把目录里**所有**文件提供给任何已登录账号。成员 B 备份一次，成员 A 就能在列表里看到文件名并下载 B 的整本通讯录。
- 修复：写入时把所有者 `owner_user_id` 作为文件**最前面的字节**写入；列表与读取只放行本人的文件（按字节前缀比较，不解析，联系人内容无法伪造）。读取他人备份返回与“文件不存在”相同的 404。
- 兼容：没有所有者标记的旧文件只对管理员可见（无法判断归属；管理员本来就拥有该目录）。JSON 仅新增一个字段，前端 `contactBackups.ts` 只读取 `format` 与 `contacts`，不受影响。

### A-02（高，已修复）F50 设备接口对成员开放
- 问题：`/api/v1/homesim/f50/{status,sms,sms/send,sms/read}` 不在 `adminOnlyAPIPath` 名单里，handler 里也没有任何检查。任何成员（包括没有任何线路的成员）可以读取 F50 上的全部短信并用它发短信。
- 修复：这组路径归入管理员专属。依据：F50 不是“线路”，没有 line_id 可用于成员授权；`access.go: canAccessLine` 对“不属于任何线路的资源”已有先例（跟随管理员）；前端这部分本来就在管理员专属的设置页（`settingsNavigation.ts: ADMIN_ONLY_SECTIONS`）。

### A-03（高，已修复）克隆的数据目录仍然接受继承来的会话与 Bearer 凭据
- 问题：判定为克隆后，`login` 拒绝继承来的密码，`api.go` 的注释也写着“克隆的网关拒绝所有会话”，但 `authorizeAPI` 并没有检查。制作镜像的人手里已有的会话 Cookie 或手机 Bearer 凭据在每一台复制出去的网关上依然有效。
- 修复：`authorizeAPI` 开头检查 `api.cloned()`，返回 409 `provision_required`，在读取任何凭据之前（不泄露凭据是否有效）。修复克隆所需的 `/session`、`/provision`、`/provision/reset`、`/health*` 都在闸门之前，不受影响。

### A-04（中，已修复）日志中的密钥
两个问题，同一处修复：
1. **只有诊断缓冲区脱敏，进程日志不脱敏。** `logHandler.Handle` 把脱敏后的字段写进缓冲区，却把原始记录交给下一个 handler（生产中是 stdout，即 `docker logs`）。现在两处使用同一份脱敏结果。
2. **按键名脱敏看不到值里的密钥。** `net/http` 的错误文本包含请求 URL，而推送通道的 URL 本身就是凭据：Bark device key、Server酱 SendKey、Telegram bot token 在路径里，钉钉/企业微信 token 在查询串里。`barkpush` 的 `"bark push failed"` 与 `homesim_forward.go` 的 `"SMS forward failed"` 每次网络失败都会把凭据写进日志。新增 `diagnostics.ScrubSecrets`：保留协议、主机、短路径段，去掉 userinfo、查询串、片段以及长度 ≥16 的路径段；另识别 Telegram token 形状与 `Bearer …`。
- 同样的文本还出现在两个**错误响应**里（`forwarders/test` 与 `bark/test` 的 502），其中 Bark key/SendKey 来自服务器环境变量、界面上从不显示；这两处也改用 `ScrubSecrets`。
- 敏感键名补充：`psk`、`passphrase`、`puk`、`otp`、`csrf`、`apikey`。
- **行为变化（请知悉）**：stdout 日志现在与诊断页一样脱敏，此前在 `docker logs` 里可见的号码、短信内容等敏感键值将显示为 `[redacted]`；URL 中的长路径段（例如带版本号的长文件名）也会显示为 `[redacted]`。

### A-05（中，未改）全局登录限流可被用来拒绝所有人登录
- 事实：任何未认证客户端对任意用户名制造 20 次失败，**所有账号**（包括管理员用正确密码）进入退避；之后每分钟一次失败请求即可无限维持。已有会话不受影响。
- 为什么不改：限流器刻意不看客户端地址，因为 API 没有可信代理名单，转发头不可信（见 `login_limiter.go` 注释）。全局计数是这一取舍的代价，去掉它就没有对“撒用户名”式猜测的任何限制。
- 建议：引入“可信代理”配置（仅当直连对端是配置的代理时才采信 `X-ModemDeck-Client-IP`），然后把全局计数换成按客户端地址计数；在此之前由边缘（Cloudflare/nginx）做 IP 限速。

### A-07（中，2026-11 已修，审查 P1-4）会话没有服务端有效期
- 事实：`Authenticate` 不比较任何时间；Cookie 400 天且每次 `GET /session` 续期。会话只因退出、吊销、改密码、停用账号或被第 9 个会话挤掉而结束。这是既有的明确设计（已有测试 `auth: TestAuthenticateHasNoTimeExpiry`）。
- 风险：被盗的 Cookie 永久有效，直到机主主动处理。
- 建议：增加空闲超时（例如 30–90 天未使用）与绝对上限；`last_seen_at_unix` 已经在维护（`sessionMetadataRefresh` 5 分钟粒度），实现成本低。属于产品行为变化，需机主决定。
- 修复：`Authenticate` 按 `MODEMDECK_SESSION_MAX_AGE`（默认 90d）与 `MODEMDECK_SESSION_IDLE_TIMEOUT`（默认 30d）判定，过期即按吊销处理（删行、清 Cookie）；会话列表返回 `expires_at`。测试：`auth: TestAuthenticateEnforcesSessionLifetime`、`TestAdversarialMultiUserSessionExpires`，`httpapi: TestAdversarialSessionExpiresOnTheServer`、`TestAdversarialIdleSessionExpiresBeforeItsMaximumAge`。iOS 配对凭据仍无有效期（可在设备列表吊销）。

### A-11（中，2026-11 已修，审查 P1-6）nMobile（NKN）聊天对所有成员开放
- 事实：`/api/v1/nkn/chat/{messages,send,clear,peers}` 没有角色或线路检查；任何成员都能读取、以网关身份发送、清空聊天记录。前端的入口也对所有角色显示（`AppShell.vue`），所以判断为有意的共享信箱，未改。
- 建议：若聊天应属于机主，把 `/api/v1/nkn/chat/` 加入 `adminOnlyAPIPath` 并隐藏成员的入口；一行改动。
- 修复：按此建议执行——聊天只有一个网关 NKN 身份，无法按成员拆分收件，故限定管理员；成员入口与路由隐藏。

### 低/信息
- **A-06**：账号表满 1024 项时淘汰最久未失败的账号，可借此清掉某个账号的计数；但需要约 1024 次失败登录，在全局限流下要数小时，换回 5 次猜测，不构成实际绕过。
- **A-08**：`GET /session` 发现会话 Cookie 没有匹配的 CSRF Cookie 时会吊销该会话（失败即关闭）。能删除/覆盖 `modemdeck_csrf` 的一方可以让用户掉线，仅此而已；探测接口不会把正确的 CSRF 令牌发给没有它的人。
- **A-09**：不检查 Origin/Referer/Host，见 1.5。
- **A-10**：会话列表里的“访问 IP”取自 `X-ModemDeck-Client-IP`/`X-Real-IP`。自带的 `web/nginx.conf` 会覆盖这两个头，经它访问时可信；直连 API 端口的客户端可以随意填写。该值仅用于展示，不参与任何授权。`X-Forwarded-For` 完全不被读取。建议同 A-05（可信代理配置）。**2026-11 已修（审查 P2-10）**：仅当直连对端是回环或私网地址（Compose 网络里的 nginx）时才采信这两个头，公网对端一律记录其真实地址。
- **A-12**：成员可读 `/system-metrics`（主机负载、网卡流量）、`/vowifi-diagnostics`（出口公网 IP、运营商）；`/contacts/backup` 的响应包含服务器上的绝对目录路径。信息泄露面很小，仪表盘在用，未改。
- **A-13**：退避超过 5 分钟窗口时，窗口到期会连同 `blockedUntil` 一起清空，封锁最多提前 60 秒结束。
- **A-14**：`PUT /account/password` 校验 `current_password` 没有失败计数。需要有效会话＋CSRF 令牌，不是入口；但持有被盗会话的人可以按 2 并发的速度猜密码。建议复用 `loginFailures`（按用户 ID 计）。
- **其他观察（信息）**：
  - `POST /setup` 在未初始化时无需认证，先到先得（首次启动的既定流程）；已初始化后返回 409。
  - `POST /provision/reset` 无需认证，仅在判定为克隆时可用，确认值是 `GET /provision` 公开返回的机器 ID —— 它防误触，不防局域网内的恶意请求（代码注释已说明）。
  - `POST /mobile/enrollment/exchange` 无需认证、无限流；邀请码 256 位随机、5 分钟有效、一次性、最多 128 个，RSA 公钥在查邀请码之前解析（纯 CPU，无状态副作用）。
  - 手机 Bearer 凭据没有有效期，随改密码、停用、关闭配对权限或手动吊销而失效；管理员的手机凭据拥有管理员权限。
  - 更新器令牌（`MODEMDECK_UPDATER_TOKEN_FILE`，`cmd/modemdeck/main.go` → `updaterclient/client.go`）是 NAS **发往**更新器的出站 Bearer 凭据，HTTP API 没有任何入口接受它；触发更新的 `/api/v1/about/update*` 是管理员专属路由（见 `TestAdversarialMemberIsRefusedOnEveryAdministratorRoute`）。
  - `auth.VerifyPassword` 接受库里任何不超过上限的 Argon2 成本参数（包括很低的）。哈希只来自本服务写入，不构成外部可利用面。

### 未在上文逐条引用的补充测试
- `auth`：`TestAdversarialCSRFTokenBelongsToExactlyOneSession`、`TestAdversarialSessionManagementStaysInsideTheCallersAccount`（成员 A 无法列出/吊销成员 B 的会话）、`TestAdversarialPasswordChangeIsScopedToTheAuthenticatedUser`、`TestAdversarialSessionClientMetadataIsSanitisedAndNonAuthoritative`
- `httpapi`：`TestAdversarialPasswordChangeNeedsTheCurrentPassword`
- `diagnostics`：`TestAdversarialScrubSecrets`、`TestAdversarialExternalEntriesAreScrubbed`

每一处修复都做过反向验证（r128 复核时逐个去掉修复、确认对应测试失败、再恢复）：A-01、A-02、A-03、A-04 的三个改动点均如此。

---

## 3. B：`TestAnIsolatedDHTIsNotRestartedInALoop` 的 `-race` 失败

结论：**竞态在我们自己的测试替身里**，不在产品代码里，也不在第三方模块里。

竞态报告（基线，节选）：
```
WARNING: DATA RACE
Write at 0x00c000131290 by goroutine 13:
  httpapi.(*fakeDHTService).ApplyDHT()          homesim_tunnel_lifecycle_test.go:34
  httpapi.(*API).dhtRuntimeStatus()             homesim_dht.go:227
  httpapi.(*API).StartHomeSIMTunnelRestore.func1.2()  homesim_tunnel_lifecycle.go:85
Previous read at 0x00c000131290 by goroutine 12:
  httpapi.TestAnIsolatedDHTIsNotRestartedInALoop.func1()  homesim_tunnel_lifecycle_test.go:204
  httpapi.waitForApply()                        homesim_tunnel_lifecycle_test.go:101
```
- 两次访问：恢复协程在 `fakeDHTService.ApplyDHT` 里 `append` 到 `applied`；测试协程在 `waitForApply` 里轮询 `len(dht.applied)`。没有任何同步。
- 报告里的 `google.golang.org/protobuf/types/gofeaturespb` 与 `nkn/pb` 只出现在 “Goroutine 12 created at” 的**创建栈**里（测试主协程经 `runtime.main` 的包初始化链被创建），不是访问点。libp2p/DHT 库没有参与：这个测试用的是假 DHT。
- 复核：把 `baseline128` 导出到临时目录后执行 `go test -race ./internal/httpapi/ -run TestAnIsolatedDHTIsNotRestartedInALoop -count=3`，得到与上面相同的两处访问；修复后同一命令通过，`go test -race ./internal/httpapi/ -count=1` 全量通过。
- 修复：给 `fakeDHTService` 与同样写法的 `fakeWireGuardService` 加互斥锁，测试通过 `appliedConfigs()`/`readCount()` 读取副本。断言一条未删、未放宽，没有 `t.Skip`，没有 build tag。产品代码未动。

---

## 4. 没有验证的部分
- 全部为离线测试；没有在真实设备、真实浏览器、真实 Cloudflare/Tailscale 链路上验证。1.5 里的四种访问形态是按各自的请求头构造的请求，不是真实链路。
- Bark/Webhook/Telegram 的密钥泄露是用不可达地址（`127.0.0.1:1`）与构造的 `*url.Error` 复现的，没有连接真实服务；Telegram 一项使用的是假的设置服务返回的错误。
- 短信“只发一次”在 HTTP 层没有端到端验证（见 1.8）。
- `barkpush` 的日志经由同一个根 logger 输出，修复在 handler 层生效；没有单独为 `barkpush` 包写测试。
- 录音、网络代理、Telegram 单元等可选服务在跨用户测试里用的是既有的测试替身，验证的是 handler 的访问检查，不是这些服务自身的实现。
