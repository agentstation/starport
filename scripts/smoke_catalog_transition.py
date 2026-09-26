"""Check aliases, streams, and removal through the installed Python SDKs."""

import os
import sys

from openai import OpenAI
from openrouter import OpenRouter

base = os.environ["STARPORT_CATALOG_URL"]
key = os.environ["STARPORT_CATALOG_KEY"]
messages = [{"role": "user", "content": "hello"}]


def check(call, model, streaming):
    removed = sys.argv[1] == "removed" or (
        sys.argv[1] == "alias-removed" and model == "author/old"
    )
    try:
        response = call()
        if streaming:
            count = sum(1 for _ in response)
            if not count:
                raise AssertionError("empty stream")
        elif "mock" not in response.choices[0].message.content:
            raise AssertionError("unexpected chat response")
    except Exception as error:
        if not removed or getattr(error, "status_code", None) != 404:
            raise
        return
    if removed:
        raise AssertionError("removed model was accepted")


for model in ("author/current", "author/old"):
    for streaming in (False, True):
        for prefix in ("/v1", "/api/v1"):
            with OpenAI(base_url=base + prefix, api_key=key, max_retries=0) as client:
                check(lambda: client.chat.completions.create(
                    model=model, messages=messages, stream=streaming
                ), model, streaming)
        with OpenRouter(server_url=base + "/api/v1", api_key=key) as client:
            check(lambda: client.chat.send(
                model=model, messages=messages, stream=streaming
            ), model, streaming)

print("PASS Python SDK catalog transition: " + sys.argv[1])
