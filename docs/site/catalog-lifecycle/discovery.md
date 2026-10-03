---
title: Model discovery and readiness
area: catalog-lifecycle
order: 4
summary: Read the permitted catalog facts for a caller, and understand why discovery readiness is always unknown.
---

Discovery shows a caller the catalog facts that its key and account permit. This topic explains what the discovery route returns, what it does not assert, and how it differs from the model lists.

## The discovery route

`GET /api/v1/catalog/discovery` needs a gateway API key with the `models:read` scope or the `admin` scope. It reads one accepted generation and the current key and account policy. It does not call a provider or a secret manager.

| Field | Meaning |
| --- | --- |
| `generation_id` | The accepted generation that supplied the facts |
| `models[].id`, `name`, `description` | One permitted model definition |
| `offerings[].provider`, `provider_model_id` | One permitted provider offering of the model |
| `offerings[].availability`, `lifecycle` | Catalog facts of the offering |
| `offerings[].operations` | The operations that the catalog declares |
| `offerings[].routable_operations` | The operations that route planning can reach in this build |
| `offerings[].readiness` | Always `unknown` |

## Readiness is always unknown

The `readiness` field of each offering is always `unknown`. Discovery does not read provider credentials, so it cannot tell a caller that a request will succeed. A missing provider credential does not remove a permitted entry.

`routable_operations` shows structural support only. It does not grant permission, and it does not show a usable credential. An empty list means that route planning excluded the offering. Refer to [Membership and routability](routability.md).

## Discovery compared with the model lists

The model lists at `GET /v1/models` and `GET /api/v1/models` use a different membership rule. They show only permitted model definitions that have at least one route. They drop each offering that is adapter-not-ready, unpriced, or unsupported. This filter comes from catalog route planning and caller permission. It is not a credential filter. A model with a route and no provider credential stays in the model lists.

| Question | Use |
| --- | --- |
| Which models can this key read in the catalog? | `GET /api/v1/catalog/discovery` |
| Which models have a route for this key? | `GET /v1/models` or `GET /api/v1/models` |
| Can a request to this model succeed now? | Send the request, or read the provider status as an admin |

## Freshness and caches

Discovery and the model lists send `Cache-Control: no-store`. Do not keep a copy of a response in a shared cache. Starport reads the caller policy again before it sends the response. If the policy changes, an identity record is not readable, or the authority withdraws permission, the route returns `503` with `Retry-After: 30` and no catalog facts.

## Canonical reconciliation

The Starmap module `github.com/agentstation/starmap` reconciles provider observations into one canonical catalog. This is the canonical reconciliation that Starport depends on. Starport uses the Starmap module that this release pins. The page header shows its version.

## Read the discovery facts for a key

### Audience

A developer who must find which models a gateway API key can read.

### Before you start

- Get a gateway API key with the `models:read` scope in `$STARPORT_API_KEY`.
- Install `jq`.

### Steps

1. Send a request to the discovery route.
2. Read `generation_id`.
3. For each offering, compare `operations` with `routable_operations`.

### Expected result

The response lists each permitted model. The `readiness` value of each offering is `unknown`.

### Verification

```bash
curl -sS -H "Authorization: Bearer $STARPORT_API_KEY" <gateway-url>/api/v1/catalog/discovery \
  | jq '[.models[].offerings[].readiness] | unique'
```

```json
["unknown"]
```

### If it fails

A `503` with `The catalog is not available.` means that the key is not valid, the key lacks the scope, or the gateway has no usable catalog. Read [Catalog failures](../troubleshoot/catalog-failures.md).

### Related settings

- Refer to [Keys and roles](../start/keys-and-roles.md) for the `models:read` scope.
