#!/usr/bin/env python3
"""Verify the console served by a local binary in an isolated environment."""

import importlib
from pathlib import Path
import socket
import sys
import tempfile

native = importlib.import_module('verify-native-release')


def verify(binary):
    with tempfile.TemporaryDirectory(prefix='starport-console-smoke-') as directory:
        root = Path(directory)
        home, temporary = root / 'home', root / 'temporary'
        home.mkdir()
        temporary.mkdir()
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            port = probe.getsockname()[1]
        environment = native.isolated_environment(home, temporary, port)
        report = {}
        native.verify_development(binary.resolve(), environment, home, port, report)
        print(f"PASS console HTTP 200 and {report['console_asset_count']} referenced assets")


if __name__ == '__main__':
    verify(Path(sys.argv[1]))
