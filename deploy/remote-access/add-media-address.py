#!/usr/bin/env python3
"""Add a private VPN ICE address without discarding existing LAN candidates."""
import ipaddress
import pathlib
import sys

env_path = pathlib.Path(sys.argv[1])
address = str(ipaddress.IPv4Address(sys.argv[2]))
if ipaddress.IPv4Address(address) not in ipaddress.IPv4Network('100.64.0.0/10'):
    raise SystemExit('Expected a Tailscale IPv4 address')
key = 'MODEMDECK_CALL_MEDIA_PUBLIC_IPS='
text = env_path.read_text()
lines = text.splitlines()
matches = [i for i, line in enumerate(lines) if line.startswith(key)]
if len(matches) > 1:
    raise SystemExit('Duplicate media address setting; inspect before changing')
values = []
if matches:
    current = lines[matches[0]][len(key):].strip().strip('\"\'')
    values = [str(ipaddress.ip_address(v.strip())) for v in current.split(',') if v.strip()]
if address not in values:
    values.append(address)
setting = key + ','.join(values)
if matches:
    lines[matches[0]] = setting
else:
    lines.append(setting)
backup = env_path.with_name(env_path.name + '.before-tailscale')
if not backup.exists():
    with backup.open('xb') as output:
        output.write(env_path.read_bytes())
    backup.chmod(0o600)
env_path.write_text('\n'.join(lines) + '\n')
print('Media addresses updated; existing addresses preserved')
