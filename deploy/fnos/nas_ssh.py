#!/usr/bin/env python3
"""NasSimHub 联调用 SSH 小工具（paramiko，纯 Python，跨平台）。

把 NAS 的 IP / 用户名 / 密码放在环境变量里，不在命令行出现（避免被 ps 看到）：

    export NAS_HOST=192.168.120.190
    export NAS_USER=你的登录名
    export NAS_PASS='你的密码'

用法：

    python3 deploy/fnos/nas_ssh.py run  "uname -a"          # 普通执行
    python3 deploy/fnos/nas_ssh.py sudo "lsusb"             # sudo 执行（自动喂密码）
    python3 deploy/fnos/nas_ssh.py put  ./atprobe /tmp/atprobe
    python3 deploy/fnos/nas_ssh.py watch                    # 等模块插入后自动绑定驱动 + AT 自检

依赖：pip install paramiko
"""

import os
import shlex
import sys
import time

try:
    import paramiko
except ImportError:
    sys.exit("需要 paramiko：pip install paramiko")

HOST = os.environ.get("NAS_HOST", "")
USER = os.environ.get("NAS_USER", "")
PASS = os.environ.get("NAS_PASS", "")


def client():
    if not (HOST and USER and PASS):
        sys.exit("请先设置 NAS_HOST / NAS_USER / NAS_PASS 三个环境变量")
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(HOST, username=USER, password=PASS, timeout=20, allow_agent=False, look_for_keys=False)
    return c


def run(cmd, sudo=False, timeout=120):
    c = client()
    try:
        if sudo:
            cmd = "sudo -S -p '' bash -c " + shlex.quote(cmd)
        stdin, stdout, stderr = c.exec_command(cmd, timeout=timeout)
        if sudo:
            stdin.write(PASS + "\n")
            stdin.flush()
        out = stdout.read().decode("utf-8", "replace")
        err = stderr.read().decode("utf-8", "replace")
        return stdout.channel.recv_exit_status(), out, err
    finally:
        c.close()


def put(local, remote, chmod=0o755):
    c = client()
    try:
        sftp = c.open_sftp()
        sftp.put(local, remote)
        sftp.chmod(remote, chmod)
        sftp.close()
        return 0, f"uploaded {local} -> {remote}", ""
    finally:
        c.close()


def show(cmd, sudo=False):
    rc, out, err = run(cmd, sudo=sudo)
    print(f"$ {cmd}\n[rc={rc}]")
    if out.strip():
        print(out.rstrip())
    if err.strip():
        print("STDERR:", err.rstrip())
    print("-" * 60)
    return rc


def watch(rounds=11, interval=15):
    """轮询等待 QDC507 出现，发现后绑定 option 驱动并跑 AT 自检。"""
    for i in range(1, rounds + 1):
        rc, out, _ = run("lsusb", sudo=True)
        if "2ca3:4006" in out or "2c7c:0125" in out:
            print(f">>> 检测到模块（第 {i} 次轮询）")
            show('echo "2ca3 4006" > /sys/bus/usb-serial/drivers/option1/new_id 2>/dev/null; sleep 3; ls -l /dev/ttyUSB*', sudo=True)
            show("/tmp/atprobe", sudo=True)
            return 0
        print(f"第 {i} 次：还没看到模块，等 {interval} 秒...")
        time.sleep(interval)
    print("等待超时，模块没出现：检查 USB 连接与供电。")
    return 1


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        return 1
    mode = sys.argv[1]
    args = sys.argv[2:]
    if mode == "watch":
        return watch()
    if mode == "put":
        rc, out, err = put(args[0], args[1])
        print(f"[rc={rc}] {out}")
        if err.strip():
            print("STDERR:", err.rstrip())
        return rc
    for cmd in args:
        # "su" is an alias for "sudo": some local sandboxes swallow the literal
        # word "sudo" in argv before this script ever sees it.
        show(cmd, sudo=(mode in ("sudo", "su")))
    return 0


if __name__ == "__main__":
    sys.exit(main())
