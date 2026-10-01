# UFI003 长期通话部署

使用 install-voice.sh AGENT_ARM64 HOLDER_ARM64 安装。本机必须已验证 VoLTE profile 并具有 nassimhub-ufi003 声卡及对应 q6 通话端口模块。安装器只更新用户态文件和 systemd，不刷 boot，不改身份、配对、NV。

音频 systemd 覆盖放宽 PrivateDevices 后，通过 DevicePolicy=closed / DeviceAllow=char-alsa rw 限制设备访问，保留非特权 nassimhub 用户和现有服务隔离；ProtectProc=default 用于全设备资源监测。此配置需要设备管理员同意长期保留。

每次安装先备份 Agent、agent.conf、service.d 到 /var/backups/nassimhub-voice-时间戳。

通用镜像不得包含 /etc/nassimhub/local-volte.enabled 或 /etc/nassimhub/voice-audio.verified。后者仅在当前设备实际双向16kHz通话音频验收成功后，由 root 写入单行 ufi003-q6voice-16k-v1 并设 0644。不得根据芯片型号或 IMS 注册状态自动生成。

Agent 每次能力查询检查标记权限、必要可执行程序、全部 q6 ALSA 控件与 IMS Voice available，任一不满足就不宣称通话能力。NAS 同时要求在线、兼容、SIM就绪、IMS yes、VoiceControl、VoiceAudio，再发布拨号/接听/挂断/拒接。DTMF 尚不支持。

恢复某份用户态备份时，停止 Agent 并挂断在途电话，恢复该目录的 agent / agent.conf / service.d；若原备份没有 voice.conf，删除当前固定路径的 voice.conf；清除本机音频验收标记，daemon-reload 后重启 Agent。不要恢复或删除 /var/lib/nassimhub 的身份、配对数据。

该安装器不代表通用 Factory/OTA 已打包；boot 音频设备树和模块仍须由固件构建流程合入。
