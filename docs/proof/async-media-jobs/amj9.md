# AMJ9 Documentation and the terminal gate

## Outcome

`scripts/verify-async-media-jobs.sh` reports `Summary: 18 passed, 0 failed`. CI
runs it beside the other release-contract gates. `AGENTS.md` names it and its
terminal count. `docs/OPERATOR-GUIDE.md` has a `## Video Jobs` section.

## A gate no workflow runs cannot report a regression

`AMJ-V18` was the last open condition, and it checks two things at once: that
`verify-async-media-jobs.sh` appears under `.github/workflows`, and that it
appears in `AGENTS.md`. Either one alone leaves a hole.

A gate named only in `AGENTS.md` depends on a person reading the list before a
pull request. A gate run only in CI is invisible to the agent that has to keep
it passing. The next task that touches a job route then learns about the gate
from a red check rather than from the evidence list.

The script joins the `Verify release contracts` step, which is where every
other verifier already runs. That step shares one Starmap checkout with the
ownership and catalog gates, so a new line costs no extra setup.

## An operator has to size the storage before the first request

The gateway holds video bytes. Nothing in the operator guide said so, said how
long, or said where.

`## Video Jobs` follows the shape of `## File Storage` above it, because the
two sections answer the same questions about two stores. It names the five
routes and the one scope. It names the five job states and which three are
terminal. It names the three `STARPORT_JOBS_` settings with their defaults, the
one-hour poll budget, and the outstanding job bound.

Two facts in it are not obvious from the settings alone. Video bytes go to the
backend that `STARPORT_FILES_BACKEND` selects. A deployment on the filesystem
default therefore serves a video from the node that stored it and not from any
node. The 24-hour window is also shorter than the 30 days a file gets. An
operator reading both windows needs the reason, or one of them looks like a
mistake.

## Why one scope covers five routes

The table lists `videos:write` against the two reads as well as the three
writes. An operator reading a route table expects a read scope. The section
states the reason under it. Only the account that submitted a job can read it,
so a `videos:read` scope would name a capability no caller can hold separately.

## What the prose lint covers

The guide carried 33 diagnostics before this task and carries 33 after. The `##
Video Jobs` section adds none. `AGENTS.md` carried 11 and carries 11.

The check is the baseline rather than zero, because this task does not own the
existing prose in either file. Rewriting it would put unrelated changes in a
documentation pull request.

## Evidence

```
bash scripts/verify-async-media-jobs.sh          Summary: 18 passed, 0 failed
bash scripts/verify-starmap-ownership.sh         passed
bash scripts/verify-v1-architecture.sh           passed
bash scripts/test-dependency-direction-verifier.sh passed
bash scripts/verify-dependency-direction.sh      passed
bash scripts/verify-catalog-driven-providers.sh  passed
bash scripts/verify-package-layout.sh            passed
bash scripts/verify-readme-quickstart.sh         passed
bash scripts/verify-v1-release.sh                passed
bash scripts/verify-release-workflow.sh          passed
bash scripts/verify-developer-experience.sh      passed
bash scripts/verify-doc-links.sh                 PASS documentation links
bash scripts/test-doc-link-verifier.sh           passed
bash scripts/verify-openrouter-parity.sh         passed
bash scripts/verify-console-modernization.sh     passed
bash scripts/verify-auth-onboarding.sh           passed
bash scripts/verify-console-session-grants.sh    passed
bash scripts/verify-model-modalities.sh          passed
bash scripts/verify-files-api.sh                 passed
bash scripts/verify-catalog-performance.sh       passed
bash scripts/verify-action-pins.sh               passed
go build ./...                                   clean
go vet ./...                                     clean
go test ./...                                    exit 0
make lint                                        0 issues
make build                                       ok
```

This task did not run `bash scripts/benchmark-overhead.sh` or `bash
scripts/smoke-openrouter-sdks.sh`. Mark both UNVERIFIED. This task changes no
Go source, so nothing moved in the request path or the SDK surface.
