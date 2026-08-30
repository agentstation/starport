# ENR-E2 proof: shared provider health

Date: 2026-08-30. Branch: `codex/enr-e2`.

## What shipped

- `internal/availability/shared.go` owns the distributed health
  exchange behind a local `KVStore` contract, the storage subset the
  deployment's distributed store satisfies. A tracker without a store
  stays process-local.
- Each replica publishes its full record set under its own instance
  key on every breaker transition. The record carries the state, the
  failure kind, the open deadline, and the transition time. The
  default TTL is one minute, so a dead replica's record expires on
  its own.
- `Refresh` merges peer state at most once per refresh interval. The
  default interval is five seconds. A local record wins when its own
  transition is at least as recent as the peer evidence. Local state
  wins recency conflicts.
- The router latency tracker gains the same publication. A replica
  writes its latency snapshot with a one minute TTL. A replica with
  no local measurement reads the freshest peer value. A local
  measurement always outranks the peer view.
- `config.StorageConfig.Distributed()` names the gate. The app turns
  the exchange on only when the operator selects the Valkey store.
  A Badger-only deployment composes exactly as before.

## Acceptance evidence

- `TestSharedStoreConvergesTwoTrackers`: the second tracker adopts a
  breaker the first one opened, on its next refresh, with the failure
  kind intact. The record TTL equals the configured lifetime.
- `TestPeerReadsAreBoundedByTheRefreshInterval`: a refresh inside the
  interval reads nothing, and the one after the interval converges.
- `TestLocalOnlyTrackerKeepsProcessState` holds the fallback: with no
  shared store a peer breaker never crosses process lines.
- `TestLocalStateWinsRecencyConflicts`: a newer local success stands
  against an older peer breaker.
- `TestSharedLatencyTrackerConvergesPeers` and
  `TestSharedLatencyLocalMeasurementWins` hold the same two rules for
  the latency view, and the option test holds the composition.
- `bash scripts/verify-enterprise-readiness.sh`: `Summary: 20 passed,
  13 failed`. ENR-V19 and ENR-V20 turned green, the exact E2
  conditions. The 13 open conditions belong to later phases.
- `go test ./internal/availability/... ./internal/router/... -race`:
  PASS. `bash scripts/benchmark-overhead.sh`: PASS, p99 0ms.

## Commands

- `go test ./...`: PASS.
- `go vet ./...`: PASS. `make lint`: 0 issues. `make build`: PASS.
- The full `verify-*.sh` battery and both smoke scripts: PASS.

## Scope notes

- The exchange rides the existing `Refresh` call in the planning
  path, so no new background loop exists.
- Publication failures land in `LastPublishError`. The gateway keeps
  serving from local state when the shared store misbehaves.
- The latency snapshot is an advisory routing hint. A write failure
  only keeps peers on their current view.
