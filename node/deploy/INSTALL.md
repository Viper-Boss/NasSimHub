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

## 7. 语音安装脚本与出厂 Agent

`/usr/bin/nassimhub-agent` 在更新机制里是**出厂 Agent**：每次启动都从它开始，更新从不改写它，
已安装的版本出问题时回退到它，已安装版本的版本号不得低于它（`agent/ota/boot.go`、`launch.go`）。

r128 起，`deploy/audio/install-voice.sh` **默认不再替换它**：

```bash
sudo sh deploy/audio/install-voice.sh /path/holder-arm64        # 只装 holder 与配置，保留出厂 Agent
```

旧的 `install-voice.sh AGENT HOLDER`（两个文件参数）会被拒绝并提示新写法，不会再悄悄覆盖出厂 Agent。

必须更换出厂 Agent 时要显式写出并给出哈希：

```bash
sha256sum agent-arm64                                            # 在构建机上记录
sudo sh deploy/audio/install-voice.sh --replace-agent agent-arm64 --expect-sha256 <hex> holder-arm64
```

脚本在改动任何东西之前拒绝以下情况：

- 哈希不符；
- 版本是 `dev` 或 `proto.ParseVersion` 解析不了的字符串（这样的 Agent 不参与更新，也不能做更新下限）；
- 版本低于当前出厂 Agent（下限不下移）；
- 版本高于状态目录里选中的已安装发布（那个发布会被新的出厂 Agent 取消选中），除非加 `--supersede-release`；
- 更新处于 `APPLYING` / `PENDING_CONFIRM` / `ROLLING_BACK`（保留模式同样拒绝：脚本要重启 Agent，
  确认前的再次重启会让 Agent 把更新判为失败并回滚）。

脚本不写、不删状态目录里的任何东西（`agent-releases/`、`ota-state.json`、身份、配对），只读取。
它会说明重启后**实际提供服务的是谁**：出厂 Agent，还是仍被选中的已安装发布——后者通过出厂 Agent 的
启动校验后运行的是发布，不是 `/usr/bin/nassimhub-agent`。

任何一步失败或被信号中断，脚本把所有文件恢复原状并重新启动它停掉的 Agent。逐条的行为差别见
`deploy/audio/README-voice.md` 的“与旧脚本的差别”。

验证：`sh scripts/test-install-voice.sh`（沙箱 + 命令替身）和 `test/voiceinstall`（Go，证明脚本的版本解析与
`proto.ParseVersion` 一致）。**只有离线测试，没有在真机上验证过。**
