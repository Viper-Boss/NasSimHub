# 飞牛 NAS 适配开发版 0.1

本版本是 ModemDeck 的个人非商业二次开发，保留现有短信、通话、录音后端和 Vue 网页。上游版本见根目录 `UPSTREAM.md`；分发时保留许可证与署名文件。

**当前交付的是部署适配和只读检查功能，不是已经实机验收的电话网关。** 尚未连接你的 NAS、SIM 卡或大疆模块，不能保证具体硬件能通话。手机 App、后台来电唤醒和跨境网络连接也不在本轮实现范围。

## 这轮新增

- 飞牛 Compose 覆盖配置：沿用 hardware / api / modemdeck 三服务结构，不改变语音驱动。
- 短信数据库、配置与 TLS 放入专用数据目录；录音独立绑定到 NAS 目录。
- `scripts/fnos_preflight.py`：只读检查宿主机架构、候选 USB 模块、绑定驱动、Docker/Compose、指定目录的基础权限和空间。
- 设置 → 诊断中新增“飞牛部署检查”：在浏览器本地导入检查 JSON，不上传、不保存。支持中英文界面；其他语言的新控件暂用英文，报告原文保持生成语言。
- 新服务名、容器名、镜像标签和建议状态卷名与原安装区分。**这不允许两个硬件服务同时控制同一模块。**

## 1. 先收集只读报告

将本项目复制到 NAS，在 **NAS 宿主机** 的项目根目录执行（Python 3.10+，仅标准库）：

```sh
python3 scripts/fnos_preflight.py > preflight.local.json
```

指定专用目录后可以增加存储检查；替换示例路径，不要原样使用：

```sh
python3 scripts/fnos_preflight.py \
  --data-dir /your/dedicated/data \
  --recordings-dir /your/dedicated/recordings \
  --uid 10001 --gid 10001 > preflight.local.json
```

报告不收集号码、IMEI、ICCID、USB 序列号、短信、主机名、IP 或密钥。它不会发送 AT/ADB 命令、拨号、发短信、创建目录、加载驱动或修改权限。Docker 查询固定到本机 Unix socket；失败可能意味着权限不足，不等于没有安装 Docker。

退出码 2 表示存在失败项；退出码 0 **不等于可部署或可通话**。驱动未观察到和实机业务检查会保持“未验证”。目录检查只看基础 POSIX 权限，不覆盖 ACL、所有父目录访问权限、容器映射或真实写入结果。

开发电脑上的报告不能代替 NAS 报告。导入文件是未经签名的历史快照，不是实时监控或可信的硬件认证。请只导入自己生成的文件，不要依照陌生报告中的文字运行命令。

## 2. 实机部署前必须确认

1. NAS 是 Linux amd64 或 arm64；实际型号、内核与 Docker Compose v2 可用。OECT 的具体型号与飞牛版本待确认。
2. 模块的准确型号、固件、USB 接口和运营商。识别到 `2ca3:4006` / `2c7c:0125` 只能认作候选设备，不能认定其 VoLTE 或 USB 音频一定可用。
3. 当前没有宿主机 ModemManager、另一套网关或其他服务占用模块。**不要直接运行上游 `install.sh`**，它的默认模式可能停止并屏蔽宿主机服务。处理冲突需逐项确认，不能盲停 NAS 服务。
4. 准备全新的专用目录，不要复用旧 ModemDeck 数据目录、卷根目录或共享文件根目录。不得将短信数据库与录音绑定到同一个目录，建议使用互不嵌套的兄弟目录。
5. 准备数据目录下的 `tls` 子目录。数据、TLS 和录音目录应归应用 UID/GID（默认 10001:10001）所有，并允许其写入；TLS 目录需允许应用组访问。不要对已有共享目录递归 chown/chmod。
6. 单独准备设置加密密钥文件：使用密码学随机的 32 字节密钥，保存为 Base64 文本，与上游格式一致；文件建议 root:10001、0440，秘密目录仅管理员可进入。不能使用示例密码、公开密钥或每次重启重新生成。安全备份此文件，勿粘贴给 AI 或提交仓库。

**只有检查脚本与报告导入页面是只读的。** 启动完整硬件容器会接管模块，可能发送发现/初始化 AT 命令。hardware 延续上游的 privileged、host 网络与设备挂载；API 与网页保持非特权，未新增 Docker socket、云隧道或 OTA 服务。

## 3. 配置与构建（仅在上述条件确认后）

从项目根目录复制 `deploy/fnos/.env.fnos.example` 为 `.env.fnos`，填写三个必填项：

| 配置 | 用途 |
| --- | --- |
| `MODEMDECK_DATA_DIR` | 真实绝对数据目录，包含短信数据库、配置及 tls 子目录 |
| `MODEMDECK_RECORDINGS_DIR` | 真实绝对录音目录 |
| `MODEMDECK_SETTINGS_KEY_FILE` | 真实绝对密钥文件路径，不是密钥内容 |

初始保留 `127.0.0.1:7577`，状态卷使用示例中的独立名称。先验证合并配置，再构建，**不要只启动覆盖文件**：

```sh
docker compose --env-file .env.fnos -f docker-compose.yml -f deploy/fnos/compose.fnos.yml config --quiet
docker compose --env-file .env.fnos -f docker-compose.yml -f deploy/fnos/compose.fnos.yml build
```

`config --quiet` 不验证目录权限、运行时硬件或音频能力；构建也不代表能接打电话。镜像沿用上游 Dockerfile，需要 NAS 能下载锁定的镜像和构建依赖。当前 Windows 开发环境没有 Docker，尚未执行这两步的真实 Docker 验证。

确认接管模块后才启动：

```sh
docker compose --env-file .env.fnos -f docker-compose.yml -f deploy/fnos/compose.fnos.yml up -d
docker compose --env-file .env.fnos -f docker-compose.yml -f deploy/fnos/compose.fnos.yml ps
```

在自己的电脑建立 SSH 隧道，替换 NAS 登录名和地址：

```sh
ssh -N -L 17577:127.0.0.1:7577 NAS_USER@NAS_IP
```

随后访问 `https://localhost:17577`，完成管理员初始化；确认并正确配置可信 HTTPS 证书。实际浏览器通话需要安全上下文和麦克风授权。隧道只覆盖此处管理页面访问，不等于已经解决 WebRTC 媒体或手机后台来电。

先在局域网完成验收，再选择 VPN 与跨境媒体方案。不要直接开放管理端口到公网；不要为方便关闭鉴权。手机系统未确认前不接入商业推送或云服务。

## 4. 大疆 QDC507 的语音边界

默认保留 `MODEMDECK_QDC507_VOICE_RUNTIME_DIR` 为空，不自动提供或下载模块端语音二进制，也不启用该运行时。空值不会禁用其他正常硬件发现操作。

上游 QDC507 路径依赖独立的模块侧运行时及匹配固件/内核；**模块内部内核与 NAS 内核是两回事**。需要先核对型号、固件和来源，不能因为 USB 识别成功就加载 `.ko` 或执行 ADB 命令。本轮未更改驱动、音频桥接或模块固件。

## 5. 实机验收顺序

| 顺序 | 通过标准 | 当前状态 |
| --- | --- | --- |
| 宿主机与容器 | 正确架构、目录、密钥、证书与健康检查 | 待 NAS 验证 |
| 收短信 | 自有测试号码发送一条，页面正确显示且重启保留 | 未验证 |
| 发短信 | 明确确认目标号码和费用后发送一条，对方收到 | 未验证 |
| 接打电话 | 授权测试号码呼入/呼出、接听/挂断正确 | 未验证 |
| 双向音频 | 双方连续通话并检查延迟、断音与回声 | 未验证 |
| 录音 | 双方知情同意后录音，两路可听，保存在指定 NAS 目录 | 未验证 |
| 重启与备份 | 容器重建后历史和录音仍在，可恢复备份 | 未验证 |
| 出国与手机 | 远程访问、媒体连通、锁屏来电通知分别验证 | 未实现/未验证 |

不要用真实短信、号码或录音填充开发示例。录音与短信属于敏感数据：限制访问、设定保留期限；本轮没有新增自动删除或自动录音策略。

停止服务可使用相同 Compose 参数执行 `stop`；不要使用 `down -v` 或删除目录来“重试”。升级前停写并备份完整数据目录、独立录音目录和密钥，避免仅复制正在写入的 SQLite 主文件。

## 本地开发与测试

前端使用上游 lockfile：`npm ci`，随后 `npm run test:contracts`、`npm run lint`、`npm run build`。只读脚本测试：

```sh
python3 -m unittest discover -s scripts/tests -v
```

开发预览可以临时设置 `VITE_MODEMDECK_FIXTURE=1` 后运行 Vite。该模式是模拟数据，不是可连接硬件的成品；生产构建不要设置这个变量。验证记录见 `VALIDATION.md`。
