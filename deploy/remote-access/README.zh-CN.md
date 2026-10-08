# NasSimHub 私有远程访问

NAS 与外部电脑登录同一 Tailscale 网络。此目录不含账户、认证密钥或设备状态。

## NAS

`compose.tailscale.yml` 使用官方镜像的固定摘要，持久保存登录状态，使用内核网络。
设置 `NASSIMHUB_ORIGIN_CA` 为 NAS 网页的公开 CA 证书路径，再启动 Compose。
状态目录应仅管理员可读；不要提交或发布 `state/`。

启动后执行 `docker exec -d nassimhub-tailscale tailscale up --hostname=nassimhub-nas --accept-dns=false --accept-routes=false`，
从 `tailscale status --json` 中读取 AuthURL，由用户本人打开授权链接。
直接运行持久化 daemon，避免默认 containerboot 等待交互登录超时并反复重启、使授权链接失效。
不启用 exit node、子网路由或 Tailscale SSH；不修改 NAS DNS。

授权后使用 `tailscale serve --bg --https=8443 https://127.0.0.1:7577`。
需在账户中启用 HTTPS（如 CLI 提示）。上游证书经挂载的 CA 校验，不能跳过校验。
外部电脑连接 Tailscale 后，打开 CLI 返回的 HTTPS 地址。
端口 8443 避免与 NAS 自带 443 服务冲突。

## 通话

HTTPS 网页访问和音频连接需分别验收。
将 NAS 的 Tailscale IPv4 加入 `MODEMDECK_CALL_MEDIA_PUBLIC_IPS`，保留现有 LAN 地址；
API 仍使用单一 UDP 7881 映射。备份环境文件后仅重建 API 容器。
设置是本机部署数据，不把个人 IP 写死到发布源码。

实测顺序：登录网页、确认线路在线、确认短信中文正常、接听电话、检查 ICE 建连、
双向讲话、挂断后确认媒体和呼叫清理。使用耳机；音箱回放对端声音不能证明真实上行正常。

## 回滚

恢复备份的 API 环境文件并重建 API 容器；关闭本 Compose 项目。
保留私有状态便于恢复，若要删除设备可由用户在 Tailscale 管理页完成。

参考：[Docker 参数](https://tailscale.com/docs/features/containers/docker/docker-params)、
[Serve](https://tailscale.com/docs/reference/tailscale-cli/serve)。
