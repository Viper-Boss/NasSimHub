#!/bin/sh
# QDC507 UAC 音频接口守护（飞牛 NAS host 端）。
#
# 背景：为打通 AT 通道，option 驱动的动态 new_id "2ca3 4006" 是泛匹配
#（不限接口类），会把模块的 USB 音频接口（if7/8/9，class=01）也抢占，
# 导致 snd-usb-audio 无法认领、host 侧永远不出现声卡（/proc/asound/cards
# 为空），浏览器语音随之不可用。
#
# 本脚本在模块每次 USB 上线时（udev add 事件触发）：
#   1. 确保 option 的 AT 动态 ID 存在（保持 AT 通道可用）；
#   2. 等待 option 绑定完成后，把 class=01 的音频接口从 option 解绑；
#   3. 将 AudioControl 接口 bind 给 snd-usb-audio（整卡随之接管）。
# 幂等可重入：已是 snd-usb-audio 的接口直接跳过。
#
# 用法（由 udev 规则调用）：qdc507-uac-fix.sh <usb devpath>
#   例：qdc507-uac-fix.sh /devices/platform/.../usb2/2-1

set -u
DEVPATH="$1"

[ -n "$DEVPATH" ] || exit 0
[ -d "/sys$DEVPATH" ] || exit 0

modprobe option 2>/dev/null
modprobe snd-usb-audio 2>/dev/null

# AT 通道：确保 option 动态 ID 存在（若已存在则 grep 命中，跳过写入）
if ! grep -q "2ca3 4006" /sys/bus/usb-serial/drivers/option1/new_id 2>/dev/null; then
    echo "2ca3 4006" > /sys/bus/usb-serial/drivers/option1/new_id 2>/dev/null
fi

# 给 option 一点时间完成绑定
sleep 6

for IF in "/sys$DEVPATH:1.7" "/sys$DEVPATH:1.8" "/sys$DEVPATH:1.9"; do
    [ -d "$IF" ] || continue
    [ "$(cat "$IF/bInterfaceClass" 2>/dev/null)" = "01" ] || continue
    DEVNAME="${IF##*/}"
    CUR="$(basename "$(readlink -f "$IF/driver" 2>/dev/null)" 2>/dev/null)"
    if [ "$CUR" = "snd-usb-audio" ]; then
        continue
    fi
    if [ "$CUR" = "option" ]; then
        echo "$DEVNAME" > /sys/bus/usb/drivers/option/unbind 2>/dev/null
    fi
    echo "$DEVNAME" > /sys/bus/usb/drivers/snd-usb-audio/bind 2>/dev/null
done

# 留一份结果便于排查
cat /proc/asound/cards > /run/qdc507-uac-fix.last 2>/dev/null || true
