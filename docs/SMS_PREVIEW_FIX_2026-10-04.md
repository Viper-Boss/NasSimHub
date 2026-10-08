# 短信正文与预览一致性修复

问题：修复 Node 短信正文后，`sms_contacts.last_content` 仍保留旧的 GLib 转义文本。

源码：`internal/store/messages.go` 的预览和搜索优先读取 `sms` 中对应的正文，
严格匹配 `last_sms_id`、线路和对端；找不到对应消息时保留原缓存回退。
不对正常正文再次执行解码。

回归：`TestMessageThreadPreviewAndSearchUseAuthoritativeBody` 验证中文预览、搜索、
旧缓存不再参与搜索、跨线路隔离、缓存回退，以及未读数和收藏状态。
本地完整 store 包测试和 vet 均通过。

现存数据：一致性备份后，仅将 Node 会话的预览同步为相同消息的已修复正文。
同步 5 条；残余转义预览为 0；未读、标记未读和收藏状态变化为 0。

NAS 备份：`/mnt/docker-data/homesim/backup-sms-preview/`。
部署前镜像另行保留为 `nas-comm-api:before-sms-preview`。
仅 API 需要重建，前端和棒子 Agent 不需要改动。
