# -*- coding: utf-8 -*-
"""Collect NKN mainnet node IPs from official seeds, geo-filter China,
then probe each China node's RPC (30003-style) reachability and latency."""
import json
import sys
import time
import urllib.request
import concurrent.futures

SEEDS = [
    "http://seed.nkn.org:30003",
] + [f"http://mainnet-seed-{i:04d}.nkn.org:30003" for i in range(1, 45)]

ROUNDS = 3
ROUND_INTERVAL = 20

def rpc(seed, method, params=None, timeout=6):
    body = json.dumps({"jsonrpc": "2.0", "method": method,
                       "params": params or {}, "id": 1}).encode()
    req = urllib.request.Request(seed, data=body,
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read().decode())

def collect_addrs():
    addrs = set()
    import re
    for round_no in range(ROUNDS):
        if round_no:
            print(f"[collect] round {round_no+1}/{ROUNDS}, sleeping {ROUND_INTERVAL}s "
                  f"for chord ring rotation ...", flush=True)
            time.sleep(ROUND_INTERVAL)
        base = len(addrs)
        for seed in SEEDS:
            try:
                resp = rpc(seed, "getchordringinfo")
                text = json.dumps(resp.get("result", ""))
                for m in re.finditer(r"(\d{1,3}(?:\.\d{1,3}){3}):(\d{2,5})", text):
                    addrs.add((m.group(1), int(m.group(2))))
            except Exception:
                pass
        print(f"[collect] round {round_no+1}: total {len(addrs)} (+{len(addrs)-base})",
              flush=True)
    return addrs

def geo_batch(ips):
    out = {}
    for i in range(0, len(ips), 100):
        chunk = ips[i:i+100]
        body = json.dumps(chunk).encode()
        req = urllib.request.Request(
            "http://ip-api.com/batch?fields=status,country,countryCode,query,isp",
            data=body, headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=15) as r:
            for row in json.loads(r.read().decode()):
                out[row.get("query")] = row
        time.sleep(1)
    return out

def probe_rpc(target, timeout=5):
    """target = (ip, rpc_port). Try a lightweight RPC call."""
    ip, port = target
    url = f"http://{ip}:{port}"
    body = json.dumps({"jsonrpc": "2.0", "method": "getconnectioncount",
                       "params": {}, "id": 1}).encode()
    req = urllib.request.Request(url, data=body,
                                 headers={"Content-Type": "application/json"})
    t0 = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            payload = r.read().decode()[:120]
            return (time.time() - t0) * 1000, payload
    except Exception as e:
        return None, f"{type(e).__name__}"

def main():
    addrs = collect_addrs()
    ips = sorted({ip for ip, _ in addrs})
    print(f"\n[geo] resolving {len(ips)} unique IPs ...", flush=True)
    geo = geo_batch(ips)
    cn = {ip: g for ip, g in geo.items()
          if g.get("status") == "success" and g.get("countryCode") == "CN"}
    print(f"[geo] China nodes by IP: {len(cn)}", flush=True)
    for ip, g in sorted(cn.items()):
        print(f"  CN {ip}  isp={g.get('isp','')[:40]}", flush=True)

    # candidate RPC ports for CN nodes: known port from chord + 30003
    cn_ports = {}
    for ip, port in addrs:
        if ip in cn:
            cn_ports.setdefault(ip, set()).add(port)
    probes = []
    for ip, ports in cn_ports.items():
        for p in sorted(ports):
            probes.append((ip, p))
        if 30003 not in ports:
            probes.append((ip, 30003))

    print(f"\n[probe] probing {len(probes)} CN endpoints ...", flush=True)
    results = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=20) as ex:
        futs = {ex.submit(probe_rpc, t): t for t in probes}
        for f in concurrent.futures.as_completed(futs):
            t = futs[f]
            ms, info = f.result()
            results.append((t, ms, info))
            tag = f"{ms:7.1f}ms" if ms else "   FAIL"
            print(f"  {t[0]}:{t[1]}  {tag}  {info if not ms else ''}", flush=True)

    ok = [(t, ms) for t, ms, info in results if ms]
    ok.sort(key=lambda x: x[1])
    print(f"\n=== RESULT: {len(ok)} reachable CN RPC endpoints ===", flush=True)
    for (ip, port), ms in ok:
        print(f"http://{ip}:{port}   # {ms:.0f}ms", flush=True)
    with open("nkn_cn_nodes.json", "w", encoding="utf-8") as f:
        json.dump({"china_ips": {ip: geo[ip] for ip in cn},
                   "reachable": [{"url": f"http://{ip}:{port}", "ms": ms}
                                 for (ip, port), ms in ok]},
                  f, ensure_ascii=False, indent=1)
    print("saved nkn_cn_nodes.json", flush=True)

if __name__ == "__main__":
    sys.exit(main())
