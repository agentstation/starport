# SVA0 baseline proof

Date: 2026-08-03
Branch: `master`
Commit: `bf6b09e11369`
Worktree before this plan: 75 dirty entries

## Baseline inventory

| Measure | Result |
|---|---:|
| Go packages | 23 |
| Local import edges | 55 |
| Top-level test functions | 401 |
| Benchmark functions | 28 |
| Fuzz functions | 0 |
| Plan tasks | 12 |
| Active tasks | 1 |
| Task blocks | 12 |
| Required task fields | 72 of 72 |
| Plan lines | 336 |
| Unresolved template placeholders | 0 |

The Go tool does not report the number of generated subtests. The source count above includes top-level `Test` functions only.

## Red architecture verifier

Command:

```bash
bash scripts/verify-v1-architecture.sh
```

Result: expected failure with exit status 1.

```text
FAIL V01 Starmap module and Go floor
FAIL V02 canonical inference contract
FAIL V03 routable snapshot generation contract
FAIL V04 deterministic route planner contract
FAIL V05 attempt state and retry budget contract
FAIL V06 provider credential schema and migration contract
FAIL V07 response cache semantic identity contract
FAIL V08 production composition fail-closed contract
FAIL V09 public package boundary contract
FAIL V10 OpenRouter protocol contract
FAIL V11 import graph architecture fitness
PASS V12 full Go test suite
Summary: 1 passed, 11 failed
```

This result proves the verifier starts red. V12 also proves that the pre-existing Go suite remains green.

## Coverage baseline

Command:

```bash
go test -cover ./...
```

Result: exit status 0.

| Package | Statement coverage |
|---|---:|
| `cmd/starport` | 67.9% |
| `examples` | 0.0% |
| `internal/apikeys` | 97.1% |
| `internal/app` | 55.0% |
| `internal/cache` | 57.0% |
| `internal/chatui` | 69.9% |
| `internal/config` | 72.8% |
| `internal/models` | 85.4% |
| `internal/providers/byok` | 59.1% |
| `internal/providers/connectors` | 71.2% |
| `internal/proxy` | 37.8% |
| `internal/registry` | 48.2% |
| `internal/router` | 76.5% |
| `internal/server` | 74.3% |
| `internal/server/controllers` | 56.5% |
| `internal/server/dto` | 0.0% |
| `internal/server/requestctx` | 0.0% |
| `internal/storage` | 70.4% |
| `internal/testutil` | 0.0% |
| `pkg/catalog` | 67.8% |
| `pkg/httpclient` | 80.3% |
| `pkg/models` | 91.2% |
| `pkg/providers` | 52.7% |

Valkey and live-provider checks need external services or credentials. SVA0 records them as `UNVERIFIED`.

## Plan review

Verdict: pass and remain `active`.

The review checked these conditions:

1. The user approved activation in the request that created this plan.
2. The plan owns one architecture outcome.
3. Scope and non-goals name their adjacent owners.
4. The architecture diagrams show the same request path before and after.
5. Every finding routes to at least one task.
6. Every task has a problem, seam, steps, acceptance, fail-before check, and verification command.
7. The ledger has exactly one `in_progress` task.
8. The goal block names the plan, documents, worktree, branch, constraints, commit policy, and completion gate.
9. The final task owns cleanup after the final pull request merges.
10. The plan has 336 lines, which is below the 500-line limit.
11. The technical-writing linter reports zero diagnostics for the plan and verifier.
12. The verifier has 12 fixed conditions and prints the required summary shape.

The task order follows the defect dependencies. It fixes durable identity before later repositories consume it. It defines canonical inference before routing and protocol migration.

The plan preserves the completed architecture-hardening ledger. It does not reopen or overwrite that evidence.

## Worktree attribution

The worktree had 75 dirty entries before plan creation. Those entries belong to prior architecture work or other user work.

SVA0 adds or changes only these plan artifacts:

- `docs/STARPORT_V1_ARCHITECTURE_PLAN.md`
- `docs/proof/starport-v1-architecture/sva0-baseline.md`
- `scripts/verify-v1-architecture.sh`
- One index line in `docs/README.md`

SVA0 changes no production Go behavior.
