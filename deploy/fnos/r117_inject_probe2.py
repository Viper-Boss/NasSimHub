#!/usr/bin/env python3
"""Inject env/hosts probes INTO the apt RUN steps of hardware/Dockerfile copy."""

SRC = '/volume1/docker/homesim/source/nas-comm-gateway-r6/hardware/Dockerfile'
DST = '/tmp/hwdebug2.Dockerfile'

src = open(SRC).read()
probe = (
    '&& echo PROXY-ENV-START && (printenv | grep -i proxy || echo no-proxy-env) '
    '&& echo APT-CONF-START '
    '&& (grep -ri proxy /etc/apt/ || echo no-apt-proxy-conf) '
    '&& echo HOSTS-INSIDE-RUN && cat /etc/hosts '
    '&& echo GETENT && getent hosts mirrors.aliyun.com; echo probe-rc=$? '
    '&& apt-get update'
)
count = src.count('&& apt-get update')
out = src.replace('&& apt-get update', probe)
open(DST, 'w').write(out)
print('injected into apt RUNs:', count)
