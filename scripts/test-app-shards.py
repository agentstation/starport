#!/usr/bin/env python3
"""Test exhaustive app discovery, failure propagation, and evidence ownership."""

import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import app_shards as shards


class AppShardTests(unittest.TestCase):
    def setUp(self):
        self.names = sorted([f"TestContract{i}" for i in range(40)] + ["ExampleGateway", "FuzzRequest"])
        self.records = {}
        for index in range(shards.SHARDS):
            selected = shards.partition(self.names, index)
            self.records[index] = {
                "roster.json": {"version": 1, "index": index, "shards": shards.SHARDS,
                                "source": "a" * 40, "workflow_head": "b" * 40, "owners": self.names, "selected": selected},
                "toolchain.txt": "same-native-toolchain\n",
                "tests.jsonl": [self.event("run", name) for name in selected] +
                               [self.event("pass", name) for name in selected] + [self.event("pass")],
            }

    def event(self, action, name=None):
        value = {"Action": action, "Package": shards.PACKAGE}
        if name:
            value["Test"] = name
        return value

    def load(self, index, name):
        value = self.records[index][name]
        if name == "tests.jsonl":
            return "".join(json.dumps(event) + "\n" for event in value)
        return json.dumps(value) if name == "roster.json" else value

    def test_every_discovered_owner_has_one_stable_assignment(self):
        self.assertEqual(sorted(n for i in range(shards.SHARDS) for n in shards.partition(self.names, i)), self.names)
        expanded = sorted(self.names + ["TestNewContract"])
        for index in range(shards.SHARDS):
            self.assertEqual([n for n in shards.partition(expanded, index) if n != "TestNewContract"], shards.partition(self.names, index))
        events, _ = shards.verify_shards(self.load)
        self.assertEqual(sum(e["Action"] == "run" for e in events), len(self.names))

    def test_duplicate_or_empty_discovery_refused(self):
        for names in [[], self.names + self.names, ["BenchmarkOnly"], list(reversed(self.names))]:
            with self.subTest(names=names), self.assertRaises(ValueError):
                shards.partition(names, 0)

    def test_missing_duplicated_failed_and_foreign_executions_refused(self):
        original = copy.deepcopy(self.records[0]["tests.jsonl"])
        for changed in [original[1:], original + original[:1], original + [self.event("fail")],
                        original[:-1], original + [self.event("pass")], [None], [True],
                        original + [self.event("run", "TestUnselected")],
                        [dict(e, Package="foreign") for e in original]]:
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                self.records[0]["tests.jsonl"] = changed
                shards.verify_shards(self.load)
        self.records[0]["tests.jsonl"] = original

    def test_selection_source_discovery_and_toolchain_must_agree(self):
        for key, value in [("source", "b" * 40), ("source", "short"), ("index", 1), ("shards", 5),
                           ("selected", []), ("owners", sorted(self.names + ["TestNewContract"]))]:
            original = copy.deepcopy(self.records[0]["roster.json"])
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.records[0]["roster.json"][key] = value
                shards.verify_shards(self.load)
            self.records[0]["roster.json"] = original
        self.records[0]["toolchain.txt"] = "different\n"
        with self.assertRaises(ValueError):
            shards.verify_shards(self.load)

    def test_uniform_foreign_source_is_refused(self):
        for expectation in [{"expected_source": "c" * 40}, {"expected_head": "c" * 40}]:
            with self.subTest(expectation=expectation), self.assertRaises(ValueError):
                shards.verify_shards(self.load, **expectation)
        shards.verify_shards(self.load, expected_source="a" * 40, expected_head="b" * 40)

    def test_optional_skip_remains_visible(self):
        event = next(e for e in self.records[0]["tests.jsonl"] if e["Action"] == "pass" and e.get("Test"))
        event["Action"] = "skip"
        events, _ = shards.verify_shards(self.load)
        self.assertEqual(sum(e["Action"] == "skip" for e in events), 1)

    def test_unicode_output_does_not_change_owner_accounting(self):
        owner = self.records[0]["roster.json"]["selected"][0]
        self.records[0]["tests.jsonl"].append(dict(self.event("output", owner), Output="catalogue café\n"))
        events, _ = shards.verify_shards(self.load)
        self.assertTrue(any("café" in e.get("Output", "") for e in events))

    def test_go_failure_cannot_be_replaced_by_successful_event_validation(self):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "proof"
            responses = ["\n".join(self.names) + "\nok\tpackage\t0.01s\n", "a" * 40, "go version\n", "native\n"]
            with patch.object(shards.subprocess, "check_output", side_effect=responses), \
                 patch.object(shards.subprocess, "run") as run:
                run.return_value.returncode = 17
                self.assertEqual(shards.run(Path(temporary), output, 0), 17)
                self.assertTrue((output / "roster.json").exists())
                self.assertIn("-race", run.call_args.args[0])
                self.assertIn("-count=1", run.call_args.args[0])


if __name__ == "__main__":
    unittest.main()
