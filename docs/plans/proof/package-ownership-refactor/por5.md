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
