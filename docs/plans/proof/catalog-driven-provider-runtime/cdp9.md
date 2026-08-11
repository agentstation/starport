# CDP9 conformance, documentation, and review

Status: `done`

Pull requests:

- Starport [#91](https://github.com/agentstation/starport/pull/91)
- Starmap [#73](https://github.com/agentstation/starmap/pull/73)

## Documentation contract

The public Starport documentation now defines these operator contracts:

- Conventional provider variables have strict catalog order.
- The first present conventional variable wins.
- An invalid selected value causes an error. Starport does not use the next
  value.
- An explicit `env:NAME` reference overrides conventional discovery.
- An explicit `file:/absolute/path` reference supports in-place rewrite,
  atomic replacement, symlink target swap, Kubernetes `..data` swap, mounted
  content replacement, and agent rerender.
- Doppler, 1Password, and Infisical wrappers supply ambient values before the
  Starport process starts.
- A verified remote catalog has a separate remote head and accepted runtime
  head. A failed candidate does not replace the accepted runtime.

Starmap documents the same explicit environment-reference contract. It also
documents the three wrapper workflows for acquisition commands.

Strict technical-writing checks passed for all changed Starport documents.
The changed Starmap README section also passed the strict check. The full
Starmap README has 61 existing diagnostics outside this change.

## Cross-repository conformance

The campaign verifier reported:

```text
Summary: 19 passed, 0 failed
```

CDP-V07 compares all 14 source-conformance vector IDs. Starport and Starmap
use the same IDs:

```text
static
default_chain
version
expiry
lease
cancellation
concurrency
denial
redaction
rotation_in_place
rotation_atomic_replace
rotation_symlink_swap
rotation_mounted_replace
rotation_agent_rerender
```

## Repository verification

Starmap passed ordinary, pure-Go consumer, file-size, full race, vet,
performance, lint, coverage, documentation, build, embedded catalog, and CLI
checks through uncapped `make verify`. The embedded catalog contained 14
providers, 104 authors, and 611 models.

Starport passed these checks:

- `bash scripts/verify-starmap-ownership.sh`: 12 passed, 0 failed.
- `bash scripts/verify-v1-architecture.sh`: 12 passed, 0 failed.
- `bash scripts/verify-catalog-driven-providers.sh`: 19 passed, 0 failed.
- `go test ./... -count=1`: 41 packages passed.
- The focused race suite passed with normal Go scheduling.
- `go vet ./...`.
- `make lint`: zero issues.
- `make build`.
- `bash scripts/smoke-first-run.sh`.
- `bash scripts/smoke-openrouter-sdks.sh`: raw HTTP and the Python,
  TypeScript, and Go SDKs passed.
- `make release-check`: ownership, architecture, release, workflow,
  developer-experience, documentation-link, GoReleaser, and action-pin checks
  passed.

The release check used the repository-pinned GoReleaser v2.17.1 from an
isolated temporary directory. It did not change the installed Homebrew tool.
No verification command used `GOFLAGS`, `-p`, or another scheduler cap.

## Protected pre-PR review

Starmap passed the Sol pre-PR review at 0.99 with no actionable finding. An
earlier Claude selection could not start because its connection rejected the
local proxy certificate. That attempt did not call a model and did not produce
a review result.

The Starport aggregate TruffleHog scan was clean. The bundle guard then found
identifier-shaped values that existed in deleted roster files and current
contract tests. The review did not disable or bypass the guard. It split the
exact substantive change set into safe commit or tree-equivalent slices.

All slices passed the Sol review with no actionable finding:

- baseline contract: 0.98
- acquisition hardening verifier and proof: 0.99
- released Starmap adoption: 0.98
- catalog-derived configuration: 0.96
- credential lifecycle: 0.97
- obsolete roster and credential-contract deletion: 0.98
- catalog-runtime activation: 0.97
- atomic runtime generations: 0.97
- request credential policy: 0.98
- direct secret sources with the current safe URI fixture: 0.98 across two
  review passes
- verified remote activation: 0.98
- operator contract and release verifier: 0.99
- transport contract test ownership: 0.99

The two catalog-runtime slices produce the exact tree of Starport commit
`442f4c0`. The direct-secret-source slice combines commit `b66d415` with the
current fixture fix from `debe506`. Its test-file blob matches the branch head.
This preserves full review coverage without exposing or restoring the removed
synthetic URI literal.

## Publication

Both pull requests use `main` as their base. Both pull requests are ready for
review. All local gates are green, and no autoreview
finding remains. Hosted checks, merge order, release, installation, and public
readback belong to CDP9.1.
