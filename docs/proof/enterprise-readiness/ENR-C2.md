# ENR-C2 proof: webhooks and eventing

Date: 2026-08-30. Branch: `codex/enr-c2`.

## What shipped

- `internal/events` owns the envelope and the delivery contract:
  - Seven event types: `budget.exhausted`, `job.completed`,
    `job.failed`, `job.cancelled`, `provider.health.changed`,
    `key.created`, and `key.deleted`.
  - One JSON envelope per event: `id` (`evt-` prefixed), `type`,
    `time` (RFC 3339 UTC), and a string-to-string `data` map.
  - HMAC-SHA256 signing under `X-Starport-Signature`, value
    `sha256=<hex>`, verified with a constant-time compare.
  - An asynchronous dispatcher: one worker and a bounded queue of
    1000 events. Each endpoint gets three attempts with 250 ms
    doubling backoff under a 10 s request timeout. A dead-letter
    count covers what never delivers.
  - `NewDispatcher` returns nil when configuration names no endpoint.
    A nil dispatcher emits nothing, so every emit site holds one.
- Configuration: `STARPORT_EVENTS_WEBHOOK_URLS` (comma separated) and
  `STARPORT_EVENTS_WEBHOOK_SECRET` land in `EventsConfig`.
- Four emit sites, each behind its owning seam:
  - A budget refusal emits `budget.exhausted` beside the 402 write,
    with scope, dimension, interval, and holder identifiers.
  - A terminal job emits through the new `jobs.Notifier` leaf seam.
    The settle stamp that keeps the charge single keeps the
    notification single.
  - A provider health transition emits from the incident publisher in
    `internal/app`, one event per indicator change.
  - Key create and delete emit from the admin controller on success
    only. The payload names the key. The token never rides it.
- Telemetry: `starport_webhook_dead_letters_total` counts drops on
  the scrape through nil-safe `ObserveWebhookDeadLetters`.
- Invariant 4 holds by construction: every payload carries
  identifiers, scopes, and states only. No credential, no token, no
  prompt or response content.
- Invariant 2 holds: no configured endpoint means no dispatcher, no
  goroutine, and no outbound push.
- `docs/OPERATOR-GUIDE.md` gained a Webhooks section with the pinned
  verification sample.

## Acceptance evidence

Named tests, all green:

- `internal/events`:
  - `TestDispatcherDeliversSignedEvents` proves a test receiver
    records signed `budget.exhausted` and `job.completed` deliveries
    and the signature verifies.
  - `TestVerifyMatchesTheDocumentedSample` pins the operator guide's
    sample: secret `whsec_demo_secret` over the documented body gives
    the documented `sha256=` value.
  - `TestDispatcherRetriesAFailedDelivery` proves three attempts and
    no dead letter on eventual success.
  - `TestDispatcherCountsADeadLetterWhenTheEndpointStaysDown` proves
    the exhausted-attempts count.
  - `TestEmitAfterCloseCountsADeadLetter`,
    `TestNewDispatcherWithoutEndpointsIsOff`,
    `TestVerifyRejectsATamperedBody`, and `TestTypeForJobState` pin
    the edges.
- `internal/jobs`: `TestATerminalJobNotifiesExactlyOnce` proves one
  notification per job across ten polls.
- `internal/server`: `TestABudgetRefusalEmitsOneNamedEvent` proves
  the 402 path emits once with the holder identifiers, and
  `TestAnAllowedRequestEmitsNothing` proves the quiet path.
- `internal/server/controllers`: `TestKeyLifecycleEmitsNamedEvents`
  pins both lifecycle payloads exactly, so the token cannot ride.
  `TestAFailedMutationEmitsNothing` proves failures stay silent.
- `internal/app`: `TestAHealthTransitionEmitsOneNamedEvent` proves
  one event per transition and none on a re-confirming pass.
- `internal/telemetry`: `TestObserveWebhookDeadLettersCounts` proves
  the counter and its nil safety.

## Commands

- `go test ./internal/events/... ./internal/jobs/...`: pass.
- `go test ./...`: pass, no failures.
- `go vet ./...`: clean.
- `make lint`: 0 issues.
- `bash scripts/benchmark-overhead.sh`: PASS.
- `bash scripts/verify-doc-links.sh`: PASS.
- Repo gates: starmap-ownership, v1-architecture,
  dependency-direction, package-layout, auth-onboarding,
  credential-sharing, async-media-jobs all PASS.
- `bash scripts/verify-enterprise-readiness.sh`: `Summary: 11 passed,
  22 failed`. ENR-V01 through ENR-V11 are the green conditions.

## Scope notes

- Delivery is at-most-once per endpoint: bounded attempts, then a
  dead letter. A receiver that needs replay reads the durable
  surfaces the events point at.
- The dispatcher drains its queue at shutdown through the runtime's
  lifecycle owner.
- An operator tunes the endpoint list and the secret only. Attempt
  counts and bounds stay fixed.
- No new dependencies.
