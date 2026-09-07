#!/bin/bash
IF=enxde47e203dbf4
ip addr flush dev $IF 2>/dev/null
ip addr add 192.168.225.2/24 dev $IF 2>/dev/null
ip link set $IF up
echo '--- ping module gateway ---'
ping -c 2 -W 2 192.168.225.1 2>&1 | tail -2
ping -c 2 -W 2 192.168.225.100 2>&1 | tail -1
echo '--- arp ---'
ip neigh show dev $IF 2>/dev/null | head -5
echo '--- adb connect attempts (container) ---'
docker exec nas-comm-hardware sh -c 'export HOME=/tmp/qdc507-adb ADB_SERVER_SOCKET=localfilesystem:/tmp/qdc507-adb/server.sock; for t in 5555 5037 5556; do echo try 192.168.225.1:$t; adb connect 192.168.225.1:$t 2>&1 | tail -1; done; sleep 3; adb devices -l 2>&1'
echo END
