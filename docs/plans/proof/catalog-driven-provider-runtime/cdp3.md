# CDP3 Starmap credential sources and operator surfaces

Status: `done`

Work commit: Starmap `54bd8de9ea9f6c26188bf2ebb54dc5f647758ef9`

## Fail-before evidence

- The prior resolver used fixed environment discovery and separate ADC logic.
- `STARMAP_OPENAI_API_KEY` could not satisfy catalog acquisition.
- The CLI notification path used a fixed provider list.
- Credential material could not represent a static version or a renewable
  lease through one contract.
- File-backed credentials had no shared rotation contract.

## Delivered contract

- Catalog credential metadata now drives ambient environment discovery.
- Conventional names precede derived `STARMAP_<PROVIDER>_<FIELD>` aliases.
- Explicit `env:` and `file:` references precede ambient discovery.
- Explicit fallback accepts only the typed not-configured result.
- One resolver owns caching, single-flight work, versions, expiry, and leases.
- Google, Azure, and AWS default chains use their official Go clients.
- Google catalog acquisition receives request-scoped credential material.
- Provider commands, update, and serve share the application resolver.
- CLI status hints use the provider IDs from the catalog preflight result.
- The obsolete duplicate ADC package and fixed notification roster are gone.

## Source conformance

`TestCredentialSourceConformance` passed these 14 vectors:

1. `static`
2. `default_chain`
3. `version`
4. `expiry`
5. `lease`
6. `cancellation`
7. `concurrency`
8. `denial`
9. `redaction`
10. `rotation_in_place`
11. `rotation_atomic_replace`
12. `rotation_symlink_swap`
13. `rotation_mounted_replace`
14. `rotation_agent_rerender`

The rotation checks compare content. They do not use modification time as the
sole change signal.

## Verification history

The first uncapped `make verify` run passed all 85 ordinary packages. The
pure-Go gate then found a stale `server-storage` consumer module. `go mod tidy`
updated that consumer to the selected AWS dependency graph.

The second uncapped run passed the complete race suite. Lint then found four
new defects. The fix preallocated the material version slice, removed two dead
production test hooks, and removed unused Google context parameters. The same
run then found stale generated OpenAPI data.

The final uncapped `make verify` run passed:

- 85 ordinary packages.
- Six isolated pure-Go consumer modules and the S3 package.
- The complete repository race suite with `CGO_ENABLED=1`.
- `go vet ./...`.
- `golangci-lint` with zero issues.
- Three catalog-access benchmarks at 8.850, 8.794, and 8.850 ns/op.
- Zero bytes and zero allocations for each catalog-access benchmark.
- All 15 critical seam coverage gates.
- Generated Go documentation and OpenAPI checks.
- File-size, whitespace, build, catalog validation, provider-list, and
  model-list checks.

The coverage results were:

| Module | Coverage |
|---|---:|
| `internal/catalog/pipeline` | 82.4% |
| `internal/catalog/query` | 77.7% |
| `internal/providers/clients` | 93.3% |
| `internal/sources/providers` | 84.1% |
| `internal/server/middleware` | 97.4% |
| `internal/server/openrouter` | 88.8% |
| `internal/server/params` | 98.5% |
| `internal/server/response` | 100.0% |
| `internal/server/sse` | 92.7% |
| `internal/transport` | 85.7% |
| `internal/catalog/authority` | 95.6% |
| `pkg/catalogs` | 72.8% |
| `pkg/errors` | 88.3% |
| `internal/catalog/reconciler` | 83.0% |
| `pkg/sources` | 68.7% |

The strict writing checks for the changed README and architecture text passed
with zero diagnostics. The repository documentation gate also passed.

## Campaign verifier

`bash scripts/verify-catalog-driven-providers.sh` reported:

```text
Summary: 3 passed, 16 failed
```

CDP-V01, CDP-V03, and CDP-V10 pass. The remaining failures belong to later
Starport runtime tasks or the final cross-repository conformance task.
