# AirSimHub 设备控制台

棒子系统源码独立维护于 [Viper-Boss/AirSimHub](https://github.com/Viper-Boss/AirSimHub)。系统基线为 Debian 13 Trixie / Linux 6.12.49，开发者源码使用 AGPLv3，内核衍生部分保留上游许可证。

NasSimHub 的设备管理页提供“打开棒子 Wi-Fi 后台”与“通过 USB 控制后台”。USB 管理页使用 NAS 自己的 HTTPS 地址 `/sim-nodes/setting/<deviceID>/`，仍需独立设备登录；关闭 Wi-Fi 不影响管理。此入口只支持已配对且在线的设备，并分别检查 NAS 管理员权限、NAS CSRF、设备证书和设备会话。

设备后台包括 24 小时登录、Wi-Fi 射频开关、默认关闭的移动数据及 APN、蓝牙开关/扫描、受限 GPS 开关及采样间隔、系统账户/SSH 管理。USB 接入时拒绝开启后台管理的移动数据，Wi-Fi 关闭不会自动使用 SIM 流量。

蓝牙实际外设连接、PIN 配对/蓝牙音频、GPS 室外定位、断开 USB 后移动数据联网仍需按设备验收。不要把源码入口视为已发布的批量刷机镜像。
