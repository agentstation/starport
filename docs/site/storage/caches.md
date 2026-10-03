---
title: Optional caches
area: storage
order: 4
summary: Turn the response, discovery, extraction, and semantic caches on or off, learn their lifetimes and memory limits, and fix a cache conflict in development mode.
---

Starport can keep copies of responses and lists to answer a repeated request without a provider call. Each cache is optional. A cache never holds durable state, and a cache loss never grants permission or skips a required budget check.

## Cache switches

`STARPORT_CACHE_ENABLED=false` turns off the response, discovery, extraction, and semantic caches. It does not turn off authorization, catalog snapshots, or budget admission. With the master switch on, these flags default to `true`:

| Setting | Cached result |
| --- | --- |
| `STARPORT_CACHE_CHAT_ENABLED` | Exact chat responses and completed streams |
| `STARPORT_CACHE_EMBEDDINGS_ENABLED` | Embedding responses |
| `STARPORT_CACHE_MODELS_ENABLED` | Model lists and model endpoints |
| `STARPORT_CACHE_PROVIDERS_ENABLED` | Provider lists |
| `STARPORT_CACHE_EXTRACTIONS_ENABLED` | Document extraction results |

Restart the gateway to apply a change. When you turn off the chat cache, the semantic cache also stops. When you turn off the extraction cache, a resent document can cause a second paid extraction.

## Lifetimes and memory limits

| Cache | Lifetime | Memory limit |
| --- | --- | --- |
| Responses in local memory | 1 hour | 256 MB. One entry is 1,024 KB or smaller. |
| Model metadata | 6 hours | 16 MB |

The process environment does not change these values in this release. Each gateway process has its own local memory cache.

## Shared response cache

Local memory is the default backend. To share response bytes between replicas, select a separate cache service:

```dotenv
STARPORT_CACHE_BACKEND=valkey
STARPORT_CACHE_URL=valkeys://<cache-host>:6379
STARPORT_CACHE_CA_FILE=/etc/starport/certificates/cache-ca.pem
```

These rules apply:

- The cache service must be a different server from the durable KV store. A different database number on the same server does not isolate eviction.
- `STARPORT_CACHE_CA_FILE` needs a `valkeys://` or `rediss://` endpoint.
- A plaintext endpoint needs a loopback host or `STARPORT_CACHE_ALLOW_INSECURE=true`.
- With the local backend, a URL, a CA file, or the insecure flag stops startup.

The deployment ID scopes each shared entry. The discovery and extraction caches stay in local memory. When the chat and embedding caches are both off, Starport opens no connection to the cache service.

## Semantic cache

The semantic cache answers a close paraphrase of a cached chat request. It keeps vectors that point to exact cache entries. It keeps no response of its own. A match stays in one scope: the same account, catalog generation, model, sampling parameters, tools, and routing policy.

```dotenv
STARPORT_SEMANTIC_CACHE_ENABLED=true
STARPORT_SEMANTIC_CACHE_MODEL=<provider>/<embedding-model>
STARPORT_SEMANTIC_CACHE_THRESHOLD=0.95
STARPORT_SEMANTIC_CACHE_MAX_ENTRIES=128
```

- The model must be a callable embedding offering. A model in the catalog without a usable credential or a price does not work.
- An enabled semantic cache without a model stops startup.
- The threshold is the minimum cosine similarity. It must be more than 0 and not more than 1.
- The entry limit applies to each scope. The oldest vector goes first.

Each request must also opt in with the `X-Semantic-Cache: true` header. Each lookup sends an embedding request through the routing of the account, and that request has its own usage record. Refer to the [operator guide](../../OPERATOR-GUIDE.md#semantic-cache).

## Response headers

| Header | Meaning |
| --- | --- |
| `X-Cache: HIT` | The response came from a cache |
| `X-Cache-Age` | The age of the cached entry |
| `X-Cache-Similarity` | The cosine score of a semantic match |

`X-Cache` describes the Starport response cache only. Provider prompt caching is a different feature, with its own `X-Cache-Write-Cost`, `X-Cache-Read-Cost`, and `X-Cache-Total-Cost` headers. Refer to [Prompt cache control](../../CACHE_CONTROL.md#response-headers).

## Cache-only cleanup

You can clear a response cache service without data loss. The next requests go to the providers again and cost money. A restart clears the local memory caches.

The cache root also holds source caches, such as `<cache>/models.dev` and `<cache>/sources/models.dev-git`. Only a permitted source acquisition can rebuild them. An offline gateway cannot rebuild them. Do not remove the catalog runtime directory or a journal to free space. They are not caches.

## Cache settings in development mode

`starport dev` uses isolated scratch storage. It refuses each setting that selects a shared service or a persistent path, which includes these cache settings:

- `STARPORT_CACHE_BACKEND` with a value other than `local`
- `STARPORT_CACHE_URL`
- `STARPORT_CACHE_ALLOW_INSECURE`
- `STARPORT_CACHE_CA_FILE`

The error contains `development requires isolated storage` and names each conflicting setting:

```text
development requires isolated storage: remove STARPORT_CACHE_URL or use starport init and starport serve for persistent storage. See docs/OPERATOR-GUIDE.md#initialize-persistent-state
```

**Correction.** Remove the named settings from the shell that starts `starport dev`. To use a shared cache, run `starport serve` with persistent settings. Refer to [Temporary development](../start/temporary-development.md#settings-that-development-mode-refuses).
