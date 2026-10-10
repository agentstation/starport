---
title: Inspect the catalog
area: start
order: 4
summary: Read the embedded catalog from the command line and the model list from a gateway, and separate catalog membership from a callable offering.
---

The catalog tells you which models and offerings Starport knows. It does not tell you that a provider will accept your request. This topic shows both questions and the commands that answer each one.

## Two questions about a model

**Catalog membership:** Is the model in the active catalog generation, with an offering that Starport can route? The catalog commands and the model lists answer this question.

**Callable offering:** Will a provider accept a request for this model with the credential that Starport resolves? Only a provider request answers this question.

A model can be in the catalog and still fail at the provider. Some causes are a missing credential, an account without access to the model, a quota limit, or a billing problem.

## Search the embedded catalog

**Audience:** a developer who wants to find a model ID before a gateway runs.

**Before you start:** Install Starport. You do not need a provider credential or network access.

**Steps:**

1. Search by ID, name, or author:

   ```bash
   starport models search gpt-4o-mini
   ```

2. Show the full record of one model:

   ```bash
   starport models show openai/gpt-4o-mini --json
   ```

**Expected result:** The search prints one line for each match and a count. The `show` command prints the model record and a list of offerings.

**Verification:** The `show` output has an `offerings` array. Each offering names a provider and an availability value:

```json
{
  "provider": "openai",
  "provider_model_id": "gpt-4o-mini",
  "availability": "unknown",
  "lifecycle": "unknown"
}
```

The value `unknown` is correct for an embedded catalog. The command sends no provider request, so it cannot know whether a provider accepts your credential.

**If it fails:** If the search finds no match, try a shorter term or the provider prefix, for example `openai/`.

**Related settings:** none. These commands read only the embedded generation.

## List models from a running gateway

**Audience:** a client developer who has a gateway API key with the scope `models:read`.

**Before you start:** Start a gateway. Refer to [Run a temporary development gateway](temporary-development.md).

**Steps:**

1. Export the gateway API key:

   ```bash
   export STARPORT_API_KEY="<gateway-api-key>"
   ```

2. Request the model list:

   ```bash
   curl --fail-with-body \
     -H "Authorization: Bearer $STARPORT_API_KEY" \
     http://127.0.0.1:7827/v1/models
   ```

**Expected result:** The response is a JSON object with a `data` array of models.

**Verification:** The `data` array contains the model ID that you want to call.

**If it fails:** A `401` response means that the request has no valid key. A `403` response means that the key does not hold `models:read`.

**Related settings:** `STARPORT_CACHE_MODELS_ENABLED`.

## What the model list includes

`GET /v1/models` and `GET /api/v1/models` list only routable offerings from the active catalog generation. Starport drops an offering for each of these reasons:

- No registered adapter supports the provider.
- The catalog marks the model as retired or not available.
- The catalog marks the offering as not available.
- The offering does not support the operation.
- The offering has no price for the operation.

This filter is catalog policy. It does not check provider credentials. The list keeps a model when no usable credential exists for it. Refer to [Routability](../catalog-lifecycle/routability.md) for the full rule.

Model discovery at `GET /api/v1/catalog/discovery` reports readiness as `unknown` for every entry. Discovery does not test provider access.

## Prove that an offering is callable

Send one small request with the model ID:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"openai/gpt-4o-mini","max_tokens":16,"messages":[{"role":"user","content":"Hello"}]}' \
  http://127.0.0.1:7827/v1/chat/completions
```

A `200` response proves that the provider accepted the credential for this offering. Starport records provider failures, such as authentication, quota, and billing failures, in its provider state. Refer to [Catalog failures](../troubleshoot/catalog-failures.md) when a listed model fails.
