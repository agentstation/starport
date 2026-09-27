#!/usr/bin/env python3
"""Qualify fresh local Compose persistence with an explicitly built image."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True, help='Previously built Starport image')
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    project = 'starport-recipe-' + secrets.token_hex(6)
    observations = []
    with tempfile.TemporaryDirectory(prefix=project) as scratch:
        directory = Path(scratch)
        dotenv = directory / '.env'
        dotenv.write_text('\n'.join([
            'STARPORT_SECURITY_MASTER_KEY=' + secrets.token_hex(32),
            'STARPORT_CATALOG_SOURCE=embedded',
            'STARPORT_CATALOG_NETWORK_MODE=offline',
            'STARPORT_CATALOG_ACQUISITION_ENABLED=false',
            'STARPORT_PORT=0', '',
        ]))
        dotenv.chmod(0o600)
        override = directory / 'image.json'
        override.write_text(json.dumps({'services': {'starport': {'image': args.image}}}))
        # Do not allow the operator's process environment to select test storage.
        environment = {name: os.environ[name] for name in (
            'PATH', 'HOME', 'DOCKER_HOST', 'DOCKER_CONTEXT', 'DOCKER_CONFIG',
            'DOCKER_TLS_VERIFY', 'DOCKER_CERT_PATH',
        ) if name in os.environ}
        compose = ['docker', 'compose', '--project-name', project,
                   '--project-directory', scratch, '--env-file', str(dotenv),
                   '-f', str(root / 'docker-compose.yml'), '-f', str(override)]

        def run(command, data=None, timeout=90):
            result = subprocess.run(command, input=data, capture_output=True,
                                    env=environment, timeout=timeout)
            if result.returncode:
                # CLI initialization can print one-time fixture credentials.
                # Do not copy stdout or stderr into retained evidence.
                raise RuntimeError(f'{command[0]} operation failed with exit {result.returncode}')
            return result.stdout

        def cli(*arguments):
            return run(compose + ['run', '--rm', '--no-deps', '--pull', 'never', 'starport', *arguments])

        def request(path, data=None, content_type='application/json'):
            headers = {'Authorization': 'Bearer ' + key}
            if data is not None:
                headers['Content-Type'] = content_type
            req = urllib.request.Request(base + path, data=data, headers=headers)
            try:
                with urllib.request.urlopen(req, timeout=5) as response:
                    return response.read()
            except urllib.error.HTTPError as error:
                raise RuntimeError(f'fixture request {path}: {error.code}: {error.read().decode()}') from None

        def start():
            run(compose + ['up', '-d', '--no-build', 'starport'])
            mapping = run(compose + ['port', 'starport', '8080']).decode().strip()
            address = 'http://' + mapping
            for _ in range(100):
                try:
                    with urllib.request.urlopen(address + '/health/ready', timeout=1) as response:
                        if response.status == 200:
                            return address
                except (urllib.error.URLError, OSError):
                    pass
                time.sleep(0.2)
            raise RuntimeError('fresh recipe did not become ready')

        def verify_records():
            account = json.loads(request('/api/v1/admin/accounts/recipe-record'))
            assert 'recipe-record' in json.dumps(account), 'KV record missing'
            assert request('/v1/files/' + file_id + '/content') == payload, 'file bytes changed'
            audit = json.loads(request('/api/v1/admin/audit'))
            assert 'recipe-record' in json.dumps(audit), 'SQL audit record missing'
            # Liveness can precede asynchronous catalog route validation.
            for _ in range(20):
                req = urllib.request.Request(base + '/api/v1/catalog',
                                             headers={'Authorization': 'Bearer ' + key})
                try:
                    with urllib.request.urlopen(req, timeout=5) as response:
                        catalog = json.load(response)
                    break
                except urllib.error.HTTPError as error:
                    if error.code != 503:
                        raise
                    time.sleep(1)
            else:
                raise RuntimeError(request('/api/v1/admin/catalog/status').decode())
            assert catalog, 'catalog unavailable'

        try:
            paths = json.loads(cli('config', 'paths', '--json'))
            for name, expected in {
                'config_file': '/var/lib/starport/config/starport/config.env',
                'badger_dir': '/var/lib/starport/data/badger',
                'sqlite_file': '/var/lib/starport/data/sqlite/starport.db',
                'files_dir': '/var/lib/starport/data/files',
                'runtime_dir': '/var/lib/starport/state/catalog/runtime/default',
                'baseline_dir': '/var/lib/starport/data/catalog/baseline',
            }.items():
                assert paths[name] == expected, f'wrong container path for {name}'
            observations.append('effective_container_paths')
            initialized = json.loads(cli('init', '--configured-storage', '--name', 'recipe-admin', '--json'))
            key = initialized['api_key']
            cli('auth', 'rotate')
            base = start()
            request('/api/v1/admin/accounts', json.dumps({'id': 'recipe-record'}).encode())
            payload = b'fresh local recipe retains these uploaded bytes\n'
            boundary = 'starport-recipe-upload'
            body = (f'--{boundary}\r\nContent-Disposition: form-data; name="purpose"\r\n\r\nuser_data\r\n'
                    f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="recipe.txt"\r\n'
                    'Content-Type: text/plain\r\n\r\n').encode() + payload + f'\r\n--{boundary}--\r\n'.encode()
            uploaded = json.loads(request('/v1/files', body, 'multipart/form-data; boundary=' + boundary))
            file_id = uploaded['id']
            verify_records()
            observations.append('fresh_start_kv_sql_file_catalog')
            run(compose + ['down'])
            base = start()
            verify_records()
            observations.append('container_recreation_preserves_records')
            run(compose + ['stop', 'starport'])
            container = run(compose + ['ps', '-a', '-q', 'starport']).decode().strip()
            backup = run(['docker', 'cp', container + ':/var/lib/starport/.', '-'])
            run(compose + ['down', '--volumes'])
            run(compose + ['create', '--no-build', 'starport'])
            container = run(compose + ['ps', '-a', '-q', 'starport']).decode().strip()
            run(['docker', 'cp', '-a', '-', container + ':/var/lib/starport'], backup)
            base = start()
            verify_records()
            observations.append('cold_backup_restores_into_fresh_volumes')
            print(json.dumps({'status': 'PASS', 'image': args.image,
                              'image_id': run(['docker', 'image', 'inspect', args.image, '--format', '{{.Id}}']).decode().strip(),
                              'observations': observations, 'source_sha256': {
                                  name: hashlib.sha256((root / name).read_bytes()).hexdigest()
                                  for name in ('Dockerfile', 'docker-compose.yml', 'scripts/test-storage-recipes.py')},
                              'limits': 'Fresh local recipe only. No host power-loss or populated fleet recovery qualification.'}))
        finally:
            # The unique project contains only this probe's disposable fixtures.
            run(compose + ['down', '--volumes', '--remove-orphans'])


if __name__ == '__main__':
    main()
