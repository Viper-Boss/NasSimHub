#!/bin/bash
echo "--- container ---"
docker ps --format '{{.Names}} {{.Status}}' | grep -E 'hardware|api'
echo "--- procs in hardware ---"
docker exec nas-comm-hardware sh -c "ps aux | grep -iE 'ModemManager|modemdeck-agent|entrypoint' | grep -v grep | awk '{print \$11, \$12, \$13}' | head -5"
echo "--- hardware log last 15 ---"
docker logs --since 120s nas-comm-hardware 2>&1 | tail -15
echo "--- dbus/MM check ---"
docker exec nas-comm-hardware sh -c "mmcli -L 2>&1 | head -2"
echo "--- usb ifaces bound drivers ---"
for i in /sys/bus/usb/devices/2-1:1.*; do
  b=$(basename "$i")
  drv=$(readlink "$i/driver" 2>/dev/null | xargs -r basename 2>/dev/null)
  cls=$(cat "$i/bInterfaceClass" 2>/dev/null)
  sub=$(cat "$i/bInterfaceSubClass" 2>/dev/null)
  echo "$b cls=$cls/$sub drv=$drv"
done
echo END
