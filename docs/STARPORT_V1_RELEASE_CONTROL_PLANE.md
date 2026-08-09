# Starport v1 Release Control Plane

Status: `active` | Owner: this plan | Created: 2026-08-09
Baseline: master @ ff293dd50189df2f9937b19e80f29be359639995
Proof root: `docs/proof/starport-v1-release/`
Next action: merge SPR1 proof and tag protected master v1.0.0

## Outcome

> Starport v1 is a tested OpenRouter-compatible gateway release that users can
> install as verified binaries or a container. Protected `master` contains no
> stale release claim or dependency queue, and every published artifact is
> reproducible, checksummed, attested, and read back before plan closeout.

## Architecture

Before:

```text
[old Dependabot branches] -> [mixed stale updates and failing checks]
[master] -> [CI] -> [manual binary build] -> [no Starport release]
[SDK smoke] -> [raw HTTP plus optional Python and TypeScript]
```

After:

```text
[grouped current updates] -> [pinned CI and release actions]
[official Python + TypeScript + Go SDKs] -> [OpenRouter codec contract]
[protected master] -> [verified tag] -> [draft assets + SBOM + provenance]
                                      -> [immutable v1.0.0 + GHCR image]
```

## Scope

- Owns:
  - The stale Starport dependency PR queue.
  - Current dependency and GitHub Action versions.
  - The official OpenRouter SDK matrix.
  - Release configuration and binary and container publication.
  - Install and security documentation.
  - Protected merge evidence, `v1.0.0` publication, and plan cleanup.
- Does not own:
  - Starmap catalog generation or hosted Starport operation.
  - Moderation, preset APIs, OpenTelemetry, billing, webhooks, or SSO/RBAC.
  - Additional OpenRouter management APIs.
  - These items need separate product plans.
- Non-goals:
  - Legacy provider aliases or storage readers.
  - Compatibility branches for unpublished behavior.
  - Feature claims beyond tested v1 routes.
  - Direct changes to protected `master`.

## Promotion gate

Promote this plan to `active` only when every item holds:

- The baseline and stale pull-request inventory have exact evidence.
- Every task has measurable acceptance and exact verification commands.
- The release target is `v1.0.0`, conditional on all release gates.
- The user authorized stale-PR closure, pull requests, merges, and release.
- The goal block names this worktree, branch, constraints, and completion gate.

## Invariants

1. Starmap remains the only owner of provider, model, capability, context,
   pricing, acquisition-auth, and catalog-generation facts.
2. Starport remains the owner of inference identity, credentials, routing,
   execution, rate limits, response caching, and HTTP protocol behavior.
3. Release code adds no prelaunch legacy alias, prefix, schema reader, or
   compatibility path.
4. A release tag must identify an exact protected-`master` commit that passed
   the complete release gate.
5. The workflow must verify draft assets before its one-way publication step.
6. Every third-party GitHub Action uses a reviewed 40-character commit pin
   with a release-tag comment and provenance verification.
7. Development checks can report optional tools as `UNVERIFIED`. The release
   gate requires all three official OpenRouter SDKs.
8. Published binaries are static, version-exact, checksummed, and accompanied
   by SBOM and GitHub build-provenance attestations.
9. The release workflow never exposes repository or provider secrets in logs.
   Only publication jobs get write permissions.
10. Unrelated user work remains unchanged.

## Status ledger

| ID | Task | Status | Evidence |
| --- | --- | --- | --- |
| SPR0 | Pin the baseline, create the red release verifier and proof root, review this plan, and activate the goal. | `done` | Baseline `ff293dd`; verifier: 2 passed and 11 failed; promotion review passed; `docs/proof/starport-v1-release/spr0.md`. |
| SPR1 | Build and merge one complete v1 release candidate: clean the stale PR queue, refresh dependencies and action pins, prove all official OpenRouter SDKs, add fail-closed distribution, and repair release documentation. | `done` | PR #73; protected `master` at `ee8c34e`; CI run 31329415791 passed 10 of 10 jobs; `docs/proof/starport-v1-release/spr1.md`. |
| SPR2 | Tag protected `master` as `v1.0.0`, publish the verified immutable release and container, and read back every public identity and artifact. | `in_progress` | |
| SPR3 | Store compact closeout proof, make the task ledger terminal, remove this active plan, merge the closeout PR, and verify protected `master`. | `todo` | |

## Test matrix

| Dimension | Required cases |
| --- | --- |
| Protocol | OpenRouter chat, streaming chat, embeddings, models, endpoints, providers, errors, and authentication |
| Official clients | Current `openrouter` Python, `@openrouter/sdk` TypeScript, and `github.com/OpenRouterTeam/go-sdk` Go packages against Starport `/api/v1` |
| Platforms | Linux, macOS, and Windows tests; Linux and macOS binary builds for amd64 and arm64; Windows zip builds for amd64 and arm64 |
| Supply chain | Exact action pins, tag-from-master, static binaries, archive hashes, SBOMs, provenance attestations, immutable-release readback, and container digest readback |
| Failure | Missing SDK, stale generated module files, unpinned action, non-master tag, bad archive checksum, wrong version output, oversized binary, and publication before verification all fail closed |

## Tasks

### SPR0 Baseline and activation

- Problem: Starport has no release, ten stale bot PRs, no release workflow, and
  contradictory release documentation.
- Owning seam and paths: this plan,
  `docs/proof/starport-v1-release/spr0.md`, `docs/TASKS.md`, and
  `scripts/verify-v1-release.sh`.
- Steps:
  1. Pin local and remote `master` and inventory open PRs and releases.
  2. Add a deterministic release verifier and capture its red summary.
  3. Review scope, invariants, ledger consistency, and release authority.
  4. Promote the plan, update `docs/TASKS.md`, and activate the goal.
- Acceptance:
  - The proof records exact baseline facts.
  - The verifier is red for release gaps.
  - The active plan has one `in_progress` task.
  - The task ledger names this branch and plan.
- Fail-before: `scripts/verify-v1-release.sh` reports at least one failure on
  baseline behavior.
- Verification:

  ```text
  bash scripts/verify-v1-release.sh
  technical-writing lint --mode developer <plan> <proof> docs/TASKS.md
  git diff --check
  ```

### SPR1 Protected v1 release candidate

- Problem: the current repository cannot make a trustworthy v1 release and
  its old Dependabot queue prevents current automated maintenance.
- Owning seam and paths: `go.mod`, `go.sum`, `.github/dependabot.yml`,
  `.github/workflows/`, `.goreleaser.yaml`, `scripts/`, SDK smoke fixtures,
  CLI version tests, `README.md`, `.env.example`, `SECURITY.md`, canonical
  architecture documents, and `docs/TASKS.md`.
- Steps:
  1. Close all ten stale Dependabot PRs with an exact superseded-or-replaced
     reason. Configure grouped updates so the queue cannot fragment again.
  2. Refresh Go modules and GitHub Actions to current reviewed releases. Pin
     actions to exact commits and add online tag-provenance verification.
  3. Make Python, TypeScript, and Go official OpenRouter SDK smoke checks
     hermetic and required. Repair protocol code only for reproduced v1 gaps.
  4. Add GoReleaser and a least-privilege, draft-first release workflow for
     static archives, checksums, SBOMs, attestations, and the GHCR image.
  5. Add binary and workflow contract verifiers, release recovery evidence,
     required operator configuration, security reporting, and truthful v1
     install documentation. Remove or replace stale plan claims.
  6. Run all focused and full gates and commit the intended scope.
  7. Run pre-PR autoreview and open one ready PR.
  8. Get exact-head required checks and merge through protected `master`.
- Acceptance:
  - No stale bot PR remains.
  - Dependency and action audits have no unreviewed update.
  - All three official SDKs pass.
  - The release verifier and all required local gates pass.
  - The exact PR head passes protected checks.
  - Protected `master` contains the release candidate.
- Fail-before:
  - Baseline has ten open bot PRs.
  - The Go SDK is explicitly `UNVERIFIED`.
  - Release files are absent and action tags are mutable.
  - The release verifier is red.
- Verification:

  ```text
  bash scripts/verify-starmap-ownership.sh
  bash scripts/verify-v1-architecture.sh
  bash scripts/verify-v1-release.sh
  bash scripts/verify-action-pins.sh
  go test ./...
  go test -race ./internal/catalog ./internal/proxy ./internal/routing ./internal/providers/connectors ./internal/app ./internal/server
  go vet ./...
  make lint
  make build
  bash scripts/smoke-openrouter-sdks.sh
  goreleaser check
  goreleaser release --snapshot --clean
  actionlint
  git diff --check
  ```

### SPR2 Publish and verify v1.0.0

- Problem: merged release-ready code is not an installable public release.
- Owning seam and paths: protected `master`, tag `v1.0.0`, GitHub Release,
  GitHub Actions run, release assets, attestations, and
  `ghcr.io/agentstation/starport`.
- Steps:
  1. Confirm the release-candidate merge is the protected `master` head and
     has no later unverified commit.
  2. Create and push annotated tag `v1.0.0` at that exact commit.
  3. Monitor the release workflow through test, assembly, verification, and
     publication. Use only the bounded recovery path if the source run fails.
  4. Download public assets and independently verify hashes and versions.
  5. Verify static linkage, SBOMs, attestations, and immutable state.
  6. Verify the publisher workflow and container tags and digest.
- Acceptance:
  - One immutable non-draft `v1.0.0` release exists.
  - The tag identifies the exact protected-`master` commit.
  - Every declared asset and attestation verifies.
  - The version command reports `1.0.0`.
  - The GHCR version and latest tags identify one verified digest.
  - No failed draft or duplicate release remains.
- Fail-before: the GitHub API reports zero Starport releases and only the
  historical `v0.0.0` tag.
- Verification:
  - GitHub release and Actions API readback.
  - Archive checksum and attestation checks.
  - `go version -m` and platform binary checks.
  - GHCR digest inspection and public install smoke.

### SPR3 Closeout

- Problem: a merged and published campaign must not leave an active control
  plane or stale task status.
- Owning seam and paths: this plan, `docs/TASKS.md`,
  `docs/proof/starport-v1-release/README.md`, final PR, and protected `master`.
- Steps:
  1. Reduce task proof to a compact release closeout with exact PR, commit,
     run, release, asset, SDK, and container identities.
  2. Mark the Starport v1 release complete in `docs/TASKS.md`.
  3. Remove this plan and stale references, then run documentation checks.
  4. Open and merge the closeout PR through protected `master`.
- Acceptance:
  - Every ledger row is terminal before removal.
  - The proof README survives.
  - `docs/TASKS.md` has no active task.
  - The plan path has no live reference.
  - The closeout PR passes and merges.
  - Protected `master` matches the recorded state.
- Fail-before: this active plan and task row exist before release closeout.
- Verification:
  - Technical-writing lint and repository documentation checks.
  - `git diff --check`.
  - GitHub PR and protected-main API readback.
  - Search for this plan path and nonterminal release-task status.

## Goal

```text
Execute docs/STARPORT_V1_RELEASE_CONTROL_PLANE.md to completion. Treat this as
a whole-plan goal. Read the plan fully. Then read AGENTS.md, docs/TASKS.md,
docs/ARCHITECTURE.md, README.md, Makefile, .github/workflows/ci.yml, and
.github/dependabot.yml. Read each release and SDK script named by the plan.
Work in /Users/jack/src/github.com/agentstation/starport. Use branch
codex/starport-v1-release until the release-candidate merge. After publication,
create a new codex/ closeout branch from protected master. Chat history is not
progress state. Resume from the status ledger, execution log, and git state.
After compaction, continue from the plan and git state. Do not restart.

Keep one task in_progress. Implement at the owning seam. Capture fail-before
evidence. Run the verification commands. Commit the work under the commit
policy. Write the proof file. Append the execution log with the work commit.
Mark the task terminal with evidence. Commit that plan update. Then advance to
the next task. Decide instead of asking. Mark a wrong or satisfied task
no-action with a short reason. Record a blocker and continue with the next
eligible task.

Preserve all ten invariants. Add no prelaunch legacy behavior. Do not publish
if a required SDK or artifact is unverified. Apply the same rule to provenance,
protected-branch, and readback gates. Do not add out-of-scope product features.

Commit only intended files in coherent task commits. Use pull requests for
protected master. Run required pre-PR autoreview after final local checks and
the release-candidate commit. Stop only at a valid plans-skill stop state.
Before stopping, update the ledger, log, and status-line next action.

The goal requires a public and immutable v1.0.0 release. Its binaries, SBOMs,
attestations, and container digest must verify. All official SDK gates must
pass. Close all stale bot PRs. Merge the closeout proof and terminal task
ledger. Remove this active plan. Verify the final protected master state.
```

## Execution log

| Date | Item | Action | Evidence |
| --- | --- | --- | --- |
| 2026-08-09 | SPR0 | Established the baseline, red verifier, proof root, active plan, and goal. | Baseline `ff293dd`; release verifier reported 2 passed and 11 failed; `docs/proof/starport-v1-release/spr0.md`. |
| 2026-08-09 | SPR1 | Built, reviewed, and merged the protected v1 release candidate; advanced publication to SPR2. | PR #73; commits `2a66b22` through `ee8c34e`; CI run 31329415791 passed 10 of 10 jobs; pre-PR Sol autoreview was clean; `docs/proof/starport-v1-release/spr1.md`. |

| Date | Item | Action | Evidence |
| --- | --- | --- | --- |
| 2026-08-09 | meta | authored | Proposed plan at baseline `ff293dd50189df2f9937b19e80f29be359639995`. No implementation behavior changed. |
| 2026-08-09 | SPR0 | done; SPR1 in_progress | Baseline proof records ten bot PRs, zero releases, and verifier summary `2 passed, 11 failed`. Promotion review passed all five conditions. |
