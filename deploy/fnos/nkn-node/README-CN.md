# 自建 NKN 节点（OECT-480，轻同步）

目的：给 NAS 网关和手机客户端提供低延迟、稳定的 NKN 第一跳（不追求奖励）。

## 当前状态（2026-09-08 15:05）

- 容器 `nkn-node` 已部署并运行：镜像 `nknorg/nkn:latest`（60.9MB），light sync 模式
- 数据目录：`/mnt/docker-data/nkn-node/`（config.json、wallet.json、wallet.pswd 均在，密码文件 600）
- 节点钱包地址：`NKNDkNJYXtmf5PVYYTys1qKR9fbQm2T2wcFz`
- **等待解锁：ID 注册费**。NKN 主网要求每个节点把公钥注册上链（防女巫攻击的 GenerateID 交易），需要钱包里有少量 NKN（约 10-20 个，现价几块人民币）。余额到账后节点自动重试注册并入网，无需人工干预
- 端口：宿主机 30000-30003/tcp（chord/node/websocket/RPC）
- 资源占用：CPU ~0.1%，内存 ~41MiB（同步+出块期会略高）

## 点亮步骤

1. 交易所购买少量 NKN（10-20 个足够），转到上面的钱包地址
2. 观察入网：`docker logs -f nkn-node`（看到 sync/started、无 CreateID error 即成功）
3. 验证 RPC：`curl -s -X POST http://127.0.0.1:30003 -d '{"jsonrpc":"2.0","method":"getnodestate","params":{},"id":1}'`

## 奖励（定位：业余互助，顺手领）

- `BeneficiaryAddr` 留空 = 挖到的奖励**自动累积到节点自己的钱包**（就是上面那个地址），不需要任何配置
- 定位是「不搞职业、业余互助」：家用宽带 NAT + 上行小，被选中做主节点的机会少，收益大概率很碎——攒着玩，不指望回血
- 流量/电费：闲置 CPU ~0.1%、内存 ~41MiB；中继流量按全网需求分摊，家用上行本身就小，基本无感
- **领取方式**（想提出来的时候）：
  1. 把 `/mnt/docker-data/nkn-node/wallet.json` 下载到电脑
  2. 导入 NKN 钱包应用（nMobile / NKN Desktop / wallet.nkn.org），密码用 `wallet.pswd` 里那份
  3. 转出到交易所或任意地址即可；wallet.json 不要外传

## 客户端怎么用（点亮后）

| 客户端 | 接法 |
| --- | --- |
| NAS 网关（api 容器） | NAS `.env.fnos` 或 credentials.env 加 `HOMESIM_NKN_SEED_RPC=http://192.168.120.190:30003`，重建 api 容器（代码已支持，见 homesim_forward_nkn.go 的 nknSeedRPCEnv；**需重建镜像**，8400079 之后） |
| 手机（同一 LAN） | 手机 NKN 客户端首选 `http://192.168.120.190:30003` |
| 手机（外网） | 走 WireGuard 隧道后用 `http://10.66.0.1:30003`（见 ../wireguard/README-CN.md） |

要点：**网关和手机连到同一个节点时，两者之间的消息在该节点本地中转**，不再绕海外 ring，延迟从数百 ms 降到几十 ms 内。

## 维护

```bash
docker logs --tail 50 nkn-node          # 日志
docker restart nkn-node                 # 重启（restart unless-stopped，开机自起）
docker stats nkn-node --no-stream       # 资源
```

- 奖励：BeneficiaryAddr 留空，奖励进节点自己的钱包，不领即无奖励运行
- 链数据：light sync，磁盘占用 GB 级且远小于全量（全量 ~20G+）
- 升级：`docker pull nknorg/nkn:latest && docker rm -f nkn-node` 后按上面的 docker run 重启

## 已知限制

- NAS 无公网 IP，节点对全网贡献≈0（只服务自家客户端）；想给全网做贡献→国内 VPS 跑同款
- 手机外网场景依赖 WireGuard 隧道落地（GPT6 的 UI + 本目录 ../wireguard 基础设施）
- 合规：个人自用节点、不领奖励、不对外提供服务，风险很低；但 NKN 本质是加密网络节点，介意者可随时 `docker rm -f nkn-node` 下线，零残留
