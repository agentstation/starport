# ENR-Z1 proof: IDENTITY-001 repair

Status date: 2026-09-01.

## What shipped

A corrupt or foreign identity hash-index record blocked an operator
from deleting its API key. The delete path decoded the index and refused
on any defect, so the only workaround was a manual storage edit. This
task lets the owner's delete complete against both defects while
runtime authentication keeps failing closed.

Execution located the owning boundary in `internal/apikey/repository.go`,
not `internal/identity`. The gateway identity store owns the
`identity:v1:` prefix and its hash index. `internal/identity` owns the
people plane and holds no index. ENR-V32's path pin moved to
`internal/apikey` to match, with the same meaning.

## The pieces

- `internal/apikey/repository.go`: `Delete` now sorts the stored index
  into three cases. An index that names this key leaves with it, as
  before. A corrupt index serves nobody, so the delete repairs it by
  removing it in the same atomic batch. A foreign index names another
  owner, so the delete has no claim to remove it. The batch holds the
  foreign record unchanged, and a concurrent rewrite still conflicts.
- `docs/TASKS.md`: the IDENTITY-001 known-issue row closes with the
  repair behavior recorded.

## Acceptance evidence

- The regression test deletes a key whose index record is corrupt, and
  the corrupt record leaves with its owner:
  `TestAPIKeyDeleteRepairsCorruptHashIndex`.
- Authentication against the corrupt index fails closed before the
  repair, inside the same test.
- A foreign index survives its non-owner's delete untouched:
  `TestAPIKeyDeleteLeavesForeignHashIndex`.
- Fail-before: both tests fail on baseline. The corrupt case returned
  the decode error, and the foreign case returned the mismatch error.

## Checks

- `go test ./internal/apikey/`: pass.
- `bash scripts/verify-enterprise-readiness.sh`: 32 passed, 1 failed.
  ENR-V32 is green. The one failure is the task that remains: ENR-V33.
- The full pre-PR battery from the repository evidence list: pass. Each
  optional SDK smoke check reports its own skip status in CI.
