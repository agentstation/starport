#!/usr/bin/env python3
"""Test the native release verifier's integrity and isolation contracts."""

import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import warnings
import zipfile

spec = importlib.util.spec_from_file_location('native_release', Path(__file__).with_name('verify-native-release.py'))
verifier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verifier)


class NativeReleaseTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)

    def test_startup_failure_keeps_the_cause_without_generated_credentials(self):
        home, temporary = self.root / 'home', self.root / 'temporary'
        home.mkdir()
        temporary.mkdir()
        environment = verifier.isolated_environment(home, temporary, 3000)
        script = "print('Gateway API key (shown once): STARPORT_fixture_secret'); print('storage failed for STARPORT_fixture_secret http://127.0.0.1/launch?lt=private-ticket'); raise SystemExit(23)"
        popen = verifier.subprocess.Popen
        def start(_arguments, **options):
            return popen([sys.executable, '-c', script], **options)
        report = {}
        with patch.object(verifier.subprocess, 'Popen', side_effect=start):
            with self.assertRaisesRegex(RuntimeError, 'storage failed') as raised:
                verifier.verify_development(Path(sys.executable), environment, home, 3000, report)
        self.assertNotIn('STARPORT_fixture_secret', str(raised.exception))
        self.assertNotIn('STARPORT_fixture_secret', json.dumps(report))
        self.assertNotIn('private-ticket', str(raised.exception))
        self.assertNotIn('private-ticket', json.dumps(report))
        self.assertEqual(report['shutdown_exit_code'], 23)

    def test_native_targets(self):
        for system, machine, expected in [('Windows', 'AMD64', ('windows', 'x86_64')),
                                          ('Windows', 'ARM64', ('windows', 'arm64')),
                                          ('Linux', 'x86_64', ('linux', 'x86_64')),
                                          ('Linux', 'aarch64', ('linux', 'arm64')),
                                          ('Darwin', 'arm64', ('darwin', 'arm64'))]:
            with self.subTest(system=system, machine=machine), patch.object(verifier.platform, 'system', return_value=system), patch.object(verifier.platform, 'machine', return_value=machine):
                self.assertEqual(verifier.native_target(), expected)
        with patch.object(verifier.platform, 'machine', return_value='unknown'), self.assertRaises(ValueError):
            verifier.native_target()

    def test_intel_macos_is_unsupported(self):
        with patch.object(verifier.platform, 'system', return_value='Darwin'), patch.object(verifier.platform, 'machine', return_value='x86_64'):
            with self.assertRaisesRegex(ValueError, 'Apple silicon'):
                verifier.native_target()

    def test_exact_release_tag(self):
        self.assertEqual(verifier.archive_name('v1.2.0', 'windows', 'arm64'), 'starport_1.2.0_windows_arm64.zip')
        for tag in ('main', '../v1.2.0', 'v1.2.0; exit 0', '--help', 'v1.2.0\n'):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                verifier.archive_name(tag, 'windows', 'arm64')

    def test_checksum_requires_both_independent_records(self):
        archive = self.root / 'release.zip'
        archive.write_bytes(b'archive content')
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        checksums = self.root / 'checksums.txt'
        line = digest + '  release.zip\n'
        release = {'assets': [{'name': 'release.zip', 'digest': 'sha256:' + digest}]}
        checksums.write_text(line)
        self.assertEqual(verifier.verify_checksum(archive, checksums, release), digest)
        for text in ('', line + line, '0' * 64 + '  release.zip\n'):
            checksums.write_text(text)
            with self.subTest(checksums=text), self.assertRaises(ValueError):
                verifier.verify_checksum(archive, checksums, release)
        checksums.write_text(line)
        for assets in ([], [{'name': 'release.zip'}], release['assets'] * 2,
                       [{'name': 'release.zip', 'digest': 'sha256:' + '0' * 64}]):
            with self.subTest(assets=assets), self.assertRaises(ValueError):
                verifier.verify_checksum(archive, checksums, {'assets': assets})

    def make_archive(self, kind, extra=None, link=False):
        executable = 'starport.exe' if kind == 'zip' else 'starport'
        names = sorted(verifier.COMMON_FILES | {executable})
        if extra:
            names.append(extra)
        archive = self.root / ('release.zip' if kind == 'zip' else 'release.tar.gz')
        with warnings.catch_warnings():
            warnings.simplefilter('ignore', UserWarning)
            if kind == 'zip':
                with zipfile.ZipFile(archive, 'w') as output:
                    for name in names:
                        entry = zipfile.ZipInfo(name)
                        entry.create_system = 3
                        entry.external_attr = ((stat.S_IFLNK if link and name == executable else stat.S_IFREG) | 0o755) << 16
                        output.writestr(entry, b'target')
            else:
                with tarfile.open(archive, 'w:gz') as output:
                    for name in names:
                        entry = tarfile.TarInfo(name)
                        entry.size = 6
                        if link and name == executable:
                            entry.type = tarfile.SYMTYPE
                            entry.linkname = 'target'
                            entry.size = 0
                        output.addfile(entry, io.BytesIO(b'target'))
        return archive, executable

    def test_exact_archives_extract(self):
        for kind in ('zip', 'tar'):
            with self.subTest(kind=kind):
                archive, executable = self.make_archive(kind)
                destination = self.root / kind
                verifier.extract_archive(archive, destination, executable)
                self.assertEqual({path.relative_to(destination).as_posix() for path in destination.rglob('*') if path.is_file()}, verifier.COMMON_FILES | {executable})

    def test_archives_refuse_extra_duplicate_traversal_and_links(self):
        for kind in ('zip', 'tar'):
            for extra, link in [('unexpected', False), ('LICENSE', False), ('../escape', False), (None, True)]:
                with self.subTest(kind=kind, extra=extra, link=link):
                    archive, executable = self.make_archive(kind, extra=extra, link=link)
                    with self.assertRaises(ValueError):
                        verifier.extract_archive(archive, self.root / 'refused', executable)
                    self.assertFalse((self.root / 'refused').exists())

    def test_child_environment_has_no_credentials_or_external_config(self):
        home, temporary = self.root / 'home', self.root / 'temporary'
        supplied = {'PATH': 'native-path', 'SystemRoot': 'system', 'OPENAI_API_KEY': 'test-secret',
                    'GH_TOKEN': 'test-token', 'STARPORT_CONFIG_FILE': '/outside', 'CATALOG_STATE_DIR': '/outside',
                    'HOME': '/outside', 'APPDATA': '/outside'}
        with patch.dict(os.environ, supplied, clear=True):
            environment = verifier.isolated_environment(home, temporary, 3000)
        self.assertFalse({'OPENAI_API_KEY', 'GH_TOKEN', 'STARPORT_CONFIG_FILE', 'CATALOG_STATE_DIR'} & environment.keys())
        for name in ('HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'XDG_CONFIG_HOME', 'XDG_STATE_HOME'):
            self.assertTrue(Path(environment[name]).is_relative_to(home))
        self.assertEqual(environment['TEMP'], str(temporary))
        self.assertEqual(environment['SystemRoot'], 'system')

    def test_catalog_requires_readme_model(self):
        verifier.verify_catalog_payload('search', {'data': [{'id': 'openai/gpt-4o-mini'}]})
        verifier.verify_catalog_payload('show', {'id': 'openai/gpt-4o-mini', 'object': 'model'})
        for command, payload in [('search', {'data': []}), ('search', {'data': [{'id': 'other'}]}),
                                 ('show', {'id': 'other', 'object': 'model'}), ('show', {'error': 'failure'}),
                                 ('show', []), ('unknown', {})]:
            with self.subTest(command=command, payload=payload), self.assertRaises(ValueError):
                verifier.verify_catalog_payload(command, payload)

    def test_native_mismatch_refuses_before_download(self):
        output = self.root / 'result.json'
        arguments = ['verify', '--tag', 'v1.2.0', '--expected-os', 'windows', '--expected-arch', 'arm64', '--output', str(output)]
        with patch.object(sys, 'argv', arguments), patch.object(verifier, 'native_target', return_value=('linux', 'x86_64')), patch.object(verifier.platform, 'platform', return_value='test-platform'), patch.object(verifier.subprocess, 'run') as run, patch('builtins.print'):
            self.assertEqual(verifier.main(), 1)
        run.assert_not_called()
        self.assertEqual(json.loads(output.read_text())['verdict'], 'FAIL')

    def test_exact_candidate_version(self):
        self.assertEqual(verifier.candidate_archive_name('1.2.2-next', 'windows', 'arm64'), 'starport_1.2.2-next_windows_arm64.zip')
        self.assertEqual(verifier.candidate_archive_name('1.2.2-next', 'linux', 'x86_64'), 'starport_1.2.2-next_linux_x86_64.tar.gz')
        for version in ('1.2.2', 'v1.2.2-next', '1.2.2-rc.1', '../1.2.2-next', '1.2.2-next\n', '--help'):
            with self.subTest(version=version), self.assertRaises(ValueError):
                verifier.candidate_archive_name(version, 'windows', 'arm64')

    def test_candidate_checksum_requires_one_exact_entry(self):
        archive = self.root / 'candidate.zip'
        archive.write_bytes(b'archive content')
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        checksums = self.root / 'checksums.txt'
        checksums.write_text(digest + '  candidate.zip\n')
        self.assertEqual(verifier.verify_checksum_entry(archive, checksums), digest)
        for text in ('', (digest + '  candidate.zip\n') * 2, '0' * 64 + '  candidate.zip\n', digest + '  other.zip\n'):
            checksums.write_text(text)
            with self.subTest(checksums=text), self.assertRaises(ValueError):
                verifier.verify_checksum_entry(archive, checksums)

    def test_catalog_generation_requires_an_identity(self):
        base = 'http://127.0.0.1:3000'
        with patch.object(verifier, 'request_json', return_value=(200, {'generation_id': 'gen-1.a:b', 'models': []})) as request:
            self.assertEqual(verifier.catalog_generation(base, 'key'), 'gen-1.a:b')
        request.assert_called_once_with(base + '/api/v1/catalog/discovery', 'key')
        for response in [(200, {}), (200, {'generation_id': ''}), (200, {'generation_id': 7}),
                         (200, {'generation_id': 'a b'}), (200, []), (203, {'generation_id': 'gen'})]:
            with self.subTest(response=response), patch.object(verifier, 'request_json', return_value=response), self.assertRaises(ValueError):
                verifier.catalog_generation(base, 'key')

    def run_candidate(self, archive_name='starport_1.2.2-next_windows_x86_64.zip', version='starport version 1.2.2-next', extra=()):
        archive, _ = self.make_archive('zip')
        candidate = self.root / 'snapshot' / archive_name
        candidate.parent.mkdir(exist_ok=True)
        archive.rename(candidate)
        checksums = self.root / 'snapshot' / 'checksums.txt'
        checksums.write_text(hashlib.sha256(candidate.read_bytes()).hexdigest() + '  ' + candidate.name + '\n')
        output = self.root / 'result.json'
        detail = {'id': 'openai/gpt-4o-mini', 'object': 'model', 'name': 'GPT-4o mini'}
        def binary(_binary, arguments, _environment, _home):
            return {'--version': version + '\n', 'search': json.dumps({'data': [detail]}), 'show': json.dumps(detail)}[
                arguments[0] if arguments[0] == '--version' else arguments[1]]
        def development(_binary, _environment, _home, _port, report, record_generation=False):
            self.assertTrue(record_generation)
            report['catalog_generation'] = 'gen-1'
        arguments = ['verify', '--archive', str(candidate), '--checksums', str(checksums), '--expected-version', '1.2.2-next',
                     '--expected-os', 'windows', '--expected-arch', 'x86_64', '--output', str(output), *extra]
        with patch.object(sys, 'argv', arguments), patch.dict(os.environ, {'STARPORT_CANDIDATE_HEAD_SHA': 'a' * 40}), \
                patch.object(verifier, 'native_target', return_value=('windows', 'x86_64')), \
                patch.object(verifier.platform, 'platform', return_value='test-platform'), \
                patch.object(verifier, 'run_binary', side_effect=binary) as run_binary, \
                patch.object(verifier, 'verify_development', side_effect=development), \
                patch.object(verifier.subprocess, 'run') as run, patch('builtins.print'):
            status = verifier.main()
        run.assert_not_called()
        return status, json.loads(output.read_text()), candidate, run_binary, detail

    def test_candidate_mode_skips_the_release_and_records_digests(self):
        status, report, candidate, _, detail = self.run_candidate()
        self.assertEqual(status, 0, report)
        self.assertEqual(report['verdict'], 'PASS')
        self.assertEqual(report['mode'], 'candidate')
        self.assertIsNone(report['release'])
        self.assertNotIn('release_url', report)
        self.assertEqual(report['candidate_version'], '1.2.2-next')
        self.assertEqual(report['workflow_head'], 'a' * 40)
        self.assertEqual(report['archive'], candidate.name)
        self.assertEqual(report['archive_sha256'], hashlib.sha256(candidate.read_bytes()).hexdigest())
        self.assertEqual(report['binary_sha256'], hashlib.sha256(b'target').hexdigest())
        self.assertEqual(report['catalog_model_sha256'], verifier.catalog_model_digest(detail))
        self.assertEqual(report['catalog_generation'], 'gen-1')
        self.assertEqual(report['keyless_catalog_commands'], 2)
        self.assertEqual(report['user_files_after_shutdown'], [])

    def test_candidate_mode_refuses_another_platform_or_version_before_execution(self):
        for name in ('starport_1.2.1-next_windows_x86_64.zip', 'starport_1.2.2-next_windows_arm64.zip', 'starport_1.2.2-next_linux_x86_64.zip'):
            with self.subTest(name=name):
                status, report, candidate, run_binary, _ = self.run_candidate(archive_name=name)
                self.assertEqual((status, report['verdict']), (1, 'FAIL'))
                run_binary.assert_not_called()
                candidate.unlink()

    def test_candidate_mode_refuses_a_binary_with_another_version(self):
        status, report, _, _, _ = self.run_candidate(version='starport version 1.2.1')
        self.assertEqual((status, report['verdict']), (1, 'FAIL'))
        self.assertIn('wrong release version', report['error'])
        self.assertNotIn('keyless_catalog_commands', report)

    def test_release_and_candidate_arguments_are_exclusive(self):
        output = str(self.root / 'result.json')
        native = ['--expected-os', 'linux', '--expected-arch', 'x86_64', '--output', output]
        for arguments in (['--archive', 'a.tar.gz', '--checksums', 'c.txt', '--expected-version', '1.2.2-next', '--tag', 'v1.2.0'],
                          ['--archive', 'a.tar.gz', '--expected-version', '1.2.2-next'],
                          ['--archive', 'a.tar.gz', '--checksums', 'c.txt'],
                          ['--tag', 'v1.2.0', '--checksums', 'c.txt'],
                          ['--tag', 'v1.2.0', '--expected-version', '1.2.2-next']):
            with self.subTest(arguments=arguments), patch.object(sys, 'argv', ['verify', *arguments, *native]), \
                    patch.dict(os.environ, {}, clear=True), patch('sys.stderr', io.StringIO()), self.assertRaises(SystemExit) as raised:
                verifier.main()
            self.assertEqual(raised.exception.code, 2)
        with patch.object(sys, 'argv', ['verify', *native]), patch.dict(os.environ, {}, clear=True), \
                patch('sys.stderr', io.StringIO()), self.assertRaises(SystemExit) as raised:
            verifier.main()
        self.assertEqual(raised.exception.code, 2)


if __name__ == '__main__':
    unittest.main()
