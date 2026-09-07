#!/bin/bash
echo '--- adb devices (container) ---'
docker exec nas-comm-hardware sh -c 'export HOME=/tmp/qdc507-adb ADB_SERVER_SOCKET=localfilesystem:/tmp/qdc507-adb/server.sock; adb kill-server 2>/dev/null; adb start-server 2>&1 | tail -1; sleep 2; adb devices -l 2>&1'
echo '--- host: load cdc_ether/usbnet ---'
modprobe cdc_ether 2>&1 | head -1
modprobe usbnet 2>&1 | head -1
sleep 3
ls /sys/bus/usb/drivers/ | grep -iE 'cdc|usbnet|rndis'
echo '--- try bind ECM ifaces ---'
for d in cdc_ether usbnet; do
  if [ -d "/sys/bus/usb/drivers/$d" ]; then
    for i in 2-1:1.4 2-1:1.5; do
      echo "$i" > "/sys/bus/usb/drivers/$d/bind" 2>/dev/null && echo "bound $i to $d"
    done
  fi
done
sleep 4
echo '--- net interfaces now ---'
ls /sys/class/net/
echo '--- link detail ---'
ip -o link show 2>/dev/null | grep -viE 'docker|veth|br-|lo:|@' 
echo END
