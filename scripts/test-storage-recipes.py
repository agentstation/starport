#!/usr/bin/env python3
"""Qualify fresh local Compose persistence with an explicitly built image."""

import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
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


# Each recipe declares its tmpfs scratch paths and its durable mounts.
READONLY_RECIPES = {
    'docker-compose.yml': ({'/tmp', '/var/lib/starport/cache'}, {
        '/var/lib/starport/config', '/var/lib/starport/data', '/var/lib/starport/state'}),
    'docker-compose.fleet.yml': ({'/tmp'}, {'/var/lib/starport'}),
}


def probe_archive(name):
    """Return a tar stream with one file that the image user owns."""
    payload = b'read-only recipe probe\n'
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode='w') as archive:
        entry = tarfile.TarInfo(name)
        entry.size, entry.uid, entry.gid, entry.mode = len(payload), 65532, 65532, 0o600
        archive.addfile(entry, io.BytesIO(payload))
    return buffer.getvalue()


def refused(run, command, data=None):
    """Report whether a command fails. The caller pairs it with a control."""
    try:
        run(command, data)
    except RuntimeError:
        return True
    return False


def wait_ready(base, attempts=150):
    """Wait for the readiness probe of one gateway."""
    for _ in range(attempts):
        try:
            with urllib.request.urlopen(base + '/health/ready', timeout=1) as response:
                if response.status == 200:
                    return True
        except (urllib.error.URLError, OSError):
            pass
        time.sleep(0.2)
    return False


def qualify_readonly_mounts(root, image, run):
    """Check that each Compose recipe writes only to its declared mounts."""
    observations = []
    with tempfile.TemporaryDirectory(prefix='starport-readonly-recipe-') as scratch:
        directory = Path(scratch)
        master_key = secrets.token_hex(32)
        (directory / '.env').write_text('\n'.join([
            'STARPORT_SECURITY_MASTER_KEY=' + master_key,
            'STARPORT_CATALOG_SOURCE=embedded',
            'STARPORT_CATALOG_NETWORK_MODE=offline',
            'STARPORT_CATALOG_ACQUISITION_ENABLED=false',
            'STARPORT_PORT=0', '',
        ]))
        # The fleet recipe needs its settings to create a container. It opens no store here.
        (directory / '.env.fleet').write_text('\n'.join([
            'STARPORT_DEPLOYMENT_ID=recipe-fixture',
            'STARPORT_SECURITY_MASTER_KEY=' + master_key,
            'STARPORT_STORAGE_VALKEY_URL=valkeys://valkey.example.test:6379/0',
            'STARPORT_STORAGE_SQL_POSTGRES_URL=postgres://fixture@postgres.example.test/starport?sslmode=verify-full',
            'STARPORT_FILES_OBJECT_STORE_BUCKET=recipe-fixture',
            'STARPORT_FILES_OBJECT_STORE_REGION=us-east-1', '',
        ]))
        for name in ('.env', '.env.fleet'):
            (directory / name).chmod(0o600)
        image_override = directory / 'image.json'
        image_override.write_text(json.dumps({'services': {'starport': {'image': image}}}))
        commands = []

        def compose(recipe, *overrides):
            dotenv = '.env.fleet' if recipe == 'docker-compose.fleet.yml' else '.env'
            command = ['docker', 'compose', '--project-name', 'starport-readonly-' + secrets.token_hex(6),
                       '--project-directory', scratch, '--env-file', str(directory / dotenv),
                       '-f', str(root / recipe), '-f', str(image_override)]
            for override in overrides:
                command += ['-f', str(override)]
            commands.append(command)
            return command

        try:
            for recipe, (scratch_paths, mounts) in READONLY_RECIPES.items():
                command = compose(recipe)
                run(command + ['create', '--no-build', 'starport'])
                container = run(command + ['ps', '-a', '-q', 'starport']).decode().strip()
                details = json.loads(run(['docker', 'container', 'inspect', container]))[0]
                assert details['HostConfig']['ReadonlyRootfs'], f'{recipe} root file system is writable'
                assert set(details['HostConfig'].get('Tmpfs') or {}) == scratch_paths, f'{recipe} scratch paths changed'
                assert {mount['Destination'] for mount in details['Mounts']} == mounts, f'{recipe} durable mounts changed'
                declared = sorted(mounts)[0]
                assert not refused(run, ['docker', 'cp', '-a', '-', f'{container}:{declared}'],
                                   probe_archive('readonly-probe')), f'{recipe} refused a write to {declared}'
                assert refused(run, ['docker', 'cp', '-a', '-', f'{container}:/usr/local'],
                               probe_archive('readonly-probe')), f'{recipe} accepted a write outside its mounts'
            observations += ['readonly_root_filesystem', 'declared_writable_mounts_only',
                             'scratch_tmpfs_only', 'write_outside_mounts_refused']

            # One deployment initializes and starts with a moved data directory. Only the mount differs.
            command = compose('docker-compose.yml')
            outcomes = {}
            for path in ('/var/lib/starport/undeclared', '/var/lib/starport/state/relocated'):
                override = directory / ('relocated-' + secrets.token_hex(4) + '.json')
                override.write_text(json.dumps({'services': {'starport': {
                    'restart': 'no', 'environment': {'STARPORT_DATA_DIR': path}}}}))
                relocated = command + ['-f', str(override)]
                one_off = relocated + ['run', '--rm', '--no-deps', '--pull', 'never', 'starport']
                initialized = not refused(run, one_off + ['init', '--configured-storage', '--name', 'readonly-admin', '--json'])
                initialized = not refused(run, one_off + ['auth', 'rotate']) and initialized
                run(relocated + ['up', '-d', '--no-build', 'starport'])
                container = run(relocated + ['ps', '-a', '-q', 'starport']).decode().strip()
                try:
                    mapping = run(relocated + ['port', 'starport', '8080']).decode().strip()
                    ready = wait_ready('http://' + mapping, attempts=100)
                except RuntimeError:
                    ready = False
                state = json.loads(run(['docker', 'container', 'inspect', container]))[0]['State']
                outcomes[path] = initialized, ready, state
                run(relocated + ['down'])
            initialized, ready, state = outcomes['/var/lib/starport/undeclared']
            assert not initialized and not ready and not state['Running'] and state['ExitCode'] != 0, \
                'an undeclared write path did not stop initialization and start'
            initialized, ready, state = outcomes['/var/lib/starport/state/relocated']
            assert initialized and ready and state['Running'], 'a declared write path did not start'
            observations += ['undeclared_write_path_stops_start', 'declared_write_path_starts']
            return observations
        finally:
            for command in commands:
                run(command + ['down', '--volumes', '--remove-orphans'])


FLEET_PLAINTEXT_OVERRIDE = 'scripts/testdata/docker-compose.fleet.plaintext-fixture.yml'
FLEET_LIMITS = ('Two replicas against plaintext fixtures on one Docker host and one Valkey process. '
                'Restore writes a final-only history package for a controlled stop and activates the target. '
                'No gateway starts on the restored target, and the history records no post-backup activity.')
# Each name resolves to a closed loopback port, so no GitHub request leaves the container.
GITHUB_HOSTS = ('github.com', 'api.github.com', 'codeload.github.com',
                'objects.githubusercontent.com', 'raw.githubusercontent.com')
FLEET_SOURCE_FILE = '/var/lib/starport/source/catalog.json'


def container_address(address):
    """Return a fixture URL that a container reaches through the Docker host."""
    parsed = urllib.parse.urlsplit(address)
    if parsed.hostname not in ('127.0.0.1', 'localhost'):
        return address
    userinfo, _, hostport = parsed.netloc.rpartition('@')
    port = hostport.rpartition(':')[2] if ':' in hostport else ''
    netloc = (userinfo + '@' if userinfo else '') + 'host.docker.internal' + (':' + port if port else '')
    return urllib.parse.urlunsplit(parsed._replace(netloc=netloc))


def private_archive(directories, files):
    """Return a tar stream of private directories and files that the image user owns."""
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode='w') as archive:
        for name in directories:
            directory = tarfile.TarInfo(name)
            directory.type, directory.uid, directory.gid, directory.mode = tarfile.DIRTYPE, 65532, 65532, 0o700
            archive.addfile(directory)
        for name, payload in files.items():
            entry = tarfile.TarInfo(name)
            entry.size, entry.uid, entry.gid, entry.mode = len(payload), 65532, 65532, 0o600
            archive.addfile(entry, io.BytesIO(payload))
    return buffer.getvalue()


def source_archive(payload):
    """Return a tar stream that places a catalog file at FLEET_SOURCE_FILE."""
    return private_archive(['source'], {'source/catalog.json': payload})


def fleet_api(base, key, path, data=None, content_type='application/json', attempts=30):
    """Send one authenticated request. Retry while the catalog is not ready."""
    headers = {'Authorization': 'Bearer ' + key}
    if data is not None:
        headers['Content-Type'] = content_type
    for _ in range(attempts):
        req = urllib.request.Request(base + path, data=data, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=10) as response:
                return response.read()
        except urllib.error.HTTPError as error:
            if error.code != 503 or data is not None:
                # Response bodies can echo request values. Report the status only.
                raise RuntimeError(f'fleet request {path} failed with status {error.code}') from None
        time.sleep(1)
    raise RuntimeError(f'fleet request {path} stayed unavailable')


def source_kinds(value):
    """Return every source_kind value in a catalog status document."""
    if isinstance(value, dict):
        found = {value['source_kind']} if isinstance(value.get('source_kind'), str) and value['source_kind'] else set()
        for item in value.values():
            found |= source_kinds(item)
        return found
    if isinstance(value, list):
        return set().union(*(source_kinds(item) for item in value)) if value else set()
    return set()


def qualify_fleet_records(root, image, inputs, run):
    """Run two fleet gateways against isolated shared stores and restore their capture."""
    observations = []
    projects = ['starport-fleet-' + name + '-' + secrets.token_hex(4) for name in ('a', 'b', 'restore')]
    master_key = secrets.token_hex(32)
    with tempfile.TemporaryDirectory(prefix='starport-fleet-records-') as scratch:
        directory = Path(scratch)

        def write_env(project, target, source='embedded'):
            values = {
                'STARPORT_DEPLOYMENT_ID': inputs['deployment_id'],
                'STARPORT_SECURITY_MASTER_KEY': master_key,
                'STARPORT_STORAGE_VALKEY_URL': container_address(target['valkey_url']),
                'STARPORT_STORAGE_SQL_POSTGRES_URL': container_address(target['postgres_url']),
                'STARPORT_FILES_OBJECT_STORE_BUCKET': target['bucket'],
                'STARPORT_FILES_OBJECT_STORE_REGION': inputs['object_store_region'],
                'STARPORT_FILES_OBJECT_STORE_ENDPOINT': container_address(inputs['object_store_endpoint']),
                'STARPORT_FILES_OBJECT_STORE_ACCESS_KEY_ID': inputs['object_store_access_key_id'],
                'STARPORT_FILES_OBJECT_STORE_SECRET_ACCESS_KEY': inputs['object_store_secret_access_key'],
                'STARPORT_CATALOG_SOURCE': source,
                'STARPORT_CATALOG_NETWORK_MODE': 'offline',
                'STARPORT_CATALOG_ACQUISITION_ENABLED': 'false',
            }
            if source == 'file':
                values['STARPORT_CATALOG_SOURCE_URL'] = FLEET_SOURCE_FILE
            assert not any("'" in value or '\n' in value for value in values.values()), 'fixture value needs quoting'
            path = directory / project / '.env.fleet'
            path.parent.mkdir(mode=0o700, exist_ok=True)
            path.write_text(''.join(f"{name}='{value}'\n" for name, value in values.items()))
            path.chmod(0o600)

        image_override = directory / 'image.json'
        image_override.write_text(json.dumps({'services': {'starport': {
            'image': image, 'extra_hosts': [host + ':127.0.0.1' for host in GITHUB_HOSTS]}}}))
        backup_volume = projects[0] + '_recipe-backup'
        # The image home directory belongs to the image user with mode 0700, and a new volume keeps it.
        capture_override = directory / 'capture.json'
        capture_override.write_text(json.dumps({
            'services': {'starport': {'volumes': ['recipe-backup:/home/nonroot']}},
            'volumes': {'recipe-backup': {}}}))
        restore_override = directory / 'restore.json'
        restore_override.write_text(json.dumps({
            'services': {'starport': {'volumes': ['recipe-backup:/home/nonroot']}},
            'volumes': {'recipe-backup': {'external': True, 'name': backup_volume}}}))
        write_env(projects[0], inputs['source'])
        write_env(projects[1], inputs['source'])
        write_env(projects[2], inputs['restore'])
        extra = {0: [capture_override], 1: [], 2: [restore_override]}

        def compose(index, *overrides):
            project = directory / projects[index]
            command = ['docker', 'compose', '--project-name', projects[index],
                       '--project-directory', str(project), '--env-file', str(project / '.env.fleet'),
                       '-f', str(root / 'docker-compose.fleet.yml'), '-f', str(root / FLEET_PLAINTEXT_OVERRIDE),
                       '-f', str(image_override)]
            for override in overrides:
                command += ['-f', str(override)]
            return command

        def cli(index, *args, overrides=(), timeout=180):
            return run(compose(index, *overrides) + ['run', '--rm', '--no-deps', '--pull', 'never', 'starport', *args],
                       timeout=timeout)

        def container(index):
            return run(compose(index) + ['ps', '-a', '-q', 'starport']).decode().strip()

        def dump_replica(index):
            # Cleanup removes the containers, so a readiness failure keeps the
            # container state and the gateway log on stderr for the caller.
            # A credential inside a URL is redacted before the copy.
            for tail in (['ps', '-a'], ['logs', '--no-color', '--tail', '200', 'starport']):
                result = subprocess.run(compose(index) + tail, capture_output=True, timeout=60)
                text = re.sub(r'://[^@\s/]+@', '://<redacted>@', result.stdout.decode(errors='replace'))
                print(f'fleet replica {index} {tail[0]}:\n{text}', file=sys.stderr)

        def start(index):
            run(compose(index) + ['up', '-d', '--no-build', 'starport'], timeout=180)
            base = 'http://' + run(compose(index) + ['port', 'starport', '8080']).decode().strip()
            if not wait_ready(base, attempts=600):
                dump_replica(index)
                raise AssertionError(f'fleet replica {index} did not become ready')
            return base

        def check_records(base):
            stored = json.loads(fleet_api(base, admin, '/api/v1/admin/keys/' + created_key['id']))
            assert created_key['id'] in json.dumps(stored), 'gateway key record missing'
            fleet_api(base, created_key['key'], '/v1/models')
            listed = json.loads(fleet_api(base, admin, '/api/v1/providers/openai/credentials'))
            assert [item['id'] for item in listed['credentials']] == [credential['id']], 'provider credential missing'
            assert fleet_api(base, admin, '/v1/files/' + file_id + '/content') == payload, 'file bytes changed'

        def check_source(bases, kind):
            for base in bases:
                status = json.loads(fleet_api(base, admin, '/api/v1/admin/catalog/status'))
                assert source_kinds(status) == {kind}, f'catalog source is not {kind}'
            for index in (0, 1):
                hosts = json.loads(run(['docker', 'container', 'inspect', container(index)]))[0]['HostConfig']['ExtraHosts']
                assert {host + ':127.0.0.1' for host in GITHUB_HOSTS} <= set(hosts), 'a GitHub route exists'

        try:
            cli(0, 'fleet', 'init', '--operation', 'recipe-fleet-init', '--evidence', 'recipe-fleet-harness', '--json')
            cli(0, 'config', 'init', '--shared', '--yes', '--json')
            admin = json.loads(cli(0, 'init', '--configured-storage', '--name', 'recipe-admin', '--json'))['api_key']
            for index in (0, 1):
                cli(index, 'auth', 'rotate')  # Discard the one-time credential output.
            bases = [start(0), start(1)]

            created_key = json.loads(fleet_api(bases[0], admin, '/api/v1/admin/keys', json.dumps({
                'name': 'recipe-record', 'scopes': ['models:read']}).encode()))['key']
            credential = json.loads(fleet_api(bases[0], admin, '/api/v1/providers/openai/credentials', json.dumps({
                'label': 'recipe-record', 'credentials': {'api-key': 'sk-recipe-' + secrets.token_hex(16)}}).encode()))
            payload = b'fleet recipe retains these uploaded bytes\n'
            boundary = 'starport-fleet-upload'
            body = (f'--{boundary}\r\nContent-Disposition: form-data; name="purpose"\r\n\r\nuser_data\r\n'
                    f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="recipe.txt"\r\n'
                    'Content-Type: text/plain\r\n\r\n').encode() + payload + f'\r\n--{boundary}--\r\n'.encode()
            file_id = json.loads(fleet_api(bases[0], admin, '/v1/files', body,
                                           'multipart/form-data; boundary=' + boundary))['id']
            check_records(bases[0])
            observations.append('fleet_records_created_through_one_replica')
            check_records(bases[1])
            observations.append('fleet_records_read_through_other_replica')

            before = [container(0), container(1)]
            for index in (0, 1):
                run(compose(index) + ['rm', '--stop', '--force', 'starport'], timeout=180)
            bases = [start(0), start(1)]
            after = [container(0), container(1)]
            assert all(after) and not set(before) & set(after), 'a gateway container was not replaced'
            observations.append('fleet_gateway_containers_replaced')
            for base in reversed(bases):
                check_records(base)
            observations.append('fleet_records_survive_container_recreation')
            check_source(bases, 'embedded')
            observations.append('fleet_catalog_source_embedded_without_github')

            for index in (0, 1):
                run(compose(index) + ['stop', 'starport'], timeout=180)
            baseline = run(['docker', 'cp', container(0) + ':/var/lib/starport/data/catalog/baseline/.', '-'], timeout=180)
            with tarfile.open(fileobj=io.BytesIO(baseline)) as archive:
                members = [member for member in archive.getmembers()
                           if member.isfile() and member.name.endswith('/catalog.json')]
                assert len(members) == 1, 'the packaged baseline has no single catalog file'
                catalog = archive.extractfile(members[0]).read()
            for index in (0, 1):
                run(['docker', 'cp', '-a', '-', container(index) + ':/var/lib/starport'], source_archive(catalog))
            local = json.loads(cli(0, 'config', 'migrate', '--to', 'local', '--yes', '--json'))['revision']
            assert local['authority'] == 'local', 'configuration did not move to local management'
            for index in (0, 1):
                write_env(projects[index], inputs['source'], source='file')
            shared = json.loads(cli(0, 'config', 'migrate', '--to', 'shared', '--json'))['revision']
            assert shared['authority'] == 'shared' and shared['sequence'] == local['sequence'] + 1, \
                'configuration did not return to shared management'
            observations.append('fleet_configuration_migrate_round_trip')
            bases = [start(0), start(1)]
            check_source(bases, 'file')
            for base in bases:
                check_records(base)
            observations.append('fleet_catalog_source_file_without_github')

            for index in (0, 1):
                run(compose(index) + ['stop', 'starport'], timeout=180)
            cli(0, 'backup', 'close', '--json')
            capture = ('--operation', 'recipe-backup-' + secrets.token_hex(4),
                       '--fencing-evidence', 'recipe-fleet-stopped')
            created = json.loads(cli(0, 'backup', 'create', '--destination', '/home/nonroot/bundle', *capture,
                                     '--key-reference', 'recipe-master-key', '--json',
                                     overrides=extra[0], timeout=600))
            verified = json.loads(cli(0, 'backup', 'verify', '--directory', '/home/nonroot/bundle',
                                      '--manifest-sha256', created['manifest_sha256'], '--json',
                                      overrides=extra[0], timeout=600))
            references = verified['references']
            assert verified['deployment_id'] == inputs['deployment_id'], 'backup names another deployment'
            assert references['credential_values'] >= 1 and references['file_records'] >= 1, \
                'backup lost a credential or a file record'
            assert references['gateway_keys']['keys'] >= 2, 'backup lost a gateway key'
            observations.append('fleet_backup_verifies_records')

            restore = ('--operation', 'recipe-restore-' + secrets.token_hex(4),
                       '--fencing-evidence', 'recipe-fleet-stopped')
            prepared = json.loads(cli(2, 'backup', 'prepare', '--directory', '/home/nonroot/bundle',
                                      '--manifest-sha256', created['manifest_sha256'],
                                      '--files-directory', '/home/nonroot/prepared', *restore, '--json',
                                      overrides=extra[2], timeout=600))
            assert prepared['references'] == references, 'preparation changed the record references'
            observations.append('fleet_restore_prepares_fresh_targets')
            closed = prepared['prepared']['boundary']
            expected = ['--expected-deployment', closed['DeploymentID'],
                        '--expected-recovery-epoch', str(closed['Epoch']),
                        '--expected-recovery-evidence', closed['Evidence']]
            if closed['BackendID']:
                expected += ['--expected-recovery-backend', closed['BackendID']]
            inspected = json.loads(cli(2, 'backup', 'inspect-import', '--directory', '/home/nonroot/bundle',
                                       '--manifest-sha256', created['manifest_sha256'], *restore,
                                       '--destination', '/home/nonroot/inspection', *expected,
                                       '--kv-replay-sequence', '0', '--sql-replay-sequence', '0',
                                       '--blob-replay-sequence', '0',
                                       '--valkey-incarnation', inputs['valkey_incarnation'], '--json',
                                       overrides=extra[2], timeout=600))
            assert inspected['inspection']['references'] == references, 'import inspection changed the references'
            observations.append('fleet_restore_import_inspected')

            def place(archive):
                # The image has no shell. A created container copies private entries into the backup volume.
                run(compose(2, *extra[2]) + ['create', '--no-build', '--pull', 'never', 'starport'], timeout=180)
                try:
                    run(['docker', 'cp', '-a', '-', container(2) + ':/home/nonroot'], archive)
                finally:
                    run(compose(2, *extra[2]) + ['rm', '--force', 'starport'], timeout=180)

            # The package directories stay outside the bundle, the scratch directory, and /var/lib/starport.
            place(private_archive(['history', 'journal', 'activation', 'scratch', 'evidence'],
                                  {'evidence/fleet-stopped.log': b'recipe fleet gateways stopped before the backup\n'}))
            bundle = {'Directory': '/home/nonroot/bundle', 'ManifestSHA256': created['manifest_sha256'],
                      'ScratchDirectory': '/home/nonroot/scratch'}
            attestation = {'operator': 'recipe-operator', 'reference': 'recipe-fleet-stopped', 'writers_fenced': True,
                           'admitted_work_accounted': True, 'complete_interval': True}
            written = json.loads(cli(2, 'backup', 'write-history', '--directory', bundle['Directory'],
                                     '--manifest-sha256', bundle['ManifestSHA256'], '--scratch', bundle['ScratchDirectory'],
                                     *restore, '--history-directory', '/home/nonroot/history',
                                     '--expected-target-sha256', inspected['target_sha256'],
                                     '--valkey-incarnation', inputs['valkey_incarnation'],
                                     '--mode', 'planned_migration', '--disposition', 'replay_complete',
                                     '--through', time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
                                     '--end-reference', 'recipe-fleet-stopped',
                                     # The stopped source used no epoch above the captured boundary.
                                     '--highest-epoch', str(created['recovery_epoch']),
                                     '--epoch-reference', 'recipe-backup-boundary', '--epoch-operator', 'recipe-operator',
                                     '--evidence-file', 'fleet-stopped=/home/nonroot/evidence/fleet-stopped.log=recipe-fleet-stopped',
                                     '--epoch-evidence', 'fleet-stopped', '--operator', attestation['operator'],
                                     '--attestation-reference', attestation['reference'], '--writers-fenced=true',
                                     '--admitted-work-accounted=true', '--complete-interval=true', '--json',
                                     overrides=extra[2], timeout=600))
            assert written['target_sha256'] == inspected['target_sha256'], 'history binds another target'
            assert written['declared_steps'] == 2, 'history is not final-only'
            observations.append('fleet_restore_history_written')

            operation = {'ID': restore[1], 'FencingEvidence': restore[3]}
            activation = {
                'Prepare': {**bundle, 'Operation': operation, 'FilesDirectory': '/home/nonroot/prepared'},
                'History': {**bundle, 'Operation': operation, 'HistoryDirectory': '/home/nonroot/history',
                            'HistorySHA256': written['history_sha256'], 'ExpectedTargetSHA256': written['target_sha256'],
                            'JournalDirectory': '/home/nonroot/journal', 'ValkeyIncarnation': inputs['valkey_incarnation'],
                            'Attestation': attestation},
                'ActivationDirectory': '/home/nonroot/activation', 'PreserveTargetWorkspace': True,
            }
            place(private_archive(['request'], {'request/activation.json': json.dumps(activation).encode()}))
            activated = json.loads(cli(2, 'backup', 'activate', '--request-file', '/home/nonroot/request/activation.json',
                                       '--json', overrides=extra[2], timeout=900))
            assert activated['historically_complete'] and activated['current_admission_valid'] \
                and not activated['restricted'], 'activation left the restored deployment restricted'
            observations.append('fleet_restore_activated')
            return observations
        finally:
            # The restore project uses the capture volume, so it goes first.
            for index in (2, 1, 0):
                try:
                    run(compose(index, *extra[index]) + ['down', '--volumes', '--remove-orphans'], timeout=180)
                except RuntimeError:
                    pass


def report_mode(root, args, run):
    """Run one additional mode and print its evidence."""
    sources = ['Dockerfile', 'docker-compose.yml', 'docker-compose.fleet.yml', 'scripts/test-storage-recipes.py']
    if args.mode == 'readonly':
        observations = qualify_readonly_mounts(root, args.image, run)
        limits = 'Container configuration and one relocated data directory. No host file system or kernel policy qualification.'
    else:
        inputs = json.loads(Path(args.fleet_inputs).read_text())
        observations = qualify_fleet_records(root, args.image, inputs, run)
        sources.append(FLEET_PLAINTEXT_OVERRIDE)
        limits = FLEET_LIMITS
    print(json.dumps({'status': 'PASS', 'mode': args.mode, 'image': args.image,
                      'image_id': run(['docker', 'image', 'inspect', args.image, '--format', '{{.Id}}']).decode().strip(),
                      'observations': observations, 'source_sha256': {
                          name: hashlib.sha256((root / name).read_bytes()).hexdigest() for name in sources},
                      'limits': limits}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True, help='Previously built Starport image')
    parser.add_argument('--mode', choices=('local', 'readonly', 'fleet'), default='local',
                        help='local qualifies the fresh recipes; readonly checks the declared mounts; '
                             'fleet runs two gateways against the stores in --fleet-inputs')
    parser.add_argument('--fleet-inputs', help='Private JSON file that names isolated shared fixture stores')
    parser.add_argument('--local-to-shared', action='store_true',
                        help='Import the populated local recipe into disposable shared stores instead of the persistence checks')
    args = parser.parse_args()
    if args.mode == 'fleet' and not args.fleet_inputs:
        parser.error('--mode fleet requires --fleet-inputs')
    if args.local_to_shared and args.mode != 'local':
        parser.error('--local-to-shared applies to --mode local only')
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

        if args.mode != 'local':
            report_mode(root, args, run)
            return
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
                                  'limits': 'Backup, prepare, inspection, and refusals only. The fleet mode qualifies write-history and activation. Plaintext Valkey on a private network.'}))
                return
            run(compose + ['down'])
            base = start()
            verify_records()
            observations.append('container_recreation_preserves_records')
            run(compose + ['stop', 'starport'])
            container = run(compose + ['ps', '-a', '-q', 'starport']).decode().strip()
            # The read-only root refuses a copy outside the declared volumes.
            mounts = ('config', 'data', 'state')
            backups = {mount: run(['docker', 'cp', f'{container}:/var/lib/starport/{mount}/.', '-']) for mount in mounts}
            run(compose + ['down', '--volumes'])
            run(compose + ['create', '--no-build', 'starport'])
            container = run(compose + ['ps', '-a', '-q', 'starport']).decode().strip()
            for mount, backup in backups.items():
                run(['docker', 'cp', '-a', '-', f'{container}:/var/lib/starport/{mount}'], backup)
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
