# 16 kHz 宽带音频验证记录

日期：2026-09-14。范围：只读真机检查及上游源码核对；未刷固件、未改 USB 组合、未停止语音桥、未测试真实 16 kHz 通话。

## 已确认

1. NAS `/proc/asound/card0/stream0`：BAIWANG USB Audio，采集及播放均只声明 Rates: 8000，S16_LE，单声道。
2. 模组 `/proc/asound/card0/pcm4c/sub0/hw_params`、`pcm4p/sub0/hw_params`：VoLTE 当前均为 rate 8000，channels 1，period_size 1024，buffer_size 4096。这是当前协商值，不代表设备支持范围。
3. 模组当前 USB functions：`diag,serial,ecm,ffs,audio`。
4. `/sys/class/android_usb/f_audio` 仅 audio_enable 等通用属性，无 sample_rate。`f_uac2_func` 也没有采样率配置属性。
5. configfs 已挂载但未暴露 usb_gadget 目录；没有找到 g_audio、u_audio、qdc507_voice、qdc507_aprv3 的可调采样率参数入口。
6. 上游 MaVo 桥接源码 `module/mavo_pcm_bridge.c` 第30行 `#define PCM_RATE 8000U`；VoLTE 两方向 pcm_open 均使用该常量，CLI 无采样率选项。当前 HomeSIM ALSA 后端也明确限制8000。

源码：[MaVo bridge](https://github.com/moluncn/mavo/blob/main/module/mavo_pcm_bridge.c)。通过 GitHub Contents API 读取；上游 main 可变化，行号以此次读取为准。

## 支持进一步实验、但不能直接等同真机能力的线索

MaVo 模组报告指向的公开 EG25 内核提交 `82ed00908b3e8efc3ff0de27d2b5a7c0524ecd7f`：

- `sound/soc/msm/qdsp6v2/msm-pcm-voice-v2.c` 第41–43行声明8000和16000。
- `drivers/usb/gadget/function/u_uac1.h` 第36行默认 UAC1_SAMPLE_RATE 16000。
- `u_uac1.c` 根据 opts->sample_rate 配置 PCM；`f_uac1.c` 据实际 rate 填写 USB 描述符并调整包长，也有 configfs sample_rate 属性。

[VoLTE PCM 驱动](https://github.com/Biktorgj/quectel_eg25_kernel/blob/82ed00908b3e8efc3ff0de27d2b5a7c0524ecd7f/sound/soc/msm/qdsp6v2/msm-pcm-voice-v2.c)

上述公开源码不能证明本机已加载的定制驱动和 USB gadget 支持同样能力；公开默认16000与本机实际8000的差异尤其需要注意。VoLTE 已注册也不等于应用拿到了宽带PCM。

## 结论与后续实验边界

- 当前部署链路确实是8kHz，无法靠前端设成48kHz恢复已丢失的频宽。
- 16kHz有源码层面的可行线索，但本机端到端16kHz能力尚未验证。不能宣称芯片只支持8kHz，也不能宣称已经支持高清通话。
- 后续实验应先确认无通话、准备恢复现有桥接进程的方法，再验证模组PCM的16kHz硬件参数约束；单纯成功打开16kHz也不足以证明存在真实宽带信息。
- 还需要找到受支持的USB宽带出口或替代音频传输，再完整匹配采集、播放、Opus和缓冲周期，使用受控音频及实际电话验证频谱/听感。
- 不应只把现有arecord/aplay参数改为16000；不应为此直接刷通用EG25固件或盲目替换内核。
