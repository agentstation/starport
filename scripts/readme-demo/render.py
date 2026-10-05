#!/usr/bin/env python3
"""Render a captured rehearsal and write its record directory.

Cuts name a scene boundary such as `install/catalog`. A cut sets the pause
before the first event of the later scene. The renderer refuses a cut that
intersects the inference interval. Rendering needs Pillow and a monospaced
font. The default font is the macOS Menlo font.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import sys

from PIL import Image, ImageDraw, ImageFont

sys.path.insert(0, str(Path(__file__).resolve().parent))
import fixture_upstream  # noqa: E402
import verify  # noqa: E402

WIDTH, HEIGHT = 1280, 800
FONT_SIZE = 26
FONT = "/System/Library/Fonts/Menlo.ttc"
ANSI = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")
# Reading pauses at each scene boundary, in seconds.
DEFAULT_CUTS = {"install/catalog": 6.0, "catalog/setup": 8.0, "setup/answer": 6.0, "answer/next": 7.0}
FINAL_HOLD_MS = {"first-use.gif": 8000, "first-use-uncut.gif": 5000}
EDITED_LABEL = "Rehearsal. Fixture answer. Scene pauses set. Inference timing unchanged."
UNCUT_LABEL = "Rehearsal. Fixture answer. Original capture timing. Credentials hidden."
FIXTURE_CREDENTIAL = "generated throwaway token, disclosed as a fixture, value not recorded"


def sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


class Screen:
    """Draw terminal text on one frame with the selected font."""

    def __init__(self, font_path):
        self.font = ImageFont.truetype(font_path, FONT_SIZE)
        try:
            # Menlo.ttc holds the bold face at index 1.
            self.title = ImageFont.truetype(font_path, 30, index=1)
        except OSError:
            self.title = ImageFont.truetype(font_path, 30)
        self.footer = ImageFont.truetype(font_path, 20)

    def render(self, text, label):
        image = Image.new("RGB", (WIDTH, HEIGHT), "#0b1019")
        draw = ImageDraw.Draw(image)
        draw.rounded_rectangle((16, 16, WIDTH - 16, HEIGHT - 16), radius=18, outline="#283447", width=2)
        lines = ANSI.sub("", text).splitlines()
        for index, line in enumerate(lines):
            selected = self.title if index == 0 else self.font
            color = "#76dec3" if index == 0 else "#a5b8dc" if index == 2 else "#e6ecf5"
            if line.startswith("$") or line.startswith("FIXTURE"):
                color = "#a5b8dc" if line.startswith("$") else "#f2c66d"
            if draw.textlength(line, font=selected) > WIDTH - 112:
                raise ValueError("A captured line exceeds the readable frame width: " + line)
            y = 46 + index * 32
            if y + 32 > HEIGHT - 74:
                raise ValueError("The capture exceeds the readable frame height.")
            draw.text((56, y), line, font=selected, fill=color)
        draw.text((56, HEIGHT - 52), label, font=self.footer, fill="#a5b8dc")
        return image


def parse_cuts(values):
    if not values:
        return dict(DEFAULT_CUTS)
    cuts = {}
    for value in values:
        boundary, separator, seconds = value.partition("=")
        if not separator:
            raise SystemExit(f"A cut needs the form scene/scene=seconds: {value}")
        cuts[boundary] = float(seconds)
    return cuts


def plan_cuts(capture, requested):
    """Resolve named boundaries to event indexes and refuse a cut inside inference."""
    events = capture["events"]
    scenes = capture["scenes"]
    names = [scene["name"] for scene in scenes]
    interval = capture["inference_interval"]
    cuts = []
    for boundary, seconds in requested.items():
        parts = boundary.split("/")
        if len(parts) != 2 or parts[0] not in names or parts[1] not in names \
                or names.index(parts[1]) != names.index(parts[0]) + 1:
            raise ValueError(f"A cut must name adjacent scenes: {boundary}")
        if seconds <= 0:
            raise ValueError(f"A cut needs a positive pause: {boundary}")
        index = scenes[names.index(parts[1])]["start_event"]
        begin, end = events[index - 1]["seconds"], events[index]["seconds"]
        if not (end <= interval["start_seconds"] or begin >= interval["end_seconds"]):
            raise ValueError(f"The cut {boundary} would change the inference interval.")
        cuts.append({"boundary": boundary, "before_event": index, "original_seconds": round(end - begin, 6),
                     "rendered_seconds": seconds})
    return sorted(cuts, key=lambda cut: cut["before_event"])


def frames_for(capture, screen, cuts, label):
    """Return frames, cumulative centisecond times, and the state index of each event."""
    replacement = {cut["before_event"]: cut["rendered_seconds"] for cut in cuts}
    frames, times, event_frames = [], [], []
    state, elapsed, previous = "", 0.0, 0.0
    for index, event in enumerate(capture["events"]):
        gap = replacement.get(index, event["seconds"] - previous)
        elapsed += gap
        data = event["data"]
        state = data if data.startswith("\033[2J\033[H") else state + data
        image = screen.render(state, label)
        # GIF timestamps use centiseconds. Round cumulative time to prevent drift.
        timestamp = round(elapsed * 100) * 10
        if times and timestamp == times[-1]:
            frames[-1] = image
        else:
            frames.append(image)
            times.append(timestamp)
        event_frames.append(len(frames) - 1)
        previous = event["seconds"]
    return frames, times, event_frames


def save_gif(path, frames, times, hold_ms):
    durations = [b - a for a, b in zip(times, times[1:])] + [hold_ms]
    frames[0].save(path, save_all=True, append_images=frames[1:], duration=durations, loop=0, optimize=True, disposal=2)
    return sum(durations)


def gif_entry(path):
    info = verify.gif_info(path)
    return {"sha256": sha256(path), "bytes": path.stat().st_size, "width": info["width"], "height": info["height"],
            "duration_seconds": info["duration_seconds"], "frames": info["frames"]}


def transcript(record, capture, render_manifest):
    candidate = record["candidate"]
    fixture = record["fixtures"][0]
    edited = record["outputs"]["first-use.gif"]
    response = capture["response"]
    interval = capture["inference_interval"]
    if candidate["source"] == "ci-run":
        source = f"CI run {candidate['run_id']} of pull request {candidate['pull_request']}"
    else:
        source = "a local development archive. Starmap check R01 refuses this source"
    lines = [
        "# README demonstration rehearsal", "",
        "This record is a rehearsal. A local fixture upstream supplies the answer, so the record cannot qualify "
        "a release case. The README does not link this record.", "",
        f"The [edited animation](first-use.gif) runs {edited['duration_seconds']:.1f} seconds. "
        "The [static preview](poster.png) and this transcript provide alternatives to animation. "
        "The [uncut capture](first-use-uncut.gif) keeps the original output timing.", "",
        "## Candidate", "",
        f"- Source: {source}.",
        f"- Head commit: `{candidate['head_commit']}`.",
        f"- Version: `{candidate['snapshot_version']}`.",
        f"- Archive: `{candidate['archive_name']}`, SHA-256 `{candidate['archive_sha256']}`.",
        f"- Binary SHA-256: `{candidate['binary_sha256']}`.", "",
        "## Fixtures", "",
        f"- `{fixture['name']}` is a local OpenAI-compatible upstream. It serves the model `{fixture['model']}` "
        f"and streams the answer \"{fixture['answer_text']}\".",
        "- The provider credential is a throwaway token. The capture generates a new token for each run. "
        "No output keeps the token value.",
        f"- `{record['discovery']['base_url_mechanism']}` routes the `openai` provider to the fixture. "
        "No request goes to OpenAI.", "",
        "## What the recording shows", "",
        "1. `install`: Verify the archive digest, extract the archive, and run `starport --version`.",
        "2. `catalog`: Run `starport models show openai/gpt-4o-mini --json` with no provider key in the environment.",
        "3. `setup`: Start the fixture upstream and `starport dev --no-open`. The provider credential and the "
        "gateway key stay hidden. They serve separate roles.",
        "4. `answer`: Stream one request to `/api/v1/chat/completions`. The screen shows the fixture notice. "
        "The stream ends with `[DONE]`.",
        "5. `next`: Show the client base URLs and the persistent setup path.", "",
        f"Starport reported provider `{response.get('reported_provider', 'not reported')}` and model "
        f"`{response.get('reported_model', 'not reported')}`. The fixture stream took "
        f"{interval['end_seconds'] - interval['start_seconds']:.3f} seconds. This is not a latency benchmark.", "",
        "## Editing", "",
        "The edited animation sets the pause at each scene boundary:", "",
    ]
    for cut in record["cuts"]:
        lines.append(f"- `{cut['boundary']}`: {cut['original_seconds']:.3f} seconds to "
                     f"{cut['rendered_seconds']:g} seconds.")
    lines += [
        "", "No edit is inside the inference interval. GIF frame timing rounds cumulative timestamps to centiseconds. "
        f"The final frame holds for {FINAL_HOLD_MS['first-use.gif'] // 1000} seconds.",
        "The [render record](render.json) holds the cut points and the output hashes. "
        "The [events](events.json) hold the captured output.", "",
        "## Capture environment", "",
        "The capture used a temporary home without a persistent catalog-state or object-store selector. "
        "After shutdown, the temporary home held no files.",
    ]
    if capture.get("network_egress_denied"):
        lines.append("The sandbox refused network access except loopback during the capture.")
    lines += ["", f"The renderer used {render_manifest['font']} at {FONT_SIZE} pixels on a {WIDTH} by {HEIGHT} frame.", ""]
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--work", type=Path, required=True, help="work directory with events.json and candidate.json")
    parser.add_argument("--output", type=Path, required=True, help="new record directory")
    parser.add_argument("--font", default=FONT, help="TrueType font path; the default is the macOS Menlo font")
    parser.add_argument("--cut", action="append", help="scene/scene=seconds; repeat for each boundary")
    args = parser.parse_args()
    capture = json.loads((args.work / "events.json").read_text())
    candidate = json.loads((args.work / "candidate.json").read_text())
    if capture.get("verdict") != "PASS":
        raise SystemExit("A successful capture is required.")
    if capture["response"]["content"] != fixture_upstream.ANSWER:
        raise SystemExit("The streamed answer is not the fixture answer.")
    cuts = plan_cuts(capture, parse_cuts(args.cut))
    args.output.mkdir(parents=True, exist_ok=True)
    if any(args.output.iterdir()):
        raise SystemExit("Select an empty record directory.")
    screen = Screen(args.font)
    font_name = screen.font.getname()[0]
    render_manifest = {"width": WIDTH, "height": HEIGHT, "font_size": FONT_SIZE, "font": font_name, "font_path": args.font,
                       "effective_font_at_900px": FONT_SIZE * 900 / WIDTH, "cuts": cuts, "final_hold_ms": FINAL_HOLD_MS,
                       "outputs": {}}
    shutil.copyfile(args.work / "events.json", args.output / "events.json")
    poster_event = next(scene for scene in capture["scenes"] if scene["name"] == "answer")["end_event"]
    for name, selected, label in (("first-use-uncut.gif", [], UNCUT_LABEL), ("first-use.gif", cuts, EDITED_LABEL)):
        frames, times, event_frames = frames_for(capture, screen, selected, label)
        path = args.output / name
        duration = save_gif(path, frames, times, FINAL_HOLD_MS[name])
        render_manifest["outputs"][name] = {"sha256": sha256(path), "bytes": path.stat().st_size, "duration_ms": duration,
                                            "event_frames": len(frames)}
        if selected:
            render_manifest["edited_event_times_ms"] = times
            frames[event_frames[poster_event]].save(args.output / "poster.png", optimize=True)
    poster = args.output / "poster.png"
    poster_size = verify.png_size(poster)
    render_manifest["outputs"]["poster.png"] = {"sha256": sha256(poster), "bytes": poster.stat().st_size,
                                                "event": poster_event}
    (args.output / "render.json").write_text(json.dumps(render_manifest, indent=2) + "\n")
    response = capture["response"]
    record = {
        "schema_version": 1, "kind": "rehearsal", "qualifies_release_cases": False,
        "captured_at": capture["captured_at"],
        "candidate": {key: candidate.get(key) for key in ("source", "run_id", "pull_request", "head_commit",
                                                          "snapshot_version", "archive_name", "archive_sha256",
                                                          "binary_sha256", "release_tag")},
        "fixtures": [{"name": "fixture_upstream", "role": "openai-compatible upstream", "model": fixture_upstream.MODEL,
                      "answer_text": fixture_upstream.ANSWER, "credential": FIXTURE_CREDENTIAL}],
        "scenes": capture["scenes"],
        "inference_interval": capture["inference_interval"],
        "cuts": cuts,
        "outputs": {},
        "uncut": dict(path="first-use-uncut.gif", **{key: value for key, value in
                                                    gif_entry(args.output / "first-use-uncut.gif").items()
                                                    if key in ("sha256", "bytes", "duration_seconds", "frames")}),
        "render": {"font": font_name, "font_path": args.font, "font_size": FONT_SIZE,
                   "effective_font_at_900px": render_manifest["effective_font_at_900px"]},
        "discovery": {
            "starport_version": capture["starport_version"],
            "catalog_generation": capture.get("catalog_generation"),
            "provider_path": "openai",
            "base_url_mechanism": capture["base_url_mechanism"],
            "catalog_environment_has_provider_key": capture["catalog_environment_has_provider_key"],
            "persistent_selectors_present": capture["persistent_selectors_present"],
            "leftover_home_files": capture["leftover_home_files"],
            "clean_shutdown": capture["shutdown_exit_code"] == 0,
            "answer_stream": {"status": response["status"], "done_event": response["final_event"] == "[DONE]",
                              "content_length": response["content_length"]},
        },
        "human_review": {"reviewer": None, "date": None, "pacing_verdict": "PENDING",
                         "notes": "Run scripts/record-readme-demo.sh --review after a person watches first-use.gif."},
    }
    record["outputs"]["first-use.gif"] = gif_entry(args.output / "first-use.gif")
    record["outputs"]["poster.png"] = {"sha256": sha256(poster), "bytes": poster.stat().st_size, **poster_size}
    (args.output / "TRANSCRIPT.md").write_text(transcript(record, capture, render_manifest))
    for name in ("events.json", "render.json", "TRANSCRIPT.md"):
        path = args.output / name
        record["outputs"][name] = {"sha256": sha256(path), "bytes": path.stat().st_size}
    (args.output / "record.json").write_text(json.dumps(record, indent=2) + "\n")
    print(json.dumps({"record": str(args.output), "outputs": record["outputs"], "cuts": cuts}, indent=2))


if __name__ == "__main__":
    main()
