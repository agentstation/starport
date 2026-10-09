# Changelog

This file carries the summary for each tagged version. The GitHub release for a tag carries the generated commit list.

## Unreleased

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
- The D42 recovery measurement harness, backup write-history, and the fleet restore activation.
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
