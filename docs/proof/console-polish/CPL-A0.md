# CPL-A0 Baseline

The verifier `scripts/verify-console-polish.sh` reports 0 passed, 48 failed
and exits with status 1. No production file changed. The reading below dates
from 2026-09-01.

## Baselines

| Item | Value |
| --- | --- |
| starport commit | `6db57d8` Close the enterprise-readiness campaign (ENR-Z3) (#331) |
| Entry chunk | `index-h5DvLrsX.js`, 282.09 kB raw, 91.71 kB gzip |
| Console tests | 213 tests in 33 files, all passing |
| Check command | `pnpm -C console check` (build, typecheck, test), exit 0 |

The plan invariant I8 bounds the entry chunk at 130 kB gzip against the
91.71 kB reading above.

## Probe table

CPL-A0 probed every grep term at the baseline before the verifier took it.
Two candidates collided with existing code, and the verifier uses a
narrower term for each. The file `format.test.ts` exists at baseline, so
V22 probes for `formatPricePair` inside it. The directory
`components/settings/` exists at baseline, so V32 probes for the section
names inside it.

| Condition | Term | Path | Baseline |
| --- | --- | --- | --- |
| V01 | `queryOptions` | `lib/queries.ts` | file absent |
| V02 | `defaultOptions` | `main.tsx` | absent |
| V03 | `defaultPreload` | `main.tsx`, `routes/__root.tsx` | absent |
| V04 | `defaultNotFoundComponent` | `main.tsx`, `routes/__root.tsx` | absent |
| V05 | `queryFn` count equals `signal` count | `lib/queries.ts` | file absent |
| V06 | `validateSearch` in at least 11 route files | `routes/` | 4 files |
| V07 | file | `console/components.json` | absent |
| V08 | `"@base-ui/react"` | `console/package.json` | absent |
| V09 | `data-theme` | `console/index.html` | absent |
| V10 | `Skip to` | `Shell.tsx` | absent |
| V11 | `dialog.tsx` present, `Modal.tsx` absent | `components/ui/` | inverse |
| V12 | `sheet.tsx` present, `SidePanel.tsx` absent | `components/ui/` | inverse |
| V13 | `DestructiveButton` | `ui/Form.tsx` | absent |
| V14 | `"sonner"` | `console/package.json` | absent |
| V15 | `budgetExhausted` | `lib/api.ts` | absent |
| V16 | zero `setNotice(` lines | `console/src` | 14 files |
| V17 | files | `ui/tooltip.tsx`, `ui/dropdown-menu.tsx` | absent |
| V18 | `aria-activedescendant` | `chat/ModelPicker.tsx` | absent |
| V19 | file | `ui/skeleton.tsx` | absent |
| V20 | `aria-invalid` | `ui/Form.tsx` | absent |
| V21 | `aria-live` | `chat/Messages.tsx` | absent |
| V22 | `formatPricePair` | `lib/format.test.ts` | absent |
| V23 | `export function formatPricePair` | `lib/format.ts` | absent |
| V24 | file | `ui/DataTable.tsx` | absent |
| V25 | `DataTable` | `routes/audit.tsx` | absent |
| V26 | `DataTable` | `providers/ProviderDetail.tsx` | absent |
| V27 | at least 7 `scope="col"` lines | the seven plain-table files | 0 |
| V28 | `syncId` | `ui/Chart.tsx`, `routes/usage.tsx` | absent |
| V29 | `ReferenceDot` | `ui/Chart.tsx`, `routes/usage.tsx` | absent |
| V30 | `TestSystemInfoReportsBuildVersion` | `internal/server/controllers` tests | absent |
| V31 | `admin/webhooks` | `internal/server` | absent |
| V32 | `Observability` and `Guardrails` | `components/settings/` | absent |
| V33 | file | `ui/BudgetLine.tsx` | absent |
| V34 | `TestTeamReadIncludesBudgetUsage` | `internal/server` tests | absent |
| V35 | files | `DeleteTeamModal.tsx`, `DeleteAccountModal.tsx` | absent |
| V36 | `until` | `routes/audit.tsx` and `lib/api.ts` | absent |
| V37 | `guardrail_verdict` | `lib/api.ts` | absent |
| V38 | `ndjson` | `routes/usage.tsx` | absent |
| V39 | `X-Semantic-Cache` | `console/src` | absent |
| V40 | file | `jobs/BatchesPanel.tsx` | absent |
| V41 | `Audit log` | `Shell.tsx` | absent, reads `Audit Log` |
| V42 | `ModelPicker` | `routes/keys.tsx` | absent |
| V43 | file | `routes/chat.test.tsx` | absent |
| V44 | `/health/live` and `/health/ready` | `routes/docs.tsx` | absent, reads `/health` |
| V45 | file | `routes/docs.test.tsx` | absent |
| V46 | file | `lib/useMediaQuery.ts` | absent |
| V47 | `verify-console-polish.sh` | `.github/workflows` | absent |
| V48 | `verify-console-polish.sh` | `AGENTS.md` | absent |

Console paths are relative to `console/src/components` or `console/src`.

## Commands

```
bash scripts/verify-console-polish.sh   # Summary: 0 passed, 48 failed, exit 1
shellcheck scripts/verify-console-polish.sh   # clean
pnpm -C console check   # exit 0, 213 tests
```
