# POR7 Starport connector-owned HTTP transport proof

POR7 starts from clean Starport `main` at
`03651443b8d71884615a635c6f4b4a52acad57aa`.

The task will place required provider HTTP transport policy with the connector
concept that owns it. It will remove unused generic production surfaces and
duplicate elapsed-time ownership without changing connector behavior.

## Fail-before evidence

The focused baseline passes all five packages:

```text
go test -count=1 ./internal/httpclient ./internal/providers/connectors ./internal/execution ./internal/router ./internal/architecture
```

The generic package contains eight Go files and 14 exported declarations. Its
only production caller uses `DefaultConfig`, `New`, and `GetHTTPClient` in the
private connector builder. The remaining surface is package-owned code and
self-tests.

The production client has a five-minute `http.Client.Timeout`. This total
timeout duplicates the execution-owned elapsed budget and can stop a healthy
stream body. Its monitored transport measures elapsed time again and adds
`X-HTTP-Client-Provider` and `X-HTTP-Client-Duration-Ms` to provider response
headers.

The unused rate-limit wrapper is the only source that imports
`golang.org/x/time/rate`. The module therefore keeps the direct
`golang.org/x/time v0.15.0` dependency only for unused production surface. None
of the four required POR7 contract tests exists at the baseline.
