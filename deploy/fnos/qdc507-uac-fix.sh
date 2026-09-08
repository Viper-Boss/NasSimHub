#!/bin/sh
# QDC507 USB 接口守护（飞牛 NAS host 端）。
#
# 背景：为打通 AT 通道，option 驱动的动态 new_id "2ca3 4006" 是泛匹配
#（不限接口类），会抢占模块的以下接口，造成两类故障：
#   1. USB 音频接口（if7/8/9，class=01）被抢占 → snd-usb-audio 无法认领、
#      host 侧永远不出现声卡（/proc/asound/cards 为空），浏览器语音不可用；
#   2. CDC 数据接口对（if4=class 02 控制 + if5=class 0a 数据）被抢占 →
#      host 侧永远不出现 ECM 网卡（usb0/enx*），MM 只能按 AT+PPP 模组对待，
#      ModemDeck 数据面（netlink bearer 所有权模型）不支持 PPP，
#      "开启移动数据" 必然报 failed_precondition。
#
# 本脚本在模块每次 USB 上线时（udev add 事件触发）：
#   1. 确保 option 的 AT 动态 ID 存在（保持 AT 通道可用，if0-3 串口）；
#   2. 等待 option 绑定完成后，把 class=01 的音频接口从 option 解绑，
#      并将 AudioControl 接口 bind 给 snd-usb-audio（整卡随之接管）；
#   3. 把 CDC 数据接口对（if4/if5）从 option 解绑，bind 给 cdc_ether，
#      恢复 ECM 数据网卡。
# 幂等可重入：已是目标驱动的接口直接跳过。
#
# 注意：QDC507 的 cdc-wdm0 不响应 QMI/MBIM 探测（实测 MM probe 45s 超时），
# 因此数据路径只能走 ECM（cdc_ether）。MM generic 插件对 AT 模组发起的
# bearer 是 PPP，ModemDeck agent 数据面不支持——打开移动数据仍会失败，
# 需要后续实现厂商 ECM 数据路径（AT+QNETDEVCTL / adb）才能端到端打通。
#
# 用法（由 udev 规则调用）：qdc507-uac-fix.sh <usb devpath>
#   例：qdc507-uac-fix.sh /devices/platform/.../usb2/2-1

set -u
DEVPATH="$1"

[ -n "$DEVPATH" ] || exit 0
[ -d "/sys$DEVPATH" ] || exit 0

modprobe option 2>/dev/null
modprobe snd-usb-audio 2>/dev/null
modprobe cdc_ether 2>/dev/null

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

# CDC 数据接口对（if4 控制 + if5 数据）：从 option 解绑并交给 cdc_ether
for IF in "/sys$DEVPATH:1.4" "/sys$DEVPATH:1.5"; do
    [ -d "$IF" ] || continue
    DEVNAME="${IF##*/}"
    CUR="$(basename "$(readlink -f "$IF/driver" 2>/dev/null)" 2>/dev/null)"
    if [ "$CUR" = "cdc_ether" ]; then
        continue
    fi
    if [ "$CUR" = "option" ]; then
        echo "$DEVNAME" > /sys/bus/usb/drivers/option/unbind 2>/dev/null
        sleep 1
    fi
done
CONTROL_IF="/sys$DEVPATH:1.4"
if [ -d "$CONTROL_IF" ] && [ "$(cat "$CONTROL_IF/bInterfaceClass" 2>/dev/null)" = "02" ]; then
    CUR="$(basename "$(readlink -f "$CONTROL_IF/driver" 2>/dev/null)" 2>/dev/null)"
    if [ -z "$CUR" ]; then
        echo "${DEVPATH##*/}:1.4" > /sys/bus/usb/drivers/cdc_ether/bind 2>/dev/null
    fi
fi

# 留一份结果便于排查
{ cat /proc/asound/cards 2>/dev/null
  echo '--- interfaces ---'
  for IF in /sys"${DEVPATH}":1.*; do
      [ -d "$IF" ] || continue
      echo "${IF##*/} drv=$(basename "$(readlink -f "$IF/driver" 2>/dev/null)" 2>/dev/null)"
  done
} > /run/qdc507-uac-fix.last 2>/dev/null || true
