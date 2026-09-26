# Optional cache controls

`STARPORT_CACHE_ENABLED=false` disables response, discovery, extraction, and
semantic caching. It does not disable authorization, catalog snapshots, or
required budget admission. Restart Starport to apply these cache settings.

With the master switch enabled, these independent flags default to `true`:

| Environment variable | Cached result |
| --- | --- |
| `STARPORT_CACHE_CHAT_ENABLED` | Exact chat responses and completed streams |
| `STARPORT_CACHE_EMBEDDINGS_ENABLED` | Embedding responses |
| `STARPORT_CACHE_MODELS_ENABLED` | Model lists and model endpoints |
| `STARPORT_CACHE_PROVIDERS_ENABLED` | Provider lists |
| `STARPORT_CACHE_EXTRACTIONS_ENABLED` | Document extraction results |

Set a flag to `false` to disable that kind. Disabling chat caching also disables
semantic lookup and its optional embedding work. Disabling extraction caching
can repeat paid recognition work when a caller resends a document.

Local memory is the default. `STARPORT_CACHE_BACKEND=valkey` and
`STARPORT_CACHE_URL` select a separate cache-only service for response bytes.
The deployment ID scopes shared entries. Discovery and extraction remain local.
When operators disable both chat and embedding caches, Starport opens no shared
cache connection. Cache loss never grants permission or bypasses a required budget.

See the [semantic cache controls](OPERATOR-GUIDE.md#semantic-cache) for request opt-in and embedding costs.
