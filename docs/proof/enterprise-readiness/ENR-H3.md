# ENR-H3 proof: agent surface

Status date: 2026-09-01.

## What shipped

A coding agent had no offline way to answer catalog questions and no
packaged instructions for operating this gateway. This task adds two
catalog verbs and one embedded skill, following the pattern
`stripe agent setup` set. The CLI carries the skill, so the installed
copy always matches the installed commands. An MCP server stays
deferred. The plan records the re-open conditions.

## The pieces

- `cmd/starport/models.go`: `starport models search <query>` matches
  every query word against model IDs, names, and authors. It answers a
  compact summary. `models show <id>` answers the full projection for
  one exact ID: prices, capabilities, modalities, and every routable
  offering. Both accept `--json` and read the embedded
  catalog offline. Both reuse the `view.ModelInfo` field names, so no
  second model vocabulary exists.
- `cmd/starport/agent.go`: `starport agent setup` installs the embedded
  skill into a shared skills root. An explicit `--dir` wins, then
  `$AGENTS_HOME/skills`, then `~/.agents/skills`. `--print` writes the
  skill to standard output instead.
- `skills/starport/SKILL.md` and `skill.go`: the canonical skill lives
  beside the CLI as a small Go package, because `go:embed` cannot reach
  a parent path. The skill teaches install, gateway start, client
  base URLs, catalog questions, and diagnosis. It keeps the two
  credential kinds separate, and it passes the technical-writing lint
  with zero diagnostics.
- `internal/cli/app.go`: `Dependencies.ExtraCommands` lets the process
  boundary register build-bound commands. The command tree in
  `internal/cli` stays free of embedded-catalog and embedded-skill
  behavior.
- `cmd/starport/run.go` and `cmd/starport/README.md`: `processCommands`
  registers both commands, and the README names the new ownership.
- `docs/OPERATOR-GUIDE.md`: an Agent Surface section documents the
  verbs and the skill install.

## Acceptance evidence

- A two-term search selects only the model that matches both terms,
  case-insensitively: `TestModelsSearchMatchesEveryTermCaseInsensitively`.
- The JSON summary carries ID, name, context length, and token prices,
  sorted by ID: `TestModelsSearchAnswersJSON`.
- A search without a query exits with the usage code:
  `TestModelsSearchNeedsAQuery`.
- `models show` round-trips the full projection with offerings:
  `TestModelsShowAnswersTheFullProjection`.
- An unknown ID refuses with a search hint and the runtime exit code:
  `TestModelsShowRejectsAnUnknownModel`.
- The embedded catalog answers a real search offline:
  `TestModelsSearchReadsTheEmbeddedCatalog`.
- `agent setup` writes the embedded skill, overwrites an older copy,
  defaults to `$AGENTS_HOME/skills`, prints with `--print`, and rejects
  arguments: the five `TestAgentSetup*` tests.

## Checks

- `go test ./cmd/starport/... ./internal/cli/...`: pass.
- `bash scripts/verify-enterprise-readiness.sh`: 31 passed, 2 failed.
  ENR-V30 and ENR-V31 are green. The two failures are the tasks that
  remain: ENR-V32 and ENR-V33.
- The full pre-PR battery from the repository evidence list: pass. Each
  optional SDK smoke check reports its own skip status in CI.
- `technical-writing lint skills/starport/SKILL.md`: zero diagnostics.
- `technical-writing lint docs/OPERATOR-GUIDE.md`: the new section is
  clean, and the file keeps its 48 baseline diagnostics.
