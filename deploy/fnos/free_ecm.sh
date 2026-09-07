#!/bin/bash
for i in 4 5 6 7 8 9; do
  iface="2-1:1.$i"
  if [ -L "/sys/bus/usb/devices/$iface/driver" ]; then
    drv=$(basename "$(readlink "/sys/bus/usb/devices/$iface/driver")")
    if [ "$drv" = "option" ]; then
      echo "$iface" > /sys/bus/usb/drivers/option/unbind 2>/dev/null && echo "unbound $iface from option"
    fi
  fi
done
sleep 5
echo '--- interfaces after unbind ---'
for i in /sys/bus/usb/devices/2-1:1.*; do
  b=$(basename "$i")
  drv=$(readlink "$i/driver" 2>/dev/null | xargs -r basename 2>/dev/null)
  echo "$b drv=${drv:-none}"
done
echo '--- net interfaces ---'
ls /sys/class/net/
echo '--- ip link detail for new ---'
ip -o link show 2>/dev/null | grep -viE 'docker|veth|br-|lo:|@'
echo '--- adb devices ---'
export HOME=/tmp/qdc507-adb ADB_SERVER_SOCKET=localfilesystem:/tmp/qdc507-adb/server.sock
adb kill-server 2>/dev/null; adb start-server 2>&1 | tail -1
sleep 3
adb devices -l 2>&1
echo END
