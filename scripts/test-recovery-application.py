#!/usr/bin/env python3
"""Test required recovery coverage, source bindings, and failure propagation."""

import copy
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import recovery_application as recovery


SOURCE = "a" * 40
HEAD = "b" * 40
PRODUCER = {"Path": "github.com/agentstation/starmap", "Version": "v0.16.6-0.20260930115749-2bb995712275",
            "GoVersion": "1.27.1", "Sum": "h1:source", "GoModSum": "h1:module"}


def event(action, name=None):
    item = {"Action": action, "Package": recovery.PACKAGE}
    if name is not None:
        item["Test"] = name
    return item


def events(group):
    names = list(recovery.GROUPS[group])
    names += [owner + "/" + subcase for owner in recovery.GROUPS[group]
              for subcase in recovery.REQUIRED_SUBCASES.get(owner, ())]
    return [event("run", name) for name in names] + [event("pass", name) for name in names] + [event("pass")]


def roster(group, mode, sources):
    return {"version": 1, "package": recovery.PACKAGE, "group": group, "mode": mode, "source": SOURCE,
            "workflow_head": HEAD, "owners": list(recovery.GROUPS[group]), "owner_sources": sources,
            "toolchain": {"GOVERSION": "go1.27.1", "GOOS": "linux", "GOARCH": "amd64", "GOHOSTOS": "linux",
                          "GOHOSTARCH": "amd64", "CGO_ENABLED": "1" if mode == "race" else "0", "GOWORK": "off"},
            "producer": dict(PRODUCER)}


def binary_metadata(path, mode):
    return (str(path) + ": go1.27.1\n\tpath\tgithub.com/agentstation/starport/cmd/starport\n" +
            "".join("\tbuild\t" + setting + "\n" for setting in (
                "vcs.revision=" + SOURCE, "vcs.modified=false", "GOOS=linux", "GOARCH=amd64",
                "CGO_ENABLED=" + ("1" if mode == "race" else "0"))) +
            ("\tbuild\t-race=true\n" if mode == "race" else ""))


def source_tree(root):
    app = root / "internal/app"
    app.mkdir(parents=True)
    names = [owner for owners in recovery.GROUPS.values() for owner in owners]
    # These declarations test the verifier only. No Go test or storage owner runs.
    (app / "contracts_test.go").write_text("\n".join("func " + name + "(t *testing.T) {}" for name in names) + "\n")
    (root / "go.mod").write_text("require github.com/agentstation/starmap " + PRODUCER["Version"] + "\n")
    (root / "go.sum").write_text("github.com/agentstation/starmap " + PRODUCER["Version"] + " " + PRODUCER["Sum"] + "\n" +
                                 "github.com/agentstation/starmap " + PRODUCER["Version"] + "/go.mod " + PRODUCER["GoModSum"] + "\n")


def artifacts(root, directory):
    directory.mkdir()
    for group in recovery.GROUPS:
        for mode in recovery.MODES:
            path = directory / f"application-recovery-{group}-{mode}"
            path.mkdir()
            record = roster(group, mode, recovery.owner_sources(root, group))
            if group == "operator":
                binary = path / "starport"
                binary.write_bytes(b"unit test executable marker")
                record["operator_binary_sha256"] = recovery.digest(binary)
                (path / "operator-build.txt").write_text(binary_metadata(binary, mode))
            (path / "roster.json").write_text(json.dumps(record) + "\n")
            (path / "tests.jsonl").write_text("".join(json.dumps(e) + "\n" for e in events(group)))


class ApplicationRecoveryTests(unittest.TestCase):
    def test_groups_are_disjoint_and_include_required_product_owners(self):
        owners = [owner for names in recovery.GROUPS.values() for owner in names]
        self.assertEqual(len(owners), len(set(owners)))
        self.assertIn("TestRecoveryActivationFullEmbeddedApplication", owners)
        self.assertIn("TestUnprefixedMigrationPreservesRecordsAcrossProcessExit", owners)
        self.assertEqual(len(recovery.REQUIRED_SUBCASES["TestRecoveryFreshReplicaActualHistoryBarriers"]), 9)

    def test_valid_event_contracts(self):
        for group in recovery.GROUPS:
            with self.subTest(group=group):
                recovery.validate_events(group, events(group))

    def test_original_durable_application_owners_must_finish_without_skips(self):
        complete = [event("run", name) for name in recovery.LEGACY_OWNERS] + [event("pass", name) for name in recovery.LEGACY_OWNERS] + [event("pass")]
        recovery.validate_named_events(recovery.LEGACY_OWNERS, complete)
        for changed in (complete[1:], complete + [event("skip", recovery.LEGACY_OWNERS[0])], complete[:-1]):
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                recovery.validate_named_events(recovery.LEGACY_OWNERS, changed)

    def test_missing_duplicate_failed_skipped_foreign_or_unfinished_owners_refuse(self):
        original = events("namespace")
        for changed in (original[1:], original + original[:1], original[:-1], original + [event("pass")],
                        original + [event("fail")], original + [event("skip", recovery.GROUPS["namespace"][0])],
                        original + [event("run", "TestUnselected")], [None], [True],
                        [dict(e, Package="foreign") for e in original]):
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                recovery.validate_events("namespace", changed)

    def test_missing_or_skipped_required_subcase_refuses(self):
        for group in recovery.GROUPS:
            for owner in recovery.GROUPS[group]:
                for subcase in recovery.REQUIRED_SUBCASES.get(owner, ()):
                    name = owner + "/" + subcase
                    complete = events(group)
                    changed = [e for e in complete if e.get("Test") != name]
                    with self.subTest(name=name), self.assertRaises(ValueError):
                        recovery.validate_events(group, changed)
                    changed = [dict(e, Action="skip") if e.get("Test") == name and e["Action"] == "pass" else e for e in complete]
                    with self.subTest(skipped=name), self.assertRaises(ValueError):
                        recovery.validate_events(group, changed)

    def test_unexpected_nested_skip_and_truncated_subtest_refuse(self):
        owner = recovery.GROUPS["namespace"][0]
        for final in ([], [event("skip", owner + "/optional")]):
            with self.subTest(final=final), self.assertRaises(ValueError):
                recovery.validate_events("namespace", events("namespace") + [event("run", owner + "/optional")] + final)

    def test_roster_rejects_wrong_group_mode_source_head_selection_or_census(self):
        sources = {"original": "source"}
        original = roster("namespace", "pure", sources)
        for key, value in (("version", 2), ("package", "foreign"), ("group", "gateway"), ("mode", "race"),
                           ("source", "c" * 40), ("workflow_head", "c" * 40), ("owners", []), ("owner_sources", {})):
            changed = dict(original, **{key: value})
            with self.subTest(key=key), self.assertRaises(ValueError):
                recovery.validate_roster(changed, "namespace", "pure", SOURCE, HEAD, sources)
        for changed in (None, [], True):
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                recovery.validate_roster(changed, "namespace", "pure", SOURCE, HEAD, sources)

    def test_wrong_compiler_platform_cgo_workspace_or_replacement_refuses(self):
        original = roster("namespace", "race", {})
        for key, value in (("GOVERSION", "go1.26.0"), ("GOOS", "darwin"), ("GOARCH", "arm64"),
                           ("GOHOSTOS", "windows"), ("GOHOSTARCH", "arm64"), ("CGO_ENABLED", "0"), ("GOWORK", "dev.work")):
            changed = copy.deepcopy(original)
            changed["toolchain"][key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                recovery.validate_roster(changed, "namespace", "race", SOURCE, HEAD, {})
        for producer in ({}, None, dict(PRODUCER, Replace={"Dir": "local"}), dict(PRODUCER, GoVersion="1.26")):
            with self.subTest(producer=producer), self.assertRaises(ValueError):
                recovery.validate_roster(dict(original, producer=producer), "namespace", "race", SOURCE, HEAD, {})

    def test_operator_binary_requires_exact_bytes_source_toolchain_command_and_mode(self):
        with tempfile.TemporaryDirectory() as temporary:
            binary = Path(temporary) / "starport"
            binary.write_bytes(b"unit executable")
            checksum = recovery.digest(binary)
            for mode in recovery.MODES:
                metadata = binary_metadata(binary, mode)
                recovery.validate_binary(binary, metadata, SOURCE, mode, checksum)
                for old, new in (("go1.27.1", "go1.27.10"), (SOURCE, "c" * 40), ("vcs.modified=false", "vcs.modified=true"),
                                 ("GOOS=linux", "GOOS=darwin"), ("GOARCH=amd64", "GOARCH=arm64"),
                                 ("cmd/starport", "cmd/foreign"), ("CGO_ENABLED=" + ("1" if mode == "race" else "0"), "CGO_ENABLED=2")):
                    with self.subTest(mode=mode, field=old), self.assertRaises(ValueError):
                        recovery.validate_binary(binary, metadata.replace(old, new), SOURCE, mode, checksum)
                with self.assertRaises(ValueError):
                    recovery.validate_binary(binary, metadata, SOURCE, "race" if mode == "pure" else "pure", checksum)
            binary.write_bytes(b"changed executable")
            with self.assertRaises(ValueError):
                recovery.validate_binary(binary, binary_metadata(binary, "pure"), SOURCE, "pure", checksum)

    def test_current_source_requires_real_named_owner_and_no_duplicate_declaration(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source_tree(root)
            recovery.owner_sources(root, "gateway")
            path = root / "internal/app/contracts_test.go"
            body = path.read_text()
            path.write_text(body.replace("TestRecoveryFreshReplicaActualHistoryBarriers", "TestMissingOwner"))
            with self.assertRaises(ValueError):
                recovery.owner_sources(root, "gateway")
            path.write_text(body + "func TestRecoveryFreshReplicaActualHistoryBarriers(t *testing.T) {}\n")
            with self.assertRaises(ValueError):
                recovery.owner_sources(root, "gateway")

    def test_published_producer_must_match_current_pin_and_both_checksums(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source_tree(root)
            recovery.validate_producer(root, PRODUCER)
            for key in ("Version", "Sum", "GoModSum"):
                with self.subTest(key=key), self.assertRaises(ValueError):
                    recovery.validate_producer(root, dict(PRODUCER, **{key: "different"}))

    def test_raw_result_truncation_and_non_json_refuse(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "tests.jsonl"
            for body in ("", '{"Action":"pass"}', "truncated\n"):
                path.write_text(body)
                with self.subTest(body=body), self.assertRaises(ValueError):
                    recovery.read_events(path)

    def test_aggregate_requires_all_groups_and_both_modes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "source"
            source_tree(root)
            directory = Path(temporary) / "proof"
            artifacts(root, directory)
            recovery.verify(root, directory, SOURCE, HEAD)
            path = directory / "application-recovery-namespace-pure"
            path.rename(directory / "missing-group")
            with self.assertRaises(ValueError):
                recovery.verify(root, directory, SOURCE, HEAD)

    def test_aggregate_rejects_changed_source_events_or_binary(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "source"
            source_tree(root)
            directory = Path(temporary) / "proof"
            artifacts(root, directory)
            with self.assertRaises(ValueError):
                recovery.verify(root, directory, "c" * 40, HEAD)
            source = root / "internal/app/contracts_test.go"
            body = source.read_text()
            source.write_text(body + "// changed original source\n")
            with self.assertRaises(ValueError):
                recovery.verify(root, directory, SOURCE, HEAD)
            source.write_text(body)
            binary = directory / "application-recovery-operator-race/starport"
            binary.write_bytes(b"foreign executable")
            with self.assertRaises(ValueError):
                recovery.verify(root, directory, SOURCE, HEAD)

    def test_go_failure_cannot_be_replaced_by_successful_validation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary) / "source"
            source_tree(root)
            output = Path(temporary) / "proof"
            responses = [SOURCE, "", json.dumps(roster("namespace", "pure", {})["toolchain"]), json.dumps(PRODUCER)]
            with patch.dict(os.environ, {name: "fixture" for name in recovery.FIXTURES}, clear=True), \
                 patch.object(recovery.subprocess, "check_output", side_effect=responses), \
                 patch.object(recovery.subprocess, "run") as run:
                run.return_value.returncode = 23
                self.assertEqual(recovery.run(root, output, "namespace", "pure"), 23)
                self.assertTrue((output / "roster.json").exists())
                self.assertIn("30m", run.call_args.args[0])
                self.assertNotIn("-race", run.call_args.args[0])
                self.assertIn("-count=1", run.call_args.args[0])

    def test_required_workflow_keeps_platforms_and_serial_native_groups(self):
        root = Path(__file__).resolve().parents[1]
        workflow = (root / ".github/workflows/ci.yml").read_text()
        application = workflow.split("  application-recovery:\n", 1)[1].split("  application-recovery-evidence:\n", 1)[0]
        for group in recovery.GROUPS:
            self.assertIn(group, application)
        self.assertIn("mode: [pure, race]", application)
        self.assertIn("max-parallel: 2", application)
        self.assertIn("go-version: \"1.27.1\"", application)
        self.assertIn("application-recovery-evidence, authorization-capacity", workflow)
        for runner in ("ubuntu-24.04", "ubuntu-24.04-arm", "macos-15", "windows-2025", "windows-11-arm"):
            self.assertIn("runner: " + runner, workflow)
        self.assertIn("name: Durable Work Recovery", workflow)
        self.assertIn("verify-legacy --results backup-application.jsonl", workflow)
        self.assertIn('APPLICATION_RECOVERY_RESULT: ${{ needs.application-recovery.result }}', workflow)
        self.assertIn('test "$APPLICATION_RECOVERY_RESULT" = success', workflow)


if __name__ == "__main__":
    unittest.main()
