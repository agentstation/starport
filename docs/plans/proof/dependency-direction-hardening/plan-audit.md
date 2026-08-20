# Dependency direction hardening plan audit

Date: 2026-08-19

Reviewed commit: `b337735f6444496d66f069e27861b171b57847cb`

Verdict: approved for a plan-only pull request.

## Independent review

The manual autoreview used the `cross-lab` profile with a P1 threshold and no
review cache. It selected these reviewers:

- GPT-5.6 Sol with high reasoning
- Claude Opus 5 with high reasoning

TruffleHog found no credential. Both reviewers reported zero finding. The panel
reported `patch is correct (0.9)`.

## Deterministic plan checks

The plan passed these checks:

- 10 required sections exist in the required order.
- Nine ledger IDs match nine task-block IDs.
- All nine tasks are `todo`, and no task is `in_progress`.
- The goal block contains no template placeholder.
- The 16 checked HTML tag types show balanced open and close counts.
- The plan names verifier ranges SM-D01 through SM-D08 and SP-D01 through
  SP-D06.
- `git diff --check` passed.
- The technical-writing linter checked four durable files with zero diagnostic.

## Evidence checks

The audit repeated the source measurements from the analysis:

- Starmap has eight direct import relationships from five catalog-tree packages
  to four repository-private packages.
- Starport proxy directly imports two concrete adapter packages.
- Starport app directly imports two Starmap acquisition-option packages.
- Neither repository has an open pull request.
- Both implementation branch names pass `git check-ref-format`.
- Both named implementation worktree paths are available.

Focused Starport tests passed for four packages:

- `internal/proxy`
- `internal/app`
- `internal/catalog`
- `internal/architecture`

Focused Starmap tests passed for 10 packages:

- `pkg/catalogs` and its six current child packages
- `internal/catalog/authority`
- `internal/sources/payload`
- `internal/embedded`

The audit found every named Make target in its repository. Starmap has
`verify`, `release-check`, `release-snapshot`, `catalog-generation-check`, and
`embedded-catalog-budget-check`. Starport has `lint`, `build`, `release-check`,
and `release-snapshot`.

## Claim review

The source supports all four findings. The proposed package moves give each new
seam at least two real consumers. The plan preserves the current immutable
generation, runtime lease, protocol, storage, and credential boundaries.

The plan rejects three changes that the evidence does not require. It does not
split the catalog domain, remove local acquisition, or replace runtime leases.
The plan also rejects name-only churn and compatibility aliases.

## Risk review

The main Starmap risk is an unintended public or byte-contract change while
moving policy and payload code. DDH1 and DDH2 require fixed-input contract tests,
consumer tests, release snapshots, and a no-alias rule.

The main Starport risk is a hidden concrete method dependency in proxy or app.
DDH5 and DDH6 require focused use-case, runtime, refresh, and architecture tests
before the full repository gates.

The two implementation pull requests are independent. The plan needs no release
tag or cross-repository module update. This keeps rollback and review local to
each repository.

## Human conformance review

The audit checked facts against source and command output. It preserved unknowns
and exact identifiers. It found no unsupported performance, security, or
compatibility claim. All commands and paths resolve at the pinned baselines.

Result: conformant.
