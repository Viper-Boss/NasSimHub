#!/bin/bash
# r117 detach wrapper: outer wrapper holds nohup+disown so ssh session teardown
# cannot reap the build (fnOS kills setsid/sudo-detached children).
nohup bash /tmp/r117_hw_build.sh > /dev/null 2>&1 &
disown
echo "hw build started PID=$! at $(date)"
