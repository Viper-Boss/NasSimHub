#!/usr/bin/env bash
# 大疆 / 百旺 QDC507（USB 2ca3:4006）在飞牛 NAS 宿主机上的联调准备。
#
# 只读优先：只加载内核模块、绑定 option 串口驱动、列出设备节点，
# 不发送业务 AT、不拨号、不发短信、不改固件。真正的 AT 自检交给 atprobe。
#
# 用法（在 NAS 宿主机、项目根目录）：
#   bash deploy/fnos/qdc507-at-check.sh
#
# 需要 root（加载模块 / 写 new_id）。执行后至少应看到 /dev/ttyUSB*。
set -euo pipefail

VID="${VID:-2ca3}"
PID="${PID:-4006}"

echo "== 1. USB 设备 =="
if command -v lsusb >/dev/null 2>&1; then
  lsusb -d "${VID}:${PID}" || echo "  未发现 ${VID}:${PID}：模块插好、供电正常吗？（QDC507 没有板载天线，必须外接 CRC9/TS5 天线）"
else
  echo "  没有 lsusb，跳过"
fi

echo "== 2. 加载内核模块 =="
for module in option usb_wwan qmi_wwan cdc_wdm rndis_host cdc_ether; do
  if modprobe "$module" 2>/dev/null; then
    echo "  已加载 $module"
  else
    echo "  跳过 $module（可能已加载或内核没有）"
  fi
done

echo "== 3. 把 ${VID}:${PID} 绑定到 option 串口驱动 =="
bound=0
for new_id in /sys/bus/usb-serial/drivers/option1/new_id /sys/bus/usb/drivers/option/new_id; do
  if [ -w "$new_id" ]; then
    if echo "$VID $PID" >"$new_id" 2>/dev/null; then
      echo "  已写入 $new_id"
      bound=1
      break
    fi
  fi
done
[ "$bound" -eq 1 ] || echo "  没能写 new_id（需要 root，或内核没有 usb-serial）"

sleep 2

echo "== 4. 设备节点 =="
if ls -l /dev/ttyUSB* /dev/cdc-wdm* 2>/dev/null; then
  :
else
  echo "  仍无 /dev/ttyUSB*，请看 dmesg 尾部："
  dmesg 2>/dev/null | tail -20 || true
fi

cat <<'TIPS'

== 5. 接下来：AT 自检 ==

把对应架构的 atprobe 传到 NAS（RK3566/OECT 用 arm64）：

  scp atprobe-linux-arm64 NAS_USER@NAS_IP:/tmp/atprobe
  ssh NAS_USER@NAS_IP chmod +x /tmp/atprobe

然后：

  /tmp/atprobe -list                 # 看看有几个 ttyUSB
  /tmp/atprobe                       # 自动扫描，打印固件/IMEI/SIM/信号/注册/运营商
  /tmp/atprobe -port /dev/ttyUSB6    # 指定 AT 口（QDC507 的 AT 口常在 interface 2/3）

判定要点：
  - ATI 应返回 Baiwang / QDC507
  - AT+CPIN? 应为 READY（否则 SIM 没插好或被锁）
  - AT+CSQ 一般要 > 10（不接天线会很低甚至 99）
  - AT+CEREG? 应为 0,1 或 0,5（5=漫游注册）
  - AT+COPS? 能看到运营商名

可选（改 USB ID 为通用 Quectel EC25 的 2c7c:0125，让内核/chan_quectel 更好认）：
  /tmp/atprobe -port /dev/ttyUSB6 -quectel-id
改完模块会重启，Linux 上还需 unbind/bind USB 才会重新枚举。

不要做的事：不要在没有确认资费/对方同意的情况下拨号或发短信；
不要在宿主机同时跑 ModemManager，它会抢占 AT 口。
TIPS
