#!/usr/bin/env python3
"""Download a file from the fnOS NAS via SFTP open+read (streamed, with retries).

Mirror of upload_stream.py: fnOS SFTP rejects paramiko.put on larger files
(size attribute quirk), but plain open+read works. Same for downloads.

Usage: download_stream.py <remote_path> <local_path>

Env: NAS_HOST / NAS_USER / NAS_PASS.
Git Bash users: pass REMOTE path with MSYS_NO_PATHCONV=1 (else /tmp gets rewritten);
LOCAL path must be a real Windows path (use cygpath -w), Windows python cannot open /tmp.
"""
import os
import sys
import time

import paramiko


def download_once(remote: str, local: str) -> int:
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(os.environ["NAS_HOST"], username=os.environ["NAS_USER"],
                   password=os.environ["NAS_PASS"], timeout=15)
    try:
        sftp = client.open_sftp()
        total = 0
        with sftp.open(remote, "rb") as r, open(local, "wb") as f:
            while True:
                chunk = r.read(1 << 20)
                if not chunk:
                    break
                f.write(chunk)
                total += len(chunk)
        st = sftp.stat(remote)
        print("downloaded %s -> %s (remote %d bytes, local %d)" % (remote, local, st.st_size, total))
        if st.st_size != total:
            print("SIZE MISMATCH")
            return 1
        return 0
    finally:
        client.close()


def main() -> int:
    if len(sys.argv) != 3:
        print(__doc__)
        return 2
    remote, local = sys.argv[1], sys.argv[2]
    for attempt in range(1, 6):
        try:
            return download_once(remote, local)
        except Exception as exc:  # noqa: BLE001
            print("attempt %d failed: %r" % (attempt, exc))
            time.sleep(2 * attempt)
    print("all attempts failed")
    return 1


if __name__ == "__main__":
    sys.exit(main())
