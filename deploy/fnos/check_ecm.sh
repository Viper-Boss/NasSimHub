#!/bin/bash
M=$(mmcli -L 2>/dev/null | grep -oP '(?<=Modem/)[0-9]+' | head -1)
echo "modem=$M"
mmcli -m "$M" --command='AT+QCFG="usbnet"?' 2>&1 | tail -1
mmcli -m "$M" --command='AT+QCFG="usbcfg"' 2>&1 | tail -1
echo '--- host net interfaces ---'
ls /sys/class/net/
echo '--- non-docker links ---'
ip -o link show 2>/dev/null | grep -viE 'docker|veth|br-|lo:|@'
echo done
