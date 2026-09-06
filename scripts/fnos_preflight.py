#!/usr/bin/env python3
"""Read-only fnOS/Linux preflight. No AT, ADB, modem reset, or Docker mutation.

JSON goes to stdout only. Never collect IMEI, ICCID, SIM/USB serials, SMS,
phone numbers, hostnames, IP addresses, secrets, or subprocess error output.
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import os
from pathlib import Path
import platform
import re
import shutil
import stat
import subprocess
import sys

SCHEMA = "nas-comm-preflight/v1"
KNOWN_USB = {
    ("2ca3", "4006"): "DJI/Baiwang QDC507 candidate",
    ("2c7c", "0125"): "Quectel EC25-family candidate (model unverified)",
}


def read_text(path: Path) -> str:
    try:
        return path.read_text(encoding="utf-8", errors="replace").strip()
    except OSError:
        return ""


def check(key: str, title: str, status: str, detail: str) -> dict:
    return {"id": key, "title": title, "status": status, "detail": detail}


def bounded_probe(argv: list[str]) -> bool:
    try:
        result = subprocess.run(argv, stdin=subprocess.DEVNULL,
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                timeout=5, check=False, shell=False)
        return result.returncode == 0
    except (OSError, subprocess.TimeoutExpired):
        return False


def usb_inventory(sys_root: Path) -> tuple[list[dict], bool]:
    """Inspect only allowlisted sysfs attributes, not device identity strings."""
    base = sys_root / "bus/usb/devices"
    try:
        paths = sorted(base.iterdir())
    except OSError:
        return [], False
    devices = []
    for path in paths:
        vid = read_text(path / "idVendor").lower()
        pid = read_text(path / "idProduct").lower()
        if (vid, pid) not in KNOWN_USB:
            continue
        drivers = set()
        for interface in base.glob(path.name + ":*"):
            driver = interface / "driver"
            try:
                name = driver.resolve(strict=True).name
                if re.fullmatch(r"[a-zA-Z0-9_-]{1,40}", name):
                    drivers.add(name)
            except OSError:
                pass
        devices.append({
            "label": f"Module {len(devices) + 1}",
            "usb_id": f"{vid}:{pid}",
            "family": KNOWN_USB[(vid, pid)],
            "drivers": sorted(drivers),
        })
    return devices, True


def storage_check(name: str, value: str | None, uid: int, gid: int) -> dict:
    title = "短信与配置目录" if name == "data" else "录音目录"
    if not value:
        return check(name, title, "not_tested", "未指定目录；使用命令参数指定 NAS 上的专用目录。")
    path = Path(value)
    if not path.is_absolute() or ".." in path.parts:
        return check(name, title, "fail", "必须使用不含 .. 的绝对路径。")
    try:
        # Prevent reporting a symlink target as a checked dedicated directory.
        if any(part.is_symlink() for part in (path, *path.parents)):
            return check(name, title, "fail", "目录或其父目录包含符号链接；请使用真实的专用目录。")
        info = path.stat()
        if not stat.S_ISDIR(info.st_mode) or len(path.parts) < 4:
            return check(name, title, "fail", "必须是专用子目录，不能使用根目录或存储卷根目录。")
        owner_write = info.st_uid == uid and info.st_mode & stat.S_IWUSR and info.st_mode & stat.S_IXUSR
        group_write = info.st_gid == gid and info.st_mode & stat.S_IWGRP and info.st_mode & stat.S_IXGRP
        if not (owner_write or group_write):
            return check(name, title, "warn", "POSIX 权限未确认应用用户可写；未创建测试文件，ACL 需另行验证。")
        free = shutil.disk_usage(path).free
        if free < 512 * 1024 * 1024:
            return check(name, title, "warn", "可用空间不足 512 MiB；请先检查空间。")
        return check(name, title, "pass", "目录存在且基础权限匹配；仍需容器内实际写入与重启保留测试。")
    except OSError:
        return check(name, title, "fail", "目录不存在或无法读取；本工具不会创建目录或修改权限。")


def collect(*, sys_root: Path = Path("/sys"), system: str | None = None,
            machine: str | None = None, data_dir: str | None = None,
            recordings_dir: str | None = None, uid: int = 10001, gid: int = 10001,
            probe=bounded_probe) -> dict:
    system = platform.system() if system is None else system
    machine = platform.machine().lower() if machine is None else machine.lower()
    arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(machine, "unsupported")
    linux = system == "Linux"
    checks = [
        check("linux", "Linux 宿主机", "pass" if linux else "fail",
              "已检测到 Linux；本工具不判断具体 NAS 品牌。" if linux else "请在 NAS 的 Linux 宿主机运行，不能用 Windows/macOS 结果代替。"),
        check("architecture", "处理器架构", "pass" if arch != "unsupported" else "fail",
              f"检测结果：{arch}。当前部署基线仅面向 amd64/arm64。"),
    ]
    devices, readable = usb_inventory(sys_root) if linux else ([], False)
    checks.append(check("usb", "候选 4G 模块", "pass" if devices else "warn" if readable else "not_tested",
                        f"识别到 {len(devices)} 个候选模块；USB 标识不能证明型号、短信或语音能力。" if devices else
                        "未识别到已知 USB 标识；可能未插入、未透传、权限不足或型号不在列表。"))
    bound = {driver for device in devices for driver in device["drivers"]}
    for key, title, drivers in [
        ("serial", "候选模块串口驱动", {"option", "qcserial"}),
        ("qmi", "候选模块 QMI 驱动", {"qmi_wwan"}),
        ("audio", "候选模块 USB 音频驱动", {"snd-usb-audio", "snd_usb_audio"}),
    ]:
        checks.append(check(key, title, "pass" if bound & drivers else "not_tested",
                            "至少一个候选模块绑定了对应驱动；不代表该通道可实际使用。" if bound & drivers else
                            "未观察到对应驱动绑定；本工具不会加载驱动、开启接口或切换模式。"))
    docker = shutil.which("docker") if linux else None
    checks.append(check("docker", "本机 Docker", "pass" if docker and probe([docker, "--host", "unix:///var/run/docker.sock", "info"]) else "not_tested",
                        "仅查询本机 Unix socket；失败可能是未安装、未启动或权限不足。"))
    checks.append(check("compose", "Docker Compose", "pass" if docker and probe([docker, "compose", "version"]) else "not_tested",
                        "仅查询 Compose 版本，不拉取镜像或启动容器。"))
    if linux:
        checks.extend([storage_check("data", data_dir, uid, gid), storage_check("recordings", recordings_dir, uid, gid)])
        if data_dir and recordings_dir and Path(data_dir).resolve() == Path(recordings_dir).resolve():
            checks.append(check("storage_separation", "目录隔离", "fail", "数据与录音目录不能是同一个目录。"))
    else:
        checks.extend(check(name, title, "not_tested", "需在 Linux 宿主机验证。") for name, title in [("data", "短信与配置目录"), ("recordings", "录音目录")])
    for key, title in [("sms_receive", "实际收短信"), ("sms_send", "实际发短信"),
                       ("call_in", "实际接听"), ("call_out", "实际拨出"),
                       ("two_way_audio", "真实双向音频"), ("recording", "录音及重启保留")]:
        checks.append(check(key, title, "not_tested", "需要在授权的测试号码和真实硬件上单独验收；只读诊断不执行此操作。"))
    return {"schema": SCHEMA, "generated_at": dt.datetime.now(dt.timezone.utc).isoformat(),
            "source": "host-read-only", "host": {"system": "Linux" if linux else "non-Linux", "architecture": arch},
            "devices": devices, "checks": checks}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data-dir")
    parser.add_argument("--recordings-dir")
    parser.add_argument("--uid", type=int, default=10001)
    parser.add_argument("--gid", type=int, default=10001)
    args = parser.parse_args()
    if args.uid <= 0 or args.gid <= 0:
        parser.error("application UID/GID must be positive and non-root")
    report = collect(**vars(args))
    sys.stdout.reconfigure(encoding="utf-8")
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 2 if any(item["status"] == "fail" for item in report["checks"]) else 0


if __name__ == "__main__":
    raise SystemExit(main())
