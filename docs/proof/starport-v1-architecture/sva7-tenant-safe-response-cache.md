# SVA7 tenant-safe response cache proof

Date: 2026-08-03
Status: done

## Fail-before

The proxy owned one response-key implementation. It included the selected
model, messages, part of the sampling configuration, tools, tool choice, and
response format. It omitted these response-changing inputs:

- authenticated tenant.
- catalog generation.
- fallback model chain.
- provider order, allow, ignore, and fallback policy.
- route mode and model overrides.
- API-key model and provider restrictions.
- candidate count, logit bias, reasoning, and user.

Therefore, two otherwise equal baseline chat requests with different provider
policies produced the same key. Tenant requests could also use the same cache
entry.

The cache package had a second `KeyGenerator`. Its embedding key sorted text
inputs. The requests `["first", "second"]` and `["second", "first"]` used the
same key even though embedding result order is observable. The two owners used
different key lengths and request types.

Cached records used provider wire types. Cached streaming reconstructed only a
subset of the first choice from text and reasoning fragments. It did not own a
versioned canonical result contract.

## Change

`internal/responsecache` now owns four related concepts:

- cache eligibility.
- semantic chat and embedding identity.
- versioned canonical result records.
- canonical stream completion and replay.

Keys use the full SHA-256 digest in the
`responsecache:v1:<kind>:<digest>` namespace. Identity contains the
authenticated API key ID, immutable catalog generation, canonical inference
request, provider policy, model chain and overrides, and API-key restrictions.
Ordered inputs keep their order. Set-like restrictions use sorted copies.
Stream delivery options do not change completed-result identity.

The cache skips requests when the tenant or catalog generation is absent. It
also skips chat requests with image inputs, unknown provider extensions, or
invalid tool and output schemas. These paths execute upstream without a cache
read or write.

Each stored record has schema version 1, result kind, semantic key, cache time,
and one canonical inference result. Reads reject invalid JSON, old schemas,
wrong keys, missing timestamps, wrong kinds, and ambiguous payloads.

Streaming and non-streaming calls share one completed canonical result. A
streaming miss writes only after a clean end-of-stream. A partial stream is not
cached. Cached replay preserves choices, text, reasoning, tool calls, log
probabilities, finish reasons, usage, and model identity. Replay emits usage
only when `stream_options.include_usage` requests it.

The cache manager now exposes one byte-response store contract. This task
removed its duplicate key generator and typed provider-wire response methods.
The proxy converts current request and response values at the canonical seam.
HTTP parsing supplies the authenticated API key ID as the tenant.

## Contract evidence

`TestSemanticKeyAndTenantIsolationContract` checks pairwise sensitivity for
all canonical chat controls, tenant and provider policy, catalog generation,
and ordered embedding inputs. It also checks stream and non-stream result
identity and canonical stream reconstruction.

The proxy integration test proves this sequence:

1. The first tenant and generation miss.
2. The same tenant and generation hit.
3. A different tenant misses.
4. A different catalog generation misses.

The corrupt-record and partial-stream tests prove fail-closed recovery. The
architecture fitness test permits `internal/responsecache` to import only
`internal/inference` from the internal package tree.

## Verification

These commands passed:

```bash
go test ./internal/responsecache ./internal/cache ./internal/proxy
go test -race ./internal/responsecache ./internal/cache ./internal/proxy
go test ./internal/responsecache -fuzz '^FuzzSemanticKey$' -fuzztime=10s
go test ./internal/server ./internal/architecture
go test ./...
go vet ./...
git diff --check
```

The fuzz gate ran 1,265,808 executions and passed. The V07 fitness gate rejects
the removed cache-key owner and typed wire-record methods. It also rejects
cache identity logic in the proxy and invalid response-cache dependencies. The
verifier reports:

```text
PASS V01 Starmap module and Go floor
PASS V02 canonical inference contract
PASS V03 routable snapshot generation contract
PASS V04 deterministic route planner contract
PASS V05 attempt state and retry budget contract
PASS V06 versioned concept repository contracts
PASS V07 response cache semantic identity contract
FAIL V08 production composition fail-closed contract
FAIL V09 public package boundary contract
FAIL V10 OpenRouter protocol contract
PASS V11 import graph architecture fitness
PASS V12 full Go test suite
Summary: 9 passed, 3 failed
```

V08 through V10 remain open for their named plan tasks. They do not conflict
with the SVA7 acceptance criteria.
