# SPR0 Baseline Evidence

Date: 2026-08-09

- Local `master`, `origin/master`, and the GitHub default branch all identified
  `ff293dd50189df2f9937b19e80f29be359639995`.
- The local worktree was clean before branch creation.
- GitHub reported zero Starport releases and one historical `v0.0.0` tag.
- GitHub reported ten open Starport pull requests. Dependabot authored all
  ten against baseline `bf6b09e1136913da87d815abfa94b975927041a0`.
- Five pull requests had conflicts. Five were mergeable, but their checks
  failed. No human implementation pull request was open.
- Pull requests 57, 65, 66, and 68 were already superseded by current
  `master`. Pull requests 54, 55, 56, 63, 64, and 67 were stale update forms.
  They still needed a current consolidated audit.
- Starport used the current published Starmap application version, `v0.3.0`,
  without a local module replacement.
- The latest protected-`master` CI run passed at the baseline commit.
- The repository had no GoReleaser configuration, release workflow, or
  `SECURITY.md`. The required `.env.example` already existed.
- `docs/PLAN.md` contradicted canonical architecture and task status by
  reporting broken authentication, missing caching, and missing rate limits.
- `docs/ARCHITECTURE.md` referred to an active v1 plan and proof files that did
  not exist.
- The SDK runner could test installed Python and TypeScript packages, but it
  always reported the official Go SDK as `UNVERIFIED`.
- Current OpenRouter documentation lists three official client SDKs:
  `openrouter` for Python, `@openrouter/sdk` for TypeScript, and
  `github.com/OpenRouterTeam/go-sdk` for Go.

SPR0 added the intentionally red release verifier. Its first run returned:

```text
Summary: 2 passed, 11 failed
```

The promotion review passed all five conditions. The plan owns one outcome,
keeps adjacent product features out of scope, and defines exact release and
cleanup gates.
