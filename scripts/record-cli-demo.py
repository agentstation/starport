#!/usr/bin/env python3
"""Record real CLI commands in an isolated VHS shell."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import socket
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parent.parent
FONT = ROOT / "scripts/demo-font/GeistMono-Regular.woff2"
TAPE = ROOT / "scripts/cli-demo.tape"
CONTROL = ROOT / "scripts/readme-demo/capture_cli.py"
SANDBOX = '(version 1)(allow default)(deny network-outbound (remote ip "*:*"))(allow network-outbound (remote ip "localhost:*"))'
OPENING = "Goal / Choose a chat model and check a local gateway"
CLOSING = "Result / Model chosen and gateway checked"
CHAPTERS = (
    ("1 / Select a chat model", "starport models search gpt-6.1-sol"),
    ("2 / Start an authenticated gateway", "starport dev --no-open >gateway.log 2>&1 &"),
    ("3 / Check the selected model", 'curl -fsS --max-time 5 -H "Authorization: Bearer $STARPORT_API_KEY"'),
)
PAUSES = (
    ("goal and first chapter", 3, "Read the goal and model selection step together."),
    ("later chapter headings", 2, "Read the next action before command entry."),
    ("model search", 2, "Compare the model matches."),
    ("model detail", 3, "Read context, price, and provider operation."),
    ("gateway launch", 1, "Read the actual development command before its isolated startup checks."),
    ("gateway banner", 2, "Read the URL and authentication mode."),
    ("readiness", 2, "Confirm the actual gateway status."),
    ("catalog offerings", 4, "Read the canonical model, exact provider ID, operations, and unknown readiness."),
    ("result and next action", 8, "Read the verified result, restart commands, and credential roles."),
)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def catalog_inputs(source, binary):
    """Bind the reviewed embedded catalog to the recorded binary."""
    embedded = source / "internal/embedded"
    catalog = embedded / "catalog"
    manifest_path = catalog / "generation.json"
    manifest = json.loads(manifest_path.read_text())
    required = (manifest_path, catalog / "authors/openai/models/gpt-6.1-sol.yaml",
                catalog / "providers/openai/models/gpt-6.1-sol.yaml")
    executable = binary.read_bytes()
    for path in required:
        if path.read_bytes() not in executable:
            raise RuntimeError("the recorded binary does not embed this catalog input: " + str(path.relative_to(source)))
    inputs = {}
    for subtree in (catalog, embedded / "sources"):
        for path in sorted(subtree.rglob("*")):
            if path.is_file() and not any(part.startswith((".", "_")) for part in path.relative_to(embedded).parts):
                inputs[str(path.relative_to(source))] = digest(path)
    inventory = json.dumps(inputs, sort_keys=True, separators=(",", ":")).encode()
    return {"manifest": manifest, "manifest_sha256": digest(manifest_path),
            "input_count": len(inputs), "input_inventory_sha256": hashlib.sha256(inventory).hexdigest(),
            "inputs_sha256": inputs, "manifest_and_selected_model_inputs_embedded": True}


def catalog_provenance(source, binary, go):
    evidence = catalog_inputs(source, binary)
    build = subprocess.check_output([str(go), "version", "-m", str(binary)], text=True)
    dependency = next((line.strip() for line in build.splitlines()
                       if line.startswith("\tdep\tgithub.com/agentstation/starmap\t")), None)
    if dependency is None or "\t(devel)" not in dependency:
        raise RuntimeError("the recorded binary must use the reviewed local Starmap workspace module")
    evidence.update({"kind": "reviewed-local-embedded-catalog", "public_release": False,
                     "starmap_source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=source, text=True).strip(),
                     "catalog_inputs_modified": bool(subprocess.check_output(
                         ["git", "status", "--porcelain", "--", "internal/embedded/catalog", "internal/embedded/sources"],
                         cwd=source, text=True).strip()), "starmap_build_dependency": dependency})
    return evidence


def tape_commands(tape):
    commands = []
    for line in tape.splitlines():
        if line.startswith("Type "):
            if not line.startswith("Type `") or not line.endswith("`"):
                raise ValueError("the runner requires backtick strings for Type commands")
            commands.append(line[6:-1])
    return commands


def verify_typing(svg):
    """Require intermediate shell command text in the exported SVG."""
    tree = ET.fromstring(svg)
    lines = {"".join(element.itertext()).replace("█", "").rstrip()
             for element in tree.iter("{http://www.w3.org/2000/svg}text")}
    counts = {}
    for command in ("starport models search gpt-6.1-sol", "starport models show openai/gpt-6.1-sol",
                    "starport dev --no-open >gateway.log 2>&1 &"):
        line = "$ " + command
        stem = "$ " + " ".join(command.split()[:3 if " models " in command else 2])
        partial = {value for value in lines if value.startswith(stem) and len(value) > len(stem)
                   and len(value) < len(line) and line.startswith(value)}
        if len(partial) < 6:
            raise RuntimeError("the SVG has too few typing frames for " + command)
        counts[command] = len(partial)
    return {"milliseconds_per_character": 35, "svg_prefix_counts": counts}


def verify_titles(svg):
    """Require each editorial title before its shell action."""
    tree = ET.fromstring(svg)
    frames = ["\n".join("".join(line.itertext()) for line in node.iter("{http://www.w3.org/2000/svg}text"))
              for node in tree.iter("{http://www.w3.org/2000/svg}g")
              if node.attrib.get("transform", "").startswith("translate(")]
    for frame in frames:
        if any(command in frame for command in ("$ demo-control", "$ gateway_pid=", "$ export STARPORT_API_KEY=")):
            raise RuntimeError("the visible SVG contains hidden gateway setup commands")
    opening = next((index for index, frame in enumerate(frames) if OPENING in frame), None)
    chapters = []
    for title, command in CHAPTERS:
        title_frame = next((index for index, frame in enumerate(frames) if title in frame), None)
        action_frame = next((index for index, frame in enumerate(frames) if "$ " + command in frame), None)
        if title_frame is None or action_frame is None or title_frame >= action_frame:
            raise RuntimeError("the editorial title must precede its action: " + title)
        chapters.append({"title": title, "title_frame": title_frame, "action_frame": action_frame,
                         "hold_seconds": 3 if not chapters else 2})
    if opening is None or opening > chapters[0]["title_frame"]:
        raise RuntimeError("the goal must precede the first chapter action")
    closing = next((index for index, frame in enumerate(frames) if CLOSING in frame), None)
    if closing is None or closing <= chapters[-1]["action_frame"]:
        raise RuntimeError("the verified result must follow catalog inspection")
    return {"goal": OPENING, "opening_frame": opening, "chapters": chapters,
            "result": CLOSING, "closing_frame": closing, "ending_hold_seconds": 8,
            "next_action": 'stop this development session; export OPENAI_API_KEY="your-openai-provider-credential"; starport dev --no-open',
            "pauses": [{"segment": segment, "seconds": seconds, "reason": reason}
                       for segment, seconds, reason in PAUSES]}


def verify_story_tape(tape):
    """Require a live ending before cleanup in the executable tape."""
    commands = tape_commands(tape)
    live_check = commands.index("demo-control closing")
    result = next(index for index, command in enumerate(commands) if CLOSING in command)
    cleanup = next(index for index, command in enumerate(commands) if command.startswith('kill -INT "$gateway_pid"'))
    if not live_check < result < cleanup:
        raise RuntimeError("the live gateway check and visible result must precede cleanup")
    visible_ending = tape[tape.index("Type `" + commands[result]):tape.index("Type `" + commands[cleanup])]
    if "Show\nSleep 8s\n\nHide" not in visible_ending:
        raise RuntimeError("the result must remain visible before hidden cleanup")
    opening = next(command for command in commands if OPENING in command)
    if CHAPTERS[0][0] not in opening:
        raise RuntimeError("the goal and first chapter must share the opening screen")
    return {"live_gateway_checked_before_result": True, "cleanup_after_visible_result": True}


def prepare(directory, binary, path):
    home = directory / "home"
    home.mkdir()
    wrapper = directory / "bin"
    wrapper.mkdir()
    environment = {"PATH": path, "HOME": str(home), "LANG": "en_US.UTF-8", "TERM": "xterm-256color",
                   "HISTFILE": "/dev/null", "STARPORT_SERVER_PORT": "19335"}
    values = " ".join(shlex.quote(f"{key}={value}") for key, value in environment.items())
    for name, command in (("starport", [str(binary)]), ("demo-control", [sys.executable, str(CONTROL)])):
        arguments = " ".join(shlex.quote(value) for value in command)
        launcher = wrapper / name
        launcher.write_text("#!/bin/sh\nexec /usr/bin/sandbox-exec -p " + shlex.quote(SANDBOX) +
                            " /usr/bin/env -i " + values + " " + arguments + ' "$@"\n')
        launcher.chmod(0o755)
    (directory / "setup.sh").write_text(
        "export PATH=" + shlex.quote(str(wrapper)) + ':"$PATH"\n'
        "export PS1='$ '\nunset PROMPT_COMMAND\nset +m\nset -euo pipefail\n"
        "unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY\n"
        "export NO_PROXY=127.0.0.1\nexport HISTFILE=/dev/null\n"
        "trap 'for demo_pid in $(jobs -pr); do kill -INT \"$demo_pid\" 2>/dev/null || true; done; wait || true' EXIT\n"
        "demo-control isolation\n")
    return home


def checked_capture(directory):
    capture = json.loads((directory / "capture.json").read_text())
    if capture.get("verdict") != "PASS" or not (directory / "complete").is_file():
        raise RuntimeError("the shell tape did not finish")
    return capture


def preflight(directory, tape, environment):
    transcript = directory / "transcript.txt"
    script = []
    for command in tape_commands(tape):
        script.extend(["printf '%s\\n' " + shlex.quote("$ " + command), command])
    with transcript.open("w") as output:
        subprocess.run(["/bin/bash", "--noprofile", "--norc", "-c", "\n".join(script)], cwd=directory,
                       env=environment, stdout=output, stderr=subprocess.STDOUT, check=True, timeout=120)
    return checked_capture(directory), transcript.read_text()


def transcript_text(transcript, catalog):
    """Explain the actual capture, catalog inputs, and local build."""
    description = "# Starport CLI capture\n\n"
    description += "The VHS tape types real shell commands at 35 milliseconds per character.\n"
    description += "The commands inspect the embedded catalog and a temporary authenticated gateway.\n"
    description += "The capture uses an isolated home and permits only loopback network access.\n"
    description += "The capture redacts the gateway API key and console launch link. It makes no provider inference request.\n\n"
    description += "## Story and pauses\n\n"
    description += "Cyan editorial headings state the next action and remain above the actual shell commands.\n"
    description += "The opening pairs the local app goal with model selection. The result screen follows verified readiness and authenticated catalog access.\n\n"
    description += "The gateway remains live through the result screen. Hidden cleanup follows it.\n\n"
    description += "The next action stops the development session, sets an OpenAI provider credential, and starts a new session. Use that session’s gateway key with its printed URL plus `/v1`.\n"
    description += "The provider credential authorizes inference. The gateway key authenticates the client. This capture does not verify inference.\n\n"
    description += "| Segment | Hold | Reason |\n| --- | --- | --- |\n"
    for segment, seconds, reason in PAUSES:
        description += f"| {segment} | {seconds} seconds | {reason} |\n"
    description += "\nPrompt and readiness checks wait for actual completion. Credential capture and shutdown remain hidden.\n\n"
    description += "## Catalog and build evidence\n\n"
    description += "This source build uses a reviewed local Starmap catalog. It is not a new public release.\n"
    if catalog.get("catalog_inputs_modified"):
        description += "The reviewed catalog includes uncommitted local changes.\n"
    description += "The build joins the Starport and Starmap source trees with a temporary Go workspace. It leaves their module files unchanged.\n\n"
    description += f"The Starmap source commit is `{catalog['starmap_source_commit']}`.\n"
    description += f"The embedded generation is `{catalog['manifest']['generation_id']}`.\n"
    description += f"Its catalog payload checksum is `{catalog['manifest']['payload']['checksum']}`.\n\n"
    description += f"The manifest SHA-256 is `{catalog['manifest_sha256']}`.\n"
    description += f"The input inventory SHA-256 is `{catalog['input_inventory_sha256']}`.\n"
    description += "The record lists each catalog and source input hash. The recorder verifies the manifest and both selected model files inside the binary.\n"
    description += "It also rejects catalog input changes during capture.\n\n"
    description += "The selected canonical ID is `openai/gpt-6.1-sol`. Its OpenAI provider ID is `gpt-6.1-sol`.\n"
    description += "The demo checks the chat offering. It makes no inference request or tool call.\n\n"
    description += "## Reproduce\n\nRun the commands from the repository root.\n"
    description += "The recorder needs macOS `sandbox-exec`, Go, Python 3, `ttyd`, `ffmpeg`, `ffprobe`, `jq`, and Google Chrome.\n"
    description += "The capture font is `scripts/demo-font/GeistMono-Regular.woff2` under the SIL Open Font License.\n"
    description += "Use the `agentstation/vhs` fork with elapsed timing and `--svg-font-file` support.\n"
    description += "This capture uses local VHS fixes that no published upstream release contains.\n"
    description += "Build that VHS source into `/tmp/agentstation-demo-vhs` before the recording command.\n\n"
    description += "Use the reviewed Starmap source and catalog identified above. The published module does not supply this local catalog update.\n\n"
    description += "```bash\nstarport_source=$PWD\nstarmap_source=/path/to/reviewed/starmap\n"
    description += 'demo_workspace=$(mktemp -d /tmp/starport-demo-workspace.XXXXXX)\n'
    description += 'GOWORK=off go -C "$demo_workspace" work init "$starport_source" "$starmap_source"\n'
    description += 'GOWORK="$demo_workspace/go.work" go build -o /tmp/starport-cli-demo ./cmd/starport\n'
    description += "python3 scripts/record-cli-demo.py \\\n  --binary /tmp/starport-cli-demo \\\n"
    description += '  --starmap-source "$starmap_source" \\\n'
    description += "  --vhs /tmp/agentstation-demo-vhs \\\n"
    description += "  --browser-path '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' \\\n"
    description += '  --output "$(mktemp -d /tmp/starport-cli-demo.XXXXXX)"\n```\n\n'
    description += "## Command preflight\n\n"
    description += "This transcript runs the exact tape commands before recording. The media repeats them with fresh state.\n\n"
    description += "The transcript removes ANSI display controls. It preserves command text and product output.\n\n"
    description += "```text\n" + re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", transcript) + "```\n"
    return description


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True, help="current source-built Starport binary")
    parser.add_argument("--starmap-source", type=Path, required=True, help="reviewed local Starmap source used in the binary build")
    parser.add_argument("--vhs", type=Path, required=True, help="agentstation/vhs binary with elapsed timing")
    parser.add_argument("--browser-path", type=Path, required=True, help="capture browser executable")
    parser.add_argument("--output", type=Path, default=ROOT / "docs/assets/cli-current")
    args = parser.parse_args()
    if not Path("/usr/bin/sandbox-exec").is_file():
        parser.error("the recording needs macOS sandbox-exec")
    for path in (args.binary, args.vhs, args.browser_path):
        if not path.is_file() or not os.access(path, os.X_OK):
            parser.error("the executable is absent: " + str(path))
    if args.output.exists() and any(args.output.iterdir()):
        parser.error("the output directory must be empty")
    tools = {}
    for name in ("ttyd", "ffmpeg", "ffprobe", "jq", "go"):
        path = shutil.which(name)
        if not path:
            parser.error("the recording needs " + name)
        tools[name] = Path(path).resolve()
    with socket.socket() as listener:
        listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        listener.bind(("127.0.0.1", 19335))
    with tempfile.TemporaryDirectory(prefix="starport-cli-vhs-") as directory:
        work = Path(directory)
        capture_tool = work / "vhs-capture"
        shutil.copy2(args.vhs.resolve(), capture_tool)
        binary = work / "starport-source"
        shutil.copy2(args.binary.resolve(), binary)
        catalog = catalog_provenance(args.starmap_source.resolve(), binary, tools["go"])
        path_dirs = [str(path.parent) for path in tools.values()] + [str(Path(sys.executable).parent),
                                                                  "/usr/bin", "/bin", "/usr/sbin", "/sbin"]
        path = ":".join(dict.fromkeys(path_dirs))
        environment = {"PATH": path, "HOME": str(work / "home"), "TMPDIR": str(work),
                       "TERM": "xterm-256color", "LANG": "en_US.UTF-8", "HISTFILE": "/dev/null"}
        tape = TAPE.read_text()
        story_contract = verify_story_tape(tape)
        rehearsal = work / "preflight"
        rehearsal.mkdir()
        preflight_home = prepare(rehearsal, binary, path)
        preflight_environment = dict(environment, HOME=str(preflight_home))
        preflight_report, transcript = preflight(rehearsal, tape, preflight_environment)
        capture_directory = work / "recording"
        capture_directory.mkdir()
        prepare(capture_directory, binary, path)
        (capture_directory / "cli.tape").write_text(tape)
        environment["HOME"] = str(capture_directory / "home")
        subprocess.run([str(capture_tool), "cli.tape", "--svg-font-file", str(FONT),
                        "--browser-path", str(args.browser_path.resolve())],
                       cwd=capture_directory, env=environment, check=True, timeout=240)
        capture = checked_capture(capture_directory)
        svg = (capture_directory / "cli.svg").read_text()
        if "@font-face" not in svg or "data:font/woff2;base64," not in svg:
            raise RuntimeError("the SVG does not contain its capture font")
        typing = verify_typing(svg)
        story = dict(verify_titles(svg), **story_contract)
        for run_directory in (rehearsal, capture_directory):
            key = (run_directory / "gateway.key").read_text()
            if key in svg or key in transcript:
                raise RuntimeError("the media or transcript contains a session key")
            for line in (run_directory / "gateway.log").read_text().splitlines():
                if line.startswith("Console (one-time launch link): "):
                    if line.split(": ", 1)[1] in svg or line.split(": ", 1)[1] in transcript:
                        raise RuntimeError("the media or transcript contains a launch link")
        svg_duration = float(re.search(r"animation: slide ([\d.]+)s", svg)[1])
        metadata = json.loads(subprocess.check_output([str(tools["ffprobe"]), "-v", "error", "-show_entries",
                                                      "format=duration:stream=width,height", "-of", "json",
                                                      str(capture_directory / "cli.mp4")], text=True))
        video_duration = float(metadata["format"]["duration"])
        if max(svg_duration, video_duration) > 60:
            raise RuntimeError(f"the CLI story exceeds 60 seconds: SVG {svg_duration:.2f}, MP4 {video_duration:.2f}")
        if abs(svg_duration - video_duration) > 1:
            raise RuntimeError("SVG and MP4 durations differ by more than one second")
        subprocess.run([str(tools["ffmpeg"]), "-v", "error", "-ss", str(video_duration - 1),
                        "-i", str(capture_directory / "cli.mp4"), "-frames:v", "1",
                        str(capture_directory / "poster.png")], check=True, timeout=30)
        if catalog_inputs(args.starmap_source.resolve(), binary)["input_inventory_sha256"] != catalog["input_inventory_sha256"]:
            raise RuntimeError("the reviewed catalog inputs changed during capture")
        args.output.mkdir(parents=True, exist_ok=True)
        outputs = {}
        for name in ("cli.svg", "cli.gif", "cli.mp4", "poster.png", "capture.json"):
            source = capture_directory / name
            shutil.copyfile(source, args.output / name)
            outputs[name] = {"sha256": digest(source), "bytes": source.stat().st_size}
        (args.output / "TRANSCRIPT.md").write_text(transcript_text(transcript, catalog))
        record = {"kind": "current-source-cli", "real_provider": False,
                  "catalog": catalog,
                  "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
                  "binary_sha256": digest(binary), "tape_sha256": digest(TAPE),
                  "capture_script_sha256": digest(CONTROL), "browser_path": str(args.browser_path.resolve()),
                  "vhs_sha256": digest(capture_tool), "font_sha256": digest(FONT),
                  "recorder_sha256": digest(Path(__file__)), "command_animation": typing,
                  "story": story,
                  "transcript_sha256": digest(args.output / "TRANSCRIPT.md"),
                  "preflight": preflight_report, "svg_duration_seconds": svg_duration,
                  "mp4_duration_seconds": video_duration, "width": metadata["streams"][0]["width"],
                  "height": metadata["streams"][0]["height"], "outputs": outputs}
        (args.output / "record.json").write_text(json.dumps(record, indent=2) + "\n")
        summary = dict(record, catalog={key: value for key, value in catalog.items() if key != "inputs_sha256"})
        print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
