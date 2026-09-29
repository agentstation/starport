"""Capture native CI results and qualify named tests against unchanged source."""

import argparse
import hashlib
import importlib.util
import json
import re
import subprocess
from pathlib import Path

# Starmap loads this module by path, outside this directory's import search path.
_shard_spec = importlib.util.spec_from_file_location("starport_app_shards", Path(__file__).with_name("app_shards.py"))
app_shards = importlib.util.module_from_spec(_shard_spec)
_shard_spec.loader.exec_module(app_shards)


REPOSITORY = "agentstation/starport"
RUNNERS = {
    "linux": {"amd64": "ubuntu-24.04", "arm64": "ubuntu-24.04-arm"},
    "darwin": {"arm64": "macos-15"},
    "windows": {"amd64": "windows-2025", "arm64": "windows-11-arm"},
}
PROOF = "docs/proof/catalog-native"


def command(args, root):
    return subprocess.run(args, cwd=root, check=True, capture_output=True, text=True, timeout=120).stdout


def evidence_only(path):
    return path.startswith(("docs/plans/", "docs/proof/"))


def unchanged_source(root, revision):
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("Native evidence requires a complete source commit.")
    changed = command(["git", "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", revision, "--"], root)
    added = command(["git", "ls-files", "--others", "--exclude-standard", "-z"], root)
    if any(path and not evidence_only(path) for path in (changed + added).split("\0")):
        raise ValueError("Native evidence does not match the current source and test inputs.")


def read_bound_file(directory, name, digests):
    if not isinstance(digests, dict):
        raise ValueError("Native evidence has no file digests.")
    data = (directory / name).read_bytes()
    if hashlib.sha256(data).hexdigest() != digests.get(name):
        raise ValueError("A native evidence file differs from its recorded digest.")
    return data.decode("utf-8")


def validate_run(run):
    if not isinstance(run, dict):
        raise ValueError("Native evidence has no workflow record.")
    run_id = run.get("databaseId")
    if type(run_id) is not int or run_id <= 0:
        raise ValueError("Native evidence has no GitHub run identity.")
    if run.get("url") != f"https://github.com/{REPOSITORY}/actions/runs/{run_id}":
        raise ValueError("Native evidence belongs to another repository or run.")
    if run.get("status") != "completed" or run.get("conclusion") != "success":
        raise ValueError("The native CI workflow did not complete successfully.")
    if not re.fullmatch(r"[0-9a-f]{40}", run.get("headSha", "")):
        raise ValueError("Native CI has no complete source commit.")


def validate_platform(directory, proof, system, tests):
    if not isinstance(system, str) or system not in RUNNERS or not isinstance(tests, list) or not tests:
        raise ValueError("Native qualification requires a supported platform and named tests.")
    run = proof["run"]
    if proof.get("format", 1) not in {1, 2, 3}:
        raise ValueError("Unsupported native evidence format.")
    validate_run(run)
    if not isinstance(run.get("jobs"), list) or any(not isinstance(job, dict) for job in run["jobs"]):
        raise ValueError("Native evidence has invalid job records.")
    observations = []
    for arch, runner in RUNNERS[system].items():
        jobs = [job for job in run["jobs"] if job.get("name") == f"Test ({runner})"]
        if len(jobs) != 1 or jobs[0].get("status") != "completed" or jobs[0].get("conclusion") != "success":
            raise ValueError("A required native architecture job did not pass.")
        prefix = f"native-catalog-{runner}/"
        toolchain = read_bound_file(directory, prefix + "toolchain.txt", proof["sha256"]).splitlines()
        cgo = "0" if (system, arch) == ("windows", "arm64") else "1"
        if toolchain != [f"go version go1.27.1 {system}/{arch}", system, arch, system, arch, cgo]:
            raise ValueError("Native evidence has a different toolchain, target, or host.")
        raw = read_bound_file(directory, prefix + "tests.jsonl", proof["sha256"])
        events = [json.loads(line) for line in raw.splitlines() if line.strip()]
        if not events or any(not isinstance(event, dict) or event.get("Action") == "fail" for event in events):
            raise ValueError("Native test evidence is empty or contains failed tests.")
        if runner == "windows-2025" and proof.get("format", 1) in {2, 3}:
            for index in range(app_shards.SHARDS):
                name = f"App test ({runner}, {index})"
                shard_jobs = [job for job in run["jobs"] if job.get("name") == name]
                if len(shard_jobs) != 1 or shard_jobs[0].get("status") != "completed" or shard_jobs[0].get("conclusion") != "success":
                    raise ValueError("A required native application shard job did not pass.")
            app_events, app_toolchain = app_shards.verify_shards(lambda index, name: read_bound_file(
                directory, f"native-catalog-app-{runner}-{index}/{name}", proof["sha256"]), expected_head=run["headSha"])
            if app_toolchain.splitlines() != toolchain:
                raise ValueError("Application shards differ from the native platform toolchain.")
            if any(event.get("Package") == app_shards.PACKAGE for event in events):
                raise ValueError("Sharded application evidence overlaps the remaining native suite.")
            events.extend(app_events)
        if system == "windows" and proof.get("format", 1) == 3:
            for index in range(app_shards.SHARDS):
                name = f"Recovery test ({runner}, {index})"
                shard_jobs = [job for job in run["jobs"] if job.get("name") == name]
                if len(shard_jobs) != 1 or shard_jobs[0].get("status") != "completed" or shard_jobs[0].get("conclusion") != "success":
                    raise ValueError("A required native recovery shard job did not pass.")
            recovery_events, recovery_toolchain = app_shards.verify_shards(lambda index, name: read_bound_file(
                directory, f"native-catalog-recovery-{runner}-{index}/{name}", proof["sha256"]),
                expected_head=run["headSha"], package=app_shards.RECOVERY_PACKAGE)
            if recovery_toolchain.splitlines() != toolchain:
                raise ValueError("Recovery shards differ from the native platform toolchain.")
            if any(event.get("Package") == app_shards.RECOVERY_PACKAGE for event in events):
                raise ValueError("Sharded recovery evidence overlaps the remaining native suite.")
            events.extend(recovery_events)
        for test in tests:
            if not isinstance(test, dict):
                raise ValueError("Native qualification has an invalid test record.")
            package, name = test.get("package", ""), test.get("test", "")
            if not (package == "github.com/agentstation/starport" or package.startswith("github.com/agentstation/starport/")) or not re.fullmatch(r"Test[A-Za-z0-9_]+", name):
                raise ValueError("Native qualification has an invalid named test.")
            matched = [event for event in events if event.get("Package") == package and event.get("Test") == name]
            if any(event.get("Action") == "skip" and event.get("Package") == package and (event.get("Test") == name or str(event.get("Test", "")).startswith(name + "/")) for event in events):
                raise ValueError("A required native test or subtest was skipped.")
            if sum(event.get("Action") == "run" for event in matched) != 1 or sum(event.get("Action") == "pass" for event in matched) != 1:
                raise ValueError("A required native test did not run and pass exactly once.")
            if not any(event.get("Package") == package and not event.get("Test") and event.get("Action") == "pass" for event in events):
                raise ValueError("A required native test package did not complete.")
        observations.append({"platform": system, "architecture": arch, "job_id": jobs[0]["databaseId"], "required_tests": len(tests)})
    return observations


def verify(root, entry):
    directory = root / PROOF
    try:
        proof = json.loads((directory / "capture.json").read_text())
        validate_run(proof["run"])
        unchanged_source(root, proof["run"]["headSha"])
        observations = validate_platform(directory, proof, entry.get("platform"), entry.get("tests"))
        return {"status": "PASS", "scope": "Recorded native component tests on the supported architectures.",
                "run": proof["run"]["url"], "source_commit": proof["run"]["headSha"], "observations": observations}
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        return {"status": "UNVERIFIED", "reason": str(error)}


def capture(root, run_id, directory):
    if directory.exists():
        raise ValueError("Select a new evidence directory to preserve earlier captures.")
    run = json.loads(command(["gh", "run", "view", str(run_id), "--repo", REPOSITORY, "--json",
                              "databaseId,url,headSha,status,conclusion,jobs"], root))
    validate_run(run)
    directory.mkdir(parents=True)
    command(["gh", "run", "download", str(run_id), "--repo", REPOSITORY, "--pattern", "native-catalog-*", "--dir", str(directory)], root)
    digests = {}
    for system, runners in RUNNERS.items():
        for runner in runners.values():
            names = ["toolchain.txt", "tests.jsonl"]
            for name in names:
                path = f"native-catalog-{runner}/{name}"
                digests[path] = hashlib.sha256((directory / path).read_bytes()).hexdigest()
    for index in range(app_shards.SHARDS):
        for name in ("roster.json", "tests.jsonl", "toolchain.txt"):
            path = f"native-catalog-app-windows-2025-{index}/{name}"
            digests[path] = hashlib.sha256((directory / path).read_bytes()).hexdigest()
    format_version = 3 if any(job.get("name", "").startswith("Recovery test (") for job in run["jobs"]) else 2
    if format_version == 3:
        for runner in RUNNERS["windows"].values():
            for index in range(app_shards.SHARDS):
                for name in ("roster.json", "tests.jsonl", "toolchain.txt"):
                    path = f"native-catalog-recovery-{runner}-{index}/{name}"
                    digests[path] = hashlib.sha256((directory / path).read_bytes()).hexdigest()
    proof = {"format": format_version, "run": run, "sha256": digests}
    # Check completeness before retaining a capture. Individual skipped contracts
    # remain unqualified when callers request them later.
    app_shards.verify_shards(lambda index, name: read_bound_file(
        directory, f"native-catalog-app-windows-2025-{index}/{name}", digests), expected_head=run["headSha"])
    if format_version == 3:
        validate_platform(directory, proof, "windows", [{"package": app_shards.RECOVERY_PACKAGE, "test": "TestRecoveryWitnessTransitions"}])
    (directory / "capture.json").write_text(json.dumps(proof, indent=2) + "\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", type=int, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    capture(Path(__file__).resolve().parents[1], args.run, args.output.resolve())
