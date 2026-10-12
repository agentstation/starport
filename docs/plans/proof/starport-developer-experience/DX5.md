# DX5 final document pass

Plan: `docs/plans/starport-developer-experience-plan.html`, task DX5.
Base commit: `1cf134d6` (DX4 head). Branch: `codex/dx-DX5`.
Go from `go.mod` (go1.27.2), macOS.

This file masks every API key and launch token. It shows only a SHA-256
prefix. Paths under the scratch root show as `<scratch>`.

## Fail-before

The README Quick start was rewritten first. Then the old README pins ran
against the new README, before any change to `cmd/starport/readme_test.go`.

```sh
go test -count=1 ./cmd/starport/ -run 'TestReadme' -v
```

```text
=== RUN   TestReadmeFollowsTheFirstUseSequence
    readme_test.go:154: README sections: item 3 is missing
--- FAIL: TestReadmeFollowsTheFirstUseSequence (0.00s)
=== RUN   TestReadmeTemporaryPathPrecedesPersistentPath
    readme_test.go:183: README has no "Keep the gateway" section
--- FAIL: TestReadmeTemporaryPathPrecedesPersistentPath (0.00s)
=== RUN   TestReadmeInspectsTheCatalogBeforeAnyCredential
--- PASS: TestReadmeInspectsTheCatalogBeforeAnyCredential (0.00s)
=== RUN   TestReadmeNamesTheThreeCredentialRolesInOrder
--- PASS: TestReadmeNamesTheThreeCredentialRolesInOrder (0.00s)
=== RUN   TestReadmeKeepsCredentialRolesApart
--- PASS: TestReadmeKeepsCredentialRolesApart (0.00s)
=== RUN   TestReadmeLinksTheStorageAndRecipePages
    readme_test.go:334: README has no "Keep the gateway" section
--- FAIL: TestReadmeLinksTheStorageAndRecipePages (0.00s)
=== RUN   TestReadmeStatesThePathAnchors
    readme_test.go:366: README has no "Keep the gateway" section
--- FAIL: TestReadmeStatesThePathAnchors (0.00s)
=== RUN   TestReadmePerformanceClaimsAreQualified
--- PASS: TestReadmePerformanceClaimsAreQualified (0.00s)
=== RUN   TestReadmeLatencyClaimsReadEachUnit
--- PASS: TestReadmeLatencyClaimsReadEachUnit (0.00s)
=== RUN   TestReadmeOmitsCatalogCounts
--- PASS: TestReadmeOmitsCatalogCounts (0.00s)
FAIL
FAIL	github.com/agentstation/starport/cmd/starport	0.887s
FAIL
```

Four pins fail. The old sequence pin `TestReadmeTemporaryPathPrecedesPersistentPath`
fails because the `Keep the gateway` section with its `Persistent local gateway`
subsection no longer exists. The section order pin fails at item 3 for the
same reason.

## Pin changes

- `TestReadmePersistentDevIsTheFirstPath` replaces
  `TestReadmeTemporaryPathPrecedesPersistentPath`. The new pin requires these
  facts:
  - Terminal 1 runs only `starport dev`.
  - The first run prints the key once, and the key name is `local-admin`.
  - The fallback order matches `endpoint.DeveloperPorts`.
  - The fallback line matches the output of `dev`.
  - The restart runs `starport dev` and states that no key prints. Its
    sample matches the second-run output of `dev`, line for line.
  - The recovery command is `setup.RecoverCommand`.
  - The scratch subsection runs only `starport dev --ephemeral` and links the
    scratch page.
  - The persistent command comes before the scratch command.
  - The scratch page keeps its five selector families and its catalog
    selector line, and it lists shutdown losses.
  - `auth bootstrap --name` is absent from the Quick start and present in the
    team section.
- `TestReadmeFollowsTheFirstUseSequence` expects `Serve and deploy` in place of
  `Keep the gateway`. It also expects `Stop and restart the gateway` and
  `Run a scratch gateway` after Terminal 2.
- `TestReadmeLinksTheStorageAndRecipePages` and `TestReadmeStatesThePathAnchors`
  read the new `Where the gateway keeps its data` subsection.
- `TestReadmeStatesThePathAnchors` compares the eight-line first-run sample with
  `starport dev --no-open`. Before, it compared a four-line sample with
  `starport serve`. The fake starter delivers the first-run key through the
  real delivery path and then returns a session.

## Real-binary runs

Two runs of persistent `dev` against one scratch root on port 18574. I built
the binary from this branch.

```sh
go build -o <scratch>/dx5-bin/starport ./cmd/starport
env -i PATH="$PATH" HOME="$HOME" STARPORT_HOME=<scratch>/dx5-run/home \
  STARPORT_SERVER_PORT=18574 STARPORT_CATALOG_NETWORK_MODE=offline \
  STARPORT_CATALOG_ACQUISITION_ENABLED=false \
  python3 docs/plans/proof/starport-developer-experience/tools/run_for.py 20 \
  <scratch>/dx5-run/raw1.txt <scratch>/dx5-bin/starport dev --no-open
# The same command again for run 2.
```

Run 1 output, without the Badger log lines:

```text
Initialized Starport.
Configuration: <scratch>/dx5-run/home/config/config.env
Data: <scratch>/dx5-run/home/data
Gateway API key (shown once): <sha256:0fe97dda46e4>
Starport development gateway
URL: http://127.0.0.1:18574
Authentication: required
Console (one-time launch link): http://127.0.0.1:18574/launch?lt=<sha256:60ed3064726a>
[run_for] exited_early=False exit=0
```

Run 2 output, without the Badger log lines:

```text
Starport development gateway
URL: http://127.0.0.1:18574
Authentication: required
Console (one-time launch link): http://127.0.0.1:18574/launch?lt=<sha256:4d5ea23b6e35>
[run_for] exited_early=False exit=0
```

The labels and the order match the README samples. I deleted the raw outputs
after the masking.

## Commands and results

| Command | Result |
| --- | --- |
| `go test -count=1 ./cmd/starport/` | `ok`, 52 top-level tests pass |
| `go test -count=1 -v -run TestReadme ./cmd/starport/` | 10 of 10 pass |
| `go test -count=1 ./internal/config/ ./internal/cli/` | `ok`, `ok` |
| `go test -count=1 -run TestDocumented ./internal/app/` | `ok` (reads the README Terminal 2 request) |
| `go test -count=1 -timeout 30m ./internal/app/` | `ok`, 539.8 s, exit 0 |
| `TMPDIR=<scratch> STARPORT_SMOKE_PORT=18573 scripts/smoke-first-run.sh` | exit 0, `PASS ephemeral dev, first-run serve, validation, diagnosis, restart, readiness, and authenticated model discovery` |
| `scripts/verify-developer-experience.sh` | exit 0, `Summary: 47 passed, 0 failed` |
| `scripts/verify-readme-quickstart.sh` | exit 0, `PASS README quickstart and dynamic stable-release selection` |
| `scripts/test-readme-quickstart-verifier.sh` | exit 0, `PASS README quickstart verifier regression tests` |
| `scripts/verify-doc-links.sh` | exit 0, `PASS documentation links` |
| `make docs-generate` | exit 0, no diff |
| `pnpm install --frozen-lockfile && pnpm test` in `website/` | 4 files, 84 of 84 tests pass |
| `pnpm lint` in `website/` (ESLint and `tsc --noEmit`) | exit 0 |
| `git grep -n -i 'starport init'` | 17 lines in 10 files, all allowed (see below) |

The first run of the quickstart verifier failed with
`README is missing the tested quickstart contract: creates no configuration files`.
The phrase wrapped across two lines, and the script matches one line. The
README sentence now keeps the phrase on one line.

The first default-timeout run of `go test ./internal/app/` stopped at the
10-minute default with no test failure. The Makefile runs the package with
`-timeout 30m`.

### `starport init` matches

| File | Lines | Kind |
| --- | --- | --- |
| `docs/assets/first-use-v1.2.0/capture.py` | 1 | Recorded release asset |
| `docs/assets/first-use-v1.2.0/events.json` | 1 | Recorded release asset |
| `docs/assets/first-use-v1.3.0/events.json` | 1 | Recorded release asset |
| `docs/proof/readme-demo/rehearsal-2026-10-04/events.json` | 1 | Transcript |
| `docs/plans/proof/starport-developer-experience/DX0.md` | 5 | Historical proof |
| `docs/plans/proof/starport-developer-experience/DX2.md` | 2 | Historical proof |
| `docs/plans/proof/starport-developer-experience/DX4.md` | 1 | Historical proof |
| `docs/plans/proof/starport-developer-experience/DX6.md` | 2 | Historical proof |
| `docs/plans/README.md` | 1 | Plan file |
| `docs/plans/starport-developer-experience-plan.html` | 2 | Plan file |

## Commits

| Commit | Subject |
| --- | --- |
| `619bd399` | `docs: make the start journey persistent-first` |
| `70514972` | `readme: lead the quick start with persistent starport dev` |

`website/test/splash.test.ts` compares the splash facts with the README. For
this reason, the README commit also holds the splash change. An exported tree
of `619bd399` passed `go test -count=1 ./cmd/starport/` and
`scripts/verify-doc-links.sh`.

## Strict lint

```sh
export TECHNICAL_WRITING="$HOME/.agents/skills/technical-writing/scripts/technical-writing"
"$TECHNICAL_WRITING" lint <path> --mode strict --format text
```

| Document | Before | After |
| --- | --- | --- |
| `README.md` | 0 | 0 |
| `docs/OPERATOR-GUIDE.md` | 68 | 67 |
| `docs/PRODUCTION-STATUS.md` | 0 | 0 |
| `docs/site/index.md` | 0 | 0 |
| `docs/site/start/index.md` | 0 | 0 |
| `docs/site/start/local-persistent.md` | 0 | 0 |
| `docs/site/start/temporary-development.md` | 0 | 0 |
| `docs/site/start/keys-and-roles.md` | 0 | 0 |
| `docs/site/start/first-catalog-inspection.md` | 0 | 0 |
| `docs/site/architecture/recipes.md` | 0 | 0 |
| `docs/site/architecture/targets.md` | 0 | 0 |
| `docs/site/storage/caches.md` | 0 | 0 |

The operator guide failed strict mode before the change. The touched lines
have no diagnostic. The rewrite of `## Local Development` removed one
semicolon diagnostic (old line 47). The other 67 diagnostics are on lines
that this task did not touch.

## Document changes

- README:
  - The Quick start runs persistent `starport dev`.
  - Its subsections are inspect, credential roles, Terminal 1, Terminal 2,
    stop and restart, data locations, and a scratch gateway.
  - Terminal 1 shows the first run, the key, and the fallback order.
  - `## Keep the gateway` is now `## Serve and deploy`. It holds the no-auth
    section and the team section.
  - The team section states that configured storage does not initialize
    itself. Then `starport auth bootstrap --name <name>` creates the first
    identity.
- Site start pages: the file names stay. Every inbound link and public URL
  still works. `local-persistent.md` is the persistent-first page. It starts
  with `starport dev` and has a restart section for `starport serve`.
  `temporary-development.md` has the title "Run a scratch development gateway".
  It keeps its anchors `#settings-that-development-mode-refuses`,
  `#scratch-directory-recovery`, and `#what-the-gateway-loses-at-shutdown`.
  The start order is index, inspect, persistent, keys, scratch.
- `docs/site/index.md`: the fastest path runs `starport dev`.
- `keys-and-roles.md` line 25: the first run on an empty root issues the first
  key. Each scratch session issues a new first key.
- `recipes.md` T2 names `starport dev` and `starport serve`. The link labels in
  `recipes.md`, `targets.md`, and `caches.md` use the new scratch page title.
- `docs/OPERATOR-GUIDE.md`: `## Local Development` describes persistent dev,
  the first-run output, the restart, the fallback, and the refusal.
  `## Initialize Persistent State` and `## Failure Diagnosis` name `dev`
  beside `serve`.
- `docs/PRODUCTION-STATUS.md` (F7): the first row is `starport dev --ephemeral`,
  and the persistent row names `starport dev` and `starport serve`.
- `console/README.md` and `docs/site/operate-starport/fleet.md` already
  described the current behavior. They have no change.
- Splash site:
  - The quick-start note is "Persistent gateway · keys survive a restart".
    Its command drops the `unset` line, because persistent dev does not
    refuse `STARPORT_CATALOG_STATE_DIR`.
  - The console chapter shows the restart banner, which has no key line.
  - The laptop chapter text says that the stores persist on disk under the
    platform storage root. This text is the claim, the body, the chips, and
    the store labels.
  - The request state is "Recorded · on disk".
  - In `world.ts`, the laptop cylinders lose their dash pattern, and the
    comment states the persistent stores.

## Removed retention claims

The README Quick start lost these claims. Each claim is still true for
`starport dev --ephemeral`, and the scratch page or the operator guide still
states it.

1. "Ephemeral development mode reads the process environment but not
   `config.env`." The scratch page keeps it.
2. "The refused selectors are the KV, SQL, file, and cache backend settings, and
   `STARPORT_CATALOG_STATE_DIR`." The scratch page keeps the full list. The
   README keeps "refuses persistent storage selectors before it opens storage".
3. The four shutdown bullets of "Stop the temporary gateway". The README keeps
   one summary sentence. The scratch page keeps the bullets. The bullets are:
   - The gateway API keys, accounts, and console sessions.
   - The usage records, request activity, and budgets.
   - The uploaded files, batches, and presets.
   - The catalog baseline and the runtime catalog state.
4. "Provider inference credentials stay in the process environment." The
   scratch page keeps it.
5. "A later development run removes abandoned scratch directories after it
   verifies their owner." The scratch page keeps it. The operator guide keeps
   it too.
6. "The first run prints the gateway API key once, before the welcome." Dev
   prints no welcome. The README now says that the first run prints the key
   once. The operator guide keeps the welcome order for `serve`.
7. "The `unset` command above removes two common selectors." The Quick start
   has no `unset` line now. The scratch page keeps the `unset` step.

## Claims that this task could not verify

- "Twenty random ports from 7000 through 7999" (operator guide) and "A random
  free port from 7000 through 7999" (README) restate `endpoint.DeveloperPorts`.
  The draws can repeat, so the gateway can try fewer than 20 distinct ports.
  Neither text promises 20 distinct ports.
- "The key from the first run still works" cites the DX3 real-binary
  acceptance, where run 2 accepts the run 1 key. This task did not send a
  request with the run 1 key.
- The fallback line `Port 7827 is busy. Bound port 7373.` comes from the CLI
  test and the DX4 proof. This task did not hold port 7827 for a real-binary
  run. Each real-binary run used an explicit port above 18000. An explicit
  port turns off the fallback.

## Findings outside the scope

- `internal/cli/auth.go:183` and `:212`: the `auth bootstrap` description says
  "the first run of starport serve initializes it", and the recovery result
  prints "Run: starport serve". Persistent `dev` also initializes a clean root.
  The text is true but names only `serve`. This task changed no CLI text.
- README lines 29 and 88 and the v1.3.0 recording alt text in
  `website/src/components/journey/journey.tsx:971` say "a temporary gateway".
  They describe recorded assets and the release archive check. Both run
  `starport dev --ephemeral`, so the text stays.
- The DX3 finding that `STARPORT_FILES_PATH` has no effect predates this task.
- The technical-writing linter does not split a sentence after "port 7678.".
  It reports one long sentence. The README and the operator guide use a
  numbered list for the fallback order.
