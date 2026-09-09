#!/usr/bin/env python3
"""VoWiFi ePDG reachability probe for QDC507 + NAS broadband.

1. Turn modem data on via agent unix socket (optimistic-concurrency PATCH).
2. Wait for ECM interface to get an address.
3. Resolve epdg.epc.mnc011.mcc460.pub.3gppnetwork.org via carrier DNS bound
   to the ECM interface (through the CT cellular path).
4. Probe UDP 500/4500 of the ePDG from cellular and broadband bearers with a
   real IKEv2 IKE_SA_INIT; any reply proves reachability.
5. Turn modem data back off.
"""
import os
import shlex
import socket
import struct
import time

import paramiko

NAS_HOST = os.environ.get("NAS_HOST", "192.168.120.190")
NAS_USER = os.environ.get("NAS_USER", "***REMOVED***")
NAS_PASS = os.environ.get("NAS_PASS", "***REMOVED***")
CONTAINER = "nas-comm-hardware"
SOCKET_PATH = "/run/modemdeck/agent.sock"
ECM_IF = "enx3209127b07e1"
EPDG_FQDN = "epdg.epc.mnc011.mcc460.pub.3gppnetwork.org"
CARRIER_DNS = ["202.101.172.37", "202.101.173.157"]


class Ssh:
    def __init__(self):
        self.client = paramiko.SSHClient()
        self.client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        self.client.connect(NAS_HOST, username=NAS_USER, password=NAS_PASS, timeout=15)

    def run(self, cmd, stdin_text=None, timeout=60):
        _stdin, out, err = self.client.exec_command(cmd, timeout=timeout)
        if stdin_text:
            _stdin.write(stdin_text)
            _stdin.flush()
            _stdin.channel.shutdown_write()
        return out.read().decode("utf-8", "replace"), err.read().decode("utf-8", "replace")

    def run_root(self, script, timeout=60, extra_stdin=None):
        cmd = "sudo -S -p '' bash -c " + shlex.quote(script)
        stdin_text = NAS_PASS + "\n" + (extra_stdin or "")
        return self.run(cmd, stdin_text=stdin_text, timeout=timeout)


def agent_patch(ssh, path, payload):
    """PATCH JSON to agent unix socket; JSON passed via stdin to avoid quoting."""
    inner = (
        f"curl -s -X PATCH --unix-socket {SOCKET_PATH} http://localhost{path} "
        f"-H 'Content-Type: application/json' -d @-"
    )
    out, err = ssh.run_root(
        f"docker exec -i {CONTAINER} sh -c {shlex.quote(inner)}", extra_stdin=payload
    )
    return out.strip().splitlines()[-1] if out.strip() else "", err.strip()


def agent_get(ssh, path):
    inner = f"curl -s --unix-socket {SOCKET_PATH} http://localhost{path}"
    out, _ = ssh.run_root(f"docker exec {CONTAINER} sh -c {shlex.quote(inner)}")
    return out.strip().splitlines()[-1] if out.strip() else ""


LINE_ID = "line_I9mGO66zALFdGy2UIv-K3HoD"


def get_revision(ssh):
    cfg = agent_get(ssh, f"/v1/lines/{LINE_ID}/configuration")
    if '"revision":"' in cfg:
        return cfg.split('"revision":"')[1].split('"')[0]
    return None


def data_on(ssh):
    print("[1] enabling modem data via agent ...")
    body = (
        '{"operation":"connect_data","apn":"","ip_family":"ipv4v6",'
        '"request_id":"vowifi-probe-%d","expected_revision":"%s"}'
        % (int(time.time()), get_revision(ssh) or "")
    )
    out, err = agent_patch(ssh, f"/v1/lines/{LINE_ID}/configuration", body)
    print("    reply:", out[:240] or "(empty)", err[:120] or "")


def wait_ecm(ssh, tries=20):
    print("[2] waiting for ECM address ...")
    for _ in range(tries):
        out, _ = ssh.run(f"ip -4 addr show {ECM_IF} 2>/dev/null")
        for line in out.splitlines():
            line = line.strip()
            if line.startswith("inet "):
                print("    ECM v4:", line.split()[1])
                return True
        time.sleep(3)
    print("    ECM did not come up")
    return False


def resolve_on_nas(ssh, iface, dns, name, qtype):
    script = f"""
import socket, struct, sys
qname = {name!r}
qtype = {qtype}
dns = {dns!r}
iface = {iface!r}
q = b''
for label in qname.split('.'):
    q += bytes([len(label)]) + label.encode()
q += b'\\x00' + struct.pack('>HH', qtype, 1)
pkt = bytes([0x12,0x34,0x01,0x00,0,1,0,0,0,0,0,0]) + q
af = socket.AF_INET6 if ':' in dns else socket.AF_INET
s = socket.socket(af, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, 25, iface.encode() + b'\\x00')
s.settimeout(6)
s.sendto(pkt, (dns, 53))
try:
    data, _ = s.recvfrom(2048)
except Exception as exc:
    sys.stderr.write('fail: %s\\n' % exc)
    sys.exit(0)
flags = data[2:4]
rcode = flags[1] & 0xF
answers = struct.unpack('>H', data[6:8])[0]
sys.stderr.write('dns rcode=%d answers=%d len=%d\\n' % (rcode, answers, len(data)))
idx = 12
while data[idx] != 0:
    idx += data[idx] + 1
idx += 5
ips = []
for _ in range(answers):
    if data[idx] & 0xC0 == 0xC0:
        idx += 2
    else:
        while data[idx] != 0:
            idx += data[idx] + 1
        idx += 1
    rtype, _c, _t, rdlen = struct.unpack('>HHIH', data[idx:idx+10])
    idx += 10
    rdata = data[idx:idx+rdlen]
    idx += rdlen
    if rtype == 1 and rdlen == 4:
        ips.append(socket.inet_ntoa(rdata))
    elif rtype == 28 and rdlen == 16:
        ips.append(socket.inet_ntop(socket.AF_INET6, rdata))
print(' '.join(ips))
"""
    out, err = ssh.run_root("python3 - <<'PYEOF'\n" + script + "\nPYEOF")
    text = out.strip()
    if not text:
        return None, (err.strip() or "empty")[-300:]
    return text.split(), None


def ike_init_packet():
    spi = os.urandom(8)
    nonce = os.urandom(32)
    transforms = [(1, 12), (2, 5), (3, 12), (4, 2)]
    proposal_body = b""
    for ttype, tid in transforms:
        proposal_body += struct.pack(">BBH", 0, ttype, tid)
    proposal_body = struct.pack(">BB", 0, len(transforms)) + proposal_body
    proposal = struct.pack(">BBHBBHI", 2, 0, 8 + len(proposal_body), 1, 0, 0, 3) + proposal_body
    sa_body = struct.pack(">BBH", 0, 1, 0) + proposal
    sa_payload = struct.pack(">BBH", 33, 0, 4 + len(sa_body)) + sa_body
    ke_body = struct.pack(">HH", 2, 0) + os.urandom(128)
    ke_payload = struct.pack(">BBH", 34, 0, 4 + len(ke_body)) + ke_body
    ni_payload = struct.pack(">BBH", 40, 0, 4 + len(nonce)) + nonce
    payloads = sa_payload + ke_payload + ni_payload
    header = spi + b"\x00" * 8 + struct.pack(">BBHII", 33, 0x20, 34, 0x08, len(payloads) + 28)
    return header + payloads


def udp_probe(target, port, iface=None):
    af = socket.AF_INET6 if ":" in target else socket.AF_INET
    s = socket.socket(af, socket.SOCK_DGRAM)
    try:
        if iface:
            s.setsockopt(socket.SOL_SOCKET, 25, iface.encode() + b"\x00")
        s.settimeout(5)
        s.sendto(ike_init_packet(), (target, port))
        data, addr = s.recvfrom(2048)
        return f"reply {len(data)}B from {addr[0]} hdr={data[:4].hex()}"
    except socket.timeout:
        return "timeout (silent)"
    except OSError as exc:
        return f"oserr {exc}"
    finally:
        s.close()


def data_off(ssh):
    print("[5] restoring: modem data off ...")
    body = (
        '{"operation":"disconnect_data","request_id":"vowifi-probe-end-%d",'
        '"expected_revision":"%s"}' % (int(time.time()), get_revision(ssh) or "")
    )
    out, err = agent_patch(ssh, f"/v1/lines/{LINE_ID}/configuration", body)
    print("    reply:", out[:240] or "(empty)", err[:120] or "")


def main():
    ssh = Ssh()
    data_on(ssh)
    ecm_up = wait_ecm(ssh)

    ips = []
    if ecm_up:
        print("[3] resolving ePDG via carrier DNS (through cellular) ...")
        for dns in CARRIER_DNS:
            for qtype, tag in ((1, "A"), (28, "AAAA")):
                got, err = resolve_on_nas(ssh, ECM_IF, dns, EPDG_FQDN, qtype)
                if got:
                    print(f"    {dns}/{tag}: {got}")
                    ips.extend(got)
                    break
                else:
                    print(f"    {dns}/{tag}: fail ({err})")
            if ips:
                break
    else:
        print("[3] skipped (no ECM)")

    print("[4] IKE UDP probes ...")
    if not ips:
        print("    no ePDG address resolved; nothing to probe")
    for ip in dict.fromkeys(ips):
        for path, iface in (("cellular", ECM_IF), ("broadband", None)):
            for port in (500, 4500):
                print(f"    {ip}:{port:<5} via {path:<9} -> {udp_probe(ip, port, iface)}")

    data_off(ssh)
    ssh.client.close()


if __name__ == "__main__":
    main()
