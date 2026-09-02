# Changelog

This file carries the summary for each tagged version. The GitHub release for a tag carries the generated commit list.

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
