# WireGuard 服务端基础设施（飞牛 NAS / OECT-480）

状态（2026-09-08）：**内核与用户态工具已就绪，尚未接入 compose，隧道未启动**。

- 内核：6.18.18 aarch64，`/dev/net/tun` 存在，`wireguard.ko` 可用
- 已安装 `wireguard-tools`（/usr/bin/wg、/usr/bin/wg-quick）
- 已写 `/etc/modules-load.d/wireguard.conf`（开机自动加载模块）
- 本目录提供：服务端初始化脚本、接口模板、启停脚本

## 快速启用（手动）

```bash
# NAS 上执行
sudo bash deploy/fnos/wireguard/wg-server-init.sh          # 生成密钥 + PSK 池
sudo install -m 600 deploy/fnos/wireguard/wg0.conf.tmpl /etc/wireguard/wg0.conf
sudo sed -i "s|__SERVER_PRIVATE_KEY__|$(sudo cat /etc/wireguard/keys/server.key)|" /etc/wireguard/wg0.conf
sudo bash deploy/fnos/wireguard/wg-ctl.sh up               # 拉起 wg0
sudo bash deploy/fnos/wireguard/wg-ctl.sh status
```

路由器需放行/映射 **UDP 51820** 到 NAS，或走 IPv6/DDNS。网段规划 `10.66.0.0/24`，NAS 固定 `10.66.0.1`，手机从 `10.66.0.201+` 分配。

## 手机直连内置 NKN 节点（RPC 转发）

内置 NKN 节点（nknd 子进程）跑在 api 容器里，**没有宿主端口映射**（NAT 后节点只服务本机客户端）。手机接入隧道后想直连这个节点，`wg-ctl.sh up` 会自动调用 `nkn-forward.sh` 把容器 RPC 转发出来：

```
手机（wg0, 10.66.0.201+）
  └─ http://10.66.0.1:30003   ← NKN JSON-RPC（仅 TCP）
       └─ wg0 DNAT → nas-comm-api 容器 IP:30003（DOCKER-USER 放行 + MASQUERADE 回程）
```

要点：
1. **只对 wg0 生效**：DNAT 规则绑定 `-i wg0`，局域网/外网访问不到 30003，面只给隧道内的设备。
2. **自动挂钩**：`wg-ctl.sh up` 末尾自动执行 `nkn-forward.sh up`；`down` 时自动 `remove` 清理规则。
3. **可重跑**：规则带 `modemdeck-nkn-rpc` comment，每次 `up` 先删后装；**容器重建后 IP 会变**，重新执行 `sudo bash deploy/fnos/wireguard/wg-ctl.sh up`（或单独跑 `nkn-forward.sh up`）即可刷新。
4. **手动管理**：`sudo bash deploy/fnos/wireguard/nkn-forward.sh {up|remove|status}`。
5. **前提**：管理页「NKN 节点」开关打开（nknd 在跑）。节点关着时规则仍会装上，但 RPC 不通。

## 与 NKN 的配合设计（手机自动配对）

GPT6 正在做「手机配对二维码（RSA + NKN 客户端信息）」。WireGuard 的密钥交换搭同一趟车，**不引入新的配对通道**：

```
手机扫码（GPT6 定稿协议）
  └─ 二维码内含：NAS RSA 公钥 + NKN 网关客户端地址等
配对完成（RSA 会话建立）
  ├─ NAS ──NKN──▶ 手机：WG 服务端公钥 + endpoint(DDNS:51820) + 一次性 PSK
  └─ 手机 ──NKN──▶ NAS：手机 WG 公钥（手机本地生成，私钥不出设备）
NAS 收到后：wg set wg0 peer <手机公钥> preshared-key <pskN> allowed-ips 10.66.0.2N/32
           （持久化写回 wg0.conf，防 wg-quick down 丢失）
```

要点：
1. **私钥永不出手机**：NAS 只收公钥；PSK 由 NAS 生成并经 NKN 加密下发，一次一用。
2. **NKN 通道本身已鉴权**：配对后的 NKN 会话经 RSA 握手，公钥/endpoint 借道传输无需再加密一层。
3. **endpoint 动态化**：公网 IPv4 会变，NAS 每次配对/重连时把当前 `DDNS 域名:端口` 推给手机；手机端 App 用 WG 的 `persistent_keepalive 25` 维持 NAT 映射。
4. **回滚**：`wg-ctl.sh down` 即可整体下线；peer 列表独立于 compose，不影响三容器。

## 已知限制

- 未接 compose：隧道生命周期由脚本手动管理（GPT6 的 UI 后续接管）
- NAT/转发：`wg-ctl.sh up` 只开运行时 ip_forward；重启后需重新 up（模板阶段可接受）
- 国内家宽若无公网 IPv4，需依赖 IPv6 或中转（后续可在 VPS 上加中继 peer）
