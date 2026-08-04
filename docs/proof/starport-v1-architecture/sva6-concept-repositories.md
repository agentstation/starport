# SVA6 concept repository proof

Date: 2026-08-03
Status: done

## Fail-before

Durable identity had several owners. Admin controllers and authentication
middleware constructed API-key record and hash-index keys. ChatUI repeated the
same two-write workflow. BYOK constructed provider-credential keys and decoded
records in the service. The server and cache manager each implemented a
rate-limit counter. The cache manager also persisted API keys and presets.

The generic storage package exported all of these concept keys:

```text
KeyPrefixAPIKey
KeyPrefixAPIKeyHash
KeyPrefixRateLimit
KeyPrefixPreset
APIKeyKey
APIKeyHashKey
RateLimitKey
PresetKey
```

The result had duplicate invariants, non-atomic identity writes, unversioned
records, and several paths that could bypass concept validation.

## Change

Four concept packages now own direct version 1 durable contracts:

- `internal/identity` owns API-key identities and the hash index under
  `identity:v1:`.
- `internal/credentials` owns encrypted provider credentials under
  `credentials:v1:`.
- `internal/ratelimit` owns fixed-window state under
  `ratelimit:v1:subject:`.
- `internal/presets` owns presets under `presets:v1:name:`.

Each repository owns key encoding, schema envelopes, validation, revision
checks, and compare-and-swap behavior. Identity create and delete update the
identity record and hash index in one transaction. Credential and preset
updates reject stale revisions. Rate-limit consumption uses a compare-and-swap
loop and returns the stored reset time.

Badger and Valkey compare-and-swap adapters now have the same create, replace,
delete, and conflict semantics as the memory adapter. The shared storage
contract checks those operations.

Admin, authentication, ChatUI, BYOK, and rate-limit middleware now use concept
repositories. The cache manager owns response and model caching only. The old
This task removed the old API-key, credential, rate-limit, and preset
namespaces and storage key helpers. It also removed the generic
`internal/models` and `internal/apikeys` ownership packages. Their concepts
moved to the packages that own them.

This is a direct pre-launch schema change. There are no old readers, migration
aliases, dual writes, or compatibility branches.

## Contract evidence

This command passed all four repository contracts on memory and Badger:

```bash
go test -v ./internal/identity ./internal/credentials ./internal/ratelimit ./internal/presets -run '^Test(Identity|ProviderCredential|RateLimit|Preset)RepositoryContract$'
```

The four Valkey subtests reported this explicit optional-gate result:

```text
UNVERIFIED: TEST_VALKEY_URL is not set
```

The contracts cover schema fixtures, creation, exact reads, lists, revisions,
stale-write conflicts, deletion, corrupt records, atomic identity index
updates, rate-limit reset, and concurrent rate-limit consumption.

## Verification

These commands passed:

```bash
go test ./internal/identity ./internal/credentials ./internal/ratelimit ./internal/presets ./internal/storage
go test ./internal/providers/byok ./internal/server ./internal/chatui ./internal/cache ./internal/proxy
go test -race ./internal/identity ./internal/credentials ./internal/ratelimit ./internal/presets ./internal/storage ./internal/providers/byok ./internal/server ./internal/chatui ./internal/cache
go test ./...
go vet ./...
go test ./internal/architecture -run '^TestImportGraphArchitecture$'
```

The V06 fitness gate checks all four versioned schemas and contract tests. It
rejects the removed namespaces and raw storage imports in migrated production
paths. The verifier reports:

```text
PASS V01 Starmap module and Go floor
PASS V02 canonical inference contract
PASS V03 routable snapshot generation contract
PASS V04 deterministic route planner contract
PASS V05 attempt state and retry budget contract
PASS V06 versioned concept repository contracts
FAIL V07 response cache semantic identity contract
FAIL V08 production composition fail-closed contract
FAIL V09 public package boundary contract
FAIL V10 OpenRouter protocol contract
PASS V11 import graph architecture fitness
PASS V12 full Go test suite
Summary: 8 passed, 4 failed
```

V07 through V10 remain open for their named plan tasks. They do not conflict
with the SVA6 acceptance criteria.
