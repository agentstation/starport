#!/usr/bin/env python3
"""Capture real release commands and streamed inference without retaining credentials."""
import argparse
import getpass
import hashlib
import json
import os
from pathlib import Path
import platform
import signal
import subprocess
import tarfile
import tempfile
import time
import urllib.error
import urllib.request

TAG = 'v1.2.0'
ARCHIVE = 'starport_1.2.0_darwin_arm64.tar.gz'
MODEL = 'openai/gpt-4o-mini'


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    started = time.monotonic()
    events = []
    commands = []
    report = {'release': TAG, 'platform': platform.platform(), 'events': events, 'commands': commands}
    process = None

    def emit(value):
        events.append({'seconds': round(time.monotonic() - started, 6), 'data': value})
        print(value, end='', flush=True)

    def scene(title):
        emit('\033[2J\033[H\033[1;36mSTARPORT\033[0m  /  first request\n\n' + title + '\n\n')

    def run(command, label=None, environment=None, cwd=None):
        if label:
            emit('\033[32m$\033[0m ' + label + '\n')
        begin = time.monotonic()
        result = subprocess.run(command, cwd=cwd, env=environment, capture_output=True, text=True, timeout=120)
        commands.append({'command': command, 'exit_code': result.returncode,
                         'elapsed_seconds': round(time.monotonic() - begin, 6)})
        if result.returncode:
            raise RuntimeError('command failed: ' + command[0])
        return result.stdout

    try:
        with tempfile.TemporaryDirectory(prefix='starport-first-use-capture-') as directory:
            root = Path(directory)
            home = root / 'home'
            home.mkdir()
            environment = {'PATH': os.environ['PATH'], 'HOME': str(home),
                           'XDG_CONFIG_HOME': str(home / 'config'), 'XDG_DATA_HOME': str(home / 'data'),
                           'XDG_STATE_HOME': str(home / 'state'), 'STARPORT_SERVER_PORT': '19325'}
            report['scratch_root'] = str(root)
            report['persistent_selectors_present'] = [n for n in ['STARPORT_CATALOG_STATE_DIR', 'STARPORT_FILES_BACKEND'] if n in environment]
            scene('1  INSTALL  /  macOS ARM64 · verified release')
            run(['gh', 'release', 'download', TAG, '--repo', 'agentstation/starport', '--pattern', ARCHIVE,
                 '--pattern', 'checksums.txt', '--dir', str(root)],
                'gh release download v1.2.0 --repo agentstation/starport\n  --pattern "*darwin_arm64.tar.gz" --pattern checksums.txt')
            archive = root / ARCHIVE
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            expected = next(line.split()[0] for line in (root / 'checksums.txt').read_text().splitlines() if line.split()[-1] == ARCHIVE)
            if digest != expected:
                raise RuntimeError('archive checksum mismatch')
            report['archive_sha256'] = digest
            emit('SHA-256 verified against the release checksum file.\n')
            with tarfile.open(archive) as bundle:
                bundle.extractall(root, filter='data')
            binary = root / 'starport'
            report['binary_sha256'] = hashlib.sha256(binary.read_bytes()).hexdigest()
            emit(run([str(binary), '--version'], 'starport --version', environment, home))
            time.sleep(3)

            scene('2  EXPLORE  /  catalog before provider credentials')
            output = run([str(binary), 'models', 'show', MODEL, '--json'],
                         "starport models show openai/gpt-4o-mini --json\n  | jq '{id, context_length, pricing}'", environment, home)
            selected = subprocess.run(['jq', '{id, context_length, pricing}'], input=output, capture_output=True, text=True, check=True)
            emit(selected.stdout)
            report['catalog'] = json.loads(output)
            report['catalog_environment_has_provider_key'] = 'OPENAI_API_KEY' in environment
            time.sleep(4)

            key = getpass.getpass('OpenAI credential for one real request (hidden): ')
            if not key:
                raise RuntimeError('a provider credential is required')
            environment['OPENAI_API_KEY'] = key
            scene('3  CONNECT  /  temporary development gateway')
            emit('Provider credential: OPENAI_API_KEY [value hidden]\n\n')
            emit('\033[32m$\033[0m starport dev --no-open\n')
            log = root / 'server.log'
            with log.open('w') as stream:
                process = subprocess.Popen([str(binary), 'dev', '--no-open'], cwd=home, env=environment,
                                           stdout=stream, stderr=subprocess.STDOUT)
            gateway_key = ''
            for _ in range(120):
                if process.poll() is not None:
                    raise RuntimeError('development gateway exited before readiness')
                for line in log.read_text().splitlines():
                    if line.startswith('Gateway API key (shown once): '):
                        gateway_key = line.split(': ', 1)[1]
                try:
                    with urllib.request.urlopen('http://127.0.0.1:19325/health/ready', timeout=1) as response:
                        if response.status == 200 and gateway_key:
                            break
                except (OSError, urllib.error.URLError):
                    pass
                time.sleep(.25)
            else:
                raise RuntimeError('development gateway did not become ready')
            emit('http://127.0.0.1:19325  ·  ready\n')
            emit('Gateway API key: [value hidden]  ·  client authentication\n')
            emit('Provider credential and gateway key serve separate roles.\n')
            emit('Startup logs omitted. This development session is temporary.\n')
            time.sleep(4)

            scene('4  ASK  /  real provider response, original timing')
            emit('Model: openai/gpt-4o-mini  ·  streaming  ·  max_tokens: 32\n')
            emit('Message: Say: Hello from Starport!\n\n')
            emit('POST http://127.0.0.1:19325/api/v1/chat/completions\n')
            emit('Authorization: Starport gateway key [value hidden]\n\n')
            payload = {'model': MODEL, 'max_tokens': 32, 'stream': True,
                       'messages': [{'role': 'user', 'content': 'Say: Hello from Starport!'}]}
            request = urllib.request.Request('http://127.0.0.1:19325/api/v1/chat/completions',
                        data=json.dumps(payload).encode(), headers={'Authorization': 'Bearer ' + gateway_key,
                                                                  'Content-Type': 'application/json'})
            report['request'] = payload
            report['inference_start_seconds'] = round(time.monotonic() - started, 6)
            chunks = []
            with urllib.request.urlopen(request, timeout=60) as response:
                report['response_status'] = response.status
                report['content_type'] = response.headers.get('Content-Type')
                for line in response:
                    if not line.startswith(b'data: '):
                        continue
                    event = line[6:].decode().strip()
                    chunks.append({'seconds': round(time.monotonic() - started, 6), 'data': event})
                    if event == '[DONE]':
                        break
                    chunk = json.loads(event)
                    if chunk.get('provider'):
                        report['reported_provider'] = chunk['provider']
                    if chunk.get('model'):
                        report['reported_model'] = chunk['model']
                    for choice in chunk.get('choices', []):
                        text = choice.get('delta', {}).get('content', '')
                        if text:
                            emit(text)
            report['inference_end_seconds'] = round(time.monotonic() - started, 6)
            report['stream_events'] = chunks
            if not chunks or chunks[-1]['data'] != '[DONE]':
                raise RuntimeError('provider stream did not complete')
            emit('\n\nStream complete: [DONE]\n')
            emit('Provider reported by Starport: ' + report.get('reported_provider', 'not reported') + '\n')
            emit('Provider time is included. This is not a gateway latency benchmark.\n')
            time.sleep(5)

            scene('5  USE YOUR CLIENT  /  change its base URL')
            emit('OpenAI client       http://127.0.0.1:19325/v1\n')
            emit('OpenRouter client   http://127.0.0.1:19325/api/v1\n\n')
            emit('Client credential: the Starport gateway API key\n\n')
            emit('Keep the gateway: starport init → starport serve\n')
            emit('Persistent and team setups: docs/OPERATOR-GUIDE.md\n')
            time.sleep(5)
            process.send_signal(signal.SIGINT)
            process.wait(timeout=30)
            report['shutdown_exit_code'] = process.returncode
            process = None
            report['remaining_home_files'] = [str(f.relative_to(home)) for f in home.rglob('*') if f.is_file()]
            if report['remaining_home_files']:
                raise RuntimeError('temporary development home retained files')
            report['verdict'] = 'PASS'
        report['scratch_removed'] = not root.exists()
    except Exception as error:
        report['verdict'] = 'FAIL'
        report['error_type'] = type(error).__name__
        report['error'] = str(error) if isinstance(error, RuntimeError) else 'capture failed; inspect locally without exposing credentials'
        raise
    finally:
        if process and process.poll() is None:
            process.send_signal(signal.SIGINT)
            process.wait(timeout=30)
        (args.output / 'capture.json').write_text(json.dumps(report, indent=2) + '\n')


if __name__ == '__main__':
    main()
