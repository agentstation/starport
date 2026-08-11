# CDP4 Starmap atomic and durable remote subscriber

Status: `done`

Work commit: Starmap `6697c478cfef4d72dd1953a7962dc07679ae92d8`

## Fail-before evidence

The original subscriber had two authorities for active generation identity.
`remote.Subscriber.active` held the generation ID, checksum, and timestamp.
The root `starmap.Client` held the catalog pointer and a different generation
ID. `remote.New` also created a private memory store.

This command captured the identity divergence:

```text
go test ./remote \
  -run '^TestSubscriberDeduplicatesNewIdentityWithSamePayload$' -count=1
```

The test failed because the subscriber reported `generation-two` while
`CurrentCatalogState` still reported `generation-one`.

## Atomic state

`CatalogState` now returns these values under one root-client lock:

- The immutable catalog pointer.
- The logical generation ID.
- The exact payload checksum.
- The generation timestamp.
- The process-local sequence.

`Subscriber.State` returns that snapshot directly. `Catalog`, health, event
deduplication, stale-generation checks, and conditional polling all use the same
root-client state. The subscriber no longer stores a second generation
identity.

A new generation ID with the same payload checksum commits to the caller store
and advances the atomic identity. It retains the catalog pointer. It emits one
generation event and no model-change callbacks. Repeating the same generation
ID changes no state, sequence, or event.

The concurrent reader test verifies that a reader sees only a complete old or
new tuple. Each tuple contains the matching catalog, generation ID, and payload
checksum. The race detector covers 32 concurrent readers during activation.

## Caller-owned durability and bootstrap

`remote.Config.CatalogStore` is now required. `remote.New` no longer creates a
memory store. Tests reject a nil interface and a typed nil store before remote
work.

`remote.NewContext` bounds store reads and an optional pinned-bootstrap commit.
The constructor creates no goroutine and sends no remote request. An optional
`PinnedBootstrap` commits only when the caller store is empty. The store's
verified durable current generation always wins. Corrupt or unavailable store
state causes construction to fail.

The pin commits before root-client construction. Thus, the root client loads it
as startup state and does not start the publication-hook dispatcher during the
constructor. A pin that matches the embedded logical identity becomes durable
without a second sequence or event.

The filesystem restart test commits a remote generation, reopens the store,
and closes the remote server. Construction returns the exact durable catalog,
generation ID, checksum, and timestamp. `Start` retains that state and enters
streaming recovery.

## Remote failure contract

The subscriber now keeps a verified local state during a nonterminal initial
fetch, stream-open, or catch-up failure. It runs normal SSE reconnect even when
polling fallback is off. A test fails the initial manifest request, then proves
that streaming reconnect fetches and activates the remote generation without a
poll.

HTTP 401 and 403 remain terminal. A durable local generation does not hide an
authentication failure. Activation, validation, and caller-store failures also
remain hard failures. Context cancellation bounds constructor store I/O and all
remote lifecycle work.

## External contract updates

The README, architecture document, remote protocol, generated API reference,
and generated remote package reference now define these rules:

- The caller owns and supplies the store.
- A durable current generation wins over the optional pin.
- Invalid durable state does not fall back.
- `State` is the atomic catalog and identity API.
- A digest-equal new ID publishes identity without copying catalog bytes.
- Normal stream recovery and optional polling are separate mechanisms.
- Terminal authentication errors do not retry.

The read-only, pinned-artifact, remote-subscriber, and server-storage consumer
fixtures use the revised public contract. The pinned-artifact consumer verifies
that an embedded-equivalent generation becomes durable without a false content
publication.

## Verification

The focused ordinary tests passed:

```text
go test ./remote .
```

The focused race matrix passed with normal scheduling:

```text
go test -race ./remote ./pkg/catalogstore ./internal/bootstrap .
```

Package times were 15.922 seconds for `remote` and 2.067 seconds for
`catalogstore`. Embedded bootstrap took 57.914 seconds. The root package took
263.185 seconds.

The first full race run found a test synchronization defect. The test added
model hooks before the initial asynchronous dispatch became idle. Race
instrumentation exposed the overlap. The product state remained race-free. The
test now waits for the initial dispatcher to become idle and uses a nonblocking
counter. Ten repeated race-instrumented runs of that test passed.

The final uncapped `make verify` passed:

- All 85 ordinary packages.
- Six external pure-Go consumer modules and the S3 package.
- The complete repository race suite with `CGO_ENABLED=1`.
- `go vet ./...`.
- `golangci-lint` with zero issues.
- Three catalog benchmarks at 9.034, 9.212, and 8.779 ns/op.
- Zero bytes and zero allocations for each catalog benchmark.
- All 15 coverage gates.
- Generated documentation, OpenAPI, and embedded catalog checks.
- File-size, whitespace, build, catalog validation, and CLI smoke checks.

The final race run completed the root package in 273.275 seconds and
`acquisition` in 123.800 seconds. The bootstrap-manifest command took 96.404
seconds. `internal/server` took 104.602 seconds. `remote` took 18.349 seconds,
and `catalogstore` took 1.975 seconds. No race report occurred. No command used
`GOFLAGS`, `-p`, a scheduler cap, or a timeout change.

Strict writing checks passed for the changed README and architecture sections,
the remote protocol section, and the complete generated remote package
reference. The repository documentation check passed.
