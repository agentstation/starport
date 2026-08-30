"""Run the installed OpenAI Python SDK against the /v1/responses route."""

import os

try:
    from openai import OpenAI
except ImportError:
    print("UNVERIFIED Python OpenAI SDK: package 'openai' is not installed")
    raise SystemExit(3)

client = OpenAI(
    base_url=os.environ["STARPORT_SMOKE_OPENAI_BASE_URL"],
    api_key=os.environ["STARPORT_SMOKE_API_KEY"],
)

response = client.responses.create(model="openai/gpt-4.1", input="smoke")
if response.output_text != "starport smoke ok":
    raise RuntimeError("unexpected OpenAI Python SDK response")
if response.status != "completed":
    raise RuntimeError("unexpected OpenAI Python SDK response status")
print("PASS Python OpenAI SDK responses")

pieces = []
completed = False
stream = client.responses.create(model="openai/gpt-4.1", input="smoke", stream=True)
for event in stream:
    if event.type == "response.output_text.delta":
        pieces.append(event.delta)
    if event.type == "response.completed":
        completed = True
if "".join(pieces) != "starport smoke ok":
    raise RuntimeError("unexpected OpenAI Python SDK stream text")
if not completed:
    raise RuntimeError("the OpenAI Python SDK stream never completed")
print("PASS Python OpenAI SDK responses stream")
