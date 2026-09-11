#!/bin/bash
# 从宿主机借 wg/ip + 依赖库打包（容器内 DNS 全断，无法 apt）
set -e
CTX=/mnt/docker-data/build/sim-autounlock/wgtools
mkdir -p $CTX/libs/aarch64-linux-gnu /mnt/docker-data/build/sim-autounlock/debs 2>/dev/null || true
cp /usr/bin/wg $CTX/wg
cp /usr/sbin/ip $CTX/ip
chmod 755 $CTX/wg $CTX/ip
for lib in libbpf.so.1 libelf.so.1 libmnl.so.0 libbsd.so.0 libcap.so.2 libz.so.1 libmd.so.0; do
  cp -L /lib/aarch64-linux-gnu/$lib $CTX/libs/aarch64-linux-gnu/ 2>/dev/null || echo "skip $lib"
done
ls -la $CTX $CTX/libs/aarch64-linux-gnu/
cat > /mnt/docker-data/build/sim-autounlock/hardware.Dockerfile << 'EOF'
FROM nas-comm-hardware:fnos-local-0.1
USER root
COPY wgtools/wg /usr/bin/wg
COPY wgtools/ip /usr/sbin/ip
COPY wgtools/libs/ /opt/host-libs/
RUN chmod 755 /usr/bin/wg /usr/sbin/ip \
 && echo /opt/host-libs/aarch64-linux-gnu > /etc/ld.so.conf.d/zz-host-libs.conf \
 && ldconfig \
 && wg --version && ip -V
USER 10001:10001
EOF
echo DOCKERFILE-READY
