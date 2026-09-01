# ENR-Z2 proof: the gate joins CI and the documents match the shipped surface

Date: 2026-09-01. Branch: `codex/enr-z2`.

## What changed

- `.github/workflows/ci.yml` runs `scripts/verify-enterprise-readiness.sh`
  in the Release Contract job's release-contracts step.
- `AGENTS.md` names the gate in the required evidence list and describes
  its coverage in a gate paragraph. It records the terminal count of 33
  conditions.
- `docs/ARCHITECTURE.md` moves the shipped campaign surfaces into the
  implemented list. The list now covers telemetry export, the audit log,
  signed webhooks, and the three new routes. It also covers guardrails,
  team budgets, the semantic cache, and preset revisions. It closes with
  routing spread, shared availability state, and the agent surface. The
  planned list keeps billing integration and the deferred MCP server.
- The package tree gains the packages that earlier campaigns added:
  `skills/starport`, `internal/cli`, `internal/telemetry`,
  `internal/audit`, `internal/events`, `internal/jobs`,
  `internal/guardrails`, `internal/files`, `internal/blob`, and
  `internal/document`.
- The security section replaces its stale planned list. It now states
  the identity plane, the audit log, the guardrail checks, and the
  IDENTITY-001 repair as implemented facts. The section points to
  `docs/SECURITY-POSTURE.md`.
- `README.md` adds the campaign surfaces to the Features list. The list
  gains the Responses API, moderations, batches, team budgets, and
  guardrails. It also gains telemetry export, the audit log, webhooks,
  the agent surface, preset revisions, and the semantic cache.

## Verification

- `bash scripts/verify-enterprise-readiness.sh` reports
  `Summary: 33 passed, 0 failed`. ENR-V33 is green: CI runs the gate and
  the evidence list names it.
- `bash scripts/verify-doc-links.sh` passes.
- `bash scripts/verify-readme-quickstart.sh` passes.
- Technical-writing lint: `README.md` reports 0 diagnostics.
  `docs/ARCHITECTURE.md` holds its pre-existing baseline of 23
  diagnostics. The edits add none.
- The full pre-PR battery result rides the pull request record.

## Acceptance

The acceptance condition is the verifier's terminal summary in CI. The
pull request's Release Contract check proves it on this branch.
