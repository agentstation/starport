---
title: Keys and roles
area: start
order: 3
summary: Compare gateway API keys, scopes, the local admin token, console sessions, and provider inference credentials.
---

Starport uses four kinds of secret. Each secret proves a different fact, so each has a different owner and lifetime.

## Credential kinds

| Secret | Proves | Owner | Stored as |
| --- | --- | --- | --- |
| Gateway API key | The client identity and its scopes | An account | A SHA-256 hash |
| Local admin token | Presence at the gateway machine | The machine | One file, mode `0600` |
| Console session | A browser that a grant admitted | The browser | A signed HttpOnly cookie |
| Provider inference credential | Payment to a provider | The operator or an account | The environment or encrypted storage |

A gateway API key never pays a provider. A provider inference credential never authenticates a client to Starport.

## Gateway API keys

A gateway API key starts with the prefix `STARPORT_`. A client sends it in the `Authorization: Bearer` header. Starport stores only the hash, so it shows a new key one time only.

`starport init` and `starport dev` each issue a first key with the wildcard scope `*`. Issue more keys in the console under Keys, or with the admin API:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"ci-runner","scopes":["chat:write","models:read"]}' \
  http://127.0.0.1:8080/api/v1/admin/keys
```

The response holds the new key in the `key.key` field. The admin API refuses a key with no scopes.

## Scopes

Most routes require a scope. The key must hold that scope or the wildcard `*`.

| Scope | Grants |
| --- | --- |
| `chat:write` | Chat completions, responses, and embeddings |
| `embeddings:write` | Embeddings only |
| `images:write`, `audio:write`, `videos:write` | Media generation |
| `rerank:write`, `moderations:write` | Rerank and moderation |
| `batches:write` | Batch jobs |
| `files:read`, `files:write` | Uploaded files |
| `models:read` | Model lists, catalog reads, and model discovery |
| `activity:read` | Request activity and usage |
| `presets:write` | Preset changes |
| `provider_keys:read`, `provider_keys:write` | Account BYOK credentials |
| `admin` | Keys, accounts, providers, and other admin routes |
| `*` | Every scope |

Only `*` is a wildcard. The `admin` scope does not grant inference scopes. Give an operator key `admin` and the inference scopes that it needs, or give it `*`.

A new key from the console gets eleven default inference and read scopes. The defaults do not include `videos:write` or `admin`.

When authentication is off, every request holds every scope except `admin`. Refer to the [operator guide](../../OPERATOR-GUIDE.md#authentication-mode) for the authentication switch.

## The local admin token

The local admin token proves that the caller is on the gateway machine. It belongs to no account. The gateway writes it to `local-admin-token.json` in the data directory on first start. It holds the scope `*`.

```bash
starport auth status
starport auth token --copy
starport auth rotate
```

`starport auth status` shows the age, the generation, and the exposure of the token. `starport auth rotate` replaces the secret. Rotate the token if you think that another person read it.

## Console sessions

A console session is a signed HttpOnly cookie. The browser cannot read it. A grant creates the session:

- A launch ticket from `starport ui` or from the start output.
- The local admin token, pasted on the first-contact page.
- An identity provider assertion, after you configure one.

`starport ui` reads the token file directly. It works when the gateway is down or refuses requests. Refer to [Authentication](../operate-starport/authentication.md) for identity providers.

## Provider inference credentials

A provider inference credential comes from one of three sources:

- `environment`: the operator sets the credential in the gateway process environment.
- `shared`: the operator stores the credential in encrypted storage for the whole deployment.
- `byok`: an account stores its own credential in encrypted storage for that account.

The account setting `provider_credential_strategy` sets the order. The default is `operator_first`. Each usage record names the source that paid as `credential_source`.

A stored credential needs the master key. Refer to [Run a persistent local gateway](local-persistent.md#keep-the-master-key).
