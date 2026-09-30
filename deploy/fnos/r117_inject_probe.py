#!/usr/bin/env python3
"""Inject DNS probes into a copy of hardware/Dockerfile on the NAS."""
import re

SRC = '/volume1/docker/homesim/source/nas-comm-gateway-r6/hardware/Dockerfile'
DST = '/tmp/hwdebug.Dockerfile'

src = open(SRC).read()
probe = (
    'RUN echo PROBE-START && cat /etc/hosts && echo PROBE-RESOLV && '
    'cat /etc/resolv.conf && getent hosts mirrors.aliyun.com; '
    'echo probe-rc=$?\n'
)
out = src.replace('RUN sed -i', probe + 'RUN sed -i')
open(DST, 'w').write(out)
print('injected probes:', out.count('PROBE-START'))
