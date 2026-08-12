# POR5 Starport state and cache path proof

POR5 starts from clean Starport `main` at
`4dadb2912555035d5a8699f8a717b79db0876241`.

The task moves provider state to `internal/providers/state` and response cache
to `internal/response/cache`. The Go imports will use the approved
`providerstate` and `responsecache` aliases where those names aid readability.
The task changes package ownership only. It will not change durable records or
protocol behavior.

## Fail-before evidence

The baseline contains 32 current source, test, script, and document matches for
the two old paths or package declarations. Both old directories exist, and Go
reports package names `providerstate` and `responsecache`.

## Focused implementation evidence

Provider state now lives in `internal/providers/state` with package name
`state`. Response cache now lives in `internal/response/cache` with package
name `cache`. Callers use the explicit aliases `providerstate` and
`responsecache`. The layout verifier rejects both old paths and package names.

These checks pass with normal, uncapped Go scheduling:

```text
go test -count=1 ./internal/providers/state ./internal/response/cache ./internal/proxy ./internal/server ./internal/app ./internal/architecture
go test -race -count=1 ./internal/providers/state ./internal/response/cache ./internal/proxy ./internal/server ./internal/app ./internal/architecture
bash scripts/verify-package-layout.sh
bash scripts/test-package-layout-verifier.sh
bash scripts/verify-v1-architecture.sh
bash scripts/verify-starmap-ownership.sh
bash scripts/verify-doc-links.sh
technical-writing lint AGENTS.md GLOSSARY.md docs/ARCHITECTURE.md docs/OPERATOR-GUIDE.md docs/TASKS.md --format text
```

The architecture verifier reports 12 passed and 0 failed. The ownership
verifier reports 12 passed and 0 failed. Strict writing reports zero
diagnostics in all five touched durable documents. The campaign verifier now
passes POR-V01 through POR-V06 and reports 6 passed and 3 failed.

## Full local verification

The complete repository checks pass with normal, uncapped Go scheduling:

```text
make verify
go test ./...
go vet ./...
make lint
make build
bash scripts/smoke-openrouter-sdks.sh
```

`make verify` reports Starmap ownership 12/12, architecture 12/12, release
15/15, release workflow success, developer experience 46/46, documentation
link success, and documentation-link regression success. All 41 Go packages
pass. Vet reports no diagnostics. Lint reports zero issues. The build completes.
All seven SDK smoke cases pass: raw HTTP chat, stream, models, embeddings,
Python, TypeScript, and Go.

## Commit and review

Starport commit `a26f825` contains the verified POR5 diff. The automatic review
selected Claude Opus 5, but its connection stopped on a local self-signed
certificate. The fallback GPT-5.6-sol review completed in isolation and found
no actionable defect. TruffleHog also reported a clean changed-content scan.

Starport PR [#107](https://github.com/agentstation/starport/pull/107) publishes
exact reviewed head `a26f825` for the hosted gates.

All 10 hosted checks passed on that exact head. PR #107 squash-merged as
`9bbeccefadce3c2afac41f6dc60135fba8b71e74`. Protected `main` is clean and
matches `origin/main`. The campaign verifier reports 6 passed and 3 failed.
