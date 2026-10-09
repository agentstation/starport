#!/usr/bin/env python3
"""Test native catalog evidence integrity and source binding."""

import copy
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import native_catalog as native


class NativeCatalogTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.test = {"package": "github.com/agentstation/starport/internal/app", "test": "TestFixture"}
        self.proof = {"run": {"databaseId": 123, "url": "https://github.com/agentstation/starport/actions/runs/123",
                              "status": "completed", "conclusion": "success", "headSha": "a" * 40, "jobs": []}, "sha256": {}}
        for arch, runner in native.RUNNERS["windows"].items():
            self.proof["run"]["jobs"].append({"name": f"Test ({runner})", "status": "completed", "conclusion": "success", "databaseId": len(self.proof["run"]["jobs"]) + 1})
            self.bind(runner, "toolchain.txt", f"go version go1.27.2 windows/{arch}\nwindows\n{arch}\nwindows\n{arch}\n" + ("0\n" if arch == "arm64" else "1\n"))
            self.bind_events(runner, [self.event("run"), self.event("pass"), self.event("pass", test=False)])

    def event(self, action, test=True, name=None):
        result = {"Action": action, "Package": self.test["package"]}
        if test:
            result["Test"] = name or self.test["test"]
        return result

    def bind(self, runner, name, content):
        relative = f"native-catalog-{runner}/{name}"
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)
        self.proof["sha256"][relative] = hashlib.sha256(path.read_bytes()).hexdigest()

    def bind_events(self, runner, events):
        self.bind(runner, "tests.jsonl", "".join(json.dumps(e) + "\n" for e in events))

    def validate(self):
        return native.validate_platform(self.root, self.proof, "windows", [self.test])

    def test_both_native_architectures_pass(self):
        self.assertEqual({x["architecture"] for x in self.validate()}, {"amd64", "arm64"})

    def test_cross_repository_or_incomplete_run_fails(self):
        for field, value in [("url", "https://github.com/agentstation/starmap/actions/runs/123"), ("status", "in_progress"), ("conclusion", "failure"), ("headSha", "short")]:
            with self.subTest(field=field):
                changed = copy.deepcopy(self.proof["run"])
                changed[field] = value
                with self.assertRaises(ValueError):
                    native.validate_run(changed)

    def test_missing_failed_or_duplicate_architecture_fails(self):
        jobs = copy.deepcopy(self.proof["run"]["jobs"])
        for changed in [jobs[:1], jobs + jobs[:1], [dict(jobs[0], conclusion="failure"), jobs[1]]]:
            self.proof["run"]["jobs"] = changed
            with self.assertRaises(ValueError):
                self.validate()

    def test_wrong_host_target_or_toolchain_fails(self):
        runner = native.RUNNERS["windows"]["amd64"]
        original = (self.root / f"native-catalog-{runner}/toolchain.txt").read_text()
        for content in [original.replace("go1.27.2", "go1.26.6"), original.replace("windows", "linux", 1), original.replace("amd64\nwindows", "arm64\nwindows"), original.removesuffix("1\n") + "0\n"]:
            self.bind(runner, "toolchain.txt", content)
            with self.assertRaises(ValueError):
                self.validate()

    def test_tampered_event_file_fails(self):
        runner = native.RUNNERS["windows"]["amd64"]
        (self.root / f"native-catalog-{runner}/tests.jsonl").write_text("{}\n")
        with self.assertRaises(ValueError):
            self.validate()

    def test_required_test_and_package_must_finish_once(self):
        runner = native.RUNNERS["windows"]["amd64"]
        for events in [[], [self.event("run")], [self.event("run"), self.event("pass")], [self.event("run"), self.event("pass"), self.event("pass"), self.event("pass", test=False)], [self.event("run"), self.event("fail")]]:
            self.bind_events(runner, events)
            with self.assertRaises(ValueError):
                self.validate()

    def test_required_subtest_skip_fails(self):
        runner = native.RUNNERS["windows"]["amd64"]
        self.bind_events(runner, [self.event("run"), self.event("skip", name="TestFixture/real-store"), self.event("pass"), self.event("pass", test=False)])
        with self.assertRaises(ValueError):
            self.validate()

    def test_unrelated_optional_skip_does_not_qualify_that_test(self):
        runner = native.RUNNERS["windows"]["amd64"]
        self.bind_events(runner, [self.event("run"), self.event("skip", name="TestOptionalStore"), self.event("pass"), self.event("pass", test=False)])
        self.assertEqual(len(self.validate()), 2)
        self.test["test"] = "TestOptionalStore"
        with self.assertRaises(ValueError):
            self.validate()

    def enable_shards(self):
        runner = "windows-2025"
        self.proof["format"] = 2
        self.bind_events(runner, [{"Package": "github.com/agentstation/starport/internal/config", "Action": "pass"}])
        names = sorted(["TestFixture"] + [f"TestOwner{i}" for i in range(30)])
        toolchain = (self.root / f"native-catalog-{runner}/toolchain.txt").read_text()
        for index in range(native.app_shards.SHARDS):
            self.proof["run"]["jobs"].append({"name": f"App test ({runner}, {index})", "status": "completed", "conclusion": "success", "databaseId": 100 + index})
            selected = native.app_shards.partition(names, index)
            roster = {"version": 1, "index": index, "shards": native.app_shards.SHARDS,
                      "owners": names, "selected": selected, "source": "b" * 40, "workflow_head": "a" * 40}
            suffix = f"app-{runner}-{index}"
            self.bind(suffix, "roster.json", json.dumps(roster))
            self.bind(suffix, "toolchain.txt", toolchain)
            self.bind_events(suffix, [self.event("run", name=name) for name in selected] +
                             [self.event("pass", name=name) for name in selected] + [self.event("pass", test=False)])

    def test_shards_qualify_named_tests_and_preserve_raw_package_completions(self):
        self.enable_shards()
        self.assertEqual(len(self.validate()), 2)

    def test_shards_require_every_job_artifact_and_matching_toolchain(self):
        self.enable_shards()
        jobs = self.proof["run"]["jobs"]
        for changed in [jobs[:-1], jobs + jobs[-1:], [dict(jobs[-1], conclusion="failure")] + jobs[:-1]]:
            self.proof["run"]["jobs"] = changed
            with self.assertRaises(ValueError):
                self.validate()
        self.proof["run"]["jobs"] = jobs
        self.bind("app-windows-2025-0", "toolchain.txt", "different\n")
        with self.assertRaises(ValueError):
            self.validate()

    def test_shards_refuse_duplicate_app_execution_in_base_artifact(self):
        self.enable_shards()
        self.bind_events("windows-2025", [self.event("run"), self.event("pass"), self.event("pass", test=False)])
        with self.assertRaises(ValueError):
            self.validate()

    def test_skipped_required_sharded_owner_remains_unqualified(self):
        self.enable_shards()
        for index in range(native.app_shards.SHARDS):
            suffix = f"app-windows-2025-{index}"
            path = self.root / f"native-catalog-{suffix}/tests.jsonl"
            events = [json.loads(line) for line in path.read_text().splitlines()]
            for event in events:
                if event.get("Test") == "TestFixture" and event["Action"] == "pass":
                    event["Action"] = "skip"
            self.bind_events(suffix, events)
        with self.assertRaises(ValueError):
            self.validate()

    def test_starmap_consumer_can_load_by_path_from_another_directory(self):
        module = Path(native.__file__).resolve()
        code = "import importlib.util; s=importlib.util.spec_from_file_location('native', " + repr(str(module)) + "); m=importlib.util.module_from_spec(s); s.loader.exec_module(m); assert m.app_shards.SHARDS == 4"
        subprocess.run([sys.executable, "-I", "-c", code], cwd=self.root, check=True, capture_output=True, text=True, timeout=10)

    def test_missing_capture_is_unverified(self):
        self.assertEqual(native.verify(self.root, {"platform": "windows", "tests": [self.test]})["status"], "UNVERIFIED")

    def test_changed_or_untracked_source_fails(self):
        def git(*args):
            return subprocess.check_output(["git", *args], cwd=self.root, text=True)
        git("init", "-q")
        source = self.root / "main.go"
        source.write_text("package fixture\n")
        git("add", "main.go")
        git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
        # This source fixture has no native artifacts in its repository.
        for directory in self.root.glob("native-catalog-*"):
            for file in directory.iterdir():
                file.unlink()
            directory.rmdir()
        revision = git("rev-parse", "HEAD").strip()
        native.unchanged_source(self.root, revision)
        source.write_text("package changed\n")
        with self.assertRaises(ValueError):
            native.unchanged_source(self.root, revision)
        git("checkout", "--", "main.go")
        (self.root / "extra.go").write_text("package fixture\n")
        with self.assertRaises(ValueError):
            native.unchanged_source(self.root, revision)

    def enable_recovery_shards(self, extra=()):
        self.enable_shards()
        self.proof["format"] = 3
        names = sorted(["TestRecoveryFixture", *extra] + [f"TestRecoveryOwner{i}" for i in range(30)])
        for runner in native.RUNNERS["windows"].values():
            toolchain = (self.root / f"native-catalog-{runner}/toolchain.txt").read_text()
            for index in range(native.app_shards.SHARDS):
                self.proof["run"]["jobs"].append({"name": f"Recovery test ({runner}, {index})", "status": "completed", "conclusion": "success", "databaseId": 200 + len(self.proof["run"]["jobs"])})
                selected = native.app_shards.partition(names, index)
                roster = {"version": 1, "index": index, "shards": native.app_shards.SHARDS,
                          "package": native.app_shards.RECOVERY_PACKAGE, "owners": names, "selected": selected,
                          "source": "b" * 40, "workflow_head": "a" * 40}
                suffix = f"recovery-{runner}-{index}"
                self.bind(suffix, "roster.json", json.dumps(roster))
                self.bind(suffix, "toolchain.txt", toolchain)
                events = [dict(self.event("run", name=name), Package=native.app_shards.RECOVERY_PACKAGE) for name in selected]
                events += [dict(self.event("pass", name=name), Package=native.app_shards.RECOVERY_PACKAGE) for name in selected]
                events += [dict(self.event("pass", test=False), Package=native.app_shards.RECOVERY_PACKAGE)]
                self.bind_events(suffix, events)
        self.test = {"package": native.app_shards.RECOVERY_PACKAGE, "test": "TestRecoveryFixture"}

    def test_recovery_shards_qualify_both_windows_architectures(self):
        self.enable_recovery_shards()
        self.assertEqual({x["architecture"] for x in self.validate()}, {"amd64", "arm64"})

    def test_recovery_jobs_and_every_bound_artifact_are_mandatory(self):
        self.enable_recovery_shards()
        jobs = copy.deepcopy(self.proof["run"]["jobs"])
        for changed in [jobs[:-1], jobs + jobs[-1:], [dict(jobs[-1], conclusion="failure")] + jobs[:-1]]:
            self.proof["run"]["jobs"] = changed
            with self.assertRaises(ValueError):
                self.validate()
        self.proof["run"]["jobs"] = jobs
        self.proof["sha256"].pop("native-catalog-recovery-windows-11-arm-0/roster.json")
        with self.assertRaises(ValueError):
            self.validate()

    def test_recovery_shards_reject_base_overlap_and_foreign_toolchain(self):
        self.enable_recovery_shards()
        self.bind_events("windows-11-arm", [dict(self.event("pass", test=False), Package=native.app_shards.RECOVERY_PACKAGE)])
        with self.assertRaises(ValueError):
            self.validate()
        self.bind_events("windows-11-arm", [self.event("run"), self.event("pass"), self.event("pass", test=False)])
        self.bind("recovery-windows-11-arm-0", "toolchain.txt", "foreign\n")
        with self.assertRaises(ValueError):
            self.validate()

    def test_recovery_source_package_and_owner_counts_cannot_be_forged(self):
        self.enable_recovery_shards()
        suffix = "recovery-windows-2025-0"
        original = (self.root / f"native-catalog-{suffix}/roster.json").read_text()
        for key, value in [("package", native.app_shards.PACKAGE), ("workflow_head", "c" * 40), ("selected", [])]:
            changed = json.loads(original)
            changed[key] = value
            self.bind(suffix, "roster.json", json.dumps(changed))
            with self.assertRaises(ValueError):
                self.validate()
        self.bind(suffix, "roster.json", original)
        events = [json.loads(line) for line in (self.root / f"native-catalog-{suffix}/tests.jsonl").read_text().splitlines()]
        self.bind_events(suffix, events + events[:1])
        with self.assertRaises(ValueError):
            self.validate()

    def test_required_recovery_subtest_skip_remains_unqualified(self):
        self.enable_recovery_shards()
        for index in range(native.app_shards.SHARDS):
            suffix = f"recovery-windows-2025-{index}"
            events = [json.loads(line) for line in (self.root / f"native-catalog-{suffix}/tests.jsonl").read_text().splitlines()]
            if any(event.get("Test") == "TestRecoveryFixture" for event in events):
                events.insert(1, dict(self.event("skip", name="TestRecoveryFixture/real-store"), Package=native.app_shards.RECOVERY_PACKAGE))
                self.bind_events(suffix, events)
        with self.assertRaises(ValueError):
            self.validate()

    def install_report(self, system, arch, runner):
        suffix = "zip" if system == "windows" else "tar.gz"
        return {"schema_version": 1, "verdict": "PASS", "mode": "candidate", "release": None,
                "candidate_version": "1.2.2-next", "workflow_run_id": "123", "workflow_head": "a" * 40,
                "archive": f"starport_1.2.2-next_{system}_{native.ARCHIVE_ARCHITECTURES[arch]}.{suffix}",
                "version": "starport version 1.2.2-next", "keyless_catalog_commands": 2, "user_files_after_shutdown": [],
                "archive_sha256": hashlib.sha256(runner.encode()).hexdigest(), "binary_sha256": "b" * 64,
                "catalog_model_sha256": "c" * 64, "catalog_generation": "generation-1"}

    def enable_install(self, extra=()):
        self.enable_recovery_shards(extra)
        self.proof["format"] = 4
        self.proof["run"]["event"] = "pull_request"
        self.proof["pull_request"] = 7
        for system, runners in native.RUNNERS.items():
            for arch, runner in runners.items():
                self.proof["run"]["jobs"].append({"name": f"Candidate install ({runner})", "status": "completed", "conclusion": "success", "databaseId": 300 + len(self.proof["run"]["jobs"])})
                self.bind(f"install-{runner}", "result.json", json.dumps(self.install_report(system, arch, runner)))

    def test_install_qualifies_each_platform_with_digests_and_generation(self):
        self.enable_install()
        for system, runners in native.RUNNERS.items():
            with self.subTest(system=system):
                observations = native.validate_install(self.root, self.proof, system)
                self.assertEqual([x["architecture"] for x in observations], list(runners))
                for observation, runner in zip(observations, runners.values()):
                    self.assertEqual(observation["archive_sha256"], hashlib.sha256(runner.encode()).hexdigest())
                    self.assertEqual((observation["binary_sha256"], observation["catalog_generation"]), ("b" * 64, "generation-1"))

    def test_install_jobs_are_mandatory_for_every_architecture(self):
        self.enable_install()
        jobs = copy.deepcopy(self.proof["run"]["jobs"])
        install = [job for job in jobs if job["name"] == "Candidate install (windows-11-arm)"]
        others = [job for job in jobs if job["name"] != "Candidate install (windows-11-arm)"]
        for changed in [others, jobs + install, others + [dict(install[0], conclusion="failure")], others + [dict(install[0], status="in_progress")]]:
            self.proof["run"]["jobs"] = changed
            with self.assertRaises(ValueError):
                native.validate_install(self.root, self.proof, "windows")

    def test_install_report_fields_cannot_be_forged(self):
        self.enable_install()
        runner = "ubuntu-24.04-arm"
        original = self.install_report("linux", "arm64", runner)
        native.validate_install(self.root, self.proof, "linux")
        for key, value in [("schema_version", 2), ("mode", "release"), ("verdict", "FAIL"), ("workflow_run_id", "124"),
                           ("workflow_head", "c" * 40), ("candidate_version", "1.2.2"), ("candidate_version", "1.2.3-next"),
                           ("archive", "starport_1.2.2-next_linux_x86_64.tar.gz"), ("version", "starport version 1.2.1"),
                           ("keyless_catalog_commands", 1), ("user_files_after_shutdown", ["config/starport.yaml"]),
                           ("user_files_after_shutdown", None), ("archive_sha256", None), ("binary_sha256", "b" * 63),
                           ("catalog_model_sha256", "C" * 64), ("catalog_generation", ""), ("catalog_generation", "a b")]:
            with self.subTest(key=key, value=value):
                self.bind(f"install-{runner}", "result.json", json.dumps(dict(original, **{key: value})))
                with self.assertRaises(ValueError):
                    native.validate_install(self.root, self.proof, "linux")
        self.bind(f"install-{runner}", "result.json", json.dumps(original))
        (self.root / f"native-catalog-install-{runner}/result.json").write_text(json.dumps(dict(original, verdict="PASS ")))
        with self.assertRaises(ValueError):
            native.validate_install(self.root, self.proof, "linux")
        self.proof["sha256"].pop(f"native-catalog-install-{runner}/result.json")
        with self.assertRaises(ValueError):
            native.validate_install(self.root, self.proof, "linux")

    def test_install_requires_format_4_and_a_supported_platform(self):
        self.enable_install()
        for changed, system in [(3, "linux"), (None, "linux"), (4, "freebsd"), (4, None)]:
            self.proof["format"] = changed
            with self.subTest(format=changed, system=system), self.assertRaises(ValueError):
                native.validate_install(self.root, self.proof, system)

    def test_install_requires_a_pull_request_run_and_number(self):
        self.enable_install()
        for event, number in [("push", 7), (None, 7), ("pull_request", None), ("pull_request", 0), ("pull_request", "7"), ("pull_request", True)]:
            self.proof["run"]["event"], self.proof["pull_request"] = event, number
            with self.subTest(event=event, number=number), self.assertRaisesRegex(ValueError, "pull request run and its pull request number"):
                native.validate_install(self.root, self.proof, "linux")

    def test_format_4_keeps_the_test_event_contracts(self):
        self.enable_install()
        self.assertEqual({x["architecture"] for x in self.validate()}, {"amd64", "arm64"})
        jobs = self.proof["run"]["jobs"]
        for name in ("App test (windows-2025, 0)", "Recovery test (windows-11-arm, 3)"):
            self.proof["run"]["jobs"] = [job for job in jobs if job["name"] != name]
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.validate()

    def write_capture(self):
        repository = self.root / "repository"
        shutil.copytree(self.root, repository / native.PROOF, ignore=shutil.ignore_patterns("repository"))
        (repository / native.PROOF / "capture.json").write_text(json.dumps(self.proof))
        return repository

    def test_verify_dispatches_on_the_evidence_field(self):
        self.enable_install()
        repository = self.write_capture()
        with patch.object(native, "unchanged_source") as unchanged:
            install = native.verify(repository, {"platform": "linux", "evidence": "install"})
            tests = native.verify(repository, {"platform": "windows", "tests": [self.test]})
            unknown = native.verify(repository, {"platform": "linux", "evidence": "tests"})
        unchanged.assert_called_with(repository, "a" * 40)
        self.assertEqual(install["status"], "PASS", install)
        self.assertEqual(install["pull_request"], 7)
        self.assertEqual([x["architecture"] for x in install["observations"]], ["amd64", "arm64"])
        self.assertIn("catalog_generation", install["observations"][0])
        self.assertEqual(tests["status"], "PASS", tests)
        self.assertEqual([x["required_tests"] for x in tests["observations"]], [1, 1])
        self.assertEqual(unknown["status"], "UNVERIFIED")

    def test_verify_without_install_evidence_refuses_an_install_entry(self):
        self.enable_recovery_shards()
        repository = self.write_capture()
        with patch.object(native, "unchanged_source"):
            install = native.verify(repository, {"platform": "windows", "evidence": "install"})
            self.assertEqual(native.verify(repository, {"platform": "windows", "tests": [self.test]})["status"], "PASS")
        self.assertEqual(install, {"status": "UNVERIFIED", "reason":
                                   "Native install evidence requires a format 4 capture of a pull request run, but this capture is format 3."})

    def capture(self, install, event="pull_request", pulls=None):
        self.enable_install(extra=["TestRecoveryWitnessTransitions"]) if install else self.enable_recovery_shards(["TestRecoveryWitnessTransitions"])
        self.proof["run"]["event"] = event
        self.proof.pop("pull_request", None)
        if pulls is None:
            pulls = [{"number": 7, "head": {"sha": "a" * 40}}, {"number": 9, "head": {"sha": "b" * 40}}]
        self.lookups = []
        for system, runners in native.RUNNERS.items():
            for arch, runner in runners.items():
                if system != "windows":
                    cgo = "1"
                    self.bind(runner, "toolchain.txt", f"go version go1.27.2 {system}/{arch}\n{system}\n{arch}\n{system}\n{arch}\n{cgo}\n")
                    self.bind_events(runner, [self.event("run"), self.event("pass"), self.event("pass", test=False)])
        source = self.root / "artifacts"
        shutil.copytree(self.root, source, ignore=shutil.ignore_patterns("artifacts"))
        def command(args, _root):
            if args[:3] == ["gh", "run", "view"]:
                return json.dumps(self.proof["run"])
            if args[:2] == ["gh", "api"]:
                self.lookups.append(args[2])
                return json.dumps(pulls)
            shutil.copytree(source, Path(args[args.index("--dir") + 1]), dirs_exist_ok=True)
            return ""
        output = self.root / "capture"
        with patch.object(native, "command", side_effect=command):
            native.capture(self.root, 123, output)
        return json.loads((output / "capture.json").read_text())

    def test_capture_records_install_results_as_format_4(self):
        proof = self.capture(install=True)
        self.assertEqual(proof["format"], 4)
        for runners in native.RUNNERS.values():
            for runner in runners.values():
                self.assertIn(f"native-catalog-install-{runner}/result.json", proof["sha256"])
        self.assertIn("native-catalog-recovery-windows-11-arm-0/tests.jsonl", proof["sha256"])
        self.assertEqual((proof["run"]["event"], proof["pull_request"]), ("pull_request", 7))
        self.assertEqual(self.lookups, [f"repos/{native.REPOSITORY}/commits/{'a' * 40}/pulls"])

    def test_capture_refuses_install_evidence_without_one_pull_request(self):
        for event, pulls in [("push", None), ("pull_request", []), ("pull_request", [{"number": 9, "head": {"sha": "b" * 40}}]),
                             ("pull_request", [{"number": 7, "head": {"sha": "a" * 40}}, {"number": 8, "head": {"sha": "a" * 40}}])]:
            with self.subTest(event=event, pulls=pulls):
                self.setUp()
                with self.assertRaisesRegex(ValueError, "pull request"):
                    self.capture(install=True, event=event, pulls=pulls)
                self.assertFalse((self.root / "capture").exists())

    def test_capture_without_install_jobs_keeps_format_3(self):
        proof = self.capture(install=False, event="push")
        self.assertEqual(proof["format"], 3)
        self.assertEqual(proof["run"]["event"], "push")
        self.assertNotIn("pull_request", proof)
        self.assertEqual(self.lookups, [])
        self.assertFalse(any(path.startswith("native-catalog-install-") for path in proof["sha256"]))


if __name__ == "__main__":
    unittest.main()
