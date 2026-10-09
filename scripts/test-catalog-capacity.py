"""Check named capacity evidence and the bounded native process runner."""

import importlib.util
import copy
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

spec = importlib.util.spec_from_file_location("catalog_capacity", Path(__file__).with_name("catalog_capacity.py"))
capacity = importlib.util.module_from_spec(spec)
spec.loader.exec_module(capacity)


def evidence():
    return [{"Package": capacity.PACKAGE, "Test": capacity.DECODED_TEST, "Action": "run"},
            {"Package": capacity.PACKAGE, "Test": capacity.DECODED_TEST, "Action": "pass"},
            {"Package": capacity.PACKAGE, "Action": "pass"}]


class CatalogCapacityEvidenceTests(unittest.TestCase):
    def test_exact_named_package_pass(self):
        capacity.check_named_result(evidence(), capacity.DECODED_TEST)

    def test_uncertain_named_evidence_is_refused(self):
        cases = {"empty": [], "malformed": [None], "missing-run": evidence()[1:],
                 "missing-pass": evidence()[:1] + evidence()[2:], "missing-package": evidence()[:2],
                 "duplicate-run": evidence() + [evidence()[0]], "duplicate-pass": evidence() + [evidence()[1]],
                 "duplicate-package": evidence() + [evidence()[2]],
                 "skipped": evidence() + [{"Package": capacity.PACKAGE, "Test": capacity.DECODED_TEST + "/fixture", "Action": "skip"}],
                 "failed": evidence() + [{"Package": capacity.PACKAGE, "Test": "Other", "Action": "fail"}],
                 "wrong-package": [{**item, "Package": "other"} for item in evidence()]}
        for name, events in cases.items():
            with self.subTest(name=name), self.assertRaises(ValueError):
                capacity.check_named_result(events, capacity.DECODED_TEST)

    def test_maximum_record_requires_native_coverage_and_independent_bounds(self):
        valid = {"entries": 96, "serialized_fleet_snapshot_bytes": 2 * capacity.GIB - (4 << 20),
                 "accepted_generation": "accepted", "candidate_generation": "candidate",
                 "original_manifest_sha256": "a" * 64, "fleet_manifest_sha256": "b" * 64, "reverse_manifest_sha256": "c" * 64,
                 "producer_batches": [{"inputs": 96, "raw_bytes": 1, "decoded_recovery_bytes": 1}] * 2,
                 "forward_stages": [{"index": 0, "mutations": 128, "bytes": 4 << 20}],
                 "reverse_stages": [{"index": 0, "mutations": 1, "bytes": 1}],
                 "forward_compiler_seal_bytes": 65536, "reverse_compiler_seal_bytes": 1,
                 "rollback_entries": 32, "lost_reply_exact_retries": 2}

        def event(record):
            return {"Package": capacity.PACKAGE, "Test": capacity.MAXIMUM_TEST, "Output": "fixture: CATALOG_MAXIMUM_EVIDENCE " + json.dumps(record) + "\n"}

        self.assertEqual(capacity.maximum_evidence([event(valid)]), valid)
        changes = [("entry-count", ("entries",), 95), ("inventory-low", ("serialized_fleet_snapshot_bytes",), capacity.GIB),
                   ("inventory-high", ("serialized_fleet_snapshot_bytes",), 2 * capacity.GIB + 1),
                   ("same-selection", ("candidate_generation",), "accepted"), ("missing-original", ("original_manifest_sha256",), ""),
                   ("lost-retention", ("producer_batches", 0, "inputs"), 95),
                   ("raw-overflow", ("producer_batches", 0, "raw_bytes"), (256 << 20) + 1),
                   ("decoded-overflow", ("producer_batches", 0, "decoded_recovery_bytes"), (256 << 20) + 1),
                   ("mutation-overflow", ("forward_stages", 0, "mutations"), 129),
                   ("byte-overflow", ("reverse_stages", 0, "bytes"), (4 << 20) + 1),
                   ("stage-order", ("reverse_stages", 0, "index"), 1), ("seal-overflow", ("forward_compiler_seal_bytes",), 65537),
                   ("rollback-loss", ("rollback_entries",), 31), ("retry-loss", ("lost_reply_exact_retries",), 1)]
        for name, path, value in changes:
            changed = copy.deepcopy(valid)
            parent = changed
            for key in path[:-1]:
                parent = parent[key]
            parent[path[-1]] = value
            with self.subTest(name=name), self.assertRaises(ValueError):
                capacity.maximum_evidence([event(changed)])
        for events in ([], [event(valid), event(valid)], [{**event(valid), "Test": "Other"}]):
            with self.assertRaises(ValueError):
                capacity.maximum_evidence(events)

    def test_scratch_counts_allocated_native_bytes_once(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            original = root / "original"
            original.write_bytes(bytes(1 << 20))
            with (root / "sparse").open("wb") as sparse:
                sparse.truncate(32 << 20)
            os.link(original, root / "same-native-file")
            expected = original.stat().st_blocks * 512 + (root / "sparse").stat().st_blocks * 512
            self.assertEqual(capacity.scratch_bytes(root), expected)

    def assert_setup_refusal(self, directory, environment, module=None, failed_query=None, failed_source=False):
        root = Path(directory)
        output = root / "evidence"

        def query(command, _, __):
            if command == failed_query:
                raise RuntimeError("setup query failed")
            if command == ["go", "env", "GOVERSION"]:
                return "go1.27.2"
            if command[:3] == ["go", "list", "-m"]:
                return json.dumps(module or {"Version": "v0.16.6-0.20260930082412-e0845d601fb5"})
            return "go version go1.27.2 linux/amd64"

        with mock.patch.dict(os.environ, environment), mock.patch.object(capacity.platform, "system", return_value="Linux"), \
                mock.patch.object(capacity.os, "sysconf", side_effect=lambda name: 4096 if name == "SC_PAGE_SIZE" else 8 << 20), \
                mock.patch.object(capacity.shutil, "disk_usage", return_value=SimpleNamespace(free=24 * capacity.GIB)), \
                mock.patch.object(capacity, "command_output", side_effect=query), \
                mock.patch.object(capacity, "source_state", side_effect=RuntimeError("source state failed") if failed_source else None, return_value={}), \
                mock.patch.object(capacity, "run_measured") as measured:
            with self.assertRaises((ValueError, RuntimeError)):
                capacity.run_profile(root, output, "decoded-pure", False)
            measured.assert_not_called()
        record = json.loads((output / "verification.json").read_text())
        self.assertEqual(record["status"], "FAIL")
        self.assertTrue(record["error"])
        self.assertIsNone(record["test_evidence_sha256"])
        self.assertFalse((output / "scratch").exists())
        return record

    def test_published_qualification_refuses_workspaces_and_module_replacements(self):
        cases = [("workspace-path", {"GOWORK": "/tmp/unpublished.work"}, None),
                 ("workspace-auto", {"GOWORK": "auto"}, None),
                 ("workspace-empty", {"GOWORK": ""}, None),
                 ("local-replacement", {"GOWORK": "off"}, {"Version": "v1.0.0", "Replace": {"Dir": "/tmp/producer"}}),
                 ("version-replacement", {"GOWORK": "off"}, {"Version": "v1.0.0", "Replace": {"Version": "v1.1.0"}}),
                 ("workspace-main", {"GOWORK": "off"}, {"Main": True}),
                 ("unpublished-module", {"GOWORK": "off"}, {"Path": "github.com/agentstation/starmap"})]
        for name, environment, module in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                self.assert_setup_refusal(directory, environment, module)

    def test_setup_failures_retain_evidence_and_remove_scratch(self):
        cases = [("toolchain-query", ["go", "env", "GOVERSION"], False),
                 ("module-query", ["go", "list", "-m", "-json", "github.com/agentstation/starmap"], False),
                 ("source-query-after-scratch", None, True),
                 ("version-query-after-scratch", ["go", "version"], False)]
        for name, query, failed_source in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                record = self.assert_setup_refusal(directory, {"GOWORK": "off"}, failed_query=query, failed_source=failed_source)
                self.assertIn("failed", record["error"])

    @unittest.skipUnless(hasattr(os, "wait4"), "Native process measurement requires wait4.")
    def test_native_process_exit_and_resource_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            command = [sys.executable, "-c", "import sys; data=bytearray(12<<20); print(len(data)); sys.exit(7)"]
            result = capacity.run_measured(command, root, os.environ.copy(), root / "stdout", root / "stderr", root, 5, capacity.GIB, capacity.GIB)
            self.assertEqual(result["exit_code"], 7)
            self.assertGreater(result["largest_process_high_water_rss_bytes"], 12 << 20)
            self.assertEqual((root / "stdout").read_text().strip(), str(12 << 20))

    @unittest.skipUnless(hasattr(os, "wait4"), "Native process measurement requires wait4.")
    def test_native_bound_stops_only_owned_process(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            command = [sys.executable, "-c", "import time; time.sleep(30)"]
            with self.assertRaises(capacity.CapacityBoundExceeded) as caught:
                capacity.run_measured(command, root, os.environ.copy(), root / "stdout", root / "stderr", root, 0.1, capacity.GIB, capacity.GIB)
            self.assertGreater(caught.exception.measurements["elapsed_seconds"], 0.1)
            self.assertGreater(caught.exception.measurements["sampled_tree_peak_rss_bytes"], 0)


if __name__ == "__main__":
    unittest.main()
