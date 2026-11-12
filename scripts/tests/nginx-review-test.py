#!/usr/bin/env python3
"""Validate real nginx syntax and the origin metadata trust maps, locally."""
import argparse
import pathlib
import socket
import subprocess
import tempfile
import time
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument("--nginx", required=True)
args = parser.parse_args()
repo = pathlib.Path(__file__).resolve().parents[2]
source = (repo / "web/nginx.conf").read_text()
with tempfile.TemporaryDirectory(prefix="nassimhub-nginx-") as work:
    directory = pathlib.Path(work)
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                    "-keyout", str(directory / "modemdeck-web-key.pem"),
                    "-out", str(directory / "modemdeck-web-cert.pem"), "-days", "1",
                    "-subj", "/CN=localhost"], check=True, stdout=subprocess.DEVNULL,
                   stderr=subprocess.DEVNULL)
    (directory / "mime.types").write_text("types {}\n")
    for port in (7575, 7576):
        (directory / f"modemdeck-origin-{port}.conf").write_text(f"listen {port};\n")
    (directory / "modemdeck-http3.conf").write_text("")
    config = source.replace("/tmp/", work + "/").replace(
        "/etc/nginx/mime.types", str(directory / "mime.types")).replace(
        "/etc/nginx/modemdeck-proxy-common.conf", str(repo / "web/nginx-proxy-common.conf")).replace(
        "/dev/stderr", str(directory / "error.log")).replace(
        "/dev/stdout", str(directory / "access.log"))
    (directory / "full.conf").write_text(config)
    subprocess.run([args.nginx, "-t", "-p", work + "/", "-c", str(directory / "full.conf")], check=True)

    # Exercise the actual production map definitions. Real-IP rewriting is
    # enabled ONLY in this isolated test server to simulate different peers.
    maps = source[source.index("    geo $tunnel_peer"):source.index("    resolver ")]
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    config = f"""pid {work}/test.pid;
error_log stderr;
events {{}}
http {{
access_log off;
client_body_temp_path {work}/body;
proxy_temp_path {work}/proxy;
fastcgi_temp_path {work}/fastcgi;
uwsgi_temp_path {work}/uwsgi;
scgi_temp_path {work}/scgi;
{maps}
server {{
listen 127.0.0.1:{port};
set_real_ip_from 127.0.0.1;
real_ip_header X-Test-Peer;
location / {{ return 200 "$cloudflare_https_redirect|$forwarded_proto|$cloudflare_client_ip"; }}
}}
}}
"""
    (directory / "maps.conf").write_text(config)
    process = subprocess.Popen([args.nginx, "-p", work + "/", "-c", str(directory / "maps.conf"), "-g", "daemon off;"],
                               stdout=subprocess.DEVNULL)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        cases = [
            ("203.0.113.9", "http", "0|http|203.0.113.9"),
            ("203.0.113.9", "https", "0|http|203.0.113.9"),
            ("172.18.0.8", "https", "0|https|198.51.100.7"),
            ("172.18.0.8", "http", "1|http|198.51.100.7"),
            ("172.18.0.8", "invalid", "0|http|198.51.100.7"),
        ]
        for peer, scheme, expected in cases:
            request = urllib.request.Request(f"http://127.0.0.1:{port}/", headers={
                "X-Test-Peer": peer, "X-Forwarded-Proto": scheme, "CF-Connecting-IP": "198.51.100.7"})
            for attempt in range(40):
                try:
                    with opener.open(request, timeout=1) as response:
                        actual = response.read().decode()
                    break
                except OSError:
                    if process.poll() is not None or attempt == 39:
                        raise
                    time.sleep(0.025)
            assert actual == expected, (peer, scheme, actual, expected)
    finally:
        process.terminate()
        process.wait(timeout=5)
print("nginx-review-test: full syntax and five origin trust cases passed")
