"""Run and verify complete native package test shards."""

import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

PACKAGE = "github.com/agentstation/starport/internal/app"
RECOVERY_PACKAGE = "github.com/agentstation/starport/internal/recovery"
PACKAGES = {"app": PACKAGE, "recovery": RECOVERY_PACKAGE}
SHARDS = 4
OWNER = re.compile(r"(?:Test|Example|Fuzz)\w*")


def partition(names, index):
    if type(index) is not int or not 0 <= index < SHARDS:
        raise ValueError("Invalid native package shard index.")
    if not names or names != sorted(set(names)) or any(not OWNER.fullmatch(n) for n in names):
        raise ValueError("Native package discovery must contain unique runnable owners.")
    return [n for n in names if int(hashlib.sha256(n.encode()).hexdigest(), 16) % SHARDS == index]


def validate(roster, events, package=PACKAGE):
    if package not in PACKAGES.values() or roster.get("package", PACKAGE) != package:
        raise ValueError("Native package shard has a different package owner.")
    selected = partition(roster["owners"], roster["index"])
    if roster.get("version") != 1 or roster.get("shards") != SHARDS or roster.get("selected") != selected or not selected:
        raise ValueError("Native package shard has an invalid selection.")
    if not events or any(not isinstance(e, dict) or e.get("Package") != package or e.get("Action") == "fail" for e in events):
        raise ValueError("Native package shard contains foreign or failed results.")
    owners = [e for e in events if e.get("Test") and "/" not in e["Test"]]
    runs = Counter(e["Test"] for e in owners if e.get("Action") == "run")
    completed = Counter(e["Test"] for e in owners if e.get("Action") in {"pass", "skip"})
    expected = Counter(selected)
    if runs != expected or completed != expected:
        raise ValueError("Every selected native package owner must run and finish exactly once.")
    for event in events:
        if event.get("Test") and event["Test"].split("/")[0] not in selected:
            raise ValueError("Native package shard contains an unselected owner.")
    packages = [e for e in events if not e.get("Test") and e.get("Action") in {"pass", "skip"}]
    if len(packages) != 1 or packages[0]["Action"] != "pass":
        raise ValueError("Native package shard did not complete successfully.")


def verify_shards(load, *, expected_source=None, expected_head=None, package=PACKAGE):
    """Read each raw shard through a caller-owned file or digest boundary."""
    names, source, toolchain, head = None, None, None, None
    combined = []
    for index in range(SHARDS):
        roster = json.loads(load(index, "roster.json"))
        if not isinstance(roster, dict) or roster.get("index") != index:
            raise ValueError("Native package shard index differs from its artifact.")
        current_toolchain = load(index, "toolchain.txt")
        if not re.fullmatch(r"[0-9a-f]{40}", roster.get("source", "")):
            raise ValueError("Native package shard has no source identity.")
        if not re.fullmatch(r"[0-9a-f]{40}", roster.get("workflow_head", "")):
            raise ValueError("Native package shard has no workflow source identity.")
        if expected_source is not None and roster["source"] != expected_source:
            raise ValueError("Native package shard differs from the checked-out source.")
        if expected_head is not None and roster["workflow_head"] != expected_head:
            raise ValueError("Native package shard belongs to another workflow head.")
        if names is None:
            names, source, toolchain, head = roster["owners"], roster["source"], current_toolchain, roster["workflow_head"]
        if (roster["owners"], roster["source"], current_toolchain, roster["workflow_head"]) != (names, source, toolchain, head):
            raise ValueError("Native package shards disagree on discovery, source, or toolchain.")
        events = [json.loads(line) for line in load(index, "tests.jsonl").splitlines() if line.strip()]
        validate(roster, events, package)
        combined.extend(events)
    selected = Counter(e["Test"] for e in combined if e.get("Test") and "/" not in e["Test"] and e.get("Action") == "run")
    if selected != Counter(names):
        raise ValueError("Native package shards do not cover every discovered owner exactly once.")
    return combined, toolchain


def run(root, output, index, package=PACKAGE, race=True):
    if package not in PACKAGES.values() or type(race) is not bool:
        raise ValueError("Native package selection or race mode is invalid.")
    path = "./" + package.removeprefix("github.com/agentstation/starport/")
    race_flags = ["-race"] if race else []
    output.mkdir(parents=True, exist_ok=False)
    def command(args, timeout=60):
        return subprocess.check_output(args, cwd=root, encoding="utf-8", timeout=timeout)
    listing = command(["go", "test", *race_flags, "-list", "^(Test|Example|Fuzz)", path], timeout=900)
    names = sorted(line for line in listing.splitlines() if OWNER.fullmatch(line))
    selected = partition(names, index)
    if not selected:
        raise ValueError("Native package shard has no runnable owners.")
    roster = {"version": 1, "shards": SHARDS, "index": index, "owners": names,
              "selected": selected, "package": package, "source": command(["git", "rev-parse", "HEAD"]).strip()}
    roster["workflow_head"] = os.environ.get("STARPORT_TEST_HEAD_SHA", roster["source"])
    (output / "roster.json").write_text(json.dumps(roster, indent=2) + "\n", encoding="utf-8")
    (output / "toolchain.txt").write_text(command(["go", "version"]) + command(["go", "env", "GOOS", "GOARCH", "GOHOSTOS", "GOHOSTARCH", "CGO_ENABLED"]), encoding="utf-8")
    expression = "^(" + "|".join(re.escape(n) for n in selected) + ")$"
    print(f"Running {package} shard {index}: {len(selected)} discovered owners.", flush=True)
    # Each shard runs a quarter of its package. The recovery package needs the
    # larger allowance. Individual contract deadlines remain unchanged.
    timeout = "20m" if package == RECOVERY_PACKAGE else "12m"
    args = ["go", "test", "-json", *race_flags, "-count=1", "-timeout", timeout, "-run", expression, path]
    with (output / "tests.jsonl").open("w", encoding="utf-8") as stream:
        result = subprocess.run(args, cwd=root, stdout=stream, timeout=1500, check=False)
    # Keep the native Go exit status. Validation cannot turn a failed run green.
    if result.returncode:
        return result.returncode
    events = [json.loads(line) for line in (output / "tests.jsonl").read_text(encoding="utf-8").splitlines() if line.strip()]
    validate(roster, events, package)
    print(f"{package} shard {index}: {len(selected)} owners completed.")
    return 0


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    execute = commands.add_parser("run")
    execute.add_argument("--index", required=True, type=int)
    execute.add_argument("--output", required=True, type=Path)
    execute.add_argument("--package", choices=PACKAGES, default="app")
    execute.add_argument("--no-race", action="store_true")
    verify = commands.add_parser("verify")
    verify.add_argument("--directory", required=True, type=Path)
    verify.add_argument("--package", choices=PACKAGES, default="app")
    verify.add_argument("--runner", choices=("windows-2025", "windows-11-arm"), default="windows-2025")
    args = parser.parse_args()
    if args.command == "run":
        sys.exit(run(Path(__file__).resolve().parents[1], args.output.resolve(), args.index, PACKAGES[args.package], not args.no_race))
    source = subprocess.check_output(["git", "rev-parse", "HEAD"], encoding="utf-8", timeout=60).strip()
    events, _ = verify_shards(lambda index, name: (args.directory / f"native-catalog-{args.package}-{args.runner}-{index}" / name).read_text(encoding="utf-8"),
                             expected_source=source, expected_head=os.environ.get("STARPORT_TEST_HEAD_SHA", source), package=PACKAGES[args.package])
    print(f"Verified {SHARDS} complete {args.package} shards ({len(events)} raw events).")
