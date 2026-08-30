# ENR-F2 Built-in guardrails

## What shipped

`internal/guardrails` registers the two built-in checks the F1 seam left
room for. Configuration names them through `GUARDRAILS_CHECKS`, and the
`Settings` carrier hands each check what it reads at construction. An
unbuildable check is a startup error, so no deployment serves as if a
check ran that it could not build.

The `pii` check detects personal identifiers with no model call: email
addresses, phone numbers, card numbers under Luhn, and dashed US SSNs.
Card candidates are digit runs of 13 through 19, and the Luhn checksum
decides. SSN detection drops the ranges the issuer never assigns. The
`redact` mode rewrites each identifier to a bracketed category label.
The `refuse` mode stops the exchange, and the reason names the
categories rather than the values.

The `moderation` check classifies text with a catalog moderation model
and refuses when any category scores at or above its threshold. A
category named in `GUARDRAILS_MODERATION_THRESHOLDS` reads its own
threshold, and every other category reads the default.

The model call rides the `Moderator` seam, so the guardrails package
never learns the gateway. `internal/proxy` implements the seam over the gateway's own
moderation surface. The call runs under the calling request's account,
key, and protocol. The gateway binds late, after composition finishes.
The classification draws its own usage record beside the turn that
asked for it. A moderator error refuses through the pipeline, so invariant 6
holds end to end.

The operator guide documents both checks with configuration samples.

## Acceptance evidence

- `go test -race ./internal/guardrails/... ./internal/proxy/...`
  passes. Table tests cover detection and redaction on positive and
  negative cases for all four identifier categories. Luhn separates a
  valid card number from a failing checksum and a short digit run.
- Moderation tests run against a stub moderator and a stub provider
  core. They prove refusal at and above threshold, allowance below,
  per-category overrides, and the fail-closed erroring moderator.
- Proxy tests prove the routed moderation request carries the calling
  account, key, derived request ID, and protocol. They prove a refused
  request never reaches planning.
- `bash scripts/verify-enterprise-readiness.sh` reports 24 passed with
  ENR-V23 and ENR-V24 green.
- `bash scripts/benchmark-overhead.sh` reports p50=0ms and p99=0ms, so
  invariant 1 holds: an unconfigured deployment still skips the seam.
- The full gate battery, `go test ./...`, `go vet ./...`, `make lint`,
  `make build`, and both smoke scripts pass.

## Commands

```bash
go test -race ./internal/guardrails/... ./internal/proxy/...
bash scripts/verify-enterprise-readiness.sh
bash scripts/benchmark-overhead.sh
```

## Scope notes

- The PII detectors are deterministic patterns. A phone number without
  separators and an SSN without dashes read as plain numbers. That
  keeps false positives off ordinary identifiers.
- The moderation check inspects one window at a time on a stream. A
  category split across windows scores per window.
- Naming the moderation check without `GUARDRAILS_MODERATION_MODEL`
  refuses to start. That keeps the active-exporter invariant: no model
  call happens until configuration names one.
