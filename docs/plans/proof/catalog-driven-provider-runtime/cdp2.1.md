# CDP2.1 model-fact migration proof

Date: 2026-08-10.

## Scope

CDP2.1 removed compiled model-family inference from the Anthropic, Google, and
OpenAI acquisition clients. Direct provider-response fields remain acquisition
evidence. Transport vocabulary remains compiled where it is necessary to call
an API. Provider YAML and verified observations now supply model authorship and
other catalog facts.

The change replaces transient reconciliation issues with durable review
candidates. An unknown provider model stays outside the canonical catalog, but
its exact identity and evidence are available for review in a published
generation. This change is a direct prelaunch break. It adds no compatibility
reader or legacy field.

## Fail-before

The implementation changed the tests before production code. This command
failed:

```text
go test ./internal/providers/anthropic ./internal/providers/google ./internal/providers/openai
```

The failures showed that Anthropic inferred an author and Claude-family
features. Google fabricated descriptions, timestamps, authors, and model-family
features. OpenAI supplied fallback authors and base feature defaults. The
provider responses did not contain these values.

The old reconciliation result also exposed only process-local issues. A run
with an unknown offering could produce no catalog changes and no durable
generation, so the evidence did not survive the run.

## Implementation evidence

- `ProviderEndpoint` no longer has `feature_rules`. Embedded provider YAML no
  longer contains executable model-family rules.
- Anthropic, Google, and OpenAI clients preserve only direct response facts.
  They do not add fallback authors, descriptions, timestamps, capabilities, or
  limits from a model ID.
- `AuthorMapping.Resolve` is the common authored mapping contract. It supports
  exact, case-insensitive, and specific glob mappings. Google Vertex AI and
  Groq author mappings now use this contract.
- Google publisher parsing accepts publisher-relative and project-prefixed
  resource names. The offering keeps the exact provider model ID from the model
  resource slot.
- Unknown offerings remain unpublished. The reconciler selects one evidence
  observation in deterministic order. It selects the primary source first. It
  then uses source and observation IDs in lexical order.
- Each `ReviewCandidate` records its code, provider ID, and exact provider model
  ID. It also records the source ID, observation ID, revision, checksum, reason,
  and optional prior reviewed model link.
- Generation manifest version 2 requires `review_candidates`. Validation binds
  each candidate to a linked observation, requires an equal revision and
  checksum, and enforces uniqueness and canonical order.
- An evidence-only run publishes a generation even when its canonical catalog
  changeset is empty. Regression tests prove that the unknown offering does not
  enter the canonical model offerings.
- Catalog schema version 5 removes `feature_rules`. Bootstrap generation can
  replace an older manifest envelope, but the runtime parser still rejects an
  unsupported schema.
- The immutable embedded generation is
  `catalog-20260810T233115Z-dbed361ef128`. Its payload checksum is
  `sha256:4b489fe712e8434fe5cf53cd59fc7b61c56982041cfaada644b697d9602bc169`
  and its semantic checksum is
  `sha256:dbed361ef12852bc3449686cae67214948fb3ba74f98298fba10c872853735d0`.
  The pinned external consumer uses digest
  `c944c2008170c383144b77a3e8e3c634a3906f627dc97a0cdab060049ea8a26a`.

## Verification

- The fail-before provider tests passed after the implementation. Focused
  catalog, reconciliation, quarantine, acquisition, generation, exact-ID, and
  embedded-catalog tests also passed.
- `go test ./...` passed across the repository.
- `make verify` passed with normal Go scheduling. No `GOFLAGS` override was
  present. It passed ordinary tests and six external pure-Go consumer tests. It
  also passed file-size checks, the complete race suite, vet, performance,
  lint, coverage, documentation, generated-diff, build, version, and catalog
  validation. The final output was `repository verification passed`.
- The race suite used the repository command with default package parallelism.
  The race detector reported no issue.
- The performance gate passed three runs at 8.814, 9.166, and 8.537 ns/op with
  zero allocations. Lint reported zero issues.
- All coverage floors passed. The command validated 14 providers, 104 authors,
  and 611 models.
- `git diff --check` passed. `rg -n 'GOFLAGS'` returned no match. No scheduler
  cap is present in Starmap source, tests, scripts, or documentation.
- The architecture scan found no executable `FeatureRule` or
  `ReconciliationIssue` production type. The only current `feature_rules` text
  is a changelog statement and two tests that prevent its return. Historical
  observation provenance retains prior source evidence and is not executable
  configuration.
- `bash scripts/verify-catalog-driven-providers.sh` reported
  `Summary: 3 passed, 16 failed`. CDP-V01, CDP-V03, and CDP-V10 remain green.
  The remaining cross-repository conditions belong to later plan items and
  remain visibly red.
