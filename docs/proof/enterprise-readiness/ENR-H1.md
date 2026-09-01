# ENR-H1 proof: semantic cache

Status date: 2026-09-01.

## What shipped

The exact response cache answers only an exact repeat of a request. This
task adds an opt-in similarity index beside it. A close paraphrase of a
cached prompt now answers from the cache with one fewer provider call.

The layer never owns a response. It holds vectors that point at exact
cache entries inside a similarity scope. The scope hashes the full exact
identity with the prompt text blanked. The account, the catalog
generation, the model, the sampling parameters, the tools, and the routing
policy all pin it. Only the prompt text may differ between two requests in
one scope. A similarity answer therefore never crosses a boundary the
exact identity keeps apart.

The scope also bounds the vector scan per account, because the account is
part of the scope.

## The pieces

- `internal/response/cache/semantic.go`: `SemanticScope` derives the
  scope key and canonical prompt text. `Cosine` scores two vectors.
  `SemanticIndex` stores bounded per-scope vectors in the KV plane.
  Scope keys read `responsecache:v6:semantic_cache:<hash>`.
- `internal/response/cache/identity.go`: `ChatKey` now builds on a shared
  payload builder. Contract tests prove the key bytes did not change, so
  no existing cache entry invalidates.
- `internal/config/semanticcache.go`: `SemanticCacheConfig` under
  `STARPORT_SEMANTIC_CACHE_*`. Enabling without an embedding model
  refuses to start.
- `internal/proxy/semantic_cache.go`: `GatewayEmbedder` rides the
  gateway's own embeddings path under the calling request's identity.
  Its records carry the `-semantic-cache` request suffix. `semanticProbe`
  runs only on an exact miss and fails open on every error.
- `internal/proxy/cache.go`: both chat paths probe on exact miss. A hit
  reuses `CacheStatusHit` plus a similarity score. The probe drops a
  matched vector whose exact entry left the store, and the lookup misses.
  A miss stores the vector after the exact entry lands.
- `internal/server/controllers/chat.go`: the `X-Semantic-Cache` request
  header is the per-request opt-in. `X-Cache-Similarity` reports the
  cosine score on both response shapes.
- `internal/app/app.go`: composition wires the embedder late-bound, the
  same way the guardrail moderator binds the finished gateway.
- `docs/OPERATOR-GUIDE.md`: a Semantic Cache section documents the two
  opt-ins, the bounds, and the headers.

## Acceptance evidence

- A paraphrase above the threshold answers with one provider call:
  `TestSemanticCacheAnswersAParaphrase` and
  `TestSemanticCacheStreamAnswersAParaphrase`.
- A prompt below the threshold pays two provider calls:
  `TestSemanticCacheRefusesBelowThreshold`.
- With the flag off, or without the request header, the embedder never
  runs and two calls pay: `TestSemanticCacheNeedsDeploymentOptIn` and
  `TestSemanticCacheNeedsRequestOptIn`.
- An embedding failure fails open:
  `TestSemanticCacheFailsOpenOnEmbedError`.
- A vector whose entry left the store drops with it:
  `TestSemanticCacheDropsAVectorWhoseEntryLeft` and
  `TestSemanticIndexDropEvictsAVector`.
- The scope splits on account, generation, model, sampling, and policy:
  `TestSemanticScopeSharedByParaphrasesAlone`.
- The per-scope bound holds: `TestSemanticIndexBoundsEntriesPerScope`.
- Key stability: the `ChatKey` contract tests pass unchanged.

## Checks

- `go test ./internal/response/cache/... ./internal/proxy/...
  ./internal/config/... ./internal/server/... ./internal/app/...`: pass.
- `go test -race ./internal/proxy/ ./internal/response/cache/`: pass.
- `bash scripts/verify-enterprise-readiness.sh`: 28 passed, 5 failed.
  ENR-V27 and ENR-V28 are green. The five failures are the tasks that
  remain: ENR-V29 through ENR-V33.
- The full pre-PR battery from the repository evidence list: pass. Each
  optional SDK smoke check reports its own skip status in CI.
- `technical-writing lint docs/OPERATOR-GUIDE.md`: the new section is
  clean, and the file keeps its 48 baseline diagnostics.
