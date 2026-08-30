# ENR-C1 proof: admin audit log

Date: 2026-08-30. Branch: `codex/enr-c1`.

## What shipped

- `internal/audit` owns the record: actor, action, subject, outcome,
  and an RFC 3339 UTC time. A record never holds a credential value.
- Migration `0006_audit_log` lands the table in all three sqlstore
  dialects. `TestAuditLogMigrationShips` pins the embedded name.
- The repository records, lists newest first with cursor paging, and
  prunes past retention on each write. The window comes from
  `STARPORT_AUDIT_RETENTION`, default `9600h` (400 days).
- Every mutating admin controller writes the trail after its store
  attempt. The trail covers keys, accounts, templates, teams,
  memberships, grants, shared credentials, BYOK, presets, and the
  authentication mode. About 24 call sites, all through one
  `writeAudit` helper.
- The actor is one prefixed string: `key:<name-or-id>`,
  `console:<grant>`, `user:<subject>`, or `anonymous`. The console
  session middleware plumbs the verified grant into request context.
- A nil trail records nothing and serves the listing a 503 answer.
  A failed trail write logs and never changes the caller's response.
- `GET /api/v1/admin/audit` serves the trail under the admin scope
  with `action`, `actor`, `since`, `until`, `limit`, and `cursor`
  filters.
- The console gained an Audit Log page (`/audit`) with infinite
  paging and a nav entry. Direct loads ride the SPA fallback.
- `docs/OPERATOR-GUIDE.md` gained an Audit Log section.

## Acceptance evidence

Named tests, all green:

- `internal/audit`:
  - `TestRecordRoundTripsThroughTheStore` proves a stored record
    lists back intact.
  - `TestListPagesNewestFirst` proves order and cursor paging.
  - `TestListFiltersNarrowThePage` proves the filter set.
  - `TestPruneDropsRecordsPastRetention` proves prune-on-write.
- `internal/server/controllers`:
  - `TestKeyLifecycleRecordsTheConsoleActor` proves a console session
    names its grant on create and delete.
  - `TestAuditActorNamesEachCaller` proves all four actor prefixes.
  - `TestMutationHandlersWriteTheAuditTrail` walks all seven
    controller surfaces and proves each action lands with outcome
    `ok`.
  - `TestAuditListDegradesWithoutTrail` proves the nil-trail 503.
  - `TestAuditListServesThePage` proves filters forward and the list
    envelope shape.
  - `TestAuditListRejectsABadQuery` proves the 400 paths.
- `internal/sqlstore`: `TestAuditLogMigrationShips` and
  `TestDialectMigrationSetsAgree` prove the migration ships in step
  across dialects.
- `internal/console`: `TestSPAPagePathsCoverClientRoutes` proves a
  direct `/audit` load serves the shell.
- Console: `pnpm -C console check` passes with 33 test files and 210
  tests.

## Commands

- `go test ./...`: pass, no failures.
- `go vet ./...`: clean.
- `make lint`: 0 issues.
- `pnpm -C console build` and `pnpm -C console check`: pass.
- `bash scripts/benchmark-overhead.sh`: pass.
- `bash scripts/verify-doc-links.sh`: PASS.
- Repo gates: v1-architecture, package-layout, dependency-direction,
  console-modernization, auth-onboarding, console-session-grants,
  credential-sharing, starmap-ownership all PASS.
- `bash scripts/verify-enterprise-readiness.sh`: `Summary: 9 passed,
  24 failed`. ENR-V01 through ENR-V09 are the green conditions.

## Scope notes

- A refusal before the store, such as a validation failure, records
  nothing. The trail answers "what changed", not "what was tried".
- `SharedValidate`, `BYOKValidate`, and catalog refresh stay off the
  trail: they mutate nothing durable.
- The trail needs the relational store. A deployment without it runs
  with a nil trail and a 503 listing, loudly.
- No new dependencies.
