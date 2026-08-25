# AON12 Campaign close

Date: 2026-08-25
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)
Verifier: `scripts/verify-auth-onboarding.sh`, terminal at 26 conditions.

## What changed

Nothing in the product. This task makes the campaign's gate permanent.

`scripts/verify-auth-onboarding.sh` has existed since AON0, where it ran red at
`0 passed, 26 failed` and defined the target. Fourteen tasks drove it green. It
was never registered anywhere that runs on a pull request, so from AON0 to
AON11 it protected the design only for as long as somebody remembered to type
it. A gate that no workflow runs cannot report a regression; it is a script
somebody wrote once.

Two lines make it a gate:

```yaml
# .github/workflows/ci.yml, Verify release contracts
bash scripts/verify-console-modernization.sh
bash scripts/verify-auth-onboarding.sh
```

```bash
# AGENTS.md, required evidence
bash scripts/verify-console-modernization.sh
bash scripts/verify-auth-onboarding.sh
```

It joins the roster in the Release Contract job, beside the other structural
gates, and in the repository's required-evidence list, which is what an agent
reads before opening a pull request. The instructions already say to keep those
two in step; this is the entry that had drifted.

The instructions also gained a paragraph naming what the gate holds, in the
same form the OpenRouter parity gate has: the five properties, the condition
range `AON-V01` through `AON-V26`, and the fact that it runs in CI. A gate whose
name is the only thing in the instructions gets deleted by the next person who
cannot tell what it protects.

## The verifier is terminal at 26

AON10 reached `26 passed, 0 failed` and every task since has held it there.
AON14 deliberately added no 27th condition: it carries the served credential
source from the choice to the record, which is a behavioral claim four tests
hold at four seams, and a structural grep would have proved only that the
field's name appears in the source.

```console
$ bash scripts/verify-auth-onboarding.sh
Summary: 26 passed, 0 failed
```

The gate is structural. It reads the repository, not a running gateway, so it
needs no credentials, no network, and no Starmap tree — which is why it fits in
the existing job rather than needing one of its own.

Nothing in it names a plan path:

```console
$ grep -n "docs/plans\|docs/proof\|auth-and-onboarding" scripts/verify-auth-onboarding.sh
$ echo $?
1
```

That matters for AON13, which deletes the plan and the proof root. The gate
outlives the campaign that produced it.

## The full roster

Every gate in the repository's required-evidence list, run on this branch:

| Gate | Result |
| --- | --- |
| `verify-starmap-ownership.sh` | 12 passed, 0 failed |
| `verify-v1-architecture.sh` | 12 passed, 0 failed |
| `test-dependency-direction-verifier.sh` | passed |
| `verify-dependency-direction.sh` | 6 passed, 0 failed |
| `verify-catalog-driven-providers.sh` | 19 passed, 0 failed |
| `verify-package-layout.sh` | passed |
| `verify-readme-quickstart.sh` | PASS |
| `verify-v1-release.sh` | 16 passed, 0 failed |
| `verify-release-workflow.sh` | PASS |
| `verify-developer-experience.sh` | 47 passed, 0 failed |
| `verify-doc-links.sh` | PASS |
| `test-doc-link-verifier.sh` | PASS |
| `verify-openrouter-parity.sh` | 16 passed, 0 failed |
| `verify-console-modernization.sh` | 21 passed, 0 failed |
| `verify-auth-onboarding.sh` | **26 passed, 0 failed** |
| `verify-catalog-performance.sh` | 20 passed, 0 failed |
| `verify-action-pins.sh` | 16 references match their release tags |
| `benchmark-overhead.sh` | PASS, p50=0ms p99=0ms over 200 requests |
| `go test ./...` | 44 packages ok, 0 failures |
| `go vet ./...` | clean |
| `make lint` | 0 issues |
| `make build` | clean |
| `smoke-openrouter-sdks.sh` | PASS Python, PASS TypeScript, PASS Go |

Nothing is `UNVERIFIED`. All three optional SDK runners were installed and ran.
`verify-catalog-driven-providers.sh` read the local `../starmap` tree, which is
the same published version CI resolves.

Three gates are absent by design. `verify-release-binaries.sh`,
`verify-release-archives.sh`, and `verify-homebrew-cask.sh` read a goreleaser
`dist` tree, so the Release Snapshot job owns them. The instructions already
record that split; this run does not change it.

## Fail-before

Not applicable. This task records a terminal state and registers a gate that
was already green. The one checkable claim is the registration itself:

```console
$ git grep -c verify-auth-onboarding f0ac004 -- .github/workflows AGENTS.md
$ echo $?
1
```

The gate appeared in no workflow and in no evidence list on the AON11 head.

## Evidence

The table above. The campaign's own gate, the one this task exists to register,
reports:

```console
$ bash scripts/verify-auth-onboarding.sh | tail -1
Summary: 26 passed, 0 failed
```

No code changed. No test was weakened or removed. No verifier condition was
added, removed, or relaxed.
