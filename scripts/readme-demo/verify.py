#!/usr/bin/env python3
"""Verify a README demonstration record against the demonstration manifest.

The verifier uses only the Python standard library, so Linux CI can check a
committed record without Pillow. It reads GIF and PNG headers directly. The
last block on standard output is one JSON report. The exit code is 0 for
PASS, 1 for FAIL, and 2 for INVALID.
"""
import argparse
import datetime
import hashlib
import json
from pathlib import Path
import re
import struct
import subprocess
import sys

CHECK_IDS = (
    "output_hashes", "gif_dimensions", "gif_budget", "effective_font", "scene_order", "catalog_keyless",
    "answer_stream", "cuts_outside_inference", "uncut_source", "no_fixture_token", "poster_dimensions",
    "transcript_fixtures", "human_review", "readme_not_linking_rehearsal", "invalidation")
OUTPUTS = ("first-use.gif", "poster.png", "events.json", "render.json", "TRANSCRIPT.md")
# The capture gives the catalog command only these names, so no provider
# credential or inference origin can reach it.
CATALOG_ENVIRONMENT = frozenset(("PATH", "HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME",
                                 "STARPORT_SERVER_PORT"))
# capture.py generates the fixture token with this prefix. A match in a text
# output means that a token value leaked. The other patterns find a provider
# key or the startup line that shows the gateway key.
SECRET_PATTERNS = (
    ("fixture token", re.compile(rb"starport-demo-fixture-[0-9a-f]{8,}")),
    ("provider key", re.compile(rb"sk-[A-Za-z0-9_-]{20,}")),
    ("gateway key line", re.compile(rb"Gateway API key \(shown once\)")),
)
# A release record comes from a real provider, so any fixture marker means that
# the fixture upstream, its token, or its answer reached the record.
FIXTURE_MARKERS = re.compile(rb"starport-demo-fixture-|(?i:rehearsal[- ]fixture)")
TEXT_FILES = ("events.json", "render.json", "TRANSCRIPT.md", "record.json")
README_LINK = re.compile(r"\]\(([^)\s]+)(?:\s+\"[^\"]*\")?\)|(?:src|href)=\"([^\"]+)\"")
MEDIA_SUFFIXES = (".gif", ".png", ".jpg", ".jpeg", ".webp", ".svg", ".mp4", ".webm")
DATE = re.compile(r"\d{4}-\d{2}-\d{2}")
HEX40 = re.compile(r"[0-9a-f]{40}")
HEX64 = re.compile(r"[0-9a-f]{64}")
RELEASE_TAG = re.compile(r"v\d+\.\d+\.\d+(?:-rc\.\d+)?")
# GIF frame delays use centiseconds, so durations agree to one centisecond
# per frame boundary at most.
DURATION_TOLERANCE_SECONDS = 0.02


class Invalid(Exception):
    """The invalidation rule fired for this record."""


def gif_info(path):
    """Return the logical screen size, frame count, and total delay of a GIF file."""
    data = Path(path).read_bytes()
    if data[:6] not in (b"GIF87a", b"GIF89a"):
        raise ValueError(f"{Path(path).name} is not a GIF file")
    width, height = struct.unpack("<HH", data[6:10])
    position = 13
    if data[10] & 0x80:
        position += 3 * 2 ** ((data[10] & 0x07) + 1)
    frames, centiseconds, delay = 0, 0, 0

    def skip_sub_blocks(offset):
        while True:
            size = data[offset]
            offset += 1
            if size == 0:
                return offset
            offset += size

    while True:
        if position >= len(data):
            raise ValueError(f"{Path(path).name} ends before its trailer")
        marker = data[position]
        if marker == 0x3B:
            break
        if marker == 0x21:
            label = data[position + 1]
            position += 2
            if label == 0xF9 and data[position] >= 4:
                delay = struct.unpack("<H", data[position + 2:position + 4])[0]
            position = skip_sub_blocks(position)
        elif marker == 0x2C:
            flags = data[position + 9]
            position += 10
            if flags & 0x80:
                position += 3 * 2 ** ((flags & 0x07) + 1)
            position = skip_sub_blocks(position + 1)
            frames += 1
            centiseconds += delay
            delay = 0
        else:
            raise ValueError(f"{Path(path).name} has an unknown block at byte {position}")
    return {"width": width, "height": height, "frames": frames, "duration_seconds": centiseconds / 100}


def png_size(path):
    """Return the width and height from the IHDR chunk of a PNG file."""
    data = Path(path).read_bytes()[:24]
    if data[:8] != b"\x89PNG\r\n\x1a\n" or data[12:16] != b"IHDR":
        raise ValueError(f"{Path(path).name} is not a PNG file")
    width, height = struct.unpack(">II", data[16:24])
    return {"width": width, "height": height}


def sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def require(condition, message):
    if not condition:
        raise ValueError(message)


def read_json(path):
    with Path(path).open(encoding="utf-8") as stream:
        return json.load(stream)


def readme_links(text):
    links = []
    for match in README_LINK.finditer(text):
        target = (match.group(1) or match.group(2)).split("#", 1)[0]
        if target and "://" not in target and not target.startswith("mailto:"):
            links.append(target)
    return links


class Verification:
    """Hold one manifest and record, and evaluate each named check."""

    def __init__(self, root, manifest_argument, record_argument):
        self.root = Path(root).resolve()
        self.manifest_argument = manifest_argument
        self.manifest = read_json(self.root / manifest_argument)
        require(isinstance(self.manifest, dict), "the manifest must be a JSON object")
        self.record_argument = record_argument or self.manifest.get("current_rehearsal")
        require(isinstance(self.record_argument, str) and self.record_argument,
                "the manifest names no current rehearsal")
        self.directory = (self.root / self.record_argument).resolve()
        self._record = self._events = self._render = None

    def file(self, name):
        path = (self.directory / name).resolve()
        require(path.parent == self.directory or self.directory in path.parents,
                f"{name} is outside the record directory")
        return path

    def loaded(self, name):
        path = self.file(name)
        require(path.is_file(), f"the record directory has no {name}: {self.record_argument}")
        value = read_json(path)
        require(isinstance(value, dict), f"{name} must be a JSON object")
        return value

    @property
    def record(self):
        if self._record is None:
            self._record = self.loaded("record.json")
        return self._record

    @property
    def events(self):
        if self._events is None:
            self._events = self.loaded("events.json")
        return self._events

    @property
    def render(self):
        if self._render is None:
            self._render = self.loaded("render.json")
        return self._render

    @property
    def limits(self):
        return self.manifest["limits"]

    def output(self, name):
        entry = self.record["outputs"][name]
        require(isinstance(entry, dict), f"the record has no output entry for {name}")
        return entry

    def scene(self, name):
        for scene in self.record["scenes"]:
            if scene.get("name") == name:
                return scene
        raise ValueError(f"the record has no {name} scene")

    def check_output_hashes(self):
        outputs = self.record["outputs"]
        missing = [name for name in OUTPUTS if name not in outputs]
        require(not missing, "the record omits outputs: " + ", ".join(missing))
        entries = [(name, entry) for name, entry in outputs.items()] + [(self.record["uncut"]["path"], self.record["uncut"])]
        for name, entry in entries:
            path = self.file(name)
            require(path.is_file(), f"{name} is absent")
            require(entry.get("sha256") == sha256(path), f"{name} does not match its recorded SHA-256")
            require(entry.get("bytes") == path.stat().st_size, f"{name} does not match its recorded size")
        return f"{len(entries)} files match"

    def check_gif_dimensions(self):
        edited = gif_info(self.file("first-use.gif"))
        uncut = gif_info(self.file(self.record["uncut"]["path"]))
        entry = self.output("first-use.gif")
        require((edited["width"], edited["height"]) == (entry.get("width"), entry.get("height")),
                f"the GIF header is {edited['width']}x{edited['height']}, not the recorded size")
        require(edited["width"] >= self.limits["min_width"],
                f"the GIF width {edited['width']} is below {self.limits['min_width']}")
        require(edited["frames"] == entry.get("frames"), "the GIF frame count does not match the record")
        require(abs(edited["duration_seconds"] - entry.get("duration_seconds", -1)) <= DURATION_TOLERANCE_SECONDS,
                "the GIF duration does not match the record")
        require((uncut["width"], uncut["height"]) == (edited["width"], edited["height"]),
                "the uncut GIF size differs from the edited GIF")
        return f"{edited['width']}x{edited['height']}, {edited['frames']} frames, {edited['duration_seconds']:.2f} s"

    def check_gif_budget(self):
        size = self.file("first-use.gif").stat().st_size
        budget = self.limits["gif_max_bytes"]
        require(size < budget, f"the GIF has {size} bytes, which is not below {budget}")
        duration = gif_info(self.file("first-use.gif"))["duration_seconds"]
        return (f"{size} of {budget} bytes; {duration:.2f} s against the "
                f"{self.limits['duration_target_seconds']} s editorial target")

    def check_effective_font(self):
        settings = self.record["render"]
        width = gif_info(self.file("first-use.gif"))["width"]
        effective = settings["font_size"] * 900 / width
        require(abs(effective - settings["effective_font_at_900px"]) < 0.01,
                "the recorded effective font does not match the font size and GIF width")
        require(self.render.get("font_size") == settings["font_size"], "render.json has a different font size")
        minimum = self.limits["min_effective_font_at_900px"]
        require(effective >= minimum, f"the effective font {effective:.2f} px is below {minimum} px")
        return f"{settings['font']} {settings['font_size']} px gives {effective:.2f} px at 900 px"

    def check_scene_order(self):
        scenes = self.record["scenes"]
        expected = self.manifest["scenes"]
        require([scene.get("name") for scene in scenes] == expected,
                "the record scenes are not the manifest scenes in order")
        events = self.events["events"]
        markers = [event.get("scene") for event in events if event.get("scene")]
        require(markers == expected, "the event markers are not the manifest scenes in order")
        previous_end = -1
        for scene in scenes:
            start, end = scene["start_event"], scene["end_event"]
            require(previous_end < start <= end < len(events), f"the {scene['name']} scene has an invalid event range")
            require(events[start].get("scene") == scene["name"], f"the {scene['name']} scene does not start at its marker")
            require(events[start]["seconds"] == scene["start_seconds"] and events[end]["seconds"] == scene["end_seconds"],
                    f"the {scene['name']} scene seconds do not match its events")
            previous_end = end
        return " -> ".join(expected)

    def check_catalog_keyless(self):
        require(self.record["discovery"]["catalog_environment_has_provider_key"] is False,
                "the record does not state a keyless catalog scene")
        commands = [command for command in self.events["commands"] if command.get("scene") == "catalog"]
        require(commands, "the events hold no catalog scene command")
        for command in commands:
            names = set(command["environment_names"])
            require(names <= CATALOG_ENVIRONMENT,
                    "the catalog command environment holds " + ", ".join(sorted(names - CATALOG_ENVIRONMENT)))
        return f"{len(commands)} catalog command(s) ran with only " + ", ".join(sorted(CATALOG_ENVIRONMENT))

    def check_answer_stream(self):
        stream = self.record["discovery"]["answer_stream"]
        response = self.events["response"]
        require(stream.get("status") == 200 and response.get("status") == 200, "the answer stream status is not 200")
        require(stream.get("done_event") is True and response.get("final_event") == "[DONE]",
                "the answer stream does not end with [DONE]")
        content = response.get("content", "")
        require(content and stream.get("content_length") == len(content), "the answer stream carries no recorded content")
        interval = self.record["inference_interval"]
        shown = "".join(event["data"] for event in self.events["events"][interval["start_event"]:interval["end_event"] + 1])
        require(shown == content, "the rendered answer events differ from the streamed content")
        return f"200, {len(content)} characters, [DONE]"

    def check_cuts_outside_inference(self):
        interval = self.record["inference_interval"]
        require(interval == self.events["inference_interval"], "the record and the events disagree on the inference interval")
        events = self.events["events"]
        answer = self.scene("answer")
        require(answer["start_event"] <= interval["start_event"] <= interval["end_event"] <= answer["end_event"],
                "the inference interval is outside the answer scene")
        require(interval["start_seconds"] <= events[interval["start_event"]]["seconds"]
                and events[interval["end_event"]]["seconds"] <= interval["end_seconds"],
                "the inference interval does not contain its events")
        cuts = self.record["cuts"]
        require(cuts == self.render.get("cuts"), "render.json and the record disagree on the cuts")
        order = self.manifest["scenes"]
        for cut in cuts:
            parts = cut["boundary"].split("/")
            require(len(parts) == 2 and parts[0] in order and parts[1] in order
                    and order.index(parts[1]) == order.index(parts[0]) + 1,
                    f"the cut {cut['boundary']} is not a boundary between adjacent scenes")
            index = cut["before_event"]
            require(index == self.scene(parts[1])["start_event"], f"the cut {cut['boundary']} is not at its scene start")
            begin, end = events[index - 1]["seconds"], events[index]["seconds"]
            require(abs((end - begin) - cut["original_seconds"]) < 1e-6, f"the cut {cut['boundary']} misstates its gap")
            require(end <= interval["start_seconds"] or begin >= interval["end_seconds"],
                    f"the cut {cut['boundary']} intersects the inference interval")
            require(cut["rendered_seconds"] > 0, f"the cut {cut['boundary']} has no rendered gap")
        return f"{len(cuts)} cut(s) outside {interval['start_seconds']:.3f}-{interval['end_seconds']:.3f} s"

    def check_uncut_source(self):
        uncut = self.record["uncut"]
        path = self.file(uncut["path"])
        require(path.is_file(), f"the uncut source is absent: {uncut['path']}")
        info = gif_info(path)
        require(info["frames"] > 0 and info["frames"] == uncut.get("frames"), "the uncut frame count does not match the record")
        require(abs(info["duration_seconds"] - uncut.get("duration_seconds", -1)) <= DURATION_TOLERANCE_SECONDS,
                "the uncut duration does not match the record")
        hold = self.render["final_hold_ms"][uncut["path"]] / 1000
        # The first frame shows the first event, so the GIF timeline starts there.
        events = self.events["events"]
        original = events[-1]["seconds"] - events[0]["seconds"] + hold
        require(abs(info["duration_seconds"] - original) <= DURATION_TOLERANCE_SECONDS,
                "the uncut GIF does not keep the original capture timing")
        return f"{uncut['path']} keeps {info['duration_seconds']:.2f} s of original timing"

    def check_no_fixture_token(self):
        release = self.record.get("kind") == "release"
        scanned = 0
        for name in TEXT_FILES:
            path = self.file(name)
            require(path.is_file(), f"{name} is absent")
            data = path.read_bytes()
            for label, pattern in SECRET_PATTERNS:
                require(not pattern.search(data), f"{name} contains a {label}")
            require(not release or not FIXTURE_MARKERS.search(data), f"the release record file {name} names a fixture")
            scanned += 1
        if release:
            return f"{scanned} text files hold no fixture marker, provider key, or gateway key line"
        return f"{scanned} text files hold no fixture token, provider key, or gateway key line"

    def check_poster_dimensions(self):
        poster = png_size(self.file("poster.png"))
        edited = gif_info(self.file("first-use.gif"))
        entry = self.output("poster.png")
        require((poster["width"], poster["height"]) == (edited["width"], edited["height"]),
                "the poster size differs from the GIF")
        require((poster["width"], poster["height"]) == (entry.get("width"), entry.get("height")),
                "the poster size differs from the record")
        return f"{poster['width']}x{poster['height']}"

    def check_transcript_fixtures(self):
        fixtures = self.record["fixtures"]
        if self.record.get("kind") == "release":
            require(fixtures == [] and self.record.get("real_provider") is True,
                    "a release record needs no fixtures and the real_provider marker")
            content = self.events["response"]["content"]
            require(content and content in self.file("TRANSCRIPT.md").read_text(encoding="utf-8"),
                    "the transcript does not quote the real provider answer")
            return "no fixtures; the transcript quotes the real provider answer"
        require(isinstance(fixtures, list) and fixtures, "the record names no fixtures")
        text = self.file("TRANSCRIPT.md").read_text(encoding="utf-8")
        for fixture in fixtures:
            for field in ("name", "model", "answer_text"):
                require(fixture.get(field) and fixture[field] in text,
                        f"the transcript does not name the {field} of fixture {fixture.get('name')}")
        return "the transcript names " + ", ".join(fixture["name"] for fixture in fixtures)

    def check_human_review(self):
        review = self.record["human_review"]
        reviewer = review.get("reviewer")
        require(isinstance(reviewer, str) and reviewer.strip(), "the human review names no reviewer")
        date = review.get("date")
        require(isinstance(date, str) and DATE.fullmatch(date), "the human review has no date")
        datetime.date.fromisoformat(date)
        verdict = review.get("pacing_verdict")
        require(verdict in ("PASS", "FAIL"), "the human review has no pacing verdict")
        require(verdict == "PASS", "the human review pacing verdict is FAIL")
        return f"{reviewer}, {date}, pacing {verdict}"

    def check_readme_not_linking_rehearsal(self):
        links = readme_links((self.root / "README.md").read_text(encoding="utf-8"))
        if self.record.get("kind") == "release":
            return "a release record; the README may link it"
        rehearsals = (self.root / "docs" / "proof" / "readme-demo").resolve()
        for link in links:
            target = (self.root / link).resolve()
            for forbidden in (self.directory, rehearsals):
                require(target != forbidden and forbidden not in target.parents,
                        f"the README links rehearsal media: {link}")
        return f"the README links no rehearsal; {len(self.readme_media(links))} media links checked"

    def readme_media(self, links=None):
        if links is None:
            links = readme_links((self.root / "README.md").read_text(encoding="utf-8"))
        media = []
        for link in links:
            if link.lower().endswith(MEDIA_SUFFIXES) and link not in media:
                media.append(link)
        return media

    def git(self, *arguments):
        return subprocess.run(["git", *arguments], cwd=self.root, capture_output=True, text=True, timeout=120)

    def check_invalidation(self):
        candidate = self.record["candidate"]
        kind = self.record.get("kind")
        if kind == "release":
            tag = candidate.get("release_tag")
            require(candidate.get("source") == "release" and isinstance(tag, str) and RELEASE_TAG.fullmatch(tag),
                    "a release record needs a release candidate and tag")
            require(self.record.get("qualifies_release_cases") is True, "a release record must qualify release cases")
            require(candidate.get("run_id") is None and candidate.get("pull_request") is None,
                    "a release candidate names no CI run or pull request")
            require(candidate.get("archive_name") == f"starport_{tag[1:]}_darwin_arm64.tar.gz",
                    f"the archive name is not the darwin arm64 asset of {tag}")
            require(isinstance(candidate.get("archive_sha256"), str) and HEX64.fullmatch(candidate["archive_sha256"]),
                    "the release candidate has no archive SHA-256")
            require(isinstance(candidate.get("head_commit"), str) and HEX40.fullmatch(candidate["head_commit"]),
                    "the release candidate has no 40-character tag commit")
            require(candidate.get("checksum_verified") is True, "the release archive has no verified checksum")
            require(candidate.get("attestation_verified") is True, "the release archive has no verified attestation")
            return f"a release record binds release tag {tag} and never invalidates by tree"
        require(kind == "rehearsal", f"unknown record kind {kind!r}")
        require(self.record.get("qualifies_release_cases") is False, "a rehearsal record cannot qualify release cases")
        source = candidate.get("source")
        require(source in ("ci-run", "local"), f"unknown candidate source {source!r}")
        head = candidate.get("head_commit")
        if source == "local" and head is None:
            return "not applicable: a local candidate without a head commit; Starmap R01 refuses a local source"
        require(isinstance(head, str) and HEX40.fullmatch(head), "the candidate has no 40-character head commit")
        if self.git("cat-file", "-e", head + "^{commit}").returncode and candidate.get("pull_request"):
            self.git("fetch", "--quiet", "--no-tags", "origin", f"refs/pull/{candidate['pull_request']}/head")
        require(not self.git("cat-file", "-e", head + "^{commit}").returncode,
                f"the candidate head {head} is not in this repository")
        rule = self.manifest["invalidation"]
        paths = list(rule["product_paths"]) + [":(exclude)" + path for path in rule.get("product_path_exclusions", [])]
        result = self.git("diff", "--name-only", head, "HEAD", "--", *paths)
        require(result.returncode == 0, "git diff could not compare the candidate head with HEAD")
        changed = result.stdout.split()
        if not changed:
            return f"no product path changed since {head[:12]}"
        summary = f"{len(changed)} product file(s) changed since {head[:12]}, first {changed[0]}"
        if source == "local":
            return "not applicable: a local candidate; " + summary + "; Starmap R01 refuses a local source"
        raise Invalid(summary)

    def run(self):
        checks, invalid = [], False
        for check in CHECK_IDS:
            try:
                checks.append({"id": check, "status": "PASS", "detail": getattr(self, "check_" + check)()})
            except Invalid as error:
                invalid = True
                checks.append({"id": check, "status": "FAIL", "detail": "INVALID: " + str(error)})
            except (OSError, ValueError, KeyError, TypeError, IndexError, AttributeError, subprocess.SubprocessError) as error:
                checks.append({"id": check, "status": "FAIL", "detail": describe(error)})
        status = "INVALID" if invalid else "PASS" if all(check["status"] == "PASS" for check in checks) else "FAIL"
        try:
            head = self.record["candidate"].get("head_commit")
            kind = self.record.get("kind")
        except (OSError, ValueError, KeyError, TypeError, AttributeError):
            head = kind = None
        try:
            media = self.readme_media()
        except OSError:
            media = []
        return {"status": status, "manifest": self.manifest_argument, "record": self.record_argument, "kind": kind,
                "candidate_head": head, "checks": checks, "readme_media": media}


def describe(error):
    if isinstance(error, KeyError):
        return f"a required field is missing: {error.args[0]}"
    if isinstance(error, json.JSONDecodeError):
        return f"invalid JSON: {error.msg}"
    return str(error) or type(error).__name__


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--manifest", required=True, help="manifest path relative to the repository root")
    parser.add_argument("--record", help="record directory; the default is the manifest current_rehearsal")
    parser.add_argument("--json", action="store_true", help="print only the JSON report")
    parser.add_argument("--root", default=".", help="Starport repository root")
    args = parser.parse_args(argv)
    try:
        report = Verification(args.root, args.manifest, args.record).run()
    except (OSError, ValueError, TypeError) as error:
        report = {"status": "FAIL", "manifest": args.manifest, "record": args.record, "kind": None, "candidate_head": None,
                  "checks": [{"id": check, "status": "FAIL", "detail": "the manifest cannot be read: " + describe(error)}
                             for check in CHECK_IDS], "readme_media": []}
    if not args.json:
        for check in report["checks"]:
            print(f"{check['status']} {check['id']}: {check['detail']}")
        print(f"{report['status']} README demonstration record {report['record']}")
    print(json.dumps(report, indent=2))
    return {"PASS": 0, "FAIL": 1, "INVALID": 2}[report["status"]]


if __name__ == "__main__":
    sys.exit(main())
