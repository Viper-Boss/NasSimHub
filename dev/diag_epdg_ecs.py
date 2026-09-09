import sys, time

sys.path.insert(0, "dev")
import vowifi_probe as v

s = v.Ssh()

# authoritative reachable from broadband too (DNS is global); no need for ECM
SCRIPT = """
import socket, struct, sys, time

AUTHS = ['118.85.203.176', '118.85.203.178', '118.85.203.180']
TARGET = 'epdg.epc.mnc011.mcc460.pub.3gppnetwork.org'

def build_query(name, qtype, ecs):
    q = b''
    for label in name.split('.'):
        q += bytes([len(label)]) + label.encode()
    q += b'\\x00' + struct.pack('>HH', qtype, 1)
    addr, plen = ecs
    packed = socket.inet_aton(addr)[: (plen + 7) // 8]
    ecs_data = struct.pack('>HBB', 1, plen, 0) + packed
    opt = struct.pack('>HH', 8, len(ecs_data)) + ecs_data
    extra = b'\\x00' + struct.pack('>HHIH', 41, 1280, 0, len(opt)) + opt
    header = struct.pack('>HHHHHH', 0x1234, 0x0100, 1, 0, 0, 1)
    return header + q + extra

def parse(data):
    rcode = data[3] & 0xF
    an = struct.unpack('>H', data[6:8])[0]
    ips = []
    idx = 12
    while data[idx] != 0:
        idx += data[idx] + 1
    idx += 5

    def skip_name(idx):
        while True:
            if data[idx] & 0xC0 == 0xC0:
                return idx + 2
            if data[idx] == 0:
                return idx + 1
            idx += data[idx] + 1

    for _ in range(an):
        idx = skip_name(idx)
        rtype, _c, _t, rdlen = struct.unpack('>HHIH', data[idx:idx+10])
        idx += 10
        rdata = data[idx:idx+rdlen]
        if rtype == 1 and rdlen == 4:
            ips.append(socket.inet_ntoa(rdata))
        elif rtype == 28 and rdlen == 16:
            ips.append(socket.inet_ntop(socket.AF_INET6, rdata))
        idx += rdlen
    return rcode, ips

ECS_VIEWS = [
    ('202.102.128.0', 24, 'Jiangsu-CT'),
    ('202.96.209.0', 24, 'Shanghai-CT'),
    ('202.96.128.0', 24, 'Guangdong-CT'),
    ('219.141.136.0', 24, 'Beijing-CT'),
    ('58.240.0.0', 16, 'Jiangsu-CT-b'),
]

for addr, plen, tag in ECS_VIEWS:
    done = False
    for auth in AUTHS:
        try:
            s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            s.settimeout(6)
            s.sendto(build_query(TARGET, 1, (addr, plen)), (auth, 53))
            d, a = s.recvfrom(1024)
            rcode, ips = parse(d)
            print(tag, auth[-3:], 'rcode', rcode, 'ips', ips)
            s.close()
            done = True
            break
        except Exception as e:
            s.close()
            err = e
    if not done:
        print(tag, 'all timeout', err)
    time.sleep(1.5)
"""

out, err = s.run_root("python3 - <<'PYEOF'\n" + SCRIPT + "\nPYEOF", timeout=180)
print("OUT:")
print(out)
print("ERR:", err[-300:])
