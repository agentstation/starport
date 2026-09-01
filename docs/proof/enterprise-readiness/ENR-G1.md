# ENR-G1 Team budgets

## What shipped

A team now carries an optional spend budget. The gateway meters it as
a third population beside the account and the key. The vocabulary
lives in `internal/limits`. `TeamBudget` holds a nano-USD limit and a
fixed UTC interval.

`ScopeTeam` names the holder. `TeamBudgetRule`
projects the budget into the `BudgetRule` list. The account and key
budgets fill the same list. The team rule joins that list, so every
meter runs. It never replaces or tightens the other two rules. A team budget
bounds spend only.

`internal/identity` gives `Team` the budget field. Teams persist as
JSON documents, so the field ships with no migration. `Team.Validate`
refuses a non-positive limit or an unknown interval on create and on
update.

Attribution rides the API key. `apikey.APIKey` and `IssueRequest`
carry `TeamID`. The admin key-mint route accepts and returns
`team_id`. `requestctx.GetTeamID` derives the team from the API key
model already in the request context. That is one derivation point
with no new middleware.

Every proxy request type carries the team into `internal/proxy`, and
`baseUsageRecord` stamps it onto the usage
record. The usage repository adds `TeamScope`. Its `accumulate` step
counts each attributed record into the team counter across every
account the team reaches. The guardrail moderation call and batch
lines carry the same attribution, so neither surface slips past the
meter.

Enforcement sits in the existing `enforceBudgets` middleware. The
server reads the team budget through a late-bound identity reader.
`ErrTeamNotFound` answers nil silently, so a deleted team never takes
its keys' traffic down. A read failure answers nil with a loud log,
which is the same fail-open rule a broken usage read follows (D6). An
exhausted team meter refuses with 402. It reports itself in the
`X-Starport-Budget-Spend-*` headers with scope `team`, and the
`budget.exhausted` event payload names the team. The batch admission
governor applies the same rule.

Operators govern the budget over `PUT /api/v1/admin/teams/{team_id}`.
The body states the team's whole mutable surface, so an omitted budget
clears it. The update audits as `team.update` with revision CAS.
`POST /api/v1/admin/teams` accepts the budget at creation. The console
teams page shows each team's budget. The detail panel edits it in
dollars against the new route.

Out of scope: the asynchronous video job path records no team
attribution, so the team meter counts synchronous and batch spend. The
job accounting boundary owns that gap.

## Acceptance evidence

- `go test -race` passes on `internal/server`, `internal/limits`,
  `internal/identity`, `internal/usage`, `internal/apikey`,
  `internal/proxy`, and `internal/server/controllers`.
- `TestTeamBudgetExhaustionRefusesEveryTeamKey` proves two keys on one
  team both draw the 402 with scope `team`. The event payload carries
  the `team_id`.
- `TestTeamBudgetLeavesTeamlessKeysAlone` proves a teamless key on the
  same gateway passes.
- `TestTeamBudgetJoinsKeyBudgetAndTightestReports` proves the team
  rule joins the key rule. The tightest meter owns the headers.
- `TestTeamBudgetReadErrorFailsOpen` proves D6.
- `TestTeamCounterSumsEveryAttributedKey` proves the team counter sums
  keys across accounts. The account counter stays unchanged.
- `TestChatUsageRecordCarriesTeamAttribution` proves the usage record
  carries the team. A teamless request carries none.
- `TestTeamRepositoryBudgetRoundTrip` proves the budget persists,
  clears, and refuses a bad limit through the repository.
- Console: 8 `TeamDetailPanel` tests pass. They cover the save in
  nano-USD, the clear by omission, and a refused negative amount.
- `tsc --noEmit` and the full vitest suite (213 tests) pass.
- `bash scripts/verify-enterprise-readiness.sh` reports 26 passed.
  ENR-V25 and ENR-V26 are green. `verify-credential-sharing.sh` stays
  at 23.
- The full gate battery, `go test ./...`, `go vet ./...`, `make lint`,
  `make build`, and both smoke scripts pass.
- `benchmark-overhead.sh` reports p50=0ms and p99=0ms.
- The architecture import graph now lets `internal/identity` read
  `internal/limits`. A team carries a budget the way an account or a
  key does. The rule's comment records that reason.

## Commands

```bash
go test -race ./internal/server/ ./internal/limits/ ./internal/identity/ \
  ./internal/usage/ ./internal/apikey/ ./internal/proxy/ \
  ./internal/server/controllers/
bash scripts/verify-enterprise-readiness.sh
cd console && npx vitest run && npx tsc --noEmit
```
