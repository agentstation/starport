# CDP8 verified remote catalog activation

Status: `done`

Work commit: Starport `5e50d70`

## Fail-before evidence

The campaign verifier reported:

```text
Summary: 18 passed, 1 failed
```

CDP-V17 was the only failure. Starport had no
`TestVerifiedRemoteCatalogActivatesProvider` test and no remote Starmap
runtime. It could read only its compiled Starmap catalog or a local workspace.

## Runtime contract

Starport now composes `remote.Subscriber` from the released Starmap v0.4.0
module. The subscriber owns remote fetches, strict manifests, payload checksum
verification, schema compatibility, generation order, SSE recovery, and one
atomic `starmap.CatalogState`.

Starport samples that atomic in-memory state every 250 milliseconds. The sample
does not cause a network request. Starmap's SSE subscriber remains the network
and retry owner. A one-element latest-value channel lets Starport coalesce
intermediate states without exposing a partial generation.

The application sends each new state through the CDP6.1 runtime transaction:

1. Resolve catalog-declared provider configuration.
2. Build credential-free connectors.
3. Validate the complete adapter projection.
4. Record the startup-safe generation.
5. Replace the catalog and registry runtime.
6. Drain the prior connectors after their final lease ends.

A duplicate generation ID and payload checksum cause no connector rebuild.
A digest-equal generation with a new identity changes the runtime and response
cache generation while it keeps the immutable catalog pointer.

## Durable recovery

One Starport KV store now has two independent current pointers over the same
immutable generation records:

| Pointer | Owner | Meaning |
|---|---|---|
| `catalog_remote_generation:v1:current` | Starmap remote subscriber | Newest verified remote generation |
| `catalog_generation:v1:current` | Starport runtime | Newest generation that passed complete runtime validation |

This split prevents a Starmap-valid generation that the current Starport binary
cannot activate from replacing the restart-safe runtime. A restart opens the
Starport accepted pointer first. The subscriber then uses its remote pointer
and receives the accepted generation as its offline pin when the remote store
is empty.

Starport verifies the generation ID, payload checksum, and timestamp against
the durable remote generation before acceptance. It rejects a backward
accepted timestamp and distinct payloads with one timestamp. A durable-store
failure closes the unpublished connectors and keeps the old runtime and cache
identity.

## Configuration and transport

The loader owns these settings:

```text
STARPORT_CATALOG_REMOTE_URL
STARPORT_CATALOG_REMOTE_API_KEY
STARPORT_CATALOG_REMOTE_ACTIVATION_INTERVAL
STARPORT_CATALOG_REFRESH_TIMEOUT
```

Configuration inspection redacts the remote URL and optional API key. The
request-cloning transport applies the key in `X-API-Key`. It does not change the
caller request.

A remote URL is mutually exclusive with a local workspace, startup
acquisition, and scheduled local acquisition. An API key without a URL fails
validation. Starmap rejects credentials, query values, and fragments in the
URL. It also requires HTTPS except for a loopback publisher.

Manifest and payload requests use the configured refresh timeout. The
long-lived SSE request does not use `http.Client.Timeout`. Starmap's heartbeat
and liveness checks bound that stream. This avoids scheduled reconnects caused
only by a client wall-clock timeout.

## Contract tests

`TestVerifiedRemoteCatalogActivatesProvider` runs the released protocol against
an authenticated test publisher. It proves all of these results:

- The verified remote generation activates without a Starport rebuild.
- Catalog provider `acme` gets a compiled OpenAI transport.
- Exact provider model ID `opaque/chat@001` remains unchanged.
- The accepted pointer records the generation.
- A restart loads the accepted generation before remote work.
- The catalog API key reaches each cloned remote request.

Additional tests prove these contracts:

- Remote and accepted pointers advance independently.
- Both pointers share immutable generation records.
- Only matching forward states can become accepted.
- The state sampler emits one value for one identity change.
- `Close` cancels and joins the sampler without parent-context cancellation.
- Fetch requests have deadlines, but SSE uses liveness.
- Invalid and unroutable candidates keep the runtime and cache identity.
- Duplicate states do not publish.
- Digest-equal new identities publish.
- Durable acceptance failure closes the candidate and keeps the old runtime.
- Configuration rejects mixed local and remote sources.
- Inspection redacts the remote URL and API key.

The released Starmap remote and protocol package tests also passed. They cover
bad checksums, malformed data, incompatible schemas, stale and duplicate
generations, catch-up, reconnects, durable restart, terminal authentication,
and nonterminal recovery.

## Verification

The final source passed these checks:

- `go test ./... -count=1`: 41 packages passed.
- `go test -race ./internal/catalog ./internal/config ./internal/app
  ./internal/registry ./internal/responsecache -count=1`: five packages passed.
- `go test github.com/agentstation/starmap/remote
  github.com/agentstation/starmap/pkg/catalogremote -count=1`: two released
  Starmap packages passed.
- The same two released Starmap packages passed under the race detector.
- `go vet ./...`.
- `make lint`: zero issues.
- `make build`.
- `bash scripts/verify-starmap-ownership.sh`: 12 passed, 0 failed.
- `bash scripts/verify-v1-architecture.sh`: 12 passed, 0 failed.
- `bash scripts/verify-catalog-driven-providers.sh`: 19 passed, 0 failed.
- `bash scripts/smoke-first-run.sh`.
- `bash scripts/smoke-openrouter-sdks.sh`: raw HTTP and all three SDKs passed.
- `git diff --check`.

No verification command used `GOFLAGS`, `-p`, or another scheduler cap.
