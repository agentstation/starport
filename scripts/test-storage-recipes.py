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


def qualify_fleet_local_state(root, image, run):
    """Check each replica's local credential without opening shared stores."""
    projects = ['starport-node-' + secrets.token_hex(6) for _ in range(2)]
    with tempfile.TemporaryDirectory(prefix='starport-fleet-recipe-') as scratch:
        directory = Path(scratch)
        dotenv = directory / '.env.fleet'
        dotenv.write_text('\n'.join([
            'STARPORT_DEPLOYMENT_ID=recipe-fixture',
            'STARPORT_SECURITY_MASTER_KEY=' + secrets.token_hex(32),
            'STARPORT_STORAGE_VALKEY_URL=valkeys://valkey.example.test:6379/0',
            'STARPORT_STORAGE_SQL_POSTGRES_URL=postgres://fixture@postgres.example.test/starport?sslmode=verify-full',
            'STARPORT_FILES_OBJECT_STORE_BUCKET=recipe-fixture',
            'STARPORT_FILES_OBJECT_STORE_REGION=us-east-1',
            'STARPORT_CATALOG_SOURCE=embedded',
            'STARPORT_CATALOG_ACQUISITION_ENABLED=false', '',
        ]))
        dotenv.chmod(0o600)
        override = directory / 'image.json'
        override.write_text(json.dumps({'services': {'starport': {'image': image}}}))
        commands = [['docker', 'compose', '--project-name', project,
                     '--project-directory', scratch, '--env-file', str(dotenv),
                     '-f', str(root / 'docker-compose.fleet.yml'), '-f', str(override)]
                    for project in projects]

        def cli(index, *args):
            return run(commands[index] + ['run', '--rm', '--no-deps', '--pull', 'never', 'starport', *args])

        def status(index):
            return json.loads(cli(index, 'auth', 'status', '--json'))

        try:
            assert not status(0)['present'], 'first replica inherited local credentials'
            cli(0, 'auth', 'rotate')  # Discard the one-time credential output.
            first = status(0)
            assert first['allows_network_bind'], 'rotation did not survive container replacement'
            assert not status(1)['present'], 'second replica shared the first local credential'
            cli(1, 'auth', 'rotate')
            second = status(1)
            assert second['allows_network_bind'], 'second replica lost its rotated token'
            cli(0, 'auth', 'rotate')
            assert status(0)['generation'] > first['generation'], 'first replica did not rotate'
            assert status(1)['generation'] == second['generation'], 'rotation crossed replica boundary'
            return ['fleet_replica_rotation_survives_replacement', 'fleet_replica_local_state_isolated']
        finally:
            for command in commands:
                run(command + ['down', '--volumes', '--remove-orphans'])


# Disposable shared stores for the local-to-shared mode. The digests match docker-compose.integration.yml.
SHARED_STORE_IMAGES = {
    'valkey': 'valkey/valkey@sha256:9acdf6f0ae1771ea63c401e127054b2d1779227b9230dcfae37fa684610eaa4f',
    'postgres': 'postgres@sha256:f1c3376c26f2609ab9f29f71f824103fe2fcd8ee0346485cb6122a4f93df6f94',
    'minio': 'quay.io/minio/minio@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e',
}


def qualify_local_to_shared(image, project, compose, master_key, run):
    """Import a stopped local recipe backup into disposable shared stores through the image CLI."""
    network, recovery, state = project + '-shared', project + '-recovery', project + '-target-state'
    stores = {name: project + '-' + name for name in SHARED_STORE_IMAGES}
    operation, fencing = 'local-to-shared-import', 'recipe-local-service-stopped'
    with tempfile.TemporaryDirectory(prefix='starport-l2s-') as scratch:
        directory = Path(scratch)

        def private_file(name, values):
            path = directory / name
            path.write_text('\n'.join(values + ['']))
            path.chmod(0o600)
            return str(path)

        def wait(*command):
            for _ in range(60):
                try:
                    return run(command)
                except RuntimeError:
                    time.sleep(1)
            raise RuntimeError(f'{command[2]} did not become ready')

        def local(*arguments):
            return json.loads(run(compose + ['run', '--rm', '--no-deps', '--pull', 'never',
                                             '-v', recovery + ':/recovery', 'starport', *arguments]))

        try:
            secret = secrets.token_hex(16)
            minio = private_file('minio.env', ['MINIO_ROOT_USER=starport-recipe', 'MINIO_ROOT_PASSWORD=' + secret])
            run(['docker', 'network', 'create', network])
            run(['docker', 'run', '-d', '--name', stores['valkey'], '--network', network, SHARED_STORE_IMAGES['valkey']], timeout=300)
            run(['docker', 'run', '-d', '--name', stores['postgres'], '--network', network,
                 '-e', 'POSTGRES_HOST_AUTH_METHOD=trust', '-e', 'POSTGRES_DB=starport', SHARED_STORE_IMAGES['postgres']], timeout=300)
            run(['docker', 'run', '-d', '--name', stores['minio'], '--network', network, '--env-file', minio,
                 SHARED_STORE_IMAGES['minio'], 'server', '/data'], timeout=300)
            for volume in (recovery, state):
                run(['docker', 'volume', 'create', volume])
            # The distroless image has no shell. The helper creates private recovery directories for its user.
            run(['docker', 'run', '--rm', '-v', recovery + ':/recovery', '--entrypoint', '/bin/sh', SHARED_STORE_IMAGES['minio'], '-c',
                 'mkdir -m 700 /recovery/work /recovery/work/scratch && chown -R 65532:65532 /recovery && chmod 700 /recovery'])
            wait('docker', 'exec', stores['valkey'], 'valkey-cli', 'ping')
            wait('docker', 'exec', stores['postgres'], 'pg_isready', '-U', 'postgres', '-d', 'starport')
            bucket = 'recipe-' + secrets.token_hex(6)
            wait('docker', 'exec', stores['minio'], 'sh', '-c',
                 'mc alias set local http://127.0.0.1:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" && mc mb local/' + bucket)

            # The caller stopped the local service. That stop is the external fence for this fixture.
            local('backup', 'close', '--json')
            capture = local('backup', 'create', '--destination', '/recovery/work/backup', '--operation', 'local-to-shared-capture',
                            '--fencing-evidence', fencing, '--key-reference', 'recipe-master-key', '--json')
            references = capture['references']
            assert references['account_records'] >= 1 and references['file_records'] == 1, 'capture missed local records'
            deployment = capture['deployment_id']
            target_env = private_file('target.env', [
                'STARPORT_DEPLOYMENT_ID=' + deployment,
                'STARPORT_SECURITY_MASTER_KEY=' + master_key,
                'STARPORT_STORAGE_MODE=valkey',
                'STARPORT_STORAGE_VALKEY_URL=redis://' + stores['valkey'] + ':6379/0',
                # Test-only plaintext on a private network. Production fleets use TLS.
                'STARPORT_STORAGE_VALKEY_ALLOW_INSECURE=true',
                'STARPORT_STORAGE_SQL_MODE=postgres',
                'STARPORT_STORAGE_SQL_POSTGRES_URL=postgres://postgres@' + stores['postgres'] + ':5432/starport?sslmode=disable',
                'STARPORT_FILES_BACKEND=objectstore',
                'STARPORT_FILES_OBJECT_STORE_BUCKET=' + bucket,
                'STARPORT_FILES_OBJECT_STORE_REGION=us-east-1',
                'STARPORT_FILES_OBJECT_STORE_ENDPOINT=http://' + stores['minio'] + ':9000',
                'STARPORT_FILES_OBJECT_STORE_PREFIX=' + deployment + '/files/',
                'STARPORT_FILES_OBJECT_STORE_ACCESS_KEY_ID=starport-recipe',
                'STARPORT_FILES_OBJECT_STORE_SECRET_ACCESS_KEY=' + secret,
                'STARPORT_CATALOG_SOURCE=embedded',
                'STARPORT_CATALOG_NETWORK_MODE=offline',
                'STARPORT_CATALOG_ACQUISITION_ENABLED=false',
            ])
            target = ['docker', 'run', '--rm', '--pull', 'never', '--network', network, '--env-file', target_env,
                      '-v', recovery + ':/recovery', '-v', state + ':/var/lib/starport']

            def shared(*arguments):
                return json.loads(run(target + [image, *arguments], timeout=180))

            backup = ['--directory', '/recovery/work/backup', '--manifest-sha256', capture['manifest_sha256'], '--scratch', '/recovery/work/scratch']
            prepared = shared('backup', 'prepare', *backup, '--files-directory', '/recovery/work/prepared-files',
                              '--operation', operation, '--fencing-evidence', fencing, '--json')
            boundary = prepared['prepared']['boundary']
            assert prepared['references'] == references, 'prepared references differ from the capture'
            # Preparation closes the target one epoch above the captured boundary.
            assert boundary['Epoch'] == capture['recovery_epoch'] + 1 and not boundary['Open'], \
                f"prepared boundary epoch {boundary['Epoch']} open {boundary['Open']} after capture epoch {capture['recovery_epoch']}"
            observations = ['local_backup_prepares_into_shared_stores']

            info = run(['docker', 'exec', stores['valkey'], 'valkey-cli', 'INFO']).decode()
            fields = dict(line.split(':', 1) for line in info.splitlines() if ':' in line)
            expected = ['--expected-deployment', boundary['DeploymentID'], '--expected-recovery-epoch', str(boundary['Epoch']),
                        '--expected-recovery-evidence', boundary['Evidence']]
            if boundary['BackendID']:
                expected += ['--expected-recovery-backend', boundary['BackendID']]
            inspected = shared('backup', 'inspect-import', *backup, '--operation', operation, '--fencing-evidence', fencing,
                               '--destination', '/recovery/work/inspection', *expected,
                               '--kv-replay-sequence', '0', '--sql-replay-sequence', '0', '--blob-replay-sequence', '0',
                               '--valkey-incarnation', fields['run_id'] + ':' + fields['master_replid'], '--json')
            assert inspected['inspection']['references'] == references, 'imported references differ from the capture'
            assert inspected['target_sha256'], 'inspection returned no target digest'
            observations.append('shared_import_inspection_matches_capture')

            # Refusals match a known phrase only. The script never retains CLI output.
            assert run(target + ['--name', project + '-early-serve', image, 'serve'], timeout=60,
                       refusal=[b'requires deployment recovery before startup']), 'shared target started before activation'
            observations.append('shared_target_refuses_start_before_activation')
            assert run(target + [image, 'backup', 'prepare', *backup, '--files-directory', '/recovery/work/second-files',
                                 '--operation', 'local-to-shared-second', '--fencing-evidence', fencing, '--json'], timeout=180,
                       refusal=[b'relational import requires deployment recovery', b'KV database is not empty', b'storage is not fresh',
                                b'import target contains existing objects']), \
                'populated shared target accepted a second import'
            observations.append('populated_shared_target_refuses_second_import')
            return observations
        finally:
            run(['docker', 'rm', '-f', project + '-early-serve', *stores.values()], check=False)
            run(['docker', 'volume', 'rm', recovery, state], check=False)
            run(['docker', 'network', 'rm', network], check=False)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True, help='Previously built Starport image')
    parser.add_argument('--local-to-shared', action='store_true',
                        help='Import the populated local recipe into disposable shared stores instead of the persistence checks')
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    project = 'starport-recipe-' + secrets.token_hex(6)
    observations = []
    master_key = secrets.token_hex(32)
    with tempfile.TemporaryDirectory(prefix=project) as scratch:
        directory = Path(scratch)
        dotenv = directory / '.env'
        dotenv.write_text('\n'.join([
            'STARPORT_SECURITY_MASTER_KEY=' + master_key,
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

        def run(command, data=None, timeout=90, refusal=None, check=True):
            result = subprocess.run(command, input=data, capture_output=True,
                                    env=environment, timeout=timeout)
            if refusal is not None:
                return result.returncode != 0 and any(phrase in result.stderr for phrase in refusal)
            if result.returncode and check:
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
            if args.local_to_shared:
                run(compose + ['stop', 'starport'])
                observations.extend(qualify_local_to_shared(args.image, project, compose, master_key, run))
                # Rollback reopens the unchanged local source. Its closed boundary does not refuse local startup.
                base = start()
                verify_records()
                observations.append('closed_local_source_restarts_unchanged')
                print(json.dumps({'status': 'PASS', 'mode': 'local-to-shared', 'image': args.image,
                                  'image_id': run(['docker', 'image', 'inspect', args.image, '--format', '{{.Id}}']).decode().strip(),
                                  'observations': observations, 'source_sha256': {
                                      name: hashlib.sha256((root / name).read_bytes()).hexdigest()
                                      for name in ('Dockerfile', 'docker-compose.yml', 'scripts/test-storage-recipes.py')},
                                  'limits': 'Backup, prepare, inspection, and refusals only. Activation needs an independent history package that the image CLI does not produce. Plaintext Valkey on a private network.'}))
                return
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
            observations.extend(qualify_fleet_local_state(root, args.image, run))
            print(json.dumps({'status': 'PASS', 'image': args.image,
                              'image_id': run(['docker', 'image', 'inspect', args.image, '--format', '{{.Id}}']).decode().strip(),
                              'observations': observations, 'source_sha256': {
                                  name: hashlib.sha256((root / name).read_bytes()).hexdigest()
                                  for name in ('Dockerfile', 'docker-compose.yml', 'docker-compose.fleet.yml', 'scripts/test-storage-recipes.py')},
                              'limits': 'Fresh local persistence and replica credential files only. No complete fleet, host power-loss, or populated recovery qualification.'}))
        finally:
            # The unique project contains only this probe's disposable fixtures.
            run(compose + ['down', '--volumes', '--remove-orphans'])


if __name__ == '__main__':
    main()
