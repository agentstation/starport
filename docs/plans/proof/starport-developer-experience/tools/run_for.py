"""Run a command for N seconds, then send SIGINT and report the exit status.

Usage: run_for.py <seconds> <output-file> <command> [args...]
"""
import signal
import subprocess
import sys
import time

seconds = float(sys.argv[1])
output = sys.argv[2]
command = sys.argv[3:]
with open(output, "w", encoding="utf-8") as sink:
    process = subprocess.Popen(command, stdout=sink, stderr=subprocess.STDOUT)
    try:
        process.wait(timeout=seconds)
        early = True
    except subprocess.TimeoutExpired:
        early = False
        process.send_signal(signal.SIGINT)
        try:
            process.wait(timeout=20)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
    sink.write(f"\n[run_for] exited_early={early} exit={process.returncode}\n")
print(f"exited_early={early} exit={process.returncode}")
