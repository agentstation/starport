# ENR-F1 Guardrail seam

## What shipped

`internal/guardrails` owns the check contract, the verdicts, the ordering,
and the refusal shape. A check reads canonical text and answers `allow`,
`redact`, or `refuse`. The ordered pipeline composes redactions in check
order. A check error or an unknown verdict refuses, so the seam fails
closed per invariant 6.

`internal/proxy` invokes the pipeline at both sides of a chat turn. The
pre-request pass runs before planning and rewrites redacted text in place.
Planning, caching, and the provider then read the redacted request. The
post-response pass runs before the caller reads the answer. The stream
wrapper holds answer text in a bounded 8 KiB window and checks each
window. A refusal withholds the held events and answers the refusal
error.

The usage record carries the strongest verdict of the turn and the check
behind a refusal. The HTTP layer maps a refusal to status 400 with the
`guardrail_refusal` type. Configuration names the checks per deployment
through `GUARDRAILS_CHECKS`, and an unknown name refuses to start. The
`Policy` seam selects the pipeline per account. When the operator
configures no check, composition skips the middleware, so an
unconfigured deployment adds nothing to the hot path.

## Acceptance evidence

- `go test -race ./internal/guardrails/... ./internal/proxy/...` passes.
  Contract tests cover allow, redact, and refuse. They also cover the
  fail-closed erroring check, the fail-closed unknown verdict, and the
  empty pipeline.
- Middleware tests prove the refused request never reaches planning.
  They prove the redacted request reaches the core rewritten. They prove
  the refused stream window never reaches the caller and the redacted
  window lands before the finish reason.
- `bash scripts/verify-enterprise-readiness.sh` reports 22 passed with
  ENR-V21 and ENR-V22 green.
- `bash scripts/benchmark-overhead.sh` reports p50=0ms and p99=0ms, so
  invariant 1 holds.
- The full gate battery, `go test ./...`, `go vet ./...`, `make lint`,
  `make build`, and both smoke scripts pass.

## Commands

```bash
go test -race ./internal/guardrails/... ./internal/proxy/...
bash scripts/verify-enterprise-readiness.sh
bash scripts/benchmark-overhead.sh
```

## Scope notes

- The build ships no checks yet. ENR-F2 registers the built-in PII and
  moderation checks, which turns ENR-V23 and ENR-V24 green.
- The checks read answer text alone. Reasoning text, audio, and images
  pass unread, and a later task can widen the contract.
- A window redaction rewrites the window as one synthesized delta,
  because the original delta boundaries cannot survive a rewrite.
