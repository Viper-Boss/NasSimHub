# -*- coding: utf-8 -*-
"""Upload a local file to the NAS via SFTP open+write (no size attr).
fnOS's SFTP server rejects paramiko `put` for larger files with a bogus
ENOENT (size attribute in the open request), but plain open+write works.
Usage: python upload_stream.py <local> <remote>
Env: NAS_HOST / NAS_USER / NAS_PASS
"""
import os
import time
import sys

import paramiko


def upload_once(local: str, remote: str) -> tuple[int, int]:
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(os.environ["NAS_HOST"],
                   username=os.environ["NAS_USER"],
                   password=os.environ["NAS_PASS"], timeout=15)
    try:
        sftp = client.open_sftp()
        total = 0
        with open(local, "rb") as f, sftp.open(remote, "wb") as r:
            while True:
                chunk = f.read(1 << 20)
                if not chunk:
                    break
                r.write(chunk)
                total += len(chunk)
        st = sftp.stat(remote)
        return total, st.st_size
    finally:
        client.close()


def main() -> int:
    local, remote = sys.argv[1], sys.argv[2]
    for attempt in range(1, 9):
        try:
            total, rsize = upload_once(local, remote)
            print("uploaded %s -> %s (local %d bytes, remote stat %d)"
                  % (local, remote, total, rsize))
            if rsize != total:
                print("SIZE MISMATCH")
                return 1
            return 0
        except Exception as exc:  # noqa: BLE001 - retry any transport error
            print("attempt %d failed: %r" % (attempt, exc))
            time.sleep(2)
    print("all attempts failed")
    return 1


if __name__ == "__main__":
    sys.exit(main())
