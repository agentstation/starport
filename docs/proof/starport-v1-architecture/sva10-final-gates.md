# SVA10 final-gate proof

Date: 2026-08-03
Status: done

## Fail-before

The final review found six release blockers after SVA9:

- The repository had no production `Dockerfile`. Its Compose file referenced
  stale configuration and a deleted development image.
- The `/v1` and `/api/v1` chat and embedding routes used the opposite protocol
  controllers.
- Empty secure storage had no authenticated first-identity path.
- An optional Chat UI flag exposed unauthenticated API-key creation.
- Provider environment namespaces and operator documents used old pre-launch
  names.
- The vulnerability scan found reachable issues in Go 1.26.4 and chi 5.2.2.

The first verifier run passed V01 through V11 but failed V12. Command tests did
not supply the new bootstrap key. The first vulnerability scan found four
reachable issues.

## Change

The OpenAI routes now use OpenAI controllers. The OpenRouter routes now use
OpenRouter controllers. A route contract sends an OpenRouter-only field to
both routes and proves the selected decoder and error shape.

Empty identity storage now requires
`STARPORT_SECURITY_BOOTSTRAP_API_KEY`. Startup stores only its SHA-256 hash and
creates one wildcard identity idempotently. Operators can use that identity to
create a named administrator key and then remove the bootstrap value.

The unauthenticated Chat UI key route, implementation, flag, controls, and
tests are gone. The exact provider configuration namespaces are
`GOOGLE_AI_STUDIO`, `GOOGLE_VERTEX`, and `AZURE_OPENAI`. The loader contract
rejects the old names.

Production composition is now an ordered `runtimeBuilder` pipeline. The
composition root validates input and delegates each owned construction step.
Constructor rollback still closes resources in reverse order.

The repository now has a Go 1.26.5 multi-stage `Dockerfile`, a distroless
non-root runtime, a focused `.dockerignore`, and a Valkey Compose deployment.
The root README, model catalog contract, architecture, operator guide, and
documentation index now describe the current v1 system. Seven obsolete
pre-v1 key and credential migration documents are gone.

The security fix pins Go 1.26.5 and chi 5.3.0. The HTTP edge no longer uses
chi's deprecated `RealIP` middleware. It records the direct TCP peer and
ignores untrusted forwarding headers. A regression test proves that
`X-Forwarded-For` and `X-Real-IP` cannot spoof the client IP.

The upstream evidence is:

- <https://pkg.go.dev/vuln/GO-2026-5856>
- <https://github.com/go-chi/chi>

## Required gates

These final-state commands passed:

```bash
bash scripts/verify-v1-architecture.sh
go test ./...
go test -race ./internal/inference ./internal/catalog ./internal/routing ./internal/execution ./internal/availability ./internal/responsecache ./internal/app ./internal/server
go vet ./...
make lint
make build
docker build .
bash scripts/smoke-openrouter-sdks.sh
```

The verifier result was:

```text
PASS V01 Starmap module and Go floor
PASS V02 canonical inference contract
PASS V03 routable snapshot generation contract
PASS V04 deterministic route planner contract
PASS V05 attempt state and retry budget contract
PASS V06 versioned concept repository contracts
PASS V07 response cache semantic identity contract
PASS V08 production composition fail-closed contract
PASS V09 public package boundary contract
PASS V10 OpenAI and OpenRouter protocol contracts
PASS V11 import graph architecture fitness
PASS V12 full Go test suite
Summary: 12 passed, 0 failed
```

`make lint` reported zero issues. The final container build used Go 1.26.5 and
produced image manifest
`sha256:a2a113edb4f7c63b94aa326a5936aa63add0500c8bb12f9d9e3587ff9bce0ce3`.
`docker compose config --quiet` also passed with test secret values.

## Reliability and security gates

The three 10-second fuzz runs passed:

- `FuzzCanonicalInference`: 3,724,517 executions.
- `FuzzRoutePlanner`: 1,437,141 executions.
- `FuzzSemanticKey`: 2,045,116 executions.

The fuzzers completed 7,206,774 executions. They wrote no failing corpus entry.

Focused fault tests passed for shared attempt budgets and cancellation. They
also passed for stream fallback, stream termination, recovery, explicit reset,
corrupt records, and partial-stream cache rejection.

Focused security tests passed for credential encryption, BYOK isolation and
concurrency, authentication, rate limiting, protocol errors, bootstrap
identity, and client-IP spoof rejection.

This final scan passed:

```bash
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

It reported zero reachable vulnerabilities. It also reported one imported and
one required vulnerability with no reachable call path.

An ephemeral Valkey 7 container ran the storage, distributed-cache, and
application integration suites. All three packages passed. The test stopped
and removed the container.

The routing, router, and authenticated server benchmark suites passed with
`-benchtime=1x -benchmem`. The server benchmark now requires HTTP 200 and does
not measure an authentication failure path.

## Protocol smoke state

The smoke result is:

```text
PASS raw HTTP chat
PASS raw HTTP stream
PASS raw HTTP models
PASS raw HTTP embeddings
UNVERIFIED Python OpenRouter SDK: package 'openrouter' is not installed
UNVERIFIED TypeScript OpenRouter SDK: package '@openrouter/sdk' is not installed
UNVERIFIED Go OpenRouter SDK: package is not part of this module
```

The release gate requires the raw HTTP cases, and they are green. The SDK
packages are optional. This proof does not report an absent SDK as compatible.

## Documentation and diff review

The technical-writing lint passed seven current documents with zero
diagnostics. `git diff --check` passed. The runtime scan found no old provider
credential namespaces, old provider adapter names, Chat UI key-generation
route, deprecated RealIP middleware, or public Starport package import.

The worktree remains intentionally dirty. It contains this whole-plan change
and unrelated baseline work. This work created no commit, branch, push, pull
request, deployment, or release.

SVA11 now waits only for the merge of an owner-approved final pull request.
