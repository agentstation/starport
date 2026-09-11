#!/usr/bin/env python3
"""Verify a published Starport archive on its native operating system and CPU."""

import argparse
import hashlib
from html.parser import HTMLParser
import json
import os
from pathlib import Path
import platform
import re
import signal
import socket
import stat
import subprocess
import tarfile
import tempfile
import threading
import time
import urllib.error
import urllib.request
import zipfile


REPOSITORY = 'agentstation/starport'
COMMON_FILES = {'.env.example', 'LICENSE', 'README.md', 'SECURITY.md',
                'completions/starport.bash', 'completions/starport.fish',
                'completions/starport.ps1', 'completions/starport.zsh', 'manpages/starport.1'}


def native_target():
    operating_system = {'Darwin': 'darwin', 'Linux': 'linux', 'Windows': 'windows'}.get(platform.system())
    architecture = {'AMD64': 'x86_64', 'x86_64': 'x86_64', 'arm64': 'arm64', 'aarch64': 'arm64', 'ARM64': 'arm64'}.get(platform.machine())
    if not operating_system or not architecture:
        raise ValueError('This native operating system or CPU is unsupported.')
    return operating_system, architecture


def archive_name(tag, operating_system, architecture):
    if not re.fullmatch(r'v\d+\.\d+\.\d+(?:-rc\.\d+)?', tag):
        raise ValueError('The release tag must be an exact application version.')
    suffix = 'zip' if operating_system == 'windows' else 'tar.gz'
    return f'starport_{tag[1:]}_{operating_system}_{architecture}.{suffix}'


def verify_checksum(archive, checksums, release):
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    matches = [line.split()[0] for line in checksums.read_text().splitlines()
               if len(line.split()) == 2 and line.split()[1] == archive.name]
    if matches != [digest]:
        raise ValueError('The archive does not match one exact checksum entry.')
    assets = [asset for asset in release['assets'] if asset['name'] == archive.name]
    if len(assets) != 1 or assets[0].get('digest') != 'sha256:' + digest:
        raise ValueError('The archive does not match the GitHub release asset digest.')
    return digest


def extract_archive(archive, destination, executable):
    expected = COMMON_FILES | {executable}
    if archive.suffix == '.zip':
        with zipfile.ZipFile(archive) as source:
            entries = source.infolist()
            if len(entries) != len(expected) or {entry.filename for entry in entries} != expected:
                raise ValueError('The release ZIP has unexpected or duplicate members.')
            if any(entry.is_dir() or stat.S_ISLNK(entry.external_attr >> 16) for entry in entries):
                raise ValueError('The release ZIP must contain regular files only.')
            source.extractall(destination)
    else:
        with tarfile.open(archive) as source:
            entries = source.getmembers()
            if len(entries) != len(expected) or {entry.name for entry in entries} != expected:
                raise ValueError('The release tar archive has unexpected or duplicate members.')
            if any(not entry.isfile() for entry in entries):
                raise ValueError('The release tar archive must contain regular files only.')
            source.extractall(destination, filter='data')


def verify_catalog_payload(command, payload):
    if not isinstance(payload, dict):
        raise ValueError('The catalog command must return a JSON object.')
    if command == 'search':
        models = payload.get('data')
        if not isinstance(models, list) or not any(isinstance(model, dict) and model.get('id') == 'openai/gpt-4o-mini' for model in models):
            raise ValueError('The catalog search did not return the README model.')
    elif command == 'show':
        if payload.get('id') != 'openai/gpt-4o-mini' or payload.get('object') != 'model':
            raise ValueError('The catalog detail did not identify the README model.')
    else:
        raise ValueError('The catalog command is unsupported by this verifier.')


def isolated_environment(home, temporary, port):
    # Windows needs its system paths. Provider and GitHub credentials stay out.
    environment = {key: os.environ[key] for key in ('PATH', 'SystemRoot', 'SYSTEMROOT', 'WINDIR', 'COMSPEC', 'PATHEXT') if key in os.environ}
    environment.update({'HOME': str(home), 'USERPROFILE': str(home), 'APPDATA': str(home / 'AppData/Roaming'),
                        'LOCALAPPDATA': str(home / 'AppData/Local'), 'XDG_CONFIG_HOME': str(home / 'config'),
                        'XDG_STATE_HOME': str(home / 'state'), 'TEMP': str(temporary), 'TMP': str(temporary),
                        'TMPDIR': str(temporary), 'STARPORT_SERVER_PORT': str(port), 'NO_COLOR': '1'})
    return environment


def run_binary(binary, arguments, environment, home):
    result = subprocess.run([str(binary), *arguments], env=environment, cwd=home,
                            text=True, capture_output=True, timeout=30)
    if result.returncode:
        raise RuntimeError(f'The released command failed: {arguments!r} (exit {result.returncode}).')
    return result.stdout


def request_json(url, key=None):
    headers = {'Authorization': 'Bearer ' + key} if key else {}
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=3) as response:
        return response.status, json.load(response)


class ConsoleAssets(HTMLParser):
    def __init__(self):
        super().__init__()
        self.paths = set()

    def handle_starttag(self, tag, attrs):
        attributes = dict(attrs)
        path = attributes.get('src') if tag == 'script' else attributes.get('href') if tag == 'link' else None
        if path and path.startswith('/assets/'):
            self.paths.add(path)


def console_response(url):
    try:
        with urllib.request.urlopen(url, timeout=5) as response:
            return response.status, response.headers.get_content_type(), response.read()
    except urllib.error.HTTPError as error:
        error.close()
        raise ValueError(f'The console returned HTTP {error.code}.') from None


def verify_console(base):
    status, content_type, body = console_response(base + '/')
    if status != 200 or content_type != 'text/html':
        raise ValueError('The console must return HTML with HTTP 200.')
    assets = ConsoleAssets()
    assets.feed(body.decode('utf-8'))
    if not any(path.endswith('.js') for path in assets.paths) or not any(path.endswith('.css') for path in assets.paths):
        raise ValueError('The console HTML must reference JavaScript and CSS assets.')
    for path in sorted(assets.paths):
        status, content_type, body = console_response(base + path)
        if status != 200 or not body or content_type == 'text/html':
            raise ValueError('A console asset is missing or returns the HTML fallback.')
    return len(assets.paths)


def verify_development(binary, environment, home, port, report):
    options = {'creationflags': subprocess.CREATE_NEW_PROCESS_GROUP} if os.name == 'nt' else {}
    process = subprocess.Popen([str(binary), 'dev', '--no-open'], env=environment, cwd=home,
                               stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, **options)
    lines = []
    def collect():
        for line in process.stdout:
            lines.append(line)
    reader = threading.Thread(target=collect, daemon=True)
    reader.start()
    key = None
    base = f'http://127.0.0.1:{port}'
    try:
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError('The released development server exited before readiness.')
            for line in list(lines):
                if line.startswith('Gateway API key (shown once): '):
                    key = line.split(': ', 1)[1].strip()
            if key:
                try:
                    status, _ = request_json(base + '/health/ready')
                    report['readiness_status'] = status
                    break
                except (OSError, ValueError, urllib.error.URLError):
                    pass
            time.sleep(0.1)
        else:
            raise RuntimeError('The released development server did not become ready.')
        report['console_asset_count'] = verify_console(base)
        report['console_status'] = 200
        status, catalog = request_json(base + '/api/v1/models', key)
        if status != 200 or not any(model['id'] == 'openai/gpt-4o-mini' for model in catalog['data']):
            raise ValueError('The native server catalog is incomplete.')
        report['authenticated_catalog_status'] = status
        report['model_count'] = len(catalog['data'])
        try:
            request_json(base + '/api/v1/admin/info')
        except urllib.error.HTTPError as error:
            if error.code != 401:
                raise ValueError('An unauthenticated admin request must return 401.') from None
            report['unauthenticated_admin_status'] = error.code
        else:
            raise ValueError('An unauthenticated admin request was accepted.')
    finally:
        if process.poll() is None:
            process.send_signal(signal.CTRL_BREAK_EVENT if os.name == 'nt' else signal.SIGINT)
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=10)
                report['forced_shutdown'] = True
        reader.join(timeout=2)
        process.stdout.close()
        # Raw startup output contains a generated gateway key and is never retained.
        report['shutdown_exit_code'] = process.returncode
        if process.returncode != 0 or report.get('forced_shutdown'):
            raise RuntimeError('The native server did not shut down cleanly.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--tag', default=os.environ.get('RELEASE_TAG'), required=not bool(os.environ.get('RELEASE_TAG')))
    parser.add_argument('--expected-os', required=True, choices=['darwin', 'linux', 'windows'])
    parser.add_argument('--expected-arch', required=True, choices=['x86_64', 'arm64'])
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    report = {'schema_version': 1, 'verdict': 'UNVERIFIED', 'release': args.tag,
              'scope': 'Native published archive, keyless catalog, development startup, authentication and graceful shutdown. No paid inference.',
              'platform': platform.platform(), 'machine': platform.machine(), 'python': platform.python_version(),
              'workflow_run_id': os.environ.get('GITHUB_RUN_ID'), 'workflow_run_attempt': os.environ.get('GITHUB_RUN_ATTEMPT'),
              'workflow_commit': os.environ.get('GITHUB_SHA')}
    try:
        operating_system, architecture = native_target()
        if (operating_system, architecture) != (args.expected_os, args.expected_arch):
            raise ValueError('The current process does not match the required native platform.')
        name = archive_name(args.tag, operating_system, architecture)
        metadata = subprocess.run(['gh', 'api', f'repos/{REPOSITORY}/releases/tags/{args.tag}'],
                                  check=True, capture_output=True, text=True, timeout=30)
        release = json.loads(metadata.stdout)
        if release['tag_name'] != args.tag or release.get('draft'):
            raise ValueError('The selected release is not a published exact tag.')
        report['release_url'] = release['html_url']
        report['published_at'] = release['published_at']
        with tempfile.TemporaryDirectory(prefix='starport-native-archive-') as directory:
            root = Path(directory)
            subprocess.run(['gh', 'release', 'download', args.tag, '--repo', REPOSITORY,
                            '--pattern', name, '--pattern', 'checksums.txt', '--dir', directory],
                           check=True, capture_output=True, text=True, timeout=120)
            archive = root / name
            report['archive'] = name
            report['archive_sha256'] = verify_checksum(archive, root / 'checksums.txt', release)
            executable = 'starport.exe' if operating_system == 'windows' else 'starport'
            extract_archive(archive, root / 'distribution', executable)
            binary = root / 'distribution' / executable
            report['binary_sha256'] = hashlib.sha256(binary.read_bytes()).hexdigest()
            home, temporary = root / 'home', root / 'temporary'
            home.mkdir()
            temporary.mkdir()
            with socket.socket() as probe:
                probe.bind(('127.0.0.1', 0))
                port = probe.getsockname()[1]
            environment = isolated_environment(home, temporary, port)
            report['version'] = run_binary(binary, ['--version'], environment, home).strip()
            if report['version'] != 'starport version ' + args.tag[1:]:
                raise ValueError('The native executable reports the wrong release version.')
            for command in (['models', 'search', 'gpt-4o', '--json'], ['models', 'show', 'openai/gpt-4o-mini', '--json']):
                payload = json.loads(run_binary(binary, command, environment, home))
                verify_catalog_payload(command[1], payload)
            report['keyless_catalog_commands'] = 2
            if any(home.rglob('*')):
                raise ValueError('A passive catalog command created persistent user files.')
            verify_development(binary, environment, home, port, report)
            report['user_files_after_shutdown'] = [str(path.relative_to(home)) for path in home.rglob('*') if path.is_file()]
            if report['user_files_after_shutdown']:
                raise ValueError('Development mode left persistent user files.')
        report['verdict'] = 'PASS'
    except Exception as error:
        report['verdict'] = 'FAIL'
        # Command output and server logs can contain credentials. Keep them private.
        report['error_type'] = type(error).__name__
        report['error'] = str(error) if not isinstance(error, subprocess.SubprocessError) else 'A bounded external command failed.'
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps({key: report.get(key) for key in ('verdict', 'release', 'machine', 'archive', 'error')}))
    return 0 if report['verdict'] == 'PASS' else 1


if __name__ == '__main__':
    raise SystemExit(main())
