# ORP0 proof: baseline, proof root, red verifier

- Date: 2026-08-20
- Baseline: `main@ca10f5e`. Feature base: `codex/console-revamp@67a800a` (PR #125, open).
- Branch: `codex/orp-0-plan`.

## Fail-before evidence

`bash scripts/verify-openrouter-parity.sh` at the baseline:

```
FAIL ORP-V01 internal/usage owns the request record repository
FAIL ORP-V02 usage storage namespace is versioned usage:v1:
FAIL ORP-V03 proxy completion path writes usage records
FAIL ORP-V04 GET /api/v1/activity route is registered
FAIL ORP-V05 admin activity route is registered
FAIL ORP-V06 console serves a /usage page
FAIL ORP-V07 catalog snapshot metadata route is registered
FAIL ORP-V08 catalog refresh endpoint exists
FAIL ORP-V09 console catalog button calls the catalog refresh endpoint
FAIL ORP-V10 preset routes are registered
FAIL ORP-V11 @preset/ request references resolve
FAIL ORP-V12 provider.sort reaches the routing policy
FAIL ORP-V13 max_price rejection code exists in routing
FAIL ORP-V14 admin key API accepts allowed_models
FAIL ORP-V15 budget exhaustion has a 402 regression test
FAIL ORP-V16 chat page has a comparison mode
Summary: 0 passed, 16 failed
```

Exit status: 1. All 16 conditions fail at the baseline; the plan requires ≥14.
Console conditions (V06, V09, V16) reference `internal/console`, which exists
only on the PR #125 branch; they stay red on `main` until #125 merges.

## Research reports

The four seam-mapping reports that ground the plan's findings ledger are
preserved under `research/`:

- `usage-accounting.md`
- `catalog-freshness.md`
- `presets-limits-tenancy.md`
- `routing-preferences.md`

## Production behavior

No production behavior changed. The diff touches `docs/`, `scripts/`, and
`CLAUDE.md` only.
