# CAT8.1 The console catalog surface

The console now states the catalog in one place. A chip sits in the shell, so
every route carries it, and it opens one panel that holds the whole catalog
story. Two surfaces told a partial story before this task. Both are gone: the
freshness bar above the model list, and the catalog card on Overview.

The chip holds one element for each status concept a reader can act on.
These concepts never share a glyph: usability, authorization, freshness,
degradation, fallback, a source this gateway could not read, and work in
flight. A reader repairs each one differently.

## Fail before

Every condition below failed on the base commit `9081ca4`, because the named
test file did not exist. The Starmap verifier reported
`56 passed, 12 failed, 0 unverified` on that commit.

| Condition | State before |
| --- | --- |
| CAT-V50 | `console/src/components/shell/CatalogChip.test.tsx` absent |
| CAT-V52 | `console/src/components/shell/CatalogPanel.test.tsx` absent |
| CAT-V53 | `console/src/components/shell/Shell.catalog.test.tsx` absent |
| CAT-V54 | `console/src/components/shell/CatalogPanel.keyboard.test.tsx` absent |
| CAT-V55 | `console/src/components/shell/CatalogChip.unauthorized.test.tsx` absent |
| CAT-V63 | `console/src/components/models/ModelDetail.lifecycle.test.tsx` absent |
| CAT-V68 | `console/src/components/shell/CatalogSummary.lifecycle.test.tsx` absent |

## What this task built

**The shell owns one read.** `console/src/components/shell/CatalogSummary.tsx`
holds the whole read lifecycle and nothing else. It owns the summary query, the
admin status query, the refresh mutations, and the session identity the query
keys carry. Every cadence rule is a pure exported function, so a test states the
rule instead of waiting for a timer.

**One chip.** `console/src/components/shell/CatalogChip.tsx` is presentational.
It takes the two reads as plain props and draws one element per concept. A
healthy catalog deserves almost no attention, so a healthy chip is a dot, the
word Catalog, a short generation, and an age. A reader with `models:read` alone
sees no admin pill and no activity icon, because the admin status never answered
for that session.

**One panel.** `console/src/components/shell/CatalogPanel.tsx` answers seven
questions in this order:

1. What does this gateway serve?
2. What moved?
3. What are the layers below the served catalog?
4. How far does the publication chain reach?
5. When does the next update land?
6. Which sources answered?
7. What can an operator do now?

Two sections belong to every reader who reads models. The other five need an
admin session. A reader without one gets one sentence that states the scope
rather than an error.

**The changes list moved into the shell.**
`console/src/components/models/ChangesPanel.tsx` became
`console/src/components/shell/CatalogChanges.tsx`. Its sheet wrapper is gone,
because the catalog panel is now the only sheet, and the body exports as
`CatalogChangesSection`.

**The model detail separates five facts.**
`console/src/components/models/ModelDetail.tsx` gives lifecycle, availability,
credential, circuit, and routing a cell each. It also names the catalog
generation the offerings came from. The circuit cell no longer falls back to the
availability. An unknown circuit is not an availability, and a reader who
confused the two would repair the wrong thing.

**No console age rule.** The seven-day `STALE_AFTER_SECONDS` constant is gone
with `FreshnessBar.tsx`. The gateway grades the age and the console reads the
grade, so one deployment policy cannot disagree with itself across two screens.

## What this task deleted

| File | Reason |
| --- | --- |
| `console/src/components/models/FreshnessBar.tsx` | the chip states freshness on every route, not on one |
| `console/src/components/models/FreshnessBar.test.tsx` | its subject is gone |
| `console/src/components/overview/CatalogCard.tsx` | the panel holds the catalog detail |
| `console/src/components/overview/CatalogCard.test.tsx` | its subject is gone |

## Tests

| Condition | Test file | Tests |
| --- | --- | --- |
| CAT-V50 | `console/src/components/shell/CatalogChip.test.tsx` | 8 |
| CAT-V52 | `console/src/components/shell/CatalogPanel.test.tsx` | 6 |
| CAT-V53 | `console/src/components/shell/Shell.catalog.test.tsx` | 5 |
| CAT-V54 | `console/src/components/shell/CatalogPanel.keyboard.test.tsx` | 3 |
| CAT-V55 | `console/src/components/shell/CatalogChip.unauthorized.test.tsx` | 3 |
| CAT-V63 | `console/src/components/models/ModelDetail.lifecycle.test.tsx` | 5 |
| CAT-V68 | `console/src/components/shell/CatalogSummary.lifecycle.test.tsx` | 6 |

## Mutation evidence

Each mutation below went into the source, the named test file ran, and the
mutation came out again. Every one of them turned a passing file red.

| Mutation | Condition | Failure |
| --- | --- | --- |
| `CatalogChip.tsx`: grade `warn` as fresh rather than stale | CAT-V50 | 1 of 8 failed |
| `CatalogPanel.tsx`: label every hop `direct` | CAT-V52 | 2 of 6 failed |
| `CatalogChip.tsx`: draw the small-screen control at 32 px | CAT-V53 | 1 of 5 failed |
| `CatalogChip.tsx`: handle Enter and drop Space | CAT-V54 | 1 of 3 failed |
| `CatalogSummary.tsx`: treat only a 401 as a refusal | CAT-V55 | 3 of 3 failed |
| `ModelDetail.tsx`: fall back to the availability in the circuit cell | CAT-V63 | 1 of 5 failed |
| `CatalogSummary.tsx`: poll the summary while the page is hidden | CAT-V68 | 1 of 6 failed |

## Design decisions this task made

The design record asks for facts the API that CAT8 shipped does not carry. The
task changed no Go code, so each gap below is a console decision, and each one
states the gap rather than inventing a value.

| Gap in the shipped API | What the console does |
| --- | --- |
| no `freshness.verdict` field | the chip maps the grade: `current` is fresh, `warn` and `critical` are stale, anything else is unknown |
| no `policy_age_seconds`, `max_age_seconds`, `reference`, or `evaluated_at` | the chip states the generation age alone and claims no policy |
| the safe route carries no `catalog_digest`, `payload_checksum`, `catalog_sequence`, or `availability_revision` | the Identity section reads them from the admin status, so only an admin sees them |
| no `counts.offerings` | the layers figure omits an offering count |
| neither route names the embedded baseline generation | the baseline node reads "generation not reported", or "serving now" when the runtime source kind is `embedded` |
| no configured maximum hop count | the hop header states the observed count alone, with no limit beside it |
| no per-provider retained last-known-good record | the Providers section renders the source observations of the snapshot |

Four more decisions the brief did not settle:

- **The query key is `catalog-summary`, not `catalog/summary`.** `queries.test.ts`
  requires a unique first key segment per factory, and a shared `catalog`
  segment breaks that rule.
- **Admin-ness is one probe, not a claim.** The console asks the admin status
  once, gated on a summary that answered. A session the gateway refused sends
  exactly one catalog request in total.
- **The chip handles Enter and Space itself and prevents the default.** The
  console holds no `@testing-library/user-event` dependency. A button that also
  let the browser synthesize a click would toggle twice. It would close what the
  key just opened.
- **Only a source this gateway read itself raises the source pill.** An
  upstream problem belongs to the hop chain. A reader cannot repair an upstream
  source from here.

## Commands

Every command ran with `GOTOOLCHAIN=go1.26.6`, from the Starport worktree,
except the last one, which ran from the plan worktree
`/Users/jack/src/github.com/agentstation/starmap-catalog-publisher` with
`CATALOG_DISTRIBUTION_STARPORT_ROOT` pointed at this worktree.

| Command | Result |
| --- | --- |
| `pnpm install --frozen-lockfile` | already up to date |
| `pnpm check` | build, typecheck, and 438 tests in 70 files pass |
| `make lint` | 0 issues |
| `make format-check` | no output |
| `make test` | pass |
| `make build` | pass |
| `bash scripts/verify-console-modernization.sh` | 21 passed, 0 failed |
| `bash scripts/verify-console-polish.sh` | 48 passed, 0 failed |
| `bash scripts/verify-console-session-grants.sh` | 16 passed, 0 failed |
| `bash scripts/verify-auth-onboarding.sh` | 26 passed, 0 failed |
| `bash scripts/verify-credential-sharing.sh` | 23 passed, 0 failed |
| `bash scripts/verify-enterprise-readiness.sh` | 33 passed, 0 failed |
| `bash scripts/verify-catalog-performance.sh` | 20 passed, 0 failed |
| `bash scripts/verify-model-modalities.sh` | 26 passed, 0 failed |
| `bash scripts/verify-reranking.sh` | 22 passed, 0 failed |
| `bash scripts/verify-files-api.sh` | 22 passed, 0 failed |
| `bash scripts/verify-async-media-jobs.sh` | 18 passed, 0 failed |
| `bash scripts/verify-document-parser.sh` | 20 passed, 0 failed |
| `bash scripts/verify-openrouter-parity.sh` | 17 passed, 0 failed |
| `bash scripts/verify-v1-architecture.sh` | 11 passed, 1 failed (V01 only) |
| `bash scripts/verify-doc-links.sh` | pass |
| `bash scripts/verify-catalog-distribution.sh` | 65 passed, 3 failed, 0 unverified |

`V01` fails for the reason CAT8 recorded: `go.mod` holds
`replace github.com/agentstation/starmap => …/starmap-catalog-publisher`. This
task changed no Go file and no module file.

`make lint` needs a private `GOLANGCI_LINT_CACHE`. A cache another worktree
shares reports that worktree's paths, which no run in this worktree can repair.

The Starmap verifier still fails three conditions, and CAT9.1 owns all three.
They are the Starport document conditions `CAT-V56`, `V57`, and `V58`. Every
condition CAT8.1 owns passes.

The first verifier run of this task reported `63 passed, 5 failed`. The two
conditions that changed between the runs are `CAT-V59` and `CAT-V64`, which the
plan worktree owns. A concurrent task moved that worktree, and no change in this
worktree can move either condition.

## Repairs outside the task

`scripts/verify-console-session-grants.sh` reserves the words *sign in* for the
identity grant. Two comments this task wrote used the phrase for the general
idea of a session, so `CSG-V16` failed. Both now name a console session
directly, which reads better and leaves the reserved words where they belong.
