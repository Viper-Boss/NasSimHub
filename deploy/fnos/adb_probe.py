"""Minimal raw-USB ADB CNXN probe for QDC507 if6 (bulk out 0x06, in 0x8a)."""
import ctypes
import fcntl
import os
import struct
import sys
import time

DEV = "/dev/bus/usb/002/009"
USBDEVFS_CLAIMINTERFACE = 0x8004550F
USBDEVFS_BULK = 0xC0185502
USBDEVFS_RELEASEINTERFACE = 0x80045510
USBDEVFS_RESET = 0x00005514
USBDEVFS_DISCONNECT = 0x4008551B
USBDEVFS_CONNECT = 0x4008551B + 1  # 0x4008551C


def reset_device(fd):
    try:
        fcntl.ioctl(fd, USBDEVFS_RESET, b"")
        print("usb reset ok")
    except OSError as e:
        print("usb reset failed:", e)


class Bulk(ctypes.Structure):
    _fields_ = [
        ("ep", ctypes.c_int),
        ("len", ctypes.c_int),
        ("timeout", ctypes.c_int),
        ("data", ctypes.c_void_p),
    ]


def xfer(fd, ep, data, timeout=5000):
    buf = ctypes.create_string_buffer(bytes(data), max(len(data), 1))
    pkt = Bulk(ep, len(data), timeout, ctypes.cast(buf, ctypes.c_void_p))
    n = fcntl.ioctl(fd, USBDEVFS_BULK, pkt)
    return n, buf.raw[:n]


def main():
    fd = os.open(DEV, os.O_RDWR)
    try:
        if "--reset" in sys.argv:
            reset_device(fd)
            time.sleep(3)
        fcntl.ioctl(fd, USBDEVFS_CLAIMINTERFACE, struct.pack("I", 6))
        print("claimed interface 6")
        msg = (
            b"CNXN"
            + struct.pack("<II", 0x00010001, 4096)
            + struct.pack("<I", 23)
            + b"host::features=shell_v2"
        )
        n, _ = xfer(fd, 0x06, msg)
        print("wrote CNXN:", n)
        for i in range(3):
            try:
                n, data = xfer(fd, 0x8A, b"\x00" * 512)
                if n == 0:
                    print("read", i, "empty")
                    continue
                cmd = data[:4]
                arg0, arg1, dlen = struct.unpack("<III", data[4:16])
                print("read:", cmd, hex(arg0), hex(arg1), "dlen", dlen, "raw", data[:min(n, 48)])
                if cmd == b"CNXN":
                    print("ADB is ALIVE (unauthenticated connect)")
                    return 0
                if cmd == b"AUTH":
                    print("ADB is ALIVE and demands AUTH, type", arg0)
                    return 2
                if cmd == b"CONN":
                    print("got CONN (html variant?)")
                    return 0
            except OSError as e:
                print("read", i, "error:", e)
        print("NO RESPONSE from adbd — module-side daemon likely not running")
        return 1
    finally:
        try:
            fcntl.ioctl(fd, USBDEVFS_RELEASEINTERFACE, struct.pack("I", 6))
        except OSError:
            pass
        os.close(fd)


if __name__ == "__main__":
    sys.exit(main())
