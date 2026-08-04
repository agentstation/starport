"""Run the installed OpenRouter Python SDK against a Starport base URL."""

import inspect
import os
import sys

try:
    from openrouter import OpenRouter
except ImportError:
    print("UNVERIFIED Python OpenRouter SDK: package 'openrouter' is not installed")
    raise SystemExit(3)

base_url = os.environ["STARPORT_SMOKE_BASE_URL"]
parameters = inspect.signature(OpenRouter).parameters
configuration = {"api_key": os.environ["STARPORT_SMOKE_API_KEY"]}
if "server_url" in parameters:
    configuration["server_url"] = base_url
elif "base_url" in parameters:
    configuration["base_url"] = base_url
else:
    print("UNVERIFIED Python OpenRouter SDK: installed version has no custom base URL option")
    raise SystemExit(3)

with OpenRouter(**configuration) as client:
    response = client.chat.send(
        model="openai/gpt-4.1",
        messages=[{"role": "user", "content": "smoke"}],
        stream=False,
    )

if response.choices[0].message.content != "starport smoke ok":
    raise RuntimeError("unexpected OpenRouter Python SDK response")
print("PASS Python OpenRouter SDK")
