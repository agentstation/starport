"""Run a command under a pseudo-terminal for N seconds, then SIGINT it.

Usage: run_for_pty.py <seconds> <output-file> <command> [args...]
"""
import os
import pty
import select
import signal
import sys
import time

seconds = float(sys.argv[1])
output = sys.argv[2]
command = sys.argv[3:]

pid, fd = pty.fork()
if pid == 0:
    os.execvp(command[0], command)

deadline = time.time() + seconds
early = False
status = None
with open(output, "wb") as sink:
    while True:
        remaining = deadline - time.time()
        if remaining <= 0:
            break
        ready, _, _ = select.select([fd], [], [], min(remaining, 0.25))
        if ready:
            try:
                chunk = os.read(fd, 65536)
            except OSError:
                chunk = b""
            if not chunk:
                early = True
                break
            sink.write(chunk)
            sink.flush()
    if not early:
        os.kill(pid, signal.SIGINT)
        end = time.time() + 20
        while time.time() < end:
            ready, _, _ = select.select([fd], [], [], 0.25)
            if ready:
                try:
                    chunk = os.read(fd, 65536)
                except OSError:
                    break
                if not chunk:
                    break
                sink.write(chunk)
                sink.flush()
            done, status = os.waitpid(pid, os.WNOHANG)
            if done:
                break
        else:
            os.kill(pid, signal.SIGKILL)
    if status is None:
        _, status = os.waitpid(pid, 0)
    code = os.waitstatus_to_exitcode(status)
    sink.write(f"\n[run_for_pty] exited_early={early} exit={code}\n".encode())
print(f"exited_early={early} exit={code}")
