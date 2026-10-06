#!/usr/bin/env python3
"""Test the README demonstration verifier against synthetic records."""

import argparse
import contextlib
import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import unittest
import unittest.mock

sys.path.insert(0, str(Path(__file__).resolve().parent / "readme-demo"))
import capture  # noqa: E402
import verify  # noqa: E402

MANIFEST = Path(__file__).resolve().parent.parent / "docs" / "assets" / "starport-demo.json"
RENDER_AVAILABLE = importlib.util.find_spec("PIL") is not None and Path("/System/Library/Fonts/Menlo.ttc").is_file()
RECORD = "docs/proof/readme-demo/rehearsal-test"
SCENES = ["install", "catalog", "setup", "answer", "next"]
ANSWER = ["Rehearsal ", "fixture"]


def gif(width, height, delays):
    """Build a GIF with one graphic control extension and one image per delay."""
    data = b"GIF89a" + struct.pack("<HHBBB", width, height, 0x80, 0, 0) + b"\x00" * 6
    for delay in delays:
        data += b"\x21\xf9\x04\x08" + struct.pack("<H", delay) + b"\x00\x00"
        data += b"\x2c" + struct.pack("<HHHHB", 0, 0, width, height, 0) + b"\x02\x02\x44\x01\x00"
    return data + b"\x3b"


def png(width, height):
    return b"\x89PNG\r\n\x1a\n" + struct.pack(">I", 13) + b"IHDR" + struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0) \
        + b"\x00" * 4 + b"\x00\x00\x00\x00IEND\xaeB`\x82"


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


class RecordHarness(unittest.TestCase):
    """A git root with the manifest, a README, one product file, and a synthetic rehearsal record."""

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.git("init", "--quiet")
        (self.root / "docs" / "assets").mkdir(parents=True)
        (self.root / "docs" / "assets" / "starport-demo.json").write_text(MANIFEST.read_text())
        (self.root / "README.md").write_text("![Demo](docs/assets/first-use-v1.2.0/first-use.gif)\n")
        (self.root / "internal").mkdir()
        (self.root / "internal" / "app.go").write_text("package internal\n")
        self.commit("initial")
        self.head = self.git("rev-parse", "HEAD").strip()
        self.directory = self.root / RECORD
        self.directory.mkdir(parents=True)
        self.write_record()

    def git(self, *arguments):
        return subprocess.run(["git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
                               "-c", "commit.gpgsign=false", *arguments], cwd=self.root, check=True,
                              capture_output=True, text=True).stdout

    def commit(self, message):
        self.git("add", "-A")
        self.git("commit", "--quiet", "-m", message)

    def write_record(self):
        # Scenes start 1 s apart. The answer scene streams two deltas.
        events = []
        for index, name in enumerate(SCENES):
            events.append({"seconds": float(index), "data": "header " + name + "\n", "scene": name})
            events.append({"seconds": index + 0.5, "data": "body " + name + "\n"})
            if name == "answer":
                start = len(events)
                events += [{"seconds": 3.6, "data": ANSWER[0]}, {"seconds": 3.7, "data": ANSWER[1]},
                           {"seconds": 3.8, "data": "\nStream complete\n"}]
                interval = {"start_event": start, "end_event": start + 1, "start_seconds": 3.55, "end_seconds": 3.75}
        content = "".join(ANSWER)
        self.capture = {
            "schema_version": 1, "kind": "rehearsal", "events": events, "scenes": capture.scene_ranges(events),
            "commands": [{"scene": "catalog", "command": ["starport", "models", "show"],
                          "environment_names": ["HOME", "PATH"], "exit_code": 0}],
            "inference_interval": interval,
            "response": {"status": 200, "final_event": "[DONE]", "content": content, "content_length": len(content)},
        }
        cuts = []
        for before, after in zip(SCENES, SCENES[1:]):
            index = next(scene for scene in self.capture["scenes"] if scene["name"] == after)["start_event"]
            gap = events[index]["seconds"] - events[index - 1]["seconds"]
            cuts.append({"boundary": before + "/" + after, "before_event": index, "original_seconds": round(gap, 6),
                         "rendered_seconds": 3.0})
        self.render = {"font_size": 26, "cuts": cuts, "final_hold_ms": {"first-use.gif": 8000, "first-use-uncut.gif": 5000}}
        # The uncut GIF spans the last event time plus its 5 s hold.
        self.files = {
            "first-use.gif": gif(1280, 800, [300, 300, 300, 1100, 800]),
            "first-use-uncut.gif": gif(1280, 800, [450, 500]),
            "poster.png": png(1280, 800),
            "TRANSCRIPT.md": "Fixture `fixture_upstream` serves `gpt-4o-mini-rehearsal-fixture` and answers "
                             "Rehearsal fixture: Hello from Starport!\n",
        }
        self.record = {
            "schema_version": 1, "kind": "rehearsal", "qualifies_release_cases": False,
            "captured_at": "2026-10-04T21:00:00Z",
            "candidate": {"source": "ci-run", "run_id": "1", "pull_request": 1, "head_commit": self.head,
                          "snapshot_version": "starport version v0.0.0", "archive_name": "starport_0_darwin_arm64.tar.gz",
                          "archive_sha256": "a" * 64, "binary_sha256": "b" * 64, "release_tag": None},
            "fixtures": [{"name": "fixture_upstream", "role": "openai-compatible upstream",
                          "model": "gpt-4o-mini-rehearsal-fixture", "answer_text": "Rehearsal fixture: Hello from Starport!",
                          "credential": "generated throwaway token, disclosed as a fixture, value not recorded"}],
            "scenes": self.capture["scenes"], "inference_interval": interval, "cuts": cuts,
            "render": {"font": "Menlo", "font_path": "/System/Library/Fonts/Menlo.ttc", "font_size": 26,
                       "effective_font_at_900px": 26 * 900 / 1280},
            "discovery": {"catalog_environment_has_provider_key": False,
                          "answer_stream": {"status": 200, "done_event": True, "content_length": len(content)}},
            "human_review": {"reviewer": "Reviewer", "date": "2026-10-04", "pacing_verdict": "PASS", "notes": None},
        }
        self.save()

    def save(self):
        """Write every file and bind the record hashes to the written bytes."""
        files = dict(self.files, **{"events.json": json.dumps(self.capture), "render.json": json.dumps(self.render)})
        for name, value in files.items():
            path = self.directory / name
            path.write_bytes(value.encode() if isinstance(value, str) else value)
        outputs = {}
        for name in verify.OUTPUTS:
            path = self.directory / name
            outputs[name] = {"sha256": digest(path), "bytes": path.stat().st_size}
        edited = verify.gif_info(self.directory / "first-use.gif")
        outputs["first-use.gif"].update(edited)
        outputs["poster.png"].update(verify.png_size(self.directory / "poster.png"))
        uncut = self.directory / "first-use-uncut.gif"
        info = verify.gif_info(uncut)
        self.record["outputs"] = outputs
        self.record["uncut"] = {"path": uncut.name, "sha256": digest(uncut), "bytes": uncut.stat().st_size,
                                "duration_seconds": info["duration_seconds"], "frames": info["frames"]}
        (self.directory / "record.json").write_text(json.dumps(self.record))

    def verify(self, record=RECORD):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            code = verify.main(["--root", str(self.root), "--manifest", "docs/assets/starport-demo.json", "--json",
                                *(["--record", record] if record else [])])
        report = json.loads(output.getvalue())
        return code, report, {check["id"]: check for check in report["checks"]}

    def failed(self):
        code, report, checks = self.verify()
        self.assertEqual((code, report["status"]), (1, "FAIL"))
        return {name for name, check in checks.items() if check["status"] != "PASS"}


class ReadmeDemoVerifierTests(RecordHarness):
    def test_complete_record_passes_every_check(self):
        code, report, checks = self.verify()
        self.assertEqual((code, report["status"]), (0, "PASS"), report)
        self.assertEqual(list(checks), list(verify.CHECK_IDS))
        self.assertEqual(report["candidate_head"], self.head)
        self.assertEqual(report["readme_media"], ["docs/assets/first-use-v1.2.0/first-use.gif"])

    def test_human_output_ends_with_the_json_report(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            verify.main(["--root", str(self.root), "--manifest", "docs/assets/starport-demo.json", "--record", RECORD])
        raw = output.getvalue()
        self.assertTrue(raw.startswith("PASS output_hashes"))
        self.assertEqual(json.loads(raw[raw.rfind("\n{") + 1:])["status"], "PASS")

    def test_absent_record_fails_every_record_check(self):
        code, report, checks = self.verify(record=None)
        self.assertEqual((code, report["status"], report["record"]), (1, "FAIL", "docs/proof/readme-demo/rehearsal-2026-10-04"))
        self.assertEqual(len(checks), len(verify.CHECK_IDS))

    def test_unreadable_manifest_fails_every_check(self):
        (self.root / "docs" / "assets" / "starport-demo.json").write_text("{")
        code, report, checks = self.verify()
        self.assertEqual((code, report["status"]), (1, "FAIL"))
        self.assertTrue(all(check["status"] == "FAIL" for check in checks.values()))

    def test_changed_output_fails_its_hash(self):
        (self.directory / "poster.png").write_bytes(png(1280, 800) + b"\x00")
        self.assertIn("output_hashes", self.failed())

    def test_secret_values_fail_the_token_check(self):
        for leak in ("starport-demo-fixture-0123456789abcdef", "sk-" + "A" * 24,
                     "Gateway API key (shown once): value"):
            with self.subTest(leak=leak[:12]):
                self.files["TRANSCRIPT.md"] = self.files["TRANSCRIPT.md"].split("\n")[0] + "\n" + leak + "\n"
                self.save()
                self.assertEqual(self.failed(), {"no_fixture_token"})

    def test_cut_inside_the_inference_interval_fails(self):
        self.capture["inference_interval"]["start_seconds"] = 2.5
        self.record["inference_interval"] = self.capture["inference_interval"]
        self.save()
        self.assertEqual(self.failed(), {"cuts_outside_inference"})

    def test_cut_that_misstates_its_gap_fails(self):
        self.render["cuts"][0]["original_seconds"] = 0.25
        self.record["cuts"] = copy.deepcopy(self.render["cuts"])
        self.save()
        self.assertEqual(self.failed(), {"cuts_outside_inference"})

    def test_catalog_environment_with_a_provider_key_fails(self):
        self.capture["commands"][0]["environment_names"].append("OPENAI_API_KEY")
        self.save()
        self.assertEqual(self.failed(), {"catalog_keyless"})

    def test_rendered_answer_must_match_the_stream(self):
        self.capture["response"]["content"] = "Rehearsal fixturex"
        self.save()
        self.assertIn("answer_stream", self.failed())

    def test_uncut_source_must_keep_original_timing(self):
        self.files["first-use-uncut.gif"] = gif(1280, 800, [450, 400])
        self.save()
        self.assertEqual(self.failed(), {"uncut_source"})

    def test_scene_markers_must_follow_the_manifest(self):
        self.capture["events"][0]["scene"] = "catalog"
        self.save()
        self.assertIn("scene_order", self.failed())

    def test_small_gif_or_poster_fails(self):
        self.files["first-use.gif"] = gif(900, 600, [300, 300, 300, 1100, 800])
        self.save()
        self.assertTrue({"gif_dimensions", "poster_dimensions"} <= self.failed())

    def test_pending_or_failed_review_fails(self):
        for review in ({"reviewer": None, "date": None, "pacing_verdict": "PENDING"},
                       {"reviewer": "Reviewer", "date": "2026-10-04", "pacing_verdict": "FAIL"}):
            with self.subTest(review=review["pacing_verdict"]):
                self.record["human_review"] = review
                self.save()
                self.assertEqual(self.failed(), {"human_review"})

    def test_transcript_must_disclose_the_fixture(self):
        self.files["TRANSCRIPT.md"] = "No fixture named.\n"
        self.save()
        self.assertEqual(self.failed(), {"transcript_fixtures"})

    def test_readme_link_to_a_rehearsal_fails(self):
        (self.root / "README.md").write_text(f"![Demo]({RECORD}/first-use.gif)\n")
        self.assertEqual(self.failed(), {"readme_not_linking_rehearsal"})

    def test_product_change_after_the_candidate_invalidates_a_ci_record(self):
        (self.root / "internal" / "app.go").write_text("package internal\n\nconst changed = true\n")
        self.commit("product change")
        code, report, checks = self.verify()
        self.assertEqual((code, report["status"]), (2, "INVALID"))
        self.assertTrue(checks["invalidation"]["detail"].startswith("INVALID: 1 product file"))

    def test_changes_outside_product_paths_keep_the_record_valid(self):
        (self.root / "internal" / "console" / "dist").mkdir(parents=True)
        (self.root / "internal" / "console" / "dist" / "index.js").write_text("built\n")
        (self.root / "docs" / "notes.md").write_text("notes\n")
        self.commit("excluded change")
        code, report, _ = self.verify()
        self.assertEqual((code, report["status"]), (0, "PASS"))

    def test_local_candidate_is_not_invalidated(self):
        self.record["candidate"].update(source="local", run_id=None, pull_request=None)
        self.save()
        (self.root / "internal" / "app.go").write_text("package internal\n\nconst changed = true\n")
        self.commit("product change")
        code, report, checks = self.verify()
        self.assertEqual((code, report["status"]), (0, "PASS"))
        self.assertTrue(checks["invalidation"]["detail"].startswith("not applicable: a local candidate"))

    def test_unknown_candidate_head_fails(self):
        self.record["candidate"].update(head_commit="c" * 40, pull_request=None)
        self.save()
        self.assertEqual(self.failed(), {"invalidation"})

    def test_rehearsal_that_claims_release_cases_fails(self):
        self.record["qualifies_release_cases"] = True
        self.save()
        self.assertEqual(self.failed(), {"invalidation"})

    def make_release(self):
        """Turn the synthetic rehearsal into a complete release record."""
        content = "Hello from Starport!"
        events = self.capture["events"]
        interval = self.capture["inference_interval"]
        events[interval["start_event"]]["data"], events[interval["end_event"]]["data"] = "Hello ", "from Starport!"
        self.capture.update(kind="release", release_tag="v1.3.0", real_provider=True)
        self.capture["response"].update(content=content, content_length=len(content))
        self.record["discovery"]["answer_stream"]["content_length"] = len(content)
        self.files["TRANSCRIPT.md"] = f"Release v1.3.0. The provider answered \"{content}\".\n"
        self.record.update(kind="release", qualifies_release_cases=True, fixtures=[], real_provider=True)
        self.record["candidate"].update(source="release", run_id=None, pull_request=None, release_tag="v1.3.0",
                                        head_commit="d" * 40, archive_name="starport_1.3.0_darwin_arm64.tar.gz",
                                        checksum_verified=True, attestation_verified=True)
        self.save()

    def test_complete_release_record_passes_every_check(self):
        self.make_release()
        # A release record never invalidates by tree, even after a product change.
        (self.root / "internal" / "app.go").write_text("package internal\n\nconst changed = true\n")
        self.commit("product change")
        code, report, checks = self.verify()
        self.assertEqual((code, report["status"], report["kind"]), (0, "PASS", "release"), report)
        self.assertIn("release tag v1.3.0", checks["invalidation"]["detail"])
        self.assertIn("real provider answer", checks["transcript_fixtures"]["detail"])

    def test_release_record_binding_refusals(self):
        cases = {
            "no tag": {"release_tag": None},
            "inexact tag": {"release_tag": "latest"},
            "rehearsal source": {"source": "ci-run"},
            "unverified attestation": {"attestation_verified": False},
            "absent attestation": {"attestation_verified": None},
            "unverified checksum": {"checksum_verified": False},
            "other archive": {"archive_name": "starport_1.2.0_darwin_arm64.tar.gz"},
            "short tag commit": {"head_commit": "d" * 12},
            "CI run": {"run_id": "1"},
        }
        for name, change in cases.items():
            with self.subTest(case=name):
                self.make_release()
                self.record["candidate"].update(change)
                self.save()
                self.assertEqual(self.failed(), {"invalidation"})

    def test_release_record_must_qualify_release_cases(self):
        self.make_release()
        self.record["qualifies_release_cases"] = False
        self.save()
        self.assertEqual(self.failed(), {"invalidation"})

    def test_release_record_refuses_fixture_markers(self):
        for marker in ("starport-demo-fixture-", "gpt-4o-mini-rehearsal-fixture", "Rehearsal fixture: Hello"):
            with self.subTest(marker=marker):
                self.make_release()
                self.files["TRANSCRIPT.md"] += marker + "\n"
                self.save()
                self.assertEqual(self.failed(), {"no_fixture_token"})

    def test_release_record_needs_the_real_provider_answer(self):
        self.make_release()
        self.record["fixtures"] = [{"name": "fixture_upstream"}]
        self.save()
        self.assertIn("transcript_fixtures", self.failed())
        self.make_release()
        self.record.pop("real_provider")
        self.save()
        self.assertEqual(self.failed(), {"transcript_fixtures"})
        self.make_release()
        self.files["TRANSCRIPT.md"] = "Release v1.3.0.\n"
        self.save()
        self.assertEqual(self.failed(), {"transcript_fixtures"})

    def test_review_records_the_verdict_on_a_release_record(self):
        self.make_release()
        self.record["human_review"] = {"reviewer": None, "date": None, "pacing_verdict": "PENDING", "notes": None}
        self.save()
        self.assertEqual(self.failed(), {"human_review"})
        script = Path(__file__).resolve().parent / "record-readme-demo.sh"
        subprocess.run(["bash", str(script), "--review", str(self.directory), "--reviewer", "Reviewer",
                        "--pacing-verdict", "PASS"], check=True, capture_output=True, text=True, timeout=60)
        review = json.loads((self.directory / "record.json").read_text())["human_review"]
        self.assertEqual((review["reviewer"], review["pacing_verdict"]), ("Reviewer", "PASS"))
        code, report, _ = self.verify()
        self.assertEqual((code, report["status"], report["kind"]), (0, "PASS", "release"), report)

    def test_release_record_may_be_linked_from_the_readme(self):
        self.make_release()
        (self.root / "README.md").write_text(f"![Demo]({RECORD}/first-use.gif)\n")
        code, report, _ = self.verify()
        self.assertEqual((code, report["status"]), (0, "PASS"))


class MediaHeaderTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)

    def test_gif_info_counts_frames_and_delays(self):
        path = self.root / "a.gif"
        path.write_bytes(gif(1280, 800, [3, 250, 7]))
        self.assertEqual(verify.gif_info(path), {"width": 1280, "height": 800, "frames": 3, "duration_seconds": 2.6})

    def test_png_size_reads_the_header(self):
        path = self.root / "a.png"
        path.write_bytes(png(1280, 800))
        self.assertEqual(verify.png_size(path), {"width": 1280, "height": 800})

    def test_wrong_or_truncated_media_is_refused(self):
        cases = {"b.gif": b"PNG", "c.gif": gif(10, 10, [1])[:-1], "d.png": b"GIF89a" + b"\x00" * 20}
        for name, data in cases.items():
            with self.subTest(name=name):
                path = self.root / name
                path.write_bytes(data)
                with self.assertRaises(ValueError):
                    (verify.gif_info if name.endswith(".gif") else verify.png_size)(path)


FAKE_GH = """#!/usr/bin/env python3
import json, os, shutil, sys
from pathlib import Path
arguments = sys.argv[1:]
assets = Path(os.environ["FAKE_GH_ASSETS"])
if arguments[:1] == ["api"]:
    print(json.dumps({"sha": os.environ["FAKE_GH_COMMIT"]}))
elif arguments[:2] == ["release", "download"]:
    target = Path(arguments[arguments.index("--dir") + 1])
    target.mkdir(parents=True, exist_ok=True)
    for name in ("starport_1.3.0_darwin_arm64.tar.gz", "checksums.txt"):
        shutil.copyfile(assets / name, target / name)
elif arguments[:2] == ["attestation", "verify"]:
    if os.environ.get("FAKE_GH_ATTEST") == "fail":
        sys.exit(1)
    print(json.dumps([{"verificationResult": {"statement": {"predicateType": "https://slsa.dev/provenance/v1"}}}]))
else:
    sys.exit(2)
"""


class ReleaseCandidateTests(unittest.TestCase):
    """Resolve a release candidate through a fake gh on PATH. No network."""

    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.assets = self.root / "assets"
        self.assets.mkdir()
        self.archive = self.assets / "starport_1.3.0_darwin_arm64.tar.gz"
        self.archive.write_bytes(b"release archive bytes")
        (self.assets / "checksums.txt").write_text(f"{digest(self.archive)}  {self.archive.name}\n")
        bin_dir = self.root / "bin"
        bin_dir.mkdir()
        gh = bin_dir / "gh"
        gh.write_text(FAKE_GH)
        gh.chmod(0o755)
        self.commit = "c" * 40
        self.environment = dict(os.environ, PATH=f"{bin_dir}:{os.environ.get('PATH', '')}",
                                FAKE_GH_ASSETS=str(self.assets), FAKE_GH_COMMIT=self.commit)
        self.work = self.root / "work"

    def resolve(self, **overrides):
        with unittest.mock.patch.dict(os.environ, dict(self.environment, **overrides), clear=True):
            return capture.resolve_release("v1.3.0", self.work)

    def test_release_candidate_binds_the_attested_archive(self):
        candidate = self.resolve()
        self.assertEqual(candidate["source"], "release")
        self.assertEqual((candidate["run_id"], candidate["pull_request"], candidate["release_tag"]),
                         (None, None, "v1.3.0"))
        self.assertEqual(candidate["head_commit"], self.commit)
        self.assertEqual(candidate["archive_name"], self.archive.name)
        self.assertEqual(candidate["archive_sha256"], digest(self.archive))
        self.assertTrue(Path(candidate["archive_path"]).is_file())
        self.assertEqual((candidate["checksum_verified"], candidate["attestation_verified"]), (True, True))
        self.assertEqual(candidate["attestation"]["predicate_types"], ["https://slsa.dev/provenance/v1"])

    def test_checksum_mismatch_refuses_the_release(self):
        (self.assets / "checksums.txt").write_text(f"{'0' * 64}  {self.archive.name}\n")
        with self.assertRaisesRegex(RuntimeError, "does not match the release checksum"):
            self.resolve()

    def test_attestation_failure_refuses_the_release(self):
        with self.assertRaisesRegex(RuntimeError, "no verified attestation"):
            self.resolve(FAKE_GH_ATTEST="fail")

    def test_inexact_tag_is_refused_before_any_download(self):
        with self.assertRaisesRegex(RuntimeError, "vX.Y.Z"):
            capture.resolve_release("latest", self.work)
        self.assertFalse(self.work.exists())


class ReleaseCaptureTests(unittest.TestCase):
    def test_release_capture_refuses_without_the_provider_credential(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            candidate = {"source": "release", "release_tag": "v1.3.0", "archive_path": str(work / "absent.tar.gz"),
                         "archive_name": "starport_1.3.0_darwin_arm64.tar.gz", "archive_sha256": "a" * 64,
                         "head_commit": "c" * 40, "checksum_verified": True, "attestation_verified": True}
            (work / "candidate.json").write_text(json.dumps(candidate))
            arguments = argparse.Namespace(work=str(work), repository=None, port=19399, scene_pause=0.0)
            environment = {name: value for name, value in os.environ.items() if name != "OPENAI_API_KEY"}
            with unittest.mock.patch.dict(os.environ, environment, clear=True):
                with self.assertRaisesRegex(RuntimeError, "needs OPENAI_API_KEY"):
                    capture.capture_main(arguments)
            events = json.loads((work / "events.json").read_text())
            self.assertEqual((events["kind"], events["verdict"], events["real_provider"]), ("release", "FAIL", True))
            self.assertEqual(events["events"], [])

    def test_release_header_and_scene_titles_name_the_verified_release(self):
        run = capture.Capture({"source": "release", "release_tag": "v1.3.0"})
        with contextlib.redirect_stdout(io.StringIO()):
            run.scene("install", "1  INSTALL")
        self.assertEqual((run.kind, run.report["kind"], run.report["real_provider"]), ("release", "release", True))
        self.assertIn("verified release", run.events[0]["data"])
        rehearsal = capture.Capture({"source": "ci-run"})
        self.assertEqual((rehearsal.kind, rehearsal.report["real_provider"]), ("rehearsal", False))


class ReleaseRenderTests(RecordHarness):
    """Render a synthetic release capture and verify the record it writes."""

    def setUp(self):
        super().setUp()
        self.work = self.root / "work"
        self.work.mkdir()
        self.output = self.root / "docs" / "proof" / "readme-demo" / "release-v1.3.0"

    def release_capture(self):
        clear = "\x1b[2J\x1b[H"
        events = []
        for index, name in enumerate(SCENES):
            events.append({"seconds": float(index * 2), "data": clear + "STARPORT / verified release\n\n" + name + "\n",
                           "scene": name})
            events.append({"seconds": index * 2 + 0.5, "data": "body " + name + "\n"})
            if name == "answer":
                start = len(events)
                events += [{"seconds": index * 2 + 0.6, "data": "Hello "}, {"seconds": index * 2 + 0.8, "data": "from Starport!"},
                           {"seconds": index * 2 + 0.9, "data": "\nStream complete: [DONE]\n"}]
                interval = {"start_event": start, "end_event": start + 1, "start_seconds": index * 2 + 0.55,
                            "end_seconds": index * 2 + 0.85}
        content = "Hello from Starport!"
        return {
            "schema_version": 1, "kind": "release", "release_tag": "v1.3.0", "real_provider": True, "verdict": "PASS",
            "captured_at": "2026-10-06T12:00:00Z", "events": events, "scenes": capture.scene_ranges(events),
            "commands": [{"scene": "catalog", "command": ["starport", "models", "show"],
                          "environment_names": ["HOME", "PATH"], "exit_code": 0}],
            "inference_interval": interval, "starport_version": "starport version v1.3.0",
            "catalog_generation": "gen", "base_url_mechanism": None, "provider_credential_source": "environment",
            "network_egress_denied": False, "network_egress_reason": "real provider",
            "catalog_environment_has_provider_key": False, "persistent_selectors_present": False,
            "leftover_home_files": [], "shutdown_exit_code": 0, "fixture_requests": [],
            "response": {"status": 200, "content_type": "text/event-stream", "final_event": "[DONE]",
                         "content": content, "content_length": len(content), "stream_events": 3,
                         "reported_provider": "openai", "reported_model": "gpt-4o-mini"},
        }

    def release_candidate(self):
        return {"source": "release", "run_id": None, "run_url": None, "pull_request": None, "head_commit": "d" * 40,
                "archive_name": "starport_1.3.0_darwin_arm64.tar.gz", "archive_sha256": "a" * 64,
                "release_tag": "v1.3.0", "archive_path": "/nonexistent", "checksum_verified": True,
                "attestation_verified": True, "attestation": {"predicate_types": ["https://slsa.dev/provenance/v1"],
                                                               "statements": 1},
                "snapshot_version": "starport version v1.3.0", "binary_sha256": "b" * 64}

    def render_release(self, events, candidate):
        import render  # noqa: PLC0415
        (self.work / "events.json").write_text(json.dumps(events))
        (self.work / "candidate.json").write_text(json.dumps(candidate))
        with contextlib.redirect_stdout(io.StringIO()):
            with unittest.mock.patch.object(sys, "argv", ["render.py", "--work", str(self.work),
                                                          "--output", str(self.output)]):
                render.main()

    @unittest.skipUnless(RENDER_AVAILABLE, "rendering needs Pillow and the Menlo font")
    def test_rendered_release_record_passes_the_verifier(self):
        self.render_release(self.release_capture(), self.release_candidate())
        record = json.loads((self.output / "record.json").read_text())
        self.assertEqual((record["kind"], record["qualifies_release_cases"], record["fixtures"], record["real_provider"]),
                         ("release", True, [], True))
        self.assertEqual(record["candidate"]["attestation_verified"], True)
        self.assertEqual(record["discovery"]["provider_credential_source"], "environment")
        transcript = (self.output / "TRANSCRIPT.md").read_text()
        self.assertIn("Hello from Starport!", transcript)
        self.assertNotRegex(transcript.lower(), "rehearsal[- ]fixture|starport-demo-fixture-")
        script = Path(__file__).resolve().parent / "record-readme-demo.sh"
        subprocess.run(["bash", str(script), "--review", str(self.output), "--reviewer", "Reviewer",
                        "--pacing-verdict", "PASS"], check=True, capture_output=True, text=True, timeout=60)
        code, report, checks = self.verify(record=str(self.output.relative_to(self.root)))
        self.assertEqual((code, report["status"], report["kind"]), (0, "PASS", "release"), report)

    @unittest.skipUnless(RENDER_AVAILABLE, "rendering needs Pillow and the Menlo font")
    def test_release_render_refuses_a_fixture_answer_or_a_rehearsal_candidate(self):
        events = self.release_capture()
        events["response"]["content"] = "Rehearsal fixture: Hello from Starport!"
        with self.assertRaisesRegex(SystemExit, "not a real provider answer"):
            self.render_release(events, self.release_candidate())
        candidate = dict(self.release_candidate(), source="ci-run")
        with self.assertRaisesRegex(SystemExit, "release candidate"):
            self.render_release(self.release_capture(), candidate)


class CaptureTests(unittest.TestCase):
    def test_scene_ranges_cover_events_between_markers(self):
        events = [{"seconds": 0.0, "data": "a", "scene": "install"}, {"seconds": 0.5, "data": "b"},
                  {"seconds": 1.0, "data": "c", "scene": "catalog"}]
        self.assertEqual(capture.scene_ranges(events), [
            {"name": "install", "start_event": 0, "end_event": 1, "start_seconds": 0.0, "end_seconds": 0.5},
            {"name": "catalog", "start_event": 2, "end_event": 2, "start_seconds": 1.0, "end_seconds": 1.0}])

    def test_local_head_needs_a_describe_version(self):
        self.assertIsNone(capture.local_head("starport version v1.2.1", "."))
        self.assertIsNone(capture.local_head("starport version v1.2.1-4-gabcdef1", None))


if __name__ == "__main__":
    unittest.main()
