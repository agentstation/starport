# Changelog

This file carries the summary for each tagged version. The GitHub release for a tag carries the generated commit list.

## Unreleased

### Breaking changes

- The default port is 7827. Set `STARPORT_SERVER_PORT=8080` to keep the old port. Compose users set `STARPORT_PORT=8080` (#443).
- `starport auth bootstrap` replaces `starport init`. The first run of `serve` or `dev` initializes a clean local root (#444, #445, #446).
- `starport auth bootstrap --name` creates the first gateway API key in configured shared storage. `starport auth bootstrap --recover` retries an interrupted local setup (#446).
- `starport dev` keeps its keys and data across runs on the platform storage root. `starport dev --ephemeral` keeps nothing, as `dev` did before (#445).

### Developer experience

- The gateway binds its port before it opens a browser or prints a link (#447).
- When port 7827 is busy, `dev` tries 7373, 7678, 20 random ports in 7000–7999, and then any free port. It prints the busy port and the bound port (#447, #453).
- An explicit port setting turns off the fallback. `serve` never falls back (#447).
- `starport ui` and `starport auth url` name the bound port (#447).
- One gateway holds one state root. A second gateway on the same root fails and names the running gateway (#453, #454).

### Fixes

- `STARPORT_FILES_PATH` takes effect (#451).
- `dev` and `serve` bind before the first-run key prints, so a busy port leaves the root clean (#453).
- On Windows, `WSAEACCES` counts as a busy port (#453).
- A stale endpoint record no longer looks live when another process reuses its process ID (#453, #454).
- Backups omit the endpoint record and its lock, so an older binary restores them (#453, #454).
- Windows blob retirement runs one replace at a time and retries a blocked replace (#430).

### Documentation and media

- The README and the site lead with persistent `dev` (#448).
- Current CLI and Console recordings with transcripts and reproduction steps (#435, #436, #452).
- The README first-request recording comes from the attested v1.3.0 archive (#429, #433).
- The splash page is a scroll journey (#427).

### Build and CI

- Go 1.27.2 and `golang.org/x/net` v0.60.0 fix GO-2026-6613 and GO-2026-6617 (#438).
- The Starmap dependency moves from v0.17.0 to main commit `120a49889009` (#436).
- CI fixtures and the Dockerfile base images come from `ghcr.io/agentstation/fixtures` (#440, #459).
- The site deploys after a successful tag release (#458).
- Dependency group updates (#420, #421, #422, #455, #456, #457).
- Test and CI stability fixes (#428, #450, #454, #460, #461, #462, #463).

### Upgrade notes

- Replace a scripted `starport init` with the first run of `serve` or `dev`. For shared storage, use `starport auth bootstrap --name`.
- A client that uses port 8080 must move to 7827 or set `STARPORT_SERVER_PORT=8080`.
- Use `starport dev --ephemeral` for a clean gateway on each run.

## v1.3.0

Starport v1.3.0 is the Starport half of the qualified catalog release pair. It pins Starmap v0.17.0.

### Catalog authority

- Starmap catalog authority gates every request admission. The gateway proves authority startup before it allows a response.
- The runtime integrates the catalog lifecycle, memory authorization, and isolated caches.
- The runtime fences fleet publication, and shared acquisition state recovers after an outage.
- The fleet leader promotes the packaged baseline.
- Usage records preserve recognition billing units and measured usage.

### Recovery

- Verified backups, restricted restores, guarded replay, and imported-state inspection.
- Closed recovery positions with native owner controls and independently retained history under closed import barriers.
- Populated adoption across the native, recovery, application, and command owners.
- The recovery measurement harness, backup write-history, and the fleet restore activation.
- Gateway admission withdraws after closure without a forced observation.
- A populated workload qualifies the local-to-shared migration.

### Configuration and storage

- One deployment configuration authority with shared SQL revisions.
- Configuration operations, field saves, and receipts in the console and the API.
- Paid-operation budgets reserve work and recover durable work safely.
- Complete local and shared storage recipes, qualified on Valkey 7.2.14 and PostgreSQL 16.15.
- The configuration loader keeps the installation default destination approvals.

### Documentation and console

- An operator documentation site at `/docs/` with a generated settings and file reference, a docs archive, and the accessibility targets.
- Deployment recipes and read-only Compose recipes.
- The README follows the first-use sequence and qualifies the candidate installs.
- The console completes the configuration view, refreshes run state, and reports chat readiness.
- The console adopts shadcn 4.21 and the shadcn lint.

### Build

- Go 1.27.1 for development, CI, and releases.

### Features

- `STARPORT_<PROVIDER>_INFERENCE_BASE_URL` approves one replacement inference origin for one provider. It applies to environment credentials only.

### Fixes

- The configuration loader no longer materializes an empty inference destination approval set. Each build after #384 denied every inference destination with a 503. The `1.2.2-next` candidate carries the defect. The v1.2.0 release does not carry the defect.

## v1.2.0

### Enterprise readiness

- A Prometheus scrape at `/metrics`, OpenTelemetry traces over OTLP, usage record sinks, and an activity export.
- A durable admin audit log at `/api/v1/admin/audit` with a console page.
- Signed webhook eventing.
- The `/v1/responses`, `/v1/batches`, and `/v1/moderations` surfaces.
- Traffic spread inside the ranking band and shared provider health through the distributed store.
- A guardrail hook with built-in PII and moderation checks that fail closed.
- Team spend budgets across every attributed key.
- An opt-in semantic cache beside the exact response cache.
- Presets as immutable revisions with pins and rollback.
- An agent surface: `starport agent setup` and `starport models search` or `show` with `--json`.
- A security posture document.

### Console polish

- shadcn primitives on Base UI: dialog, sheet, popover, menu, tooltip, toast, skeleton, and command palette.
- Query factories and route loaders own every read. List state lives in the address.
- A shared data table with sortable columns and footers, a formatting vocabulary, and relative time.
- Charts with interval buckets and a series legend.
- Settings sections, system info, a webhook summary, and budget meters.
- Audit investigation tools, usage guardrail fields, a usage export, and a batches panel.
- Copy and form polish across the shell, the catalog, providers, accounts, and chat.
- A docs page in sync with the sidebar through a navigation coverage test.
- Small screens: a navigation sheet, a picker bottom sheet, 44 px touch targets, and no horizontal overflow at 375 px.

### Fixes

- Repair of a corrupt identity hash-index record on delete.

## v1.1.0 and earlier

See the GitHub releases.
