"""Run required catalog capacity tests and retain exact named evidence."""

import argparse
import hashlib
import json
import os
import platform
import shutil
import signal
import subprocess
import time
from pathlib import Path

PACKAGE = "github.com/agentstation/starport/internal/catalog"
DECODED_TEST = "TestRecoveryCatalogCapacityPacksDecodedProducerLimit"
MAXIMUM_TEST = "TestRecoveryCatalogMaximumSerializedInventoryRoundTrip"
PROFILES = {
    "decoded-race": {"test": DECODED_TEST, "flags": ["-race"], "cgo": "1", "timeout": "6m", "deadline": 480},
    "decoded-pure": {"test": DECODED_TEST, "flags": [], "cgo": "0", "timeout": "3m", "deadline": 240},
    "maximum-pure": {"test": MAXIMUM_TEST, "flags": [], "cgo": "0", "timeout": "28m", "deadline": 1800,
                     "host_gib": 24, "free_disk_gib": 26, "go_gib": 12, "rss_gib": 16, "scratch_gib": 24},
}
GIB = 1 << 30


class CapacityBoundExceeded(ValueError):
    def __init__(self, elapsed, rss, disk):
        super().__init__("Capacity run exceeded its RSS, scratch disk, or duration bound.")
        self.measurements = {"elapsed_seconds": elapsed, "sampled_tree_peak_rss_bytes": rss, "sampled_scratch_allocated_peak_bytes": disk}


def check_named_result(events, name):
    if not events or any(not isinstance(item, dict) for item in events):
        raise ValueError("Capacity evidence is empty or malformed.")
    if any(item.get("Action") == "fail" for item in events):
        raise ValueError("A capacity test or package failed.")
    selected = [item for item in events if item.get("Package") == PACKAGE and item.get("Test") == name]
    nested = [item for item in events if item.get("Package") == PACKAGE and (item.get("Test") == name or str(item.get("Test", "")).startswith(name + "/"))]
    if any(item.get("Action") == "skip" for item in nested):
        raise ValueError("A required capacity test was skipped.")
    if sum(item.get("Action") == "run" for item in selected) != 1 or sum(item.get("Action") == "pass" for item in selected) != 1:
        raise ValueError("A required capacity test did not run and pass exactly once.")
    if sum(item.get("Package") == PACKAGE and not item.get("Test") and item.get("Action") == "pass" for item in events) != 1:
        raise ValueError("The capacity package did not pass exactly once.")


def maximum_evidence(events):
    output = "".join(item.get("Output", "") for item in events if item.get("Package") == PACKAGE and item.get("Test") == MAXIMUM_TEST)
    records = [line.partition("CATALOG_MAXIMUM_EVIDENCE ")[2] for line in output.splitlines() if "CATALOG_MAXIMUM_EVIDENCE " in line]
    if len(records) != 1:
        raise ValueError("Maximum capacity requires one complete native workload record.")
    record = json.loads(records[0])
    if record.get("entries") != 96 or not (2 * GIB - (8 << 20) <= record.get("serialized_fleet_snapshot_bytes", -1) <= 2 * GIB):
        raise ValueError("Maximum evidence did not reach the native serialized inventory boundary.")
    if not record.get("accepted_generation") or not record.get("candidate_generation") or record["accepted_generation"] == record["candidate_generation"]:
        raise ValueError("Maximum evidence lost distinct accepted and candidate selections.")
    for name in ("original_manifest_sha256", "fleet_manifest_sha256", "reverse_manifest_sha256"):
        value = record.get(name, "")
        if len(value) != 64 or any(character not in "0123456789abcdef" for character in value):
            raise ValueError("Maximum evidence omits an original full backup identity.")
    batches = record.get("producer_batches", [])
    if not batches or sum(batch.get("inputs", 0) for batch in batches) != 192:
        raise ValueError("Maximum evidence lost forward or reverse retention coverage.")
    for batch in batches:
        if not (0 < batch.get("inputs", 0) <= 4096 and 0 < batch.get("raw_bytes", 0) <= 256 << 20 and 0 < batch.get("decoded_recovery_bytes", 0) <= 256 << 20):
            raise ValueError("Maximum evidence exceeds a producer batch dimension.")
    for name in ("forward_stages", "reverse_stages"):
        stages = record.get(name, [])
        if not stages or [stage.get("index") for stage in stages] != list(range(len(stages))):
            raise ValueError("Maximum evidence omits ordered native transactions.")
        if any(not (0 < stage.get("mutations", 0) <= 128 and 0 < stage.get("bytes", 0) <= 4 << 20) for stage in stages):
            raise ValueError("Maximum evidence exceeds a native transaction dimension.")
    if any(not (0 < record.get(name, 0) <= 64 << 10) for name in ("forward_compiler_seal_bytes", "reverse_compiler_seal_bytes")):
        raise ValueError("Maximum evidence exceeds a compiler record bound.")
    if record.get("rollback_entries") != 32 or record.get("lost_reply_exact_retries") != 2:
        raise ValueError("Maximum evidence omits rollback or exact-retry coverage.")
    return record


def scratch_bytes(directory):
    seen, total = set(), 0
    for parent, _, names in os.walk(directory):
        for name in names:
            try:
                state = (Path(parent) / name).stat(follow_symlinks=False)
            except FileNotFoundError:
                continue
            identity = (state.st_dev, state.st_ino)
            if identity not in seen:
                seen.add(identity)
                total += state.st_blocks * 512
    return total


def tree_rss(pid):
    output = subprocess.run(["ps", "-e", "-o", "pid=", "-o", "ppid=", "-o", "rss="], check=True, capture_output=True, text=True, timeout=5).stdout
    rows = [tuple(map(int, line.split())) for line in output.splitlines() if line.strip()]
    descendants = {pid}
    while True:
        found = descendants | {child for child, parent, _ in rows if parent in descendants}
        if found == descendants:
            return sum(rss * 1024 for child, _, rss in rows if child in descendants)
        descendants = found


def run_measured(command, root, environment, stdout, stderr, scratch, deadline, rss_limit, disk_limit):
    started = time.monotonic()
    sampled_rss, sampled_disk = 0, 0
    with stdout.open("wb") as out, stderr.open("wb") as err:
        process = subprocess.Popen(command, cwd=root, env=environment, stdout=out, stderr=err, start_new_session=True)
        try:
            while True:
                pid, status, usage = os.wait4(process.pid, os.WNOHANG)
                if pid:
                    process.returncode = os.waitstatus_to_exitcode(status)
                    scale = 1 if platform.system() == "Darwin" else 1024
                    return {"exit_code": process.returncode, "elapsed_seconds": time.monotonic() - started,
                            "largest_process_high_water_rss_bytes": int(usage.ru_maxrss) * scale,
                            "sampled_tree_peak_rss_bytes": sampled_rss, "sampled_scratch_allocated_peak_bytes": sampled_disk}
                sampled_rss = max(sampled_rss, tree_rss(process.pid))
                sampled_disk = max(sampled_disk, scratch_bytes(scratch))
                if sampled_rss > rss_limit or sampled_disk > disk_limit or time.monotonic() - started > deadline:
                    raise CapacityBoundExceeded(time.monotonic() - started, sampled_rss, sampled_disk)
                time.sleep(1)
        finally:
            if process.returncode is None:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                _, status, _ = os.wait4(process.pid, 0)
                process.returncode = os.waitstatus_to_exitcode(status)


def command_output(command, root, environment):
    return subprocess.run(command, cwd=root, env=environment, check=True, capture_output=True, text=True, timeout=120).stdout.strip()


def source_state(root, environment):
    diff = subprocess.run(["git", "diff", "--binary", "HEAD", "--"], cwd=root, env=environment, check=True, capture_output=True, timeout=120).stdout
    untracked = subprocess.run(["git", "ls-files", "--others", "--exclude-standard", "-z"], cwd=root, env=environment, check=True, capture_output=True, timeout=120).stdout
    return {"head": command_output(["git", "rev-parse", "HEAD"], root, environment),
            "diff_sha256": hashlib.sha256(diff).hexdigest(),
            "untracked_sha256": {name: hashlib.sha256((root / name).read_bytes()).hexdigest() for name in sorted(untracked.decode().strip("\0").split("\0")) if name}}


def run_profile(root, output, profile, provisional):
    if output.exists():
        raise ValueError("Use a new capacity evidence directory.")
    output.mkdir(mode=0o700)
    scratch = output / "scratch"
    record = {"profile": profile, "qualification": "provisional-workspace" if provisional else "published-module"}
    try:
        config = PROFILES[profile]
        if platform.system() not in {"Linux", "Darwin"}:
            raise ValueError("Capacity measurement requires a Linux or macOS host.")
        total_memory = os.sysconf("SC_PAGE_SIZE") * os.sysconf("SC_PHYS_PAGES") if platform.system() == "Linux" else int(command_output(["sysctl", "-n", "hw.memsize"], root, os.environ.copy()))
        if total_memory < config.get("host_gib", 12) * GIB:
            raise ValueError("Capacity qualification requires its profile's minimum host RAM.")
        if shutil.disk_usage(output.parent).free < config.get("free_disk_gib", 2) * GIB:
            raise ValueError("Capacity qualification requires its profile's free disk budget.")
        go_limit, rss_limit, disk_limit = config.get("go_gib", 4), config.get("rss_gib", 8) * GIB, config.get("scratch_gib", 1) * GIB
        environment = os.environ.copy()
        environment.update({"CGO_ENABLED": config["cgo"], "GOMEMLIMIT": str(go_limit) + "GiB"})
        if not provisional:
            if environment.get("GOWORK", "off") != "off":
                raise ValueError("Published-module qualification refuses a Go workspace.")
            environment["GOWORK"] = "off"
        if command_output(["go", "env", "GOVERSION"], root, environment) != "go1.27.2":
            raise ValueError("Capacity qualification requires exactly Go 1.27.2.")
        module = json.loads(command_output(["go", "list", "-m", "-json", "github.com/agentstation/starmap"], root, environment))
        if not provisional and (module.get("Replace") is not None or module.get("Main") or not module.get("Version")):
            raise ValueError("Published-module qualification refuses a producer replacement or unpublished module.")
        scratch.mkdir(mode=0o700)
        environment.update({"TMPDIR": str(scratch), "GOTMPDIR": str(scratch)})
        command = ["go", "test", "-json", "-tags=catalogcapacity", "-count=1", "-p=1", "-timeout=" + config["timeout"], "-run=^" + config["test"] + "$", *config["flags"], "./internal/catalog"]
        record.update({"source": source_state(root, environment), "toolchain": command_output(["go", "version"], root, environment),
                       "producer_module": module, "command": command, "host_memory_bytes": total_memory,
                       "bounds": {"go_memory_target_bytes": go_limit * GIB, "sampled_tree_rss_bytes": rss_limit, "scratch_allocated_bytes": disk_limit, "deadline_seconds": config["deadline"]}})
        record["measurements"] = run_measured(command, root, environment, output / "tests.jsonl", output / "stderr.log", scratch, config["deadline"], rss_limit, disk_limit)
        if record["measurements"]["exit_code"]:
            raise ValueError("Capacity command failed; see its retained evidence.")
        events = [json.loads(line) for line in (output / "tests.jsonl").read_text().splitlines() if line.strip()]
        check_named_result(events, config["test"])
        if profile == "maximum-pure":
            record["native_workload"] = maximum_evidence(events)
        record["status"] = "PASS"
    except Exception as error:
        record.update({"status": "FAIL", "error": str(error)})
        if isinstance(error, CapacityBoundExceeded):
            record["measurements"] = error.measurements
        raise
    finally:
        record["test_evidence_sha256"] = hashlib.sha256((output / "tests.jsonl").read_bytes()).hexdigest() if (output / "tests.jsonl").exists() else None
        (output / "verification.json").write_text(json.dumps(record, indent=2) + "\n")
        if scratch.exists():
            shutil.rmtree(scratch)
    return record


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("profile", choices=PROFILES)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--provisional-workspace", action="store_true", help="Label paired unpublished API development; never used by CI.")
    arguments = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    run_profile(root, arguments.output.resolve(), arguments.profile, arguments.provisional_workspace)


if __name__ == "__main__":
    main()
