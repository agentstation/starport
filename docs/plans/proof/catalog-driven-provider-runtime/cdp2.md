# CDP2 provider acquisition migration proof

Date: 2026-08-10.

## Scope

CDP2 migrated all 14 embedded Starmap providers to the strict credential
schema. Catalog acquisition now selects catalog-declared profiles, fields,
endpoint bindings, authentication primitives, placements, and typed protocol
options. Shared acquisition code contains no provider credential roster.

The change removes the former API-key, environment-variable, catalog-auth,
endpoint-default, request-version, scope, and price-unit selection paths. It is
a direct prelaunch break. It adds no legacy reader or compatibility alias.

## Fail-before

The CDP0 verifier reported that CDP-V03 and CDP-V10 failed. Provider YAML used
the old `api_key`, `env_vars`, and `catalog.auth` fields. Acquisition clients,
request authentication, and CLI checks selected behavior from provider facts
in Go. Runtime provider objects also retained environment-loaded credential
values.

The old request path could not prove that credential material was request
bound. The credential-free repository check also maintained a fixed list of
provider environment variables to unset.

## Implementation evidence

- Every embedded provider uses credential fields, profiles, and ordered
  catalog and inference alternatives.
- `providers.yaml` defines placements, endpoint bindings, scopes, and typed
  protocol options.
- Conventional environment names have first priority. The derived
  `STARMAP_<PROVIDER>_<FIELD>` name has last priority. The first nonempty value
  wins, and an invalid selected value fails without fallback.
- A primitive-keyed resolver replaces provider-keyed authentication selection.
  API-key, bearer, Google-default, Azure-default, AWS-default, and no-auth
  primitives have explicit behavior. Azure-default and AWS-default remain
  unavailable and fail closed until CDP3 admits their source adapters.
- Provider clients receive `ProviderCredentialMaterial` for each call. Clients
  and transport objects do not load or retain environment secrets.
- Placement kind and scheme select header and query authentication.
  Endpoint bindings validate URL values and percent-encode path segments.
- OpenAI-compatible catalog price units and the Anthropic protocol version now
  come from typed YAML options. Google scopes, project, location, and API-key
  placement come from catalog credential metadata.
- CDP2 deleted old provider validation and runtime credential fields. The
  provider table now derives missing-key names from catalog alternatives. In an
  empty environment it reports `OPENAI_API_KEY`, `(not set)`, and `Missing` for
  OpenAI.
- The credential-free verification check uses `env -i` with an explicit
  non-secret allowlist. Its regression test rejects provider-specific `-u`
  entries.
- The immutable embedded generation is
  `catalog-20260810T221739Z-ea42f3868179`. Its payload checksum is
  `sha256:0d7b6a52e2db3afd7149ed98da5fbfaf94862215eb20f15e70dda67df4d7bf5e`
  and its semantic checksum is
  `sha256:ea42f3868179424d235b8f4b54fc8c440292b30cf4e16bd1b2fcd7062b3c05c5`.
  The pinned external consumer uses digest
  `d4e41df425ec3cd8445232c7a8b16956c77b27821763774084c6a85b3112aede`.

## Verification

- The focused acquisition, auth, transport, CLI, embedded, catalog, source,
  and provider tests passed.
- `make verify` passed with ordinary Go scheduling and no `GOFLAGS` override.
  It passed unit tests and six external consumer tests. Pure-Go execution and
  file-size checks passed. The race matrix, vet, performance budget, and lint
  passed. Lint reported zero issues.
- All coverage floors and documentation checks passed. Generated-diff, build,
  version, and list checks passed. The command validated 14 providers, 104
  authors, and 611 models.
  The final output was `repository verification passed`.
- The final race run used `go test ./... -race -short -timeout=20m` with default
  package parallelism. The root package passed in 281.736 seconds. The race
  detector reported no issue.
- `rg -n 'GOFLAGS'` returned no match in Starmap. No scheduler cap is present in
  source, tests, scripts, or documentation.
- The performance gate passed three runs at 9.225, 9.015, and 8.925 ns/op with
  zero allocations.
- `bash -n scripts/verify.sh`, ShellCheck when available, and
  `git diff --check` passed.
- `bash scripts/verify-catalog-driven-providers.sh` reported
  `Summary: 3 passed, 16 failed`. CDP-V01 remained green. CDP-V03 and CDP-V10
  became green. The Starmap halves of environment precedence and
  primitive-keyed transport checks passed. The cross-repository conditions
  remain red until their Starport tasks.

The rejected `GOFLAGS=-p=2` attempt is not acceptance evidence. The normal,
uncapped gate is the acceptance result.
