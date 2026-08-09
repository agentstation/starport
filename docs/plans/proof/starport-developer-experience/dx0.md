# DX0 baseline proof

Date: 2026-08-09

## Baseline

- Repository: `agentstation/starport`
- Baseline branch: `master`
- Baseline commit: `a62fc289ed59c058013ee0ff5a411dd083e8454c`
- Plan branch: `codex/starport-developer-experience`
- Plan work commit: `fb5a821c87e0a7f3ccd72268e403526e6a959bc4`
- Pull request: `https://github.com/agentstation/starport/pull/76`
- Merge commit: `7be7b08f54e816782b2098eab5a7f90dbc1d352d`
- Runtime behavior changes: none

## Fail-before verifier

Command:

```bash
bash scripts/verify-developer-experience.sh
```

Result:

```text
Summary: 1 passed, 38 failed
```

The command exited with status 1. The 39 fixed checks have one passing
baseline condition and 38 missing target conditions.

## Plan review

- The plan has 11 stable ledger rows.
- DX0 is the only `in_progress` row.
- The plan has a final cleanup task.
- The outcome, architecture, scope, ledger, tasks, goal, and execution log are present.
- The overview and status ledger start within the first 120 lines.
- The file has 278 lines, which is below the 500-line plan limit.
- Each implementation task names an owning seam, fail-before evidence, acceptance, and commands.
- The goal block names the repository, branch, required files, commit policy, and completion gate.

## Writing checks

Commands:

```bash
technical-writing glossary check
technical-writing lint GLOSSARY.md docs/plans/README.md \
  docs/plans/starport-developer-experience-plan.html --format text
```

Results:

```text
PASS: GLOSSARY.md (16 term(s), 0 error(s))
PASS: 3 file(s), 0 diagnostic(s)
```

Human review confirmed the source for each claim, exact identifiers, safe
procedure order, glossary terms, task ownership, and no unsupported guarantee.

## File checks

Commands:

```bash
bash -n scripts/verify-developer-experience.sh
git diff --check
```

Both commands exited with status 0 before the work commit.

## Pull request gate

All 10 CI checks passed before merge:

- Action Pin Provenance
- Build
- Lint
- OpenRouter SDK Compatibility
- Release Contract
- Release Snapshot
- Security Scan
- Test on macOS
- Test on Ubuntu
- Test on Windows

Pull request #76 merged on 2026-08-09 at 20:40:46 UTC.
