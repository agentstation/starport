#!/usr/bin/env python3
"""Test native catalog evidence integrity and source binding."""

import copy
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

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
            self.bind(runner, "toolchain.txt", f"go version go1.27.1 windows/{arch}\nwindows\n{arch}\nwindows\n{arch}\n" + ("0\n" if arch == "arm64" else "1\n"))
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
        for content in [original.replace("go1.27.1", "go1.26.6"), original.replace("windows", "linux", 1), original.replace("amd64\nwindows", "arm64\nwindows"), original.removesuffix("1\n") + "0\n"]:
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


if __name__ == "__main__":
    unittest.main()
