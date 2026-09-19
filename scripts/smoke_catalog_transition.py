"""Check catalog removal through the installed Python SDKs."""

import os
import sys

from openai import OpenAI
from openrouter import OpenRouter

base = os.environ["STARPORT_CATALOG_URL"]
key = os.environ["STARPORT_CATALOG_KEY"]
removed = sys.argv[1] == "removed"
messages = [{"role": "user", "content": "hello"}]


def check(call):
    try:
        response = call()
    except Exception as error:
        if not removed or getattr(error, "status_code", None) != 404:
            raise
        return
    if removed:
        raise AssertionError("removed model was accepted")
    if "mock" not in response.choices[0].message.content:
        raise AssertionError("unexpected chat response")


for prefix in ("/v1", "/api/v1"):
    with OpenAI(base_url=base + prefix, api_key=key, max_retries=0) as client:
        check(lambda: client.chat.completions.create(model="author/current", messages=messages))

with OpenRouter(server_url=base + "/api/v1", api_key=key) as client:
    check(lambda: client.chat.send(model="author/current", messages=messages, stream=False))

print("PASS Python SDK catalog transition: " + sys.argv[1])
