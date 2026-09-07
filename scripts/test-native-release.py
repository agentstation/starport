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

    def test_native_targets(self):
        for system, machine, expected in [('Windows', 'AMD64', ('windows', 'x86_64')),
                                          ('Windows', 'ARM64', ('windows', 'arm64')),
                                          ('Linux', 'aarch64', ('linux', 'arm64')),
                                          ('Darwin', 'arm64', ('darwin', 'arm64'))]:
            with self.subTest(system=system, machine=machine), patch.object(verifier.platform, 'system', return_value=system), patch.object(verifier.platform, 'machine', return_value=machine):
                self.assertEqual(verifier.native_target(), expected)
        with patch.object(verifier.platform, 'machine', return_value='unknown'), self.assertRaises(ValueError):
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


if __name__ == '__main__':
    unittest.main()
