# ENR-A0 — Author the campaign verifier red

Date: 2026-08-29. Branch: `codex/enr-a0`. Baseline: main @ `f7dfb6b`.

## What landed

- `scripts/verify-enterprise-readiness.sh`: 33 conditions (`ENR-V01`
  through `ENR-V33`) in the repository verifier convention. The helpers
  are `all_present`, `tests_all_present`, and `ts_all_present`, copied
  from `scripts/verify-credential-sharing.sh`.
- `docs/plans/enterprise-readiness-plan.html`: the activated plan.
- `docs/TASKS.md`: the plan registered under Active Work.
- This proof root.

## Red run at baseline

Every condition failed at `f7dfb6b`, so no condition can pass by accident:

```
Summary: 0 passed, 33 failed
exit=1
```

All 33 lines printed `FAIL`. No line printed `PASS`.

## Vocabulary probes

Each grep term was probed at baseline before authoring. Four first-choice
terms collided with existing code and were replaced:

| Rejected term | Collision | Replacement |
| --- | --- | --- |
| `"/metrics"` in source | admin route in `internal/server/routes.go` | test-only check (`tests_all_present`) |
| `semantic` | `SemanticKeyVersion` in `internal/response/cache` | `Cosine` + `semantic_cache` |
| `corrupt` | `internal/identity` prose | `RepairsCorrupt` test name |
| `audit` (console) | `EntityLogo.tsx` | `AuditLog` component name |

The replacement terms returned zero hits at baseline.

## Condition counts by phase

Phase B 6 (V01–V06), C 6 (V07–V12), D 5 (V13–V17), E 3 (V18–V20),
F 4 (V21–V24), G 2 (V25–V26), H 5 (V27–V31), close 2 (V32–V33). Total 33.

ENR-G2 (organization tier) is deferred and holds no conditions.

## CI wiring

The gate joins `.github/workflows` and the AGENTS.md evidence list at
ENR-Z2, which `ENR-V33` verifies. A gate that no workflow runs cannot
report a regression.
