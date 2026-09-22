#!/usr/bin/env python3
"""Test console release checks with missing builds and HTTP failures."""

from contextlib import contextmanager
import http.server
import importlib
from pathlib import Path
import tempfile
import threading
import unittest
import urllib.error

embedded = importlib.import_module('verify-embedded-console')
native = importlib.import_module('verify-native-release')


@contextmanager
def server(responses):
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            status, content_type, body = responses.get(self.path, (404, 'text/plain', b'missing'))
            self.send_response(status)
            self.send_header('Content-Type', content_type)
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *args):
            pass

    with http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler) as instance:
        worker = threading.Thread(target=instance.serve_forever)
        worker.start()
        try:
            yield f'http://127.0.0.1:{instance.server_port}'
        finally:
            instance.shutdown()
            worker.join()


class ConsoleReleaseTests(unittest.TestCase):
    def test_embedded_build_requires_every_asset(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            dist = root / 'dist'
            dist.mkdir()
            binary = root / 'binary'
            binary.write_bytes(b'no console')
            with self.assertRaises(ValueError):
                embedded.verify(binary, dist)
            for name, data in [('index.html', b'console html'), ('app.js', b'console javascript'), ('app.css', b'console css')]:
                (dist / name).write_bytes(data)
            with self.assertRaises(ValueError):
                embedded.verify(binary, dist)
            binary.write_bytes(b'console html\0console javascript\0console css')
            self.assertEqual(embedded.verify(binary, dist), 3)
            (dist / 'lazy.js').write_bytes(b'missing lazy chunk')
            with self.assertRaises(ValueError):
                embedded.verify(binary, dist)

    def test_served_console_requires_html_and_assets(self):
        html = b'<script src="/assets/app.js"></script><link rel="stylesheet" href="/assets/app.css">'
        responses = {'/': (200, 'text/html', html),
                     '/assets/app.js': (200, 'text/javascript', b'console.log("app")'),
                     '/assets/app.css': (200, 'text/css', b'body {color: red}')}
        with server(responses) as base:
            self.assertEqual(native.verify_console(base), 2)
        failures = [('/', (503, 'text/plain', b'console not built')),
                    ('/', (200, 'text/html', b'no asset references')),
                    ('/assets/app.js', (404, 'text/plain', b'missing')),
                    ('/assets/app.js', (200, 'text/html', html)),
                    ('/assets/app.css', (200, 'text/css', b''))]
        for path, response in failures:
            with self.subTest(path=path, response=response), server(responses | {path: response}) as base:
                with self.assertRaises((ValueError, urllib.error.HTTPError)):
                    native.verify_console(base)


if __name__ == '__main__':
    unittest.main()
