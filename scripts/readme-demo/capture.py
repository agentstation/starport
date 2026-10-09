#!/usr/bin/env python3
"""Identify a candidate archive and capture the README demonstration.

The `candidate` command resolves the candidate identity. It needs network
access for a CI run or a release tag. The `capture` command installs the
archive in a temporary home, runs the five scenes, and writes events.json.

A rehearsal candidate (a CI run or a local archive) answers from a local
fixture upstream. The record script runs that capture with loopback-only
network access. The capture generates a throwaway fixture token for each run,
and no output holds the token value.

A release candidate (`--release-tag`) binds the attested darwin arm64 archive
of a published release and answers from the real provider. The capture reads
the provider credential from `OPENAI_API_KEY` in its own environment. It never
prints, stores, or hashes the value. No output holds the value.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import signal
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.request

REPOSITORY = "agentstation/starport"
ARTIFACT = "starport-release-snapshot"
ARCHIVE_PATTERN = re.compile(r"starport_[^/]+_darwin_arm64\.tar\.gz")
MODEL = "openai/gpt-4o-mini"
PORT = 19325
BASE_URL_MECHANISM = "STARPORT_OPENAI_INFERENCE_BASE_URL"
SCENES = ("install", "catalog", "setup", "answer", "next")
TOKEN_PREFIX = "starport-demo-fixture-"
PERSISTENT_SELECTORS = ("STARPORT_CATALOG_STATE_DIR", "STARPORT_FILES_BACKEND")
HEADER = "\033[2J\033[H\033[1;36mSTARPORT\033[0m  /  first request  ·  "
HEADER_KIND = {"rehearsal": "rehearsal", "release": "verified release"}
FIXTURE = Path(__file__).resolve().with_name("fixture_upstream.py")
PROVIDER_CREDENTIAL = "OPENAI_API_KEY"
ATTESTATION_PREDICATE = "https://slsa.dev/provenance/v1"


def sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def command(arguments, cwd=None):
    return subprocess.run(arguments, cwd=cwd, check=True, capture_output=True, text=True, timeout=600).stdout


def checksum_entry(checksums, name):
    for line in Path(checksums).read_text().splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[1].lstrip("*") == name:
            return fields[0]
    raise RuntimeError(f"the checksum file has no entry for {name}")


def resolve_run(run_id, work):
    """Download the release snapshot of one pull request run and bind its identity."""
    run = json.loads(command(["gh", "run", "view", str(run_id), "--repo", REPOSITORY, "--json",
                              "databaseId,url,headSha,status,conclusion,event,jobs"]))
    if run.get("event") != "pull_request":
        raise RuntimeError("the candidate must come from a pull request run")
    snapshot = [job for job in run.get("jobs", []) if job.get("name") == "Release Snapshot"]
    if len(snapshot) != 1 or snapshot[0].get("conclusion") != "success":
        raise RuntimeError("the run has no successful Release Snapshot job")
    head = run["headSha"]
    # The run record loses its pull requests after the merge. The commit
    # record keeps them.
    pulls = json.loads(command(["gh", "api", f"repos/{REPOSITORY}/commits/{head}/pulls"]))
    numbers = [pull.get("number") for pull in pulls
               if isinstance(pull, dict) and isinstance(pull.get("head"), dict) and pull["head"].get("sha") == head]
    if len(numbers) != 1:
        raise RuntimeError("exactly one pull request must have the run head commit as its head")
    artifact = work / "artifact"
    command(["gh", "run", "download", str(run_id), "--repo", REPOSITORY, "--name", ARTIFACT, "--dir", str(artifact)])
    archives = sorted(path for path in artifact.rglob("*.tar.gz") if ARCHIVE_PATTERN.fullmatch(path.name))
    if len(archives) != 1:
        raise RuntimeError("the release snapshot must hold exactly one darwin arm64 archive")
    archive = archives[0]
    digest = sha256(archive)
    if digest != checksum_entry(next(artifact.rglob("checksums.txt")), archive.name):
        raise RuntimeError("the archive does not match the release snapshot checksum file")
    return {"source": "ci-run", "run_id": str(run["databaseId"]), "run_url": run["url"], "pull_request": numbers[0],
            "head_commit": head, "archive_name": archive.name, "archive_sha256": digest, "release_tag": None,
            "archive_path": str(archive), "checksum_verified": True}


def resolve_local(archive, checksums, head):
    """Bind a local development archive. Starmap R01 refuses this source."""
    archive = Path(archive).resolve()
    digest = sha256(archive)
    verified = False
    if checksums:
        if digest != checksum_entry(checksums, archive.name):
            raise RuntimeError("the archive does not match the checksum file")
        verified = True
    return {"source": "local", "run_id": None, "run_url": None, "pull_request": None, "head_commit": head,
            "archive_name": archive.name, "archive_sha256": digest, "release_tag": None,
            "archive_path": str(archive), "checksum_verified": verified}


def release_archive_name(tag):
    return f"starport_{tag[1:]}_darwin_arm64.tar.gz"


def resolve_release(tag, work):
    """Download the darwin arm64 asset of a published release and verify its checksum and attestation."""
    if not re.fullmatch(r"v\d+\.\d+\.\d+(?:-rc\.\d+)?", tag):
        raise RuntimeError("the release tag must have the form vX.Y.Z")
    name = release_archive_name(tag)
    commit = json.loads(command(["gh", "api", f"repos/{REPOSITORY}/commits/{tag}"])).get("sha")
    if not isinstance(commit, str) or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise RuntimeError("the release tag resolves to no 40-character commit")
    assets = work / "release"
    assets.mkdir(parents=True, exist_ok=True)
    command(["gh", "release", "download", tag, "--repo", REPOSITORY, "--dir", str(assets),
             "--pattern", name, "--pattern", "checksums.txt"])
    archive = assets / name
    checksums = assets / "checksums.txt"
    if not archive.is_file() or not checksums.is_file():
        raise RuntimeError("the release download did not supply the archive and checksums.txt")
    digest = sha256(archive)
    if digest != checksum_entry(checksums, name):
        raise RuntimeError("the archive does not match the release checksum file")
    try:
        attested = command(["gh", "attestation", "verify", str(archive), "--repo", REPOSITORY,
                            "--predicate-type", ATTESTATION_PREDICATE, "--format", "json"])
    except subprocess.CalledProcessError as error:
        raise RuntimeError("the release archive has no verified attestation") from error
    statements = json.loads(attested)
    types = sorted({entry["verificationResult"]["statement"]["predicateType"] for entry in statements
                    if isinstance(entry, dict)} if isinstance(statements, list) else set())
    if ATTESTATION_PREDICATE not in types:
        raise RuntimeError("the attestation output names no provenance statement")
    return {"source": "release", "run_id": None, "run_url": None, "pull_request": None, "head_commit": commit,
            "archive_name": name, "archive_sha256": digest, "release_tag": tag, "archive_path": str(archive),
            "checksum_verified": True, "attestation_verified": True,
            "attestation": {"predicate_types": types, "statements": len(statements)}}


def candidate_main(args):
    work = Path(args.work)
    work.mkdir(parents=True, exist_ok=True)
    if args.run:
        candidate = resolve_run(args.run, work)
    elif args.release_tag:
        candidate = resolve_release(args.release_tag, work)
    else:
        candidate = resolve_local(args.archive, args.checksums, args.head)
    (work / "candidate.json").write_text(json.dumps(candidate, indent=2) + "\n")
    print(json.dumps({key: candidate[key] for key in ("source", "run_id", "pull_request", "release_tag", "head_commit",
                                                      "archive_name")}))


class Capture:
    """Run the five scenes and keep timestamped output without credential values."""

    def __init__(self, candidate):
        self.candidate = candidate
        self.kind = "release" if candidate.get("source") == "release" else "rehearsal"
        self.started = time.monotonic()
        self.events = []
        self.commands = []
        self.current_scene = None
        captured = datetime.datetime.now(datetime.timezone.utc).replace(microsecond=0)
        self.report = {"schema_version": 1, "kind": self.kind, "release_tag": candidate.get("release_tag"),
                       "real_provider": self.kind == "release",
                       "captured_at": captured.isoformat().replace("+00:00", "Z"),
                       "events": self.events, "commands": self.commands}

    def now(self):
        return round(time.monotonic() - self.started, 6)

    def emit(self, value, scene=None):
        event = {"seconds": self.now(), "data": value}
        if scene:
            event["scene"] = scene
        self.events.append(event)
        print(value, end="", flush=True)

    def scene(self, name, title):
        self.current_scene = name
        self.emit(HEADER + HEADER_KIND[self.kind] + "\n\n" + title + "\n\n", scene=name)

    def run(self, arguments, label, environment, cwd):
        if label:
            self.emit("\033[32m$\033[0m " + label + "\n")
        begin = time.monotonic()
        result = subprocess.run(arguments, cwd=cwd, env=environment, capture_output=True, text=True, timeout=120)
        self.commands.append({"scene": self.current_scene, "command": ["starport", *arguments[1:]],
                              "environment_names": sorted(environment), "exit_code": result.returncode,
                              "elapsed_seconds": round(time.monotonic() - begin, 6)})
        if result.returncode:
            raise RuntimeError("command failed: starport " + " ".join(arguments[1:]))
        return result.stdout


def wait_for(predicate, seconds, message):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(0.25)
    raise RuntimeError(message)


def http_ready(url):
    try:
        with urllib.request.urlopen(url, timeout=1) as response:
            return response.status == 200
    except (OSError, urllib.error.URLError):
        return False


def egress_denied():
    """Report whether the sandbox refuses a connection to a documentation-only address.

    192.0.2.1 (RFC 5737) reaches no host. Under the loopback-only sandbox the
    connection fails at once with a permission error.
    """
    try:
        socket.create_connection(("192.0.2.1", 443), timeout=1).close()
    except PermissionError:
        return True
    except OSError:
        return False
    return False


def capture_main(args):
    work = Path(args.work)
    candidate = json.loads((work / "candidate.json").read_text())
    capture = Capture(candidate)
    report, emit, scene = capture.report, capture.emit, capture.scene
    release = capture.kind == "release"
    pause = args.scene_pause
    port = args.port
    gateway = f"http://127.0.0.1:{port}"
    secrets_seen = []
    processes = []
    fixture = None
    try:
        provider_credential = os.environ.get(PROVIDER_CREDENTIAL, "") if release else None
        if release and not provider_credential:
            raise RuntimeError(f"a release capture needs {PROVIDER_CREDENTIAL} in its environment")
        if release:
            secrets_seen.append(provider_credential)
        with tempfile.TemporaryDirectory(prefix="starport-readme-demo-") as directory:
            root = Path(directory)
            home = root / "home"
            home.mkdir()
            environment = {"PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "HOME": str(home),
                           "XDG_CONFIG_HOME": str(home / "config"), "XDG_DATA_HOME": str(home / "data"),
                           "XDG_STATE_HOME": str(home / "state"), "STARPORT_SERVER_PORT": str(port)}
            report["network_egress_denied"] = egress_denied()
            report["network_egress_reason"] = "real provider" if release else "loopback-only sandbox"
            if release and report["network_egress_denied"]:
                raise RuntimeError("a release capture needs network access to the provider")
            report["persistent_selectors_present"] = any(name in environment for name in PERSISTENT_SELECTORS)

            archive = Path(candidate["archive_path"])
            if release:
                scene("install", f"1  INSTALL  /  macOS ARM64 · verified release {candidate['release_tag']}")
                emit(f"Release: {candidate['release_tag']} · tag commit {candidate['head_commit'][:12]}\n")
            else:
                scene("install", "1  INSTALL  /  macOS ARM64 · candidate archive")
                if candidate["source"] == "ci-run":
                    emit(f"Candidate: CI run {candidate['run_id']} · pull request {candidate['pull_request']}\n")
                else:
                    emit("Candidate: local development archive\n")
            emit("Archive: " + candidate["archive_name"] + "\n")
            if sha256(archive) != candidate["archive_sha256"]:
                raise RuntimeError("the archive changed after candidate resolution")
            if release:
                if candidate.get("attestation_verified") is not True:
                    raise RuntimeError("the release candidate has no verified attestation")
                # Two lines: the one-line form exceeds the readable frame width.
                emit("SHA-256 verified against checksums.txt.\n")
                emit("Provenance verified by gh attestation.\n\n")
            elif candidate["checksum_verified"]:
                emit("SHA-256 verified against checksums.txt.\n\n")
            else:
                emit("SHA-256 recorded. A local archive has no checksum file.\n\n")
            with tarfile.open(archive) as bundle:
                if hasattr(tarfile, "data_filter"):
                    bundle.extractall(root / "install", filter="data")
                else:
                    bundle.extractall(root / "install")
            binary = root / "install" / "starport"
            if not binary.is_file():
                raise RuntimeError("the archive has no starport binary at its root")
            report["binary_sha256"] = sha256(binary)
            version = capture.run([str(binary), "--version"], "starport --version", environment, home)
            emit(version)
            report["starport_version"] = version.strip()
            time.sleep(pause)

            scene("catalog", "2  EXPLORE  /  catalog before provider credentials")
            output = capture.run([str(binary), "models", "show", MODEL, "--json"],
                                 "starport models show " + MODEL + " --json", environment, home)
            model = json.loads(output)
            emit("Selected fields: id, context_length, pricing\n")
            emit(json.dumps({key: model.get(key) for key in ("id", "context_length", "pricing")}, indent=2) + "\n")
            report["catalog_model"] = model.get("id")
            report["catalog_environment_has_provider_key"] = any(
                name.endswith("_API_KEY") or name.endswith("_INFERENCE_BASE_URL") for name in environment)
            time.sleep(pause)

            if release:
                environment[PROVIDER_CREDENTIAL] = provider_credential
                report["base_url_mechanism"] = None
                report["provider_credential_source"] = "environment"
            else:
                token = TOKEN_PREFIX + secrets.token_hex(24)
                secrets_seen.append(token)
                requests = root / "fixture-requests.jsonl"
                ready = root / "fixture-port"
                fixture = subprocess.Popen(
                    [sys.executable, str(FIXTURE), "--request-log", str(requests), "--ready-file", str(ready)],
                    env={"PATH": environment["PATH"], "STARPORT_DEMO_FIXTURE_TOKEN": token},
                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                processes.append(fixture)
                fixture_port = int(wait_for(lambda: ready.is_file() and ready.read_text().strip(), 15,
                                            "the fixture upstream did not start"))
                environment[PROVIDER_CREDENTIAL] = token
                environment[BASE_URL_MECHANISM] = f"http://127.0.0.1:{fixture_port}"
                report["base_url_mechanism"] = BASE_URL_MECHANISM
                report["provider_credential_source"] = "generated"

            scene("setup", "3  CONNECT  /  temporary development gateway")
            if release:
                emit("Provider: OpenAI · real inference through the gateway\n")
                emit(f"Provider credential: {PROVIDER_CREDENTIAL} [from the environment, value hidden]\n\n")
            else:
                emit("Fixture upstream: local OpenAI-compatible rehearsal server\n")
                emit(f"{BASE_URL_MECHANISM}=http://127.0.0.1:{fixture_port}\n")
                emit(f"Provider credential: {PROVIDER_CREDENTIAL} [fixture token, value hidden]\n\n")
            emit("\033[32m$\033[0m starport dev --no-open\n")
            log = root / "server.log"
            with log.open("w") as stream:
                process = subprocess.Popen([str(binary), "dev", "--no-open"], cwd=home, env=environment,
                                           stdout=stream, stderr=subprocess.STDOUT)
            processes.append(process)

            def gateway_key():
                if process.poll() is not None:
                    raise RuntimeError("the development gateway exited before readiness")
                for line in log.read_text().splitlines():
                    if line.startswith("Gateway API key (shown once): "):
                        return line.split(": ", 1)[1].strip()
                return None

            key = wait_for(gateway_key, 60, "the development gateway printed no gateway key")
            secrets_seen.append(key)
            wait_for(lambda: http_ready(gateway + "/health/ready"), 60, "the development gateway did not become ready")
            command_line = len(capture.commands)
            capture.commands.append({"scene": "setup", "command": ["starport", "dev", "--no-open"],
                                     "environment_names": sorted(environment), "exit_code": None,
                                     "elapsed_seconds": None})
            emit(f"{gateway}  ·  ready\n")
            emit("Gateway API key: [value hidden]  ·  client authentication\n")
            emit("Provider credential and gateway key serve separate roles.\n")
            emit("Startup logs omitted. This development session is temporary.\n")
            discovery = urllib.request.Request(gateway + "/api/v1/catalog/discovery",
                                               headers={"Authorization": "Bearer " + key})
            with urllib.request.urlopen(discovery, timeout=30) as response:
                report["catalog_generation"] = json.loads(response.read()).get("generation_id")
            time.sleep(pause)

            if release:
                scene("answer", "4  ASK  /  real provider response, original timing")
                emit("REAL PROVIDER: OpenAI answers through the gateway. Timing is unchanged.\n\n")
            else:
                scene("answer", "4  ASK  /  disclosed fixture answer, original timing")
                emit("FIXTURE: a local rehearsal server answers. No provider is called.\n\n")
            emit(f"Model: {MODEL}  ·  streaming  ·  max_tokens: 32\n")
            emit("Message: Say: Hello from Starport!\n\n")
            emit(f"POST {gateway}/api/v1/chat/completions\n")
            emit("Authorization: Starport gateway key [value hidden]\n\n")
            payload = {"model": MODEL, "max_tokens": 32, "stream": True,
                       "messages": [{"role": "user", "content": "Say: Hello from Starport!"}]}
            request = urllib.request.Request(gateway + "/api/v1/chat/completions", data=json.dumps(payload).encode(),
                                             headers={"Authorization": "Bearer " + key,
                                                      "Content-Type": "application/json"})
            report["request"] = payload
            interval = {"start_event": len(capture.events), "start_seconds": capture.now()}
            response_report = {"content": "", "stream_events": 0, "final_event": None}
            with urllib.request.urlopen(request, timeout=60) as response:
                response_report["status"] = response.status
                response_report["content_type"] = response.headers.get("Content-Type")
                for line in response:
                    if not line.startswith(b"data: "):
                        continue
                    data = line[6:].decode().strip()
                    response_report["stream_events"] += 1
                    response_report["final_event"] = data
                    if data == "[DONE]":
                        break
                    chunk = json.loads(data)
                    if chunk.get("provider"):
                        response_report["reported_provider"] = chunk["provider"]
                    if chunk.get("model"):
                        response_report["reported_model"] = chunk["model"]
                    for choice in chunk.get("choices", []):
                        text = (choice.get("delta") or {}).get("content") or ""
                        if text:
                            response_report["content"] += text
                            emit(text)
            interval["end_seconds"] = capture.now()
            interval["end_event"] = len(capture.events) - 1
            if response_report["final_event"] != "[DONE]":
                raise RuntimeError("the answer stream did not end with [DONE]")
            if interval["end_event"] < interval["start_event"]:
                raise RuntimeError("the answer stream carried no content")
            report["inference_interval"] = {key: interval[key] for key in
                                            ("start_event", "end_event", "start_seconds", "end_seconds")}
            response_report["content_length"] = len(response_report["content"])
            report["response"] = response_report
            if release:
                report["fixture_requests"] = []
            else:
                fixture_requests = [json.loads(line) for line in requests.read_text().splitlines() if line]
                report["fixture_requests"] = fixture_requests
                routed = [entry for entry in fixture_requests if entry.get("method") == "POST" and entry.get("authorized")
                          and entry.get("path", "").endswith("/chat/completions")]
                if len(routed) != 1:
                    raise RuntimeError("the fixture upstream did not receive exactly one authorized chat request")
            emit("\n\nStream complete: [DONE]\n")
            emit("Provider reported by Starport: " + response_report.get("reported_provider", "not reported") + "\n")
            if release:
                emit("Upstream: the real provider answered one chat request.\n")
            else:
                emit("Upstream: the fixture received 1 authorized chat request.\n")
            time.sleep(pause)

            scene("next", "5  USE YOUR CLIENT  /  change its base URL")
            emit(f"OpenAI client       {gateway}/v1\n")
            emit(f"OpenRouter client   {gateway}/api/v1\n\n")
            emit("Client credential: the Starport gateway API key\n\n")
            emit("Keep the gateway: starport init → starport serve\n")
            emit("Persistent and team setups: docs/OPERATOR-GUIDE.md\n")
            time.sleep(pause)
            process.send_signal(signal.SIGINT)
            process.wait(timeout=30)
            capture.commands[command_line]["exit_code"] = process.returncode
            report["shutdown_exit_code"] = process.returncode
            if fixture is not None:
                fixture.terminate()
                fixture.wait(timeout=10)
            processes.clear()
            report["leftover_home_files"] = sorted(str(path.relative_to(home)) for path in home.rglob("*")
                                                   if path.is_file())
            if report["leftover_home_files"]:
                raise RuntimeError("the temporary development home kept files")
            if not candidate.get("head_commit") and candidate["source"] == "local":
                candidate["head_commit"] = local_head(report["starport_version"], args.repository)
            candidate["snapshot_version"] = report["starport_version"]
            candidate["binary_sha256"] = report["binary_sha256"]
            report["verdict"] = "PASS"
        report["scratch_removed"] = not root.exists()
    except Exception as error:
        report["verdict"] = "FAIL"
        report["error_type"] = type(error).__name__
        report["error"] = str(error) if isinstance(error, RuntimeError) else "the capture failed"
        raise
    finally:
        for process in processes:
            if process.poll() is None:
                process.send_signal(signal.SIGINT)
                try:
                    process.wait(timeout=30)
                except subprocess.TimeoutExpired:
                    process.kill()
        report["scenes"] = scene_ranges(capture.events)
        text = json.dumps(report, indent=2) + "\n"
        # A leaked credential stops the capture before any output keeps it.
        if any(value and value in text for value in secrets_seen):
            raise RuntimeError("a credential value reached the capture output")
        (work / "events.json").write_text(text)
        (work / "candidate.json").write_text(json.dumps(candidate, indent=2) + "\n")


def local_head(version, repository):
    """Resolve the commit that a `git describe` version names, or return None."""
    match = re.search(r"-g([0-9a-f]{7,40})$", version)
    if not match or not repository:
        return None
    result = subprocess.run(["git", "-C", repository, "rev-parse", "--verify", "--quiet", match.group(1) + "^{commit}"],
                            capture_output=True, text=True, timeout=30)
    return result.stdout.strip() or None


def scene_ranges(events):
    starts = [(index, event["scene"]) for index, event in enumerate(events) if event.get("scene")]
    scenes = []
    for position, (start, name) in enumerate(starts):
        end = starts[position + 1][0] - 1 if position + 1 < len(starts) else len(events) - 1
        scenes.append({"name": name, "start_event": start, "end_event": end,
                       "start_seconds": events[start]["seconds"], "end_seconds": events[end]["seconds"]})
    return scenes


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    commands = parser.add_subparsers(dest="action", required=True)
    candidate = commands.add_parser("candidate", help="resolve and bind the candidate archive")
    candidate.add_argument("--work", required=True, help="private work directory")
    source = candidate.add_mutually_exclusive_group(required=True)
    source.add_argument("--run", help="pull request CI run ID")
    source.add_argument("--release-tag", help="published release tag, for example v1.3.0")
    source.add_argument("--archive", help="local development archive")
    candidate.add_argument("--checksums", help="checksum file for a local archive")
    candidate.add_argument("--head", help="40-character source commit of a local archive")
    run = commands.add_parser("capture", help="capture the five scenes")
    run.add_argument("--work", required=True, help="private work directory with candidate.json")
    run.add_argument("--repository", help="Starport checkout that resolves a local version commit")
    run.add_argument("--port", type=int, default=PORT)
    run.add_argument("--scene-pause", type=float, default=1.0, help="seconds between scenes before the edit")
    args = parser.parse_args()
    if args.action == "candidate":
        if args.head and not re.fullmatch(r"[0-9a-f]{40}", args.head):
            parser.error("--head needs a 40-character commit")
        if (args.run or args.release_tag) and (args.checksums or args.head):
            parser.error("--checksums and --head apply only to --archive")
        candidate_main(args)
    else:
        capture_main(args)


if __name__ == "__main__":
    main()
