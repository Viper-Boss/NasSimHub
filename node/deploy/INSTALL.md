# 安装 nassimhub-agent

本阶段**不涉及真实 MSM8916/UFI003 硬件**。下面的步骤把 agent 作为一个普通的 systemd
服务装到一台 Linux 机器上（包括 mock 模式下的开发机），不刷写任何设备。

## 1. 装包

```bash
sudo dpkg -i nassimhub-agent_<version>_arm64.deb
```

postinst 会：建立 `nassimhub` 系统账号；创建 `/var/lib/nassimhub`（0700）和
`/var/log/nassimhub`（0750）；**先校验配置文件**，不通过就拒绝启动并说明原因；然后
enable + restart 服务。

或者用 tar 包手工装：

```bash
tar xzf nassimhub-agent-<version>-linux-arm64.tar.gz
cd nassimhub-agent-<version>
sudo install -m 0755 nassimhub-agent /usr/bin/
sudo install -D -m 0644 nassimhub-agent.service /lib/systemd/system/nassimhub-agent.service
sudo install -D -m 0644 nassimhub-agent.conf /etc/nassimhub/agent.conf
sudo adduser --system --group --no-create-home --home /var/lib/nassimhub nassimhub
sudo install -d -o nassimhub -g nassimhub -m 0700 /var/lib/nassimhub
sudo install -d -o nassimhub -g nassimhub -m 0750 /var/log/nassimhub
sudo systemctl daemon-reload && sudo systemctl enable --now nassimhub-agent
```

## 2. 改配置

```bash
sudo nano /etc/nassimhub/agent.conf
sudo nassimhub-agent -config /etc/nassimhub/agent.conf -check-config   # 先验证再重启
sudo systemctl restart nassimhub-agent
```

配置文件里写错一个键名**会直接报错并给出行号**，不会被静默忽略。`agent.conf` 是
conffile，升级时你的修改不会被覆盖。

## 3. 确认

```bash
systemctl status nassimhub-agent
curl -k https://127.0.0.1:7580/v1/node        # -k：设备证书由设备身份密钥自签
journalctl -u nassimhub-agent -n 50
```

`/v1/node` 是**不需要认证**的身份文档：device_id、平台、协议版本、是否已配对。它里面
没有 SIM 信息、没有号码、没有地址。

## 4. 关于 `-k`

设备证书是用 device_id 所派生的那把 Ed25519 私钥自签的，所以没有 CA 可以验证它，`curl`
必须加 `-k`。**这不是"关掉了校验"**：NAS 侧钉的是公钥，而 device_id 就是这把公钥的哈希，
所以"证书对不对"和"设备是不是它自称的那台"是同一个问题。细节见
`docs/NASSIMHUB_NODE_ARCHITECTURE.md` 第 15 节。

## 5. 卸载

```bash
sudo apt remove nassimhub-agent      # 或 dpkg -r
```

`/var/lib/nassimhub` **不会**被删除，即使 `purge` 也不会。那里面是设备身份，删掉它
意味着重装之后这台设备在 NAS 上会变成另一台设备，用户得重新配对一台自己从没换过的
硬件。确实想重置身份，手工删除那个目录即可。

## 6. 硬件阶段要改的一处

unit 里现在是 `PrivateDevices=yes`，modem 和声卡节点对服务不可见。真机阶段需要时，
**加上所需的设备节点，而不是删掉这一行**：

```ini
PrivateDevices=no
DeviceAllow=/dev/cdc-wdm0 rw
DeviceAllow=char-alsa rw
```
