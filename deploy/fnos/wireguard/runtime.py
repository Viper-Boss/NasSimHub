#!/usr/bin/env python3
"""Restricted HomeSIM WireGuard reconciler. Never executes configuration as shell."""
import base64
import datetime
import ipaddress
import json
import os
import signal
from pathlib import Path
import subprocess
import tempfile
import time

IFACE = 'hswg0'
ALIAS = 'homesim-managed-wireguard'
CHAIN = 'HOMESIM_WG'


class MTProto:
    def __init__(self):
        self.process = None
        self.config = None

    def stop(self):
        if self.process is not None and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        self.process = None
        run('iptables', '-w', '-D', 'INPUT', '-j', 'HOMESIM_MTG', check=False)
        run('iptables', '-w', '-F', 'HOMESIM_MTG', check=False)

    def reconcile(self, config):
        if not config.get('enabled', False):
            if self.config is not None or self.process is not None:
                self.stop()
            self.config = None
            return 'disabled'
        if config == self.config and self.process is not None and self.process.poll() is None:
            return 'running'
        address = ipaddress.ip_address(config['bind_address'])
        port = config['port']
        if address.version != 4 or not address.is_private or type(port) is not int or not 1024 <= port <= 65535:
            raise ValueError('MTProto requires a private LAN IPv4 and an unprivileged port')
        raw = bytes.fromhex(config['secret'])
        if not 20 <= len(raw) <= 270 or raw[0] != 0xee or not all(c in b'abcdefghijklmnopqrstuvwxyz0123456789.-' for c in raw[17:]):
            raise ValueError('invalid MTProto secret')
        lan = None
        for link in json.loads(run('ip', '-j', '-4', 'address', 'show')):
            for item in link.get('addr_info', []):
                if item.get('local') == str(address):
                    lan = ipaddress.ip_network(str(address) + '/' + str(item['prefixlen']), strict=False)
        if lan is None:
            raise ValueError('MTProto listening address is not assigned to this NAS')
        if not Path('/usr/local/bin/mtg').is_file():
            raise ValueError('MTProto runtime is not installed')
        import pwd
        identity = pwd.getpwnam('nobody')
        self.stop()
        directory = Path('/run/homesim-mtg')
        directory.mkdir(mode=0o700, exist_ok=True)
        os.chown(directory, identity.pw_uid, identity.pw_gid)
        config_path = directory / 'config.toml'
        fd = os.open(config_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'w') as stream:
            stream.write('secret = ' + json.dumps(config['secret']) + '\n')
            stream.write('bind-to = ' + json.dumps(str(address) + ':' + str(port)) + '\n')
            stream.write('concurrency = 256\nprefer-ip = "prefer-ipv4"\nauto-update = false\n[network]\ndns = ""\n')
        os.chown(config_path, identity.pw_uid, identity.pw_gid)
        run('iptables', '-w', '-N', 'HOMESIM_MTG', check=False)
        run('iptables', '-w', '-A', 'HOMESIM_MTG', '-p', 'tcp', '-d', str(address), '--dport', str(port), '!', '-s', str(lan), '-j', 'REJECT')
        run('iptables', '-w', '-I', 'INPUT', '1', '-j', 'HOMESIM_MTG')
        def demote():
            os.setgroups([])
            os.setgid(identity.pw_gid)
            os.setuid(identity.pw_uid)
        self.process = subprocess.Popen(['/usr/local/bin/mtg', 'run', str(config_path)],
                                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                        preexec_fn=demote)
        time.sleep(0.2)
        if self.process.poll() is not None:
            raise ValueError('MTProto failed to start; check listening port and configuration')
        self.config = dict(config)
        return 'running'


def run(*args, input=None, check=True):
    return subprocess.run(args, input=input, text=True, capture_output=True,
                          timeout=15, check=check).stdout.strip()


def validate(data):
    if type(data.get('revision')) is not int or data['revision'] < 1:
        raise ValueError('invalid revision')
    if type(data.get('enabled')) is not bool:
        raise ValueError('invalid enabled flag')
    network = ipaddress.ip_interface(data['address'])
    if network.version != 4 or not network.ip.is_private or not 16 <= network.network.prefixlen <= 30:
        raise ValueError('invalid tunnel address')
    if network.ip in (network.network.network_address, network.network.broadcast_address):
        raise ValueError('reserved tunnel address')
    port = data['listen_port']
    if type(port) is not int or not 1024 <= port <= 65535:
        raise ValueError('invalid port')
    peers = data.get('peers', [])
    if not isinstance(peers, list) or len(peers) > 64:
        raise ValueError('too many peers')
    keys, routes = set(), set()
    for peer in peers:
        key = peer['public_key']
        raw = base64.b64decode(key, validate=True)
        if len(raw) != 32 or not any(raw) or key in keys:
            raise ValueError('invalid or duplicate public key')
        keys.add(key)
        allowed = peer['allowed_ips']
        if len(allowed) != 1:
            raise ValueError('each client needs one tunnel /32 address')
        route = ipaddress.ip_network(allowed[0], strict=True)
        if route.version != 4 or route.prefixlen != 32 or route.network_address not in network.network or route.network_address in (network.ip, network.network.network_address, network.network.broadcast_address) or str(route) in routes:
            raise ValueError('invalid or duplicate client tunnel address')
        routes.add(str(route))
        endpoint = peer.get('endpoint', '')
        if endpoint:
            host, sep, port = endpoint.rpartition(':')
            if not sep or not host or not port.isdecimal() or not 1 <= int(port) <= 65535 or any(c in endpoint for c in '\r\n\t /#=;'):
                raise ValueError('invalid endpoint')
    return network


def atomic(path, data):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, temporary = tempfile.mkstemp(dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump(data, stream)
            stream.flush()
            os.fsync(stream.fileno())
        # Status is non-secret and must be readable by the unprivileged API.
        os.chmod(temporary, 0o644)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def owned_interface():
    result = json.loads(run('ip', '-j', 'link', 'show'))
    for link in result:
        if link['ifname'] == IFACE:
            if link.get('ifalias') != ALIAS:
                raise ValueError('interface belongs to another service')
            return True
    return False


def firewall(network=None):
    # Dedicated chains only; never flush host or Docker chains.
    for table, parent in [('filter', 'FORWARD'), ('nat', 'POSTROUTING')]:
        run('iptables', '-w', '-t', table, '-N', CHAIN, check=False)
        while subprocess.run(['iptables', '-w', '-t', table, '-C', parent, '-j', CHAIN], capture_output=True, timeout=15).returncode == 0:
            run('iptables', '-w', '-t', table, '-D', parent, '-j', CHAIN)
        run('iptables', '-w', '-t', table, '-F', CHAIN)
    if network is None:
        return
    run('iptables', '-w', '-A', CHAIN, '-i', IFACE, '-o', IFACE, '-j', 'DROP')
    run('iptables', '-w', '-A', CHAIN, '-i', IFACE, '-s', str(network.network), '-j', 'ACCEPT')
    run('iptables', '-w', '-A', CHAIN, '-o', IFACE, '-m', 'conntrack', '--ctstate', 'ESTABLISHED,RELATED', '-j', 'ACCEPT')
    run('iptables', '-w', '-A', CHAIN, '-o', IFACE, '-j', 'DROP')
    run('iptables', '-w', '-I', 'FORWARD', '1', '-j', CHAIN)
    run('iptables', '-w', '-t', 'nat', '-A', CHAIN, '-s', str(network.network), '!', '-o', IFACE, '-j', 'MASQUERADE')
    run('iptables', '-w', '-t', 'nat', '-A', 'POSTROUTING', '-j', CHAIN)


def apply(data, key_path):
    network = validate(data)
    exists = owned_interface()
    if not data['enabled']:
        firewall()
        if exists:
            run('ip', 'link', 'delete', IFACE)
        return 'disabled', ''
    # Do not introduce a connected route over an existing LAN or Docker subnet.
    for entry in json.loads(run('ip', '-j', '-4', 'route', 'show', 'table', 'all')):
        destination = entry.get('dst', 'default')
        if destination == 'default' or entry.get('dev') == IFACE:
            continue
        if network.network.overlaps(ipaddress.ip_network(destination, strict=False)):
            raise ValueError('tunnel subnet overlaps an existing host route')
    key_path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if not key_path.exists():
        fd = os.open(key_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as stream:
            stream.write(run('wg', 'genkey') + '\n')
    key = key_path.read_text().strip()
    config = ['[Interface]', 'PrivateKey = ' + key, 'ListenPort = ' + str(data['listen_port'])]
    for peer in data['peers']:
        config += ['[Peer]', 'PublicKey = ' + peer['public_key'], 'AllowedIPs = ' + peer['allowed_ips'][0]]
        if peer.get('endpoint'):
            config += ['Endpoint = ' + peer['endpoint'], 'PersistentKeepalive = 25']
    if not exists:
        run('ip', 'link', 'add', IFACE, 'type', 'wireguard')
        run('ip', 'link', 'set', IFACE, 'alias', ALIAS)
    # Private key is supplied in a protected temporary file, never argv/status.
    fd, path = tempfile.mkstemp(prefix='homesim-wg-', dir='/run')
    try:
        with os.fdopen(fd, 'w') as stream:
            stream.write('\n'.join(config) + '\n')
        run('wg', 'syncconf', IFACE, path)
    finally:
        os.unlink(path)
    run('ip', '-4', 'address', 'flush', 'dev', IFACE)
    run('ip', 'address', 'add', str(network), 'dev', IFACE)
    run('ip', 'link', 'set', IFACE, 'mtu', '1420', 'up')
    run('sysctl', '-w', 'net.ipv4.ip_forward=1')
    firewall(network)
    return 'running', run('wg', 'show', IFACE, 'public-key')


def main():
    directory = Path(os.environ['HOMESIM_WG_DIRECTORY']).resolve(strict=True)
    key_path = Path('/etc/homesim-wireguard/private.key')
    applied = None
    last_good = None
    status = {'state': 'disabled', 'revision': 0}
    mtproto = MTProto()
    def stop_service(*_):
        mtproto.stop()
        firewall()
        if owned_interface():
            run('ip', 'link', 'delete', IFACE)
        raise SystemExit(0)
    signal.signal(signal.SIGTERM, stop_service)
    signal.signal(signal.SIGINT, stop_service)
    while True:
        try:
            path = directory / 'settings.json'
            if path.exists():
                if path.stat().st_size > 65536:
                    raise ValueError('configuration too large')
                data = json.loads(path.read_text())
                if data != applied or (data['enabled'] and not owned_interface()):
                    try:
                        state, public = apply(data, key_path)
                    except Exception:
                        if last_good is not None:
                            apply(last_good, key_path)
                        else:
                            firewall()
                            if owned_interface():
                                run('ip', 'link', 'delete', IFACE)
                        raise
                    applied = data
                    last_good = data
                    status = {'state': state, 'public_key': public, 'revision': data['revision']}
                status['mtproto_state'] = mtproto.reconcile(data.get('mtproto', {}))
            status['error'] = ''
        except Exception as error:
            # Do not leak subprocess input, config, or private keys through errors.
            status = {'state': 'error', 'revision': status.get('revision', 0),
                      'error': str(error) if isinstance(error, ValueError) else 'WireGuard operation failed; inspect host service logs'}
        status['updated_at'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        atomic(directory / 'status.json', status)
        time.sleep(5)


if __name__ == '__main__':
    main()
