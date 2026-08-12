# POR4 Starport protocol and test-path proof

POR4 starts from clean Starport `main` at
`1243849111f5814d0e3418ac7920900fc4a0ea98`.

The task will move protocol adapters and narrow test support to their approved
owners. It will preserve HTTP response bytes, keep storage waits local, remove
unused generic test helpers, and reject the old package paths through the
architecture verifier.

## Focused implementation evidence

OpenAI and OpenRouter codecs now live below `internal/protocol`. The repository
contract harness now lives in `internal/repotest`. Storage tests own their three
polling helpers. This task deletes the other five generic test helper exports,
which had no callers. The layout guard rejects all three old paths on current authority
surfaces and ignores archived review evidence.

SHA-256 comparisons prove that the two codecs and two protocol contract tests
are byte-identical after the move. These focused gates pass:

```text
go test -count=1 ./internal/protocol/... ./internal/repotest ./internal/storage ./internal/architecture ./internal/server
go test -race -count=1 ./internal/protocol/... ./internal/repotest ./internal/storage ./internal/architecture ./internal/server
bash scripts/verify-package-layout.sh
bash scripts/test-package-layout-verifier.sh
bash scripts/verify-v1-architecture.sh
bash scripts/verify-developer-experience.sh
bash scripts/verify-doc-links.sh
technical-writing lint AGENTS.md DEVELOPMENT.md docs/ARCHITECTURE.md docs/ARCHITECTURE_CONTROL_PLANE.md docs/CACHE_CONTROL.md docs/CONTRIBUTING.md docs/TASKS.md internal/storage/README.md --format text
```

The architecture verifier reports 12 passed and 0 failed. The developer
experience verifier reports 46 passed and 0 failed. Strict writing reports zero
diagnostics in all eight touched durable documents. The campaign verifier now
passes POR-V01 through POR-V05 and reports 5 passed and 4 failed.
