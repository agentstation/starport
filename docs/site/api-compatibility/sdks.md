---
title: Connect an SDK
area: api-compatibility
order: 2
summary: Point an OpenAI or OpenRouter SDK at Starport with a base URL and a gateway key, and find the SDK versions that the repository tests.
---

An SDK connects to Starport when you change two values: the base URL and the API key. Keep the request and response types of the SDK.

## Base URLs

| SDK family | Base URL |
| --- | --- |
| OpenAI SDKs | `http://<gateway-host>:7827/v1` |
| OpenRouter SDKs | `http://<gateway-host>:7827/api/v1` |

The API key is a Starport gateway key. The examples read it from `STARPORT_API_KEY`. Do not write a key into source code.

```bash
export STARPORT_API_KEY='<gateway-api-key>'
```

## Tested SDK versions

The repository smoke test runs these SDKs against a gateway with a test provider. A CI job runs it.

| SDK | Version | Route that the test sends |
| --- | --- | --- |
| OpenAI Python, `openai` | `3.6.0` | `/v1/responses`, with and without streaming |
| OpenRouter Python, `openrouter` | `1.1.38` | `/api/v1/chat/completions` |
| OpenRouter TypeScript, `@openrouter/sdk` | `1.2.18` | `/api/v1/chat/completions` |
| OpenRouter Go, `github.com/OpenRouterTeam/go-sdk` | `v0.7.32` | `/api/v1` |

The repository records no tested version of the OpenAI TypeScript SDK. Other versions can work, but the repository does not test them.

## Select a callable model

A model in the catalog is not always callable. Before you write a client, list the models that this gateway can route:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  http://<gateway-host>:7827/v1/models
```

Use an `id` from the response in place of `<provider>/<model>` in the examples. A model that is in the catalog but not in this list fails.

## OpenAI Python

```python
import os

from openai import OpenAI

client = OpenAI(
    base_url="http://<gateway-host>:7827/v1",
    api_key=os.environ["STARPORT_API_KEY"],
)

response = client.chat.completions.create(
    model="<provider>/<model>",
    messages=[{"role": "user", "content": "Hello"}],
)
print(response.choices[0].message.content)

result = client.responses.create(model="<provider>/<model>", input="Hello")
print(result.output_text)
```

## OpenAI TypeScript

```typescript
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "http://<gateway-host>:7827/v1",
  apiKey: process.env.STARPORT_API_KEY,
});

const response = await client.chat.completions.create({
  model: "<provider>/<model>",
  messages: [{ role: "user", content: "Hello" }],
});
console.log(response.choices[0].message.content);
```

## OpenRouter TypeScript

```typescript
import { OpenRouter } from "@openrouter/sdk";

const client = new OpenRouter({
  apiKey: process.env.STARPORT_API_KEY,
  serverURL: "http://<gateway-host>:7827/api/v1",
});

const response = await client.chat.send({
  chatRequest: {
    model: "<provider>/<model>",
    messages: [{ role: "user", content: "Hello" }],
    stream: false,
  },
});
console.log(response.choices[0].message.content);
```

## OpenRouter Python

```python
import os

from openrouter import OpenRouter

with OpenRouter(
    api_key=os.environ["STARPORT_API_KEY"],
    server_url="http://<gateway-host>:7827/api/v1",
) as client:
    response = client.chat.send(
        model="<provider>/<model>",
        messages=[{"role": "user", "content": "Hello"}],
        stream=False,
    )
    print(response.choices[0].message.content)
```

## Verify the connection

**Audience:** a developer who connects an SDK for the first time.

**Before you start:** Get a gateway key with the `chat:write` and `models:read` scopes. Refer to [Keys and roles](../start/keys-and-roles.md).

**Steps:**

1. Export the key as `STARPORT_API_KEY`.
2. List the models with the `curl` command in this topic.
3. Run one of the examples with a model ID from the list.

**Expected result:** The SDK prints the text of the model reply.

**Verification:** The response names the model. A key with the `activity:read` scope can find the request in `GET /api/v1/activity`.

**If it fails:** A `401` error means that the key is not valid. A `403` error means that the key has no scope for the route. For a model error, select a model ID from the model list again. Refer to [Errors and limits](errors-and-limits.md).

**Related settings:** `STARPORT_SERVER_PORT`, `STARPORT_SERVER_HOST`.
