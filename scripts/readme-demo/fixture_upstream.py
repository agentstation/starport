#!/usr/bin/env python3
"""Serve a disclosed OpenAI-compatible chat fixture for README demonstration rehearsals.

The fixture answers every chat completion with one fixed text. It never calls
a provider. It requires the bearer token that the caller passes through the
STARPORT_DEMO_FIXTURE_TOKEN environment variable, and it never writes that
value to standard output or to the request log.
"""
import argparse
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import threading
import time

ANSWER = "Rehearsal fixture: Hello from Starport!"
MODEL = "gpt-4o-mini-rehearsal-fixture"
TOKEN_ENVIRONMENT = "STARPORT_DEMO_FIXTURE_TOKEN"
# The fixture splits the answer into these stream deltas.
DELTAS = ["Rehearsal fixture: ", "Hello ", "from ", "Starport!"]


def completion_chunks(created):
    base = {"id": "chatcmpl-rehearsal-fixture", "object": "chat.completion.chunk", "created": created, "model": MODEL}
    yield dict(base, choices=[{"index": 0, "delta": {"role": "assistant", "content": ""}, "finish_reason": None}])
    for text in DELTAS:
        yield dict(base, choices=[{"index": 0, "delta": {"content": text}, "finish_reason": None}])
    yield dict(base, choices=[{"index": 0, "delta": {}, "finish_reason": "stop"}],
               usage={"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20})


class FixtureHandler(BaseHTTPRequestHandler):
    server_version = "StarportRehearsalFixture/1"

    def log_message(self, format, *args):
        pass

    def record(self, entry):
        with self.server.log_lock:
            with self.server.request_log.open("a") as stream:
                stream.write(json.dumps(entry, sort_keys=True) + "\n")

    def authorized(self):
        return self.headers.get("Authorization", "") == "Bearer " + self.server.token

    def send_json(self, status, value):
        body = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        authorized = self.authorized()
        self.record({"method": "GET", "path": self.path, "authorized": authorized})
        if self.path.rstrip("/").endswith("/models") and authorized:
            self.send_json(200, {"object": "list", "data": [{"id": MODEL, "object": "model", "owned_by": "fixture"}]})
            return
        self.send_json(401 if not authorized else 404, {"error": {"message": "fixture route not found"}})

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        try:
            request = json.loads(self.rfile.read(length) or b"{}")
        except json.JSONDecodeError:
            request = {}
        authorized = self.authorized()
        chat = self.path.rstrip("/").endswith("/chat/completions")
        self.record({"method": "POST", "path": self.path, "authorized": authorized,
                     "requested_model": request.get("model"), "stream": bool(request.get("stream"))})
        if not authorized:
            self.send_json(401, {"error": {"message": "fixture credential rejected", "type": "invalid_request_error"}})
            return
        if not chat:
            self.send_json(404, {"error": {"message": "fixture route not found"}})
            return
        created = int(time.time())
        if not request.get("stream"):
            self.send_json(200, {"id": "chatcmpl-rehearsal-fixture", "object": "chat.completion", "created": created,
                                 "model": MODEL, "choices": [{"index": 0, "finish_reason": "stop",
                                                              "message": {"role": "assistant", "content": ANSWER}}],
                                 "usage": {"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20}})
            return
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        for chunk in completion_chunks(created):
            self.wfile.write(b"data: " + json.dumps(chunk).encode() + b"\n\n")
            self.wfile.flush()
            # A short pause keeps the fixture stream visibly incremental.
            time.sleep(self.server.delta_seconds)
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()
        self.close_connection = True


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=0, help="0 selects a free port")
    parser.add_argument("--request-log", type=Path, required=True, help="JSON lines file without credential values")
    parser.add_argument("--ready-file", type=Path, help="write the selected port here after the socket listens")
    parser.add_argument("--delta-seconds", type=float, default=0.12)
    args = parser.parse_args()
    token = os.environ.get(TOKEN_ENVIRONMENT, "")
    if len(token) < 16:
        raise SystemExit(TOKEN_ENVIRONMENT + " must hold a generated token of at least 16 characters")
    server = ThreadingHTTPServer((args.host, args.port), FixtureHandler)
    server.token = token
    server.request_log = args.request_log
    server.log_lock = threading.Lock()
    server.delta_seconds = args.delta_seconds
    args.request_log.touch()
    port = server.server_address[1]
    if args.ready_file:
        # A rename publishes the port in one step, so a reader never sees a partial value.
        partial = args.ready_file.with_name(args.ready_file.name + ".partial")
        partial.write_text(str(port) + "\n")
        partial.replace(args.ready_file)
    print(json.dumps({"fixture": "fixture_upstream", "port": port, "model": MODEL}), flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
