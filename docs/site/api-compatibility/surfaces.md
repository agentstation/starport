---
title: Supported API surfaces
area: api-compatibility
order: 1
summary: Find each OpenAI and OpenRouter route that Starport serves, the scope that each route needs, and the routes that each family does not have.
---

Starport serves an OpenAI family under `/v1` and an OpenRouter family under `/api/v1`. Both families use the same gateway keys, routing, budgets, and catalog.

## OpenAI family

| Route | Scope |
| --- | --- |
| `POST /v1/chat/completions` | `chat:write` |
| `POST /v1/responses` | `chat:write` |
| `POST /v1/embeddings` | `chat:write` or `embeddings:write` |
| `POST /v1/rerank` | `rerank:write` |
| `POST /v1/moderations` | `moderations:write` |
| `POST /v1/images/generations`, `POST /v1/images/edits` | `images:write` |
| `POST /v1/audio/speech`, `/transcriptions`, `/translations` | `audio:write` |
| `/v1/videos`: submit, list, get, content, cancel, reconcile | `videos:write` |
| `/v1/files`: upload and delete | `files:write` |
| `/v1/files`: list, get, and content | `files:read` |
| `/v1/batches`: create, list, get, cancel | `batches:write` |
| `GET /v1/models`, `GET /v1/models/{model}` | `models:read` |

## OpenRouter family

| Route | Scope |
| --- | --- |
| `POST /api/v1/chat/completions` | `chat:write` |
| `POST /api/v1/embeddings` | `chat:write` or `embeddings:write` |
| `POST /api/v1/rerank` | `rerank:write` |
| `POST /api/v1/images` | `images:write` |
| `POST /api/v1/audio/speech`, `/audio/transcriptions` | `audio:write` |
| `/api/v1/videos`: submit, list, get, content, cancel, reconcile | `videos:write` |
| `GET /api/v1/models`, `/models/{model}`, `/models/{model}/endpoints` | `models:read` |
| `GET /api/v1/providers`, `/authors`, `/authors/{author}` | `models:read` |
| `GET /api/v1/catalog`, `/catalog/discovery`, `/catalog/changes` | `models:read` |
| `GET /api/v1/activity`, `/activity/export` | `activity:read` |
| `/api/v1/providers/{provider}/credentials` | `admin` |
| `/api/v1/accounts/{account_id}/byok` | `provider_keys:read` or `provider_keys:write`, or `admin` |
| `/api/v1/presets` | Reads need a key. Writes need `presets:write`. |
| `/api/v1/admin/...` | `admin`. The authentication switch has its own local checks. |

`GET /api/v1/auth/mode` and the logo routes need no key. Each other route needs a gateway key.

## Routes that do not exist

- Neither family has a `/messages` route. Use chat completions or responses.
- The OpenRouter family has no image edit, audio translation, moderation, file, or batch route. Use the OpenAI family for these.
- The OpenAI family has no provider, author, catalog, preset, or activity route.

## Model lists and the catalog

`/v1/models` and `/api/v1/models` list only routable offerings. They drop an offering whose adapter is not ready, an offering without a price, and an unsupported offering. This is catalog policy, not a credential filter. A listed model can still fail when its provider refuses the credential. The first inference request is the proof.

Example: the catalog contains `<provider>/<model>`, but the catalog has no price for it. The model is a catalog member. It is not in `/api/v1/models`, and a request for it fails.

`GET /api/v1/catalog/discovery` shows the wider catalog view. Its readiness value is always `unknown` in this release. Refer to [Routability](../catalog-lifecycle/routability.md).

The model, provider, author, `/catalog`, and `/catalog/changes` routes send `Cache-Control: no-store`. When the gateway has no usable catalog snapshot, these routes return `503` with `The catalog is not available.` and `Retry-After: 30`.

## Presets

A preset is a stored set of request settings. Select the latest revision with `@preset/<name>` in the `model` field. Select one revision with `@preset/<name>@<revision>`. The OpenRouter `preset` body field also selects a preset.

```json
{
  "model": "@preset/<name>",
  "messages": [{ "role": "user", "content": "Hello" }]
}
```

A pin to a revision that does not exist fails like an unknown preset. Refer to [Preset revisions](../../OPERATOR-GUIDE.md#preset-revisions).

## Health routes

`GET /health/live` and `GET /health/ready` are outside both families. Use `/health/ready` before you send traffic to a new gateway.
