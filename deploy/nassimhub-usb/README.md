# NasSimHub USB 节点：宿主机安装

仅启动 Docker 容器不会配置宿主机 USB 网卡。发布和部署 UFI003 支持时，需要在 NAS 宿主机的仓库根目录执行：

```sh
sudo sh deploy/nassimhub-usb/install.sh
sudo sh deploy/nassimhub-usb/install.sh status
```

支持先插棒子后安装，也支持安装后再插入。安装 udev 规则及 systemd 服务，仅匹配 VID 1d6b、PID 0104、manufacturer NasSimHub 的网卡，不接管 QDC507、普通网卡或 NAS 默认路由。

NetworkManager 正在运行时，初始化脚本先对匹配的接口执行 managed no，结束可能已有的 DHCP 配置，再设置 10.55.0.1/29。仅追加 udev 规则不足以可靠释放先前已受管理的接口，会导致地址随后被 DHCP 流程清除。没有运行 NetworkManager 的主机跳过该步骤。

验收必须包括：先插棒子再安装，等超过一个 DHCP 超时周期后地址仍存在；NAS 主机和 API 容器均能访问 https://10.55.0.2:7580/v1/node；网页发现的 device_id 与实物一致；重新插入和 NAS 重启后复测。网络可达不等于已配对，也不等于音频功能通过验收。

```sh
sudo sh deploy/nassimhub-usb/install.sh uninstall
```

以上撤销本安装脚本管理的文件和 USB 地址。页面深浅主题使用应用的 surface/text/border/muted 变量，发布网页应从修复后的源码重新构建，不使用旧 dist。

USB reset 或 NAS 启动时若 udev 激活丢失，nassimhub-usb-reconcile.timer 每15秒检查匹配的 NasSimHub 接口并恢复服务；正常链路不重配，不修改普通网卡和默认路由。卸载会关闭并移除此恢复服务。
