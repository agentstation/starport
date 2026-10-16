# Starport launch-readiness audit

Worktree: `/private/tmp/starport-launch` at `b310cad7` (origin/main). Read-only audit on 2026-10-15.
Paths are relative to the worktree. "Live" items come from `gh` and anonymous `curl` checks made on that date.

## Blockers (must fix before launch)

### B1. Every install path ships v1.3.0, but README, site source, and docs describe unreleased main behavior
- `README.md:66-72` installs the released cask. The live cask is `version "1.3.0"` (`agentstation/homebrew-tap/Casks/starport.rb`).
- `README.md:105-305` documents behavior that exists only after v1.3.0:
  - persistent `starport dev` (#445)
  - port 7827 (#443)
  - `dev --ephemeral` (#445)
  - `auth bootstrap` and `--recover` (#446)
  - the port fallback (#447, #453)
- v1.3.0 differs from that text:
  - `git show v1.3.0:internal/config/config.go:143` gives `Port int env:"PORT,default=8080"`.
  - `git grep ephemeral v1.3.0 -- cmd/starport` finds no match.
  - `v1.3.0:README.md:71` says "`starport dev`, a temporary gateway".
- Effect: a new user who runs `brew install` and follows the README gets port 8080. `curl :7827` then fails, `--ephemeral` is an unknown flag, and `auth bootstrap` does not exist.
- Fix: cut the next release (see "Proposed next release") before the announcement. As an alternative, pin the README to the v1.3.0 flow until the release ships.

### B2. The README hero recording shows a removed command and the old dev model
- `README.md:12-17` puts `docs/assets/first-use-v1.3.0/first-use.gif` first.
- Its events show `Keep the gateway: starport init → starport serve` (`docs/assets/first-use-v1.3.0/events.json:191`) and "temporary development gateway".
- The removal of `init` is in `a8562b1d` (#446). `internal/cli/*.go` now has only `serve`, `dev`, `auth bootstrap|token|rotate`, and `ui`.
- `website/src/components/journey/journey.tsx:971-976` uses the same recording and calls it "a temporary gateway".
- Fix: after B1, re-record first-use against the new tag. Update `README.md:12-16`, `website/scripts/release-facts.mjs`, and `website/src/lib/splash.ts` (both pin `v1.3.0`).

### B3. The live website contradicts main
- Live `https://starport.agentstation.ai` contains "Temporary by default" once and `127.0.0.1:8080` six times (`curl` on 2026-10-15).
- Source now says "Persistent by default. Scratch by choice." (`website/src/lib/splash-facts.ts:510-512`) and uses 7827 (`splash-facts.ts:310,409`).
- The deploy depends on the known empty `starport-site` environment (owner item, not re-investigated).
- Fix: release (B1), then add the environment secrets, then deploy. Launch only after the live page says 7827.

### B4. The issue chooser links to disabled Discussions and the wrong security address
- `.github/ISSUE_TEMPLATE/config.yml` gives "Community Support" → `https://github.com/agentstation/starport/discussions`. Discussions are disabled (`hasDiscussionsEnabled:false`), so the link returns 404.
- The same file sends "Security Issues" to `mailto:support@agentstation.ai`. `SECURITY.md:10` says `security@agentstation.ai`.
- `blank_issues_enabled: false` makes this chooser the only way in.
- Fix: enable Discussions or remove that link. Point the security link at `security@agentstation.ai` or `https://github.com/agentstation/starport/security/advisories/new`.

### B5. CODEOWNERS names a team that does not exist
- `.github/CODEOWNERS:6-17` uses `@agentstation/maintainers` throughout.
- `gh api orgs/agentstation/teams` lists only `founders`, and `.../teams/maintainers` returns 404.
- GitHub flags the file as invalid, and no review is requested automatically.
- Fix: create the team or use `@agentstation/founders` / named users.

## Should fix

### S1. CHANGELOG has an empty Unreleased section
- `CHANGELOG.md:5` `## Unreleased` is empty. 39 commits follow v1.3.0 (`git log v1.3.0..origin/main`), and several are user-visible breaking changes (#443, #445, #446).
- Fix: fill it with the outline in "Proposed next release", including a "Breaking changes" section.

### S2. The privacy claim in the security posture is wrong for the default config
- `docs/SECURITY-POSTURE.md:134-135` says "Nothing leaves the process unless the operator configures a destination."
- `docs/SECURITY-POSTURE.md:152-153` says "A remote Starmap catalog … empty by default".
- The default catalog source is `public`. It polls the GitHub channel hourly:
  - `docs/site/catalog-lifecycle/sources-and-baseline.md:12,41`
  - `.env.example:69,78,80`
  - `README.md:466-468`
- Status-page polling is driven by the catalog, not by an operator choice (`SECURITY-POSTURE.md:150-151`).
- "No telemetry" itself holds: I found no phone-home or update-check code, and metrics, OTLP, and the usage sink are opt-in (`internal/app/app.go:824-844`).
- Fix: list "public catalog channel on GitHub (hourly, default)" and "provider status pages (default, catalog-declared)" as default egress. Name the opt-out (`STARPORT_CATALOG_SOURCE=embedded` or `file`). Add a short "Telemetry: none" line to README.

### S3. The license identity is inconsistent
- `LICENSE:632-639` changes the AGPL "How to Apply" appendix template in place. It adds "Copyright (C) 2025 AgentStation" and "either version 3 … or (at your option) any later version", which reads as AGPL-3.0-or-later.
- `Dockerfile:56` and `skills/starport/SKILL.md:4` say `AGPL-3.0-only`.
- The copyright year is 2025. `.github/workflows/update-copyright-year.yml:29` rewrites only that appendix line, each 1 January.
- Fix: restore the verbatim license text. Add one project notice (README "License" section or a `NOTICE` file) that states `AGPL-3.0-only` (or `-or-later`) and "Copyright 2025-2026 AgentStation". Align the Dockerfile label and the skill.

### S4. The repository description overclaims and has no topics
- The description is "high-performance, local, open-source, drop-in replacement for OpenRouter" (`gh repo view`).
- `README.md:567-573` marks the performance targets UNVERIFIED, and README positions Starport as self-hosted for teams and enterprises as well.
- `repositoryTopics` is null.
- Fix: use the README sentence (`README.md:7-8`) and add topics (llm, gateway, openai, openrouter, self-hosted, go).

### S5. Internal plan jargon in public docs
- `README.md:569`: "CSP22 owns their qualification on dedicated runners."
- `CHANGELOG.md:24`: "The D42 recovery measurement harness".
- Fix: replace each with plain words ("qualification on dedicated runners is pending").

### S6. Stale or internal contributor workflow artifacts at the top level
- `.github/pull_request_template.md` starts `## Task: [TASK-ID]` and "From TASKS.md acceptance criteria". That does not fit outside contributors.
- `docs/TASKS.md:8-12` calls the completed product-demos plan "Active Work" (last updated 2026-10-08).
- `spawn-agent.sh` (repo root) starts Claude Code agents in `$HOME/starport-development` and tells them to edit TASKS.md.
- `docs/plans/product-demos-plan.html` is complete. `docs/README.md:31-33` says the close of a campaign deletes its plan.
- Fix: write a generic PR template (summary, tests, docs, breaking change). Delete the finished plan. Move `spawn-agent.sh` into `scripts/` (or delete it). Update TASKS.md "Active Work".

### S7. CONTRIBUTING points to Discussions
- `docs/CONTRIBUTING.md:180`: "Use a GitHub issue or discussion for design questions." Discussions are disabled.
- Fix: same decision as B4.

### S8. Repository security features are off
- `gh api repos/agentstation/starport` returns:
  - `secret_scanning`, `push_protection`, and `dependabot_security_updates` all `disabled`
  - private vulnerability reporting `{"enabled":false}`
  - code-scanning default setup `not-configured`
- CI runs gosec and uploads SARIF only as an artifact (`.github/workflows/ci.yml:787-794`). No step uploads it to code scanning, and no `govulncheck` step exists.
- Fix: enable secret scanning, push protection, Dependabot security updates, and private vulnerability reporting (a public repo pays nothing). Optionally enable CodeQL default setup or upload the gosec SARIF.

### S9. macOS binaries are not signed or notarized, and the cask strips quarantine
- The notarization block is conditional (`.goreleaser.yaml:117-131`).
- The v1.3.0 release log shows `MACOS_SIGN_P12:` and `MACOS_SIGN_PASSWORD:` empty (run 37437368384). Repo secrets have only `MACOS_NOTARY_*` and `HOMEBREW_TAP_DEPLOY_KEY`.
- The cask runs `xattr -dr com.apple.quarantine` (`.goreleaser.yaml:106-112`), so Homebrew users avoid Gatekeeper. A user who downloads the tarball directly gets a Gatekeeper block that README does not mention.
- Fix: add the Developer ID P12 secrets, or add a README note for direct downloads (`xattr -d com.apple.quarantine ./starport`).

### S10. The native-release verifier defaults to an old tag
- `.github/workflows/native-release.yaml:86`: `RELEASE_TAG: ${{ inputs.tag || 'v1.2.0' }}`. PR runs verify v1.2.0 archives, not the latest release.
- Fix: default to the latest release (`gh release view --json tagName`) or v1.3.0.

### S11. No Open Graph or social card metadata
- `website/src/app/layout.tsx:5-14` sets `title`, `description`, and the favicon only. It has no `openGraph`, `twitter`, or image. The live HTML has no `og:image` or `twitter:card`.
- A launch post on X, HN, or Slack gets a bare link preview.
- Fix: add `openGraph` and `twitter: { card: 'summary_large_image' }` with a 1200×630 image under `website/public`.

## Nice to have

- **N1. Stale example:** `examples/cache_control_demo.go:12` uses `anthropic/claude-3-5-sonnet` and only prints JSON. The send is commented out (`:50`). Use a current catalog model or delete the example.
- **N2. Cask homepage:** the cask homepage is GitHub (`.goreleaser.yaml:93`). Use `https://starport.agentstation.ai`.
- **N3. Dormant Pages workflow:** `docs-pages.yaml` is dormant by design (`:1-8`), and Pages is off (API 404). The site at `/docs/` overlaps it. Delete the workflow or document which docs host is canonical.
- **N4. Bug template:** `.github/ISSUE_TEMPLATE/bug_report.md` asks for "Go Version" and gives the example `v0.1.0`. Ask for `starport --version`, the install method (brew, archive, or container), and `starport doctor` output, and add a secrets warning like `SECURITY.md:13-15`.
- **N5. Recording wording:** `README.md:29` says the CLI recording shows "a temporary gateway". The recording runs `dev --ephemeral` (`docs/assets/cli-current/TRANSCRIPT.md:101`). Say "scratch gateway" to match the new vocabulary (`README.md:289-305`).
- **N6. Public tap noise:** the public tap repo `agentstation/homebrew-tap` contains `.claude/` and `CLAUDE.md`. Check that they hold nothing internal.
- **N7. Missing FUNDING:** `.github/FUNDING.yml` is absent. It is optional.
- **N8. Old v1.2.0 assets:** `docs/assets/first-use-v1.2.0/` and `docs/assets/2026-08-29_starport-console.gif` are superseded media in the repo. Remove them to slim clones (check references first).

## Already good

- **Install paths match published artifacts.**
  - The v1.3.0 release has darwin_arm64, linux x86_64/arm64, and windows x86_64/arm64 archives, SBOMs, `checksums.txt`, and the docs tarball (`gh release view v1.3.0`). This matches `README.md:63-84` and `.goreleaser.yaml:31-38`.
  - The tap `agentstation/homebrew-tap` is public and has `Casks/starport.rb` at 1.3.0.
- **Container.**
  - `ghcr.io/agentstation/starport:1.3.0` is anonymously pullable (manifest 200).
  - Release attests archives, the checksum file, and the image (`release.yaml:233-247`), and re-verifies after publication (`:341-371`). This matches `README.md:364-372`.
  - The Dockerfile base images under `ghcr.io/agentstation/fixtures/{node,golang}` are anonymously pullable, so outside contributors can `docker compose build`.
- **Dockerfile.** Nonroot distroless, digest pins, 0700 state roots, `EXPOSE 7827` (`Dockerfile:47-77`).
- **Compose ports.** Compose maps `127.0.0.1:${STARPORT_PORT:-7827}:7827` (`docker-compose.yml:10-11`). The fleet file uses `127.0.0.1::7827`.
- **Port 8080.** Main mentions 8080 only as migration guidance (`README.md:422-425`, `docs/OPERATOR-GUIDE.md:414-418`) and as Starmap's own port (`docs/site/operate-starmap/central-server.md:12`).
- **Persistence copy on main is consistent.** README, `docs/site/start/*`, `docs/site/index.md:56`, `docs/PRODUCTION-STATUS.md`, and `website/src/lib/splash-facts.ts:510-512` all say persistent `dev` with `--ephemeral` for scratch. "Temporary by default" no longer appears anywhere in source.
- **README checks.**
  - Every relative link resolves (scripted check over README, docs/README, CONTRIBUTING, SECURITY, DEVELOPMENT, and OPERATOR-GUIDE).
  - All media paths exist.
  - Badges point at the right repo.
  - `cmd/starport/readme_test.go:152-655` tests the README flow (first-use sequence, persistent dev first, credential roles, latency qualifiers).
- **`starport --version` source.** `cmd/starport/run.go:30-32,265` gets its values from goreleaser `-X main.version` (`.goreleaser.yaml:24-29`) and from Docker build args (`Dockerfile:40`). The module path `github.com/agentstation/starport` fits v1.
- **No telemetry.** I found no phone-home, analytics, or update check in Go code. Console devtools load only in development (`console/src/main.tsx:33`). The console and site have no production `localhost` URLs, and their only hard-coded external links go to the GitHub repo and the site.
- **Website.** `metadataBase` and the description are correct (`website/src/app/layout.tsx:5-14`). It has a real 404 (`wrangler.jsonc` `not_found_handling: 404-page`, `website/src/app/not-found.tsx`; live `/nope-404` returns 404). The custom-domain route is correct.
- **SECURITY.md.** It has a supported-versions statement, a dedicated address, and a secret-redaction rule (`SECURITY.md:1-30`).
- **Code of Conduct.** Contributor Covenant 2.0 with a real contact (`docs/CODE_OF_CONDUCT.md:63`).
- **Env examples.** `.env.example` and `.env.fleet.example` contain only `replace-with-…` and `example.com` placeholders. Release scripts check `.env.example` (`scripts/verify-release-archives.sh`).
- **Docs hygiene.** I found no TODO, FIXME, or "coming soon" in user-facing docs.
- **Dependabot.** It covers actions, gomod, and console npm (`.github/dependabot.yml`).
- **Branch protection.** It requires Build, Security Scan, Release Snapshot, and Action Pin Provenance.

## Proposed next release

**Version: v1.4.0.** The release has breaking CLI and default changes: `init` removed, default port 8080→7827, `dev` persists. Strict semver would make it 2.0.0. But a v2 needs a `/v2` module path (`go.mod:1`). README already states "It does not yet promise a compatibility window" (`README.md:342-343`). Recommend v1.4.0 with an explicit **Breaking changes** section at the top. Pair it with a Starmap pin note if the Starmap dependency moved (#456 bumped go-modules; check `go.mod` against v1.3.0 before tagging).

Draft `## v1.4.0` outline:

- **Breaking changes**
  - The default port is now 7827. Set `STARPORT_SERVER_PORT=8080` (or Compose `STARPORT_PORT=8080`) to keep the old port (#443).
  - `starport init` is removed. `serve` and `dev` initialize an empty local root on first run. `starport auth bootstrap --name` creates the first key in configured shared storage, and `--recover` retries an interrupted setup (#444, #446).
  - `starport dev` now keeps its keys and data across runs under the platform root. Use `starport dev --ephemeral` for the old scratch behavior (#445, #448).
- **Developer experience**
  - `dev` binds before it opens the browser and falls back through 7373, 7678, a random port in 7000–7999, and then any port. It prints the busy and bound ports (#447, #453).
  - One gateway seats on one state root, and a second gateway gets a clear name (#453, #454).
  - Recording needs no state, and `STARPORT_FILES_PATH` is honored (#451).
- **Fixes**
  - Windows busy-port detection, the live record dial, and bind before the first-run key (#453).
  - Backups omit the endpoint role (#453).
  - Windows blob retirement is serialized and retries the replace (#430).
- **Documentation and media**
  - README and site lead with persistent dev (#448).
  - Current CLI and Console demos (#435, #436, #452).
  - Splash scroll journey (#427).
- **Build and CI**
  - Go 1.27.2 and x/net v0.60.0 (#438).
  - Dockerfile and CI fixtures pulled from the organization registry (#440, #459).
  - The site deploys after a tag release (#458).
  - Dependency group updates (#420, #421, #422, #455, #456, #457).
  - Test flake fixes (#428, #450, #460–#463).
- **Upgrade notes**
  - Existing v1.3.0 users who scripted `starport init` must switch to the first run of `serve` or to `auth bootstrap`.
  - Clients pinned to `:8080` must set the port.

Post-tag checklist:
1. Re-record first-use for the new tag (B2).
2. Update `website/scripts/release-facts.mjs` and `website/src/lib/splash.ts`.
3. Set the `starport-site` secrets and deploy (B3).
4. Run `native-release.yaml` with the new tag (S10).

## Not checked / uncertain

- I did not run `go build`, `go test`, the console build, or the website build. All findings are static or come from live API and `curl` checks.
- I did not open `.env.example` key by key against the config schema. Release scripts already check it.
- I did not read the 1,018-line `ci.yml` or `site.yaml` in full.
- I did not verify that `.claude/` in the tap holds anything sensitive (N6).
- Team membership checks may be limited by token scope. The `teams` listing worked and showed only `founders`.
- I did not judge whether the console version string appears in the UI. `console/package.json` is `0.0.0`, and the server version comes from ldflags.
