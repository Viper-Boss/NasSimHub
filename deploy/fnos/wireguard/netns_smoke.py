#!/usr/bin/env python3
"""Root-only, disposable network-namespace handshake test. No production peers."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import uuid


def run(*args, input=None):
    return subprocess.run(args, input=input, text=True, capture_output=True, timeout=30, check=True).stdout.strip()


def main():
    suffix = uuid.uuid4().hex[:8]
    server, client = 'hs-test-s-'+suffix, 'hs-test-c-'+suffix
    created = []
    with tempfile.TemporaryDirectory(prefix='homesim-netns-') as directory:
        base = Path(directory)
        try:
            for ns in [server,client]:
                run('ip','netns','add',ns); created.append(ns)
                run('ip','-n',ns,'link','set','lo','up')
            run('ip','link','add','hs-s-'+suffix,'type','veth','peer','name','hs-c-'+suffix)
            for ns,iface,addr in [(server,'hs-s-'+suffix,'192.0.2.1/24'),(client,'hs-c-'+suffix,'192.0.2.2/24')]:
                run('ip','link','set',iface,'netns',ns)
                run('ip','-n',ns,'addr','add',addr,'dev',iface)
                run('ip','-n',ns,'link','set',iface,'up')
            key = run('wg','genkey')
            public = run('wg','pubkey',input=key+'\n')
            config=dict(revision=1,enabled=True,address='10.77.0.1/24',listen_port=51820,peers=[dict(public_key=public,endpoint='192.0.2.2:51821',allowed_ips=['10.77.0.2/32'])])
            (base/'settings.json').write_text(json.dumps(config))
            code='import json,sys;from pathlib import Path;sys.path.insert(0,sys.argv[1]);from runtime import apply;s=json.loads(Path(sys.argv[2]).read_text());state,key=apply(s,Path(sys.argv[3]));print(state);print(key)'
            lines=run('ip','netns','exec',server,'python3','-c',code,str(Path(__file__).parent),str(base/'settings.json'),str(base/'server.key')).splitlines()
            assert lines[0]=='running'
            peer_config=base/'client.conf'
            peer_config.write_text('[Interface]\nPrivateKey = '+key+'\nListenPort = 51821\n[Peer]\nPublicKey = '+lines[1]+'\nAllowedIPs = 10.77.0.1/32\nEndpoint = 192.0.2.1:51820\nPersistentKeepalive = 1\n')
            peer_config.chmod(0o600)
            run('ip','-n',client,'link','add','wgtest','type','wireguard')
            run('ip','netns','exec',client,'wg','setconf','wgtest',str(peer_config))
            run('ip','-n',client,'addr','add','10.77.0.2/24','dev','wgtest')
            run('ip','-n',client,'link','set','wgtest','up')
            run('ip','netns','exec',client,'ping','-c','2','-W','3','10.77.0.1')
            handshakes=run('ip','netns','exec',server,'wg','show','hswg0','latest-handshakes')
            assert int(handshakes.split()[-1])>0
            config['enabled']=False;config['revision']=2
            (base/'settings.json').write_text(json.dumps(config))
            assert run('ip','netns','exec',server,'python3','-c',code,str(Path(__file__).parent),str(base/'settings.json'),str(base/'server.key')).splitlines()[0]=='disabled'
            print('PASS: isolated WireGuard apply, handshake, encrypted ping, disable')
        finally:
            for ns in reversed(created):
                subprocess.run(['ip','netns','delete',ns],capture_output=True,timeout=15)


if __name__=='__main__':
    main()
