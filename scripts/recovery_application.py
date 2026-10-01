"""Qualify named application recovery contracts against native storage owners."""

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
MODES = ("pure", "race")
LEGACY_OWNERS = (
    "TestRestoreApplicationSharedRecipe", "TestRestorePublishFilesSharedRecipe",
    "TestUnprefixedBackupRequiresValkeyBeforeOpeningStores", "TestUnprefixedBackupMigratesVerifiedRecordsUnderBarriers",
)
GROUPS = {
    "activation": (
        "TestRecoveryActivationFreshProcessNativeBoundaries",
        "TestRecoveryActivationCurrentChoiceOrOriginalEvidenceFailureKeepsRemainingOwnersClosed",
        "TestRecoveryActivationCompletedRetryPreservesLaterWithdrawal",
        "TestRecoveryActivationSQLApprovalRejectsChangedCurrentSource",
        "TestRecoveryActivationNativeTopologyRecipes",
        "TestRecoveryActivationBoundedSourceNormalStartup",
        "TestRecoveryActivationExactPendingPhaseRestart",
        "TestRecoveryActivationJournalExactPublicationProcessExit",
        "TestRecoveryActivationSealedRetryRequiresOriginalSelector",
        "TestRecoveryCanonicalCheckedSourceRefusesChangedOriginalBeforeRelease",
        "TestRecoveryCanonicalFilesChecksRoleOwnersAndInactiveEvidence",
        "TestRecoveryCanonicalFilesRefusesDiagnosticOnlyOrSubstitutedOriginals",
        "TestRecoveryCanonicalFilesPassiveRestartRefusesChangedEvidenceAndTargets",
        "TestRecoveryCanonicalFilesFreshProcessAfterBlobRelease",
        "TestRecoveryCanonicalFilesRefusesUnresolvedSelectedRoleWithoutPreparation",
    ),
    "gateway": (
        "TestRecoveryFreshGatewayRejectsUnapprovedHistoryBeforeEffects",
        "TestRecoveryOldWarmedGatewayRefusesAfterKnownClosure",
        "TestRecoveredSharedGatewayFreshStartupAndBudgetAdmission",
        "TestRecoveryActivationReplaysActualPostBackupRevocationSpendAndUncertainDispatch",
        "TestRecoveryFreshReplicaActualHistoryBarriers",
    ),
    "operator": (
        "TestRecoveryActivationLocalOperatorCommands",
        "TestRecoveryPopulatedOperatorCommandsAcrossNativePhaseCut",
        "TestRecoveryOperatorInputsSharedNativeTargets",
        "TestRecoveryOperatorInputsInspectionNeverCreatesMissingDecision",
        "TestRecoveryOperatorInputsInspectionReopensOriginalEvidence",
    ),
    "namespace": (
        "TestUnprefixedBackupRequiresValkeyBeforeOpeningStores",
        "TestUnprefixedBackupMigratesVerifiedRecordsUnderBarriers",
        "TestUnprefixedMigrationPreservesRecordsAcrossProcessExit",
    ),
    "full-embedded": ("TestRecoveryActivationFullEmbeddedApplication",),
}
REQUIRED_SUBCASES = {
    "TestRecoveryActivationNativeTopologyRecipes": (
        "local-to-fleet", "fleet-to-local", "local-restore", "fleet-restore",
    ),
    "TestRecoveryActivationFreshProcessNativeBoundaries": (
        "sealed", "blob-native-commit", "kv-native-commit", "sql-native-commit",
    ),
    "TestRecoveryActivationCurrentChoiceOrOriginalEvidenceFailureKeepsRemainingOwnersClosed": (
        "settings", "token", "source-file", "runtime", "original-history", "decision",
    ),
    "TestRecoveryFreshGatewayRejectsUnapprovedHistoryBeforeEffects": (
        "missing-sql-approval", "missing-native-approval", "stale-native-epoch", "replacement-incarnation",
    ),
    "TestRecoveryOldWarmedGatewayRefusesAfterKnownClosure": ("required-budget", "confirmed-no-budget"),
    "TestRecoveryFreshReplicaActualHistoryBarriers": (
        "prepared-original-history-not-replayed", "sealed-history-native-owners-closed",
        "blob-native-completed-sql-kv-closed", "kv-native-completed-sql-closed",
        "completed-import-missing-sql-approval", "completed-sql-original-kv-import-barrier",
        "completed-import-missing-native-approval", "completed-import-prior-native-epoch",
        "completed-import-distinct-native-incarnation",
    ),
    "TestRecoveryOperatorInputsSharedNativeTargets": ("postgres", "mysql"),
    "TestRecoveryPopulatedOperatorCommandsAcrossNativePhaseCut": ("shipping-binary",),
    "TestRecoveryActivationExactPendingPhaseRestart": tuple(
        f"{phase}/{cut}" for phase in ("blobs", "kv", "sql") for cut in ("header", "empty", "prepared", "published")
    ),
    "TestRecoveryActivationJournalExactPublicationProcessExit": tuple(
        f"{phase}/{cut}" for phase in ("blobs", "kv", "sql")
        for cut in ("header", "empty", "prepared", "published", "foreign", "wrong-prefix", "wrong-bytes", "unowned", "incomplete")
    ),
}
FIXTURES = (
    "TEST_VALKEY_URL", "TEST_VALKEY_REPLACEMENT_URL", "TEST_UNPREFIXED_VALKEY_URL",
    "TEST_POSTGRES_URL", "TEST_MYSQL_DSN", "TEST_BLOB_S3_ENDPOINT",
)


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def owner_sources(root, group):
    sources = {}
    for path in sorted((root / "internal/app").glob("*_test.go")):
        declarations = re.findall(r"^func (Test\w+)\(t \*testing\.T\)", path.read_text(encoding="utf-8"), re.MULTILINE)
        for name in declarations:
            if name in GROUPS[group]:
                if name in sources:
                    raise ValueError("Required recovery owner has duplicate declarations.")
                sources[name] = {"path": str(path.relative_to(root)), "sha256": digest(path)}
    if set(sources) != set(GROUPS[group]):
        missing = sorted(set(GROUPS[group]) - set(sources))
        raise ValueError("Required recovery source is missing: " + ", ".join(missing))
    return sources


def validate_events(group, events):
    validate_named_events(GROUPS[group], events)


def validate_named_events(selected, events):
    if not events or any(not isinstance(e, dict) or e.get("Package") != PACKAGE for e in events):
        raise ValueError("Application recovery contains missing or foreign results.")
    if any(e.get("Action") in {"fail", "skip"} for e in events):
        raise ValueError("Required application recovery contains a failure or skip.")
    owners = Counter(e["Test"] for e in events if e.get("Action") == "run" and e.get("Test") and "/" not in e["Test"])
    if owners != Counter(selected):
        raise ValueError("Required application owners must run exactly once.")
    runs = Counter(e["Test"] for e in events if e.get("Action") == "run" and e.get("Test"))
    passes = Counter(e["Test"] for e in events if e.get("Action") == "pass" and e.get("Test"))
    if runs != passes or any(n != 1 for n in runs.values()):
        raise ValueError("Every application recovery subcase must finish exactly once.")
    if any(name.split("/")[0] not in selected for name in runs):
        raise ValueError("Application recovery contains an unselected owner.")
    for owner in selected:
        for subcase in REQUIRED_SUBCASES.get(owner, ()):
            if runs[owner + "/" + subcase] != 1:
                raise ValueError("Required application recovery subcase is missing: " + owner + "/" + subcase)
    completed = [e for e in events if not e.get("Test") and e.get("Action") == "pass"]
    if len(completed) != 1:
        raise ValueError("Application recovery package did not finish exactly once.")


def validate_roster(roster, group, mode, source, head, sources):
    if not isinstance(roster, dict):
        raise ValueError("Application recovery roster is invalid.")
    expected = {"version": 1, "package": PACKAGE, "group": group, "mode": mode,
                "source": source, "workflow_head": head, "owners": list(GROUPS[group]), "owner_sources": sources}
    if any(roster.get(key) != value for key, value in expected.items()):
        raise ValueError("Application recovery selection or source binding differs.")
    if not re.fullmatch(r"[0-9a-f]{40}", source) or not re.fullmatch(r"[0-9a-f]{40}", head):
        raise ValueError("Application recovery requires exact source identities.")
    toolchain = roster.get("toolchain", {})
    if toolchain != {"GOVERSION": "go1.27.1", "GOOS": "linux", "GOARCH": "amd64", "GOHOSTOS": "linux",
                     "GOHOSTARCH": "amd64", "CGO_ENABLED": "1" if mode == "race" else "0", "GOWORK": "off"}:
        raise ValueError("Application recovery toolchain or native mode differs.")
    producer = roster.get("producer", {})
    if (not isinstance(producer, dict) or producer.get("Path") != "github.com/agentstation/starmap" or producer.get("Replace") is not None
            or producer.get("GoVersion") != "1.27.1" or not producer.get("Version")
            or not producer.get("Sum") or not producer.get("GoModSum")):
        raise ValueError("Application recovery requires the pinned published producer module.")


def validate_binary(path, metadata, source, mode, expected_digest):
    if digest(path) != expected_digest:
        raise ValueError("Operator executable bytes differ from the source-bound build.")
    if not metadata.splitlines() or not metadata.splitlines()[0].endswith(": go1.27.1") or "\tpath\tgithub.com/agentstation/starport/cmd/starport\n" not in metadata:
        raise ValueError("Operator executable has a different toolchain or command owner.")
    for setting in ("vcs.revision=" + source, "vcs.modified=false", "GOOS=linux", "GOARCH=amd64",
                    "CGO_ENABLED=" + ("1" if mode == "race" else "0")):
        if "\tbuild\t" + setting + "\n" not in metadata:
            raise ValueError("Operator executable build binding differs.")
    if ("\tbuild\t-race=true\n" in metadata) != (mode == "race"):
        raise ValueError("Operator executable race mode differs.")


def read_events(path):
    body = path.read_text(encoding="utf-8")
    if not body.endswith("\n"):
        raise ValueError("Application recovery evidence is truncated.")
    return [json.loads(line) for line in body.splitlines() if line.strip()]


def validate_producer(root, producer):
    matches = re.findall(r"^\s*(?:require\s+)?github\.com/agentstation/starmap\s+(\S+)", (root / "go.mod").read_text(encoding="utf-8"), re.MULTILINE)
    if matches != [producer["Version"]]:
        raise ValueError("Application recovery producer differs from the checked-out module pin.")
    sums = (root / "go.sum").read_text(encoding="utf-8").splitlines()
    for version, checksum in ((producer["Version"], producer["Sum"]), (producer["Version"] + "/go.mod", producer["GoModSum"])):
        if "github.com/agentstation/starmap " + version + " " + checksum not in sums:
            raise ValueError("Application recovery producer checksum differs from the checked-out source.")


def run(root, output, group, mode):
    if any(not os.environ.get(name) for name in FIXTURES):
        raise ValueError("Required native application recovery fixtures are missing.")
    sources = owner_sources(root, group)
    if output.is_relative_to(root):
        raise ValueError("Application recovery build evidence must stay outside the source tree.")
    output.mkdir(parents=True, exist_ok=False)
    def command(args, timeout=60):
        return subprocess.check_output(args, cwd=root, encoding="utf-8", timeout=timeout)
    source = command(["git", "rev-parse", "HEAD"]).strip()
    if command(["git", "status", "--porcelain"]).strip():
        raise ValueError("Application recovery requires unchanged checked-out source.")
    head = os.environ.get("STARPORT_TEST_HEAD_SHA", source)
    toolchain = json.loads(command(["go", "env", "-json", "GOVERSION", "GOOS", "GOARCH", "GOHOSTOS", "GOHOSTARCH", "CGO_ENABLED", "GOWORK"]))
    producer = json.loads(command(["go", "list", "-m", "-json", "github.com/agentstation/starmap"], timeout=900))
    roster = {"version": 1, "package": PACKAGE, "group": group, "mode": mode, "source": source,
              "workflow_head": head, "owners": list(GROUPS[group]), "owner_sources": sources,
              "toolchain": toolchain, "producer": producer}
    validate_roster(roster, group, mode, source, head, sources)
    validate_producer(root, producer)
    flags = ["-race"] if mode == "race" else []
    env = os.environ.copy()
    private = output / "private-child-output"
    private.mkdir(mode=0o700)
    env["STARPORT_UNPREFIXED_EVIDENCE_DIR"] = str(private.resolve())
    if group == "operator":
        binary = output / "starport"
        subprocess.run(["go", "build", "-buildvcs=true", *flags, "-o", str(binary.resolve()), "./cmd/starport"],
                       cwd=root, check=True, timeout=900)
        metadata = command(["go", "version", "-m", str(binary.resolve())])
        roster["operator_binary_sha256"] = digest(binary)
        validate_binary(binary, metadata, source, mode, roster["operator_binary_sha256"])
        (output / "operator-build.txt").write_text(metadata, encoding="utf-8")
        env["STARPORT_RECOVERY_OPERATOR_BINARY"] = str(binary.resolve())
    (output / "roster.json").write_text(json.dumps(roster, indent=2) + "\n", encoding="utf-8")
    expression = "^(" + "|".join(re.escape(name) for name in GROUPS[group]) + ")$"
    args = ["go", "test", "-json", *flags, "-p", "1", "-count=1", "-timeout", "30m", "-run", expression, "./internal/app"]
    # The event file holds only the JSON stream. Toolchain notices such as module downloads go to stderr.
    with (output / "tests.jsonl").open("w", encoding="utf-8") as stream, (output / "tests.stderr.txt").open("w", encoding="utf-8") as errors:
        result = subprocess.run(args, cwd=root, env=env, stdout=stream, stderr=errors, timeout=1860, check=False)
    if result.returncode:
        return result.returncode
    validate_events(group, read_events(output / "tests.jsonl"))
    print(f"Qualified application recovery {group}/{mode}: {len(GROUPS[group])} owners; no skips.")
    return 0


def verify(root, directory, source, head):
    all_owners = [owner for owners in GROUPS.values() for owner in owners]
    if len(all_owners) != len(set(all_owners)):
        raise ValueError("Application recovery groups contain duplicate owners.")
    expected = {f"application-recovery-{group}-{mode}" for group in GROUPS for mode in MODES}
    if {p.name for p in directory.iterdir()} != expected:
        raise ValueError("Required application recovery groups are missing or duplicated.")
    producer = None
    for group in GROUPS:
        sources = owner_sources(root, group)
        for mode in MODES:
            path = directory / f"application-recovery-{group}-{mode}"
            roster = json.loads((path / "roster.json").read_text(encoding="utf-8"))
            validate_roster(roster, group, mode, source, head, sources)
            validate_producer(root, roster["producer"])
            identity = {key: roster["producer"].get(key) for key in ("Path", "Version", "GoVersion", "Sum", "GoModSum")}
            if producer is not None and producer != identity:
                raise ValueError("Application recovery groups use different producer modules.")
            producer = identity
            validate_events(group, read_events(path / "tests.jsonl"))
            if group == "operator":
                validate_binary(path / "starport", (path / "operator-build.txt").read_text(encoding="utf-8"),
                                source, mode, roster.get("operator_binary_sha256"))
    print(f"Verified {len(expected)} application recovery groups; {len(all_owners)} owners in each mode; no skips.")


if __name__ == "__main__":
    root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    execute = commands.add_parser("run")
    execute.add_argument("--group", choices=GROUPS, required=True)
    execute.add_argument("--mode", choices=MODES, required=True)
    execute.add_argument("--output", type=Path, required=True)
    check = commands.add_parser("verify")
    check.add_argument("--directory", type=Path, required=True)
    legacy = commands.add_parser("verify-legacy")
    legacy.add_argument("--results", type=Path, required=True)
    args = parser.parse_args()
    if args.command == "run":
        sys.exit(run(root, args.output.resolve(), args.group, args.mode))
    if args.command == "verify-legacy":
        validate_named_events(LEGACY_OWNERS, read_events(args.results))
        print("Verified four original application recovery owners; no skips.")
        sys.exit(0)
    source = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, encoding="utf-8", timeout=60).strip()
    verify(root, args.directory, source, os.environ.get("STARPORT_TEST_HEAD_SHA", source))
