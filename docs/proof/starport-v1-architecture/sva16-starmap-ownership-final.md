# SVA16 Starmap Ownership Final Gates

Date: 2026-08-04

Status: `complete`

## Outcome

The cross-repository ownership boundary is complete and verified. Starmap
v0.3.0 is public and immutable. Starport uses that release without a local
module replacement. Both terminal verifiers report 12/12.

Starmap owns provider identity, catalog acquisition, inference service
templates, operation endpoints, stream endpoints, author-specific protocols
and paths, definitions, offerings, capabilities, prices, lifecycle, and
reliability sources. Its catalog acquisition can use provider API keys, cloud
credential chains, and workload identity.

Starport owns gateway authentication, encrypted inference credentials, BYOK,
provider wire adapters, tenant policy, runtime availability, route planning,
and attempt execution. An inference credential cannot activate or enter
Starmap acquisition. An acquisition credential cannot activate or enter a
Starport inference adapter.

## Final Architecture Repairs

- Provider constructors have no default provider URL.
- Provider requests fail closed without an exact Starmap offering endpoint.
- Connector code has no static health model, model discovery, fabricated
  deployment, provider model prefix, or provider-specific transport guess.
- Starmap exposes distinct non-stream and stream endpoint URLs.
- Starmap exposes author-specific endpoint protocols and paths.
- Starport binds runtime endpoint variables through one generic contract.
- Missing endpoint bindings fail provider activation.
- Vertex has no model-name, publisher, path, region, or default-location
  heuristic in Starport.
- Azure does not inject an adapter-owned API version into the Starmap v1
  endpoint.
- The obsolete YAML files that duplicated provider models, prices,
  capabilities, and URLs are gone.

The catalog mutation contract changes provider facts without a new Starport
provider conditional. These facts include model ID, operation, protocol,
endpoint, stream endpoint, capability, cache support, definition, and price.
The inference credential mutation contract changes one adapter descriptor. It
uses the same registry for validation and request authentication.

## Finding Dispositions

| Finding | Terminal disposition |
|---|---|
| SVF-010 | Resolved. Starmap has Mistral, Azure OpenAI, and Ollama provider records. Active routes require the provider, offering, adapter, and configuration intersection. |
| SVF-011 | Resolved. Google inference keys use the registered header and never enter a URL. |
| SVF-012 | Resolved. One adapter registry owns inference credential membership and validation. Unsupported Bedrock inference credentials fail closed. |
| SVF-013 | Resolved. Embeddings require an exact Starmap offering operation, endpoint, and compiled adapter capability. |
| SVF-014 | Resolved. Provider, model, and endpoint discovery use one immutable routable snapshot and make no provider discovery call. |
| SVF-015 | Resolved. Exact provider model IDs remain opaque. Starmap selects Vertex author protocol and endpoint paths. |
| SVF-016 | Resolved. Static probe models, Gemini-only filtering, and fake Azure deployments are deleted. |
| SVF-017 | Resolved. Dormant billing and sample-price fallbacks are deleted. Exact offering prices are the only price facts. |
| SVF-018 | Resolved. Starmap supplies service URLs, prompt-cache facts, provider metadata, and endpoints. One Starport adapter registry owns inference auth. |
| SVF-019 | Resolved. The ownership verifier and mutation contracts fail on duplicated provider facts and prove catalog propagation. |
| SVF-020 | Resolved. Starmap acquisition is the only dynamic catalog update path. Inference connectors do not discover models. |
| SVF-021 | Resolved. The documentation, registry resolver, model-list generation read, and verifiers are fixed. Starmap v0.3.0 is immutable, and Starport pins it without a local replacement. |

The final review accepted no residual architecture risk. All SVA16 findings
are terminal.

## Starmap Evidence

`make verify` passed. It includes:

- The full Go suite.
- The full short race suite.
- Pure-Go external consumer and pinned artifact tests.
- File-size and catalog performance gates.
- `go vet`, lint with zero issues, and coverage thresholds.
- Generated documentation and `git diff --check`.
- The CLI build and credential-free CLI checks.
- Catalog validation for 14 providers, 104 authors, 611 models, and all
  cross-references.

The embedded Vertex contract proves the Gemini `generateContent` and
`streamGenerateContent` paths. It also proves the Anthropic `rawPredict` and
`streamRawPredict` paths. The pinned artifact consumer uses archive digest
`c02f92dfa1edd05b15731a867fcfa4e3346f9439723f3f1064d20ddb09d34364`.

Starmap provider-contract PR #64 merged as
`821ed93a8848ec36ce0919ad695e1c9179cccee3`. The annotated `v0.3.0` tag resolves
to that commit. Release run `30875507565` built and signed six archives and six
SBOMs, but the inherited Homebrew token returned HTTP 401.

Recovery PR #65 merged as `9c5c3175b6a03a1259147e40af15d4ba9d6e84b7`.
It replaced the broad token with a tap-scoped SSH deploy key. Recovery run
`30881177476` reused the exact failed-run artifact. It verified the metadata,
binaries, checksums, signature, asset set, and provenance. It then published
the immutable release, verified all public downloads, updated the tap, and
passed a fresh macOS Homebrew install.

## Starport Evidence

The following gates passed:

```text
bash scripts/verify-starmap-ownership.sh
Summary: 12 passed, 0 failed

bash scripts/verify-v1-architecture.sh
Summary: 12 passed, 0 failed

go test ./...
go test -race ./internal/catalog ./internal/proxy ./internal/routing \
  ./internal/providers/connectors ./internal/app ./internal/server \
  ./internal/storage ./internal/identity ./internal/ratelimit \
  ./internal/repositorytest
go vet ./...
make lint
make build
git diff --check
```

Starport pins `github.com/agentstation/starmap v0.3.0`. The hardened V01 check
confirms the semantic version and rejects local replacements. `make lint`
reported zero issues. The three repeated 10-second fuzz gates passed:

- `FuzzCanonicalInference`: 4,260,623 executions.
- `FuzzRoutePlanner`: 1,677,590 executions.
- `FuzzSemanticKey`: 1,962,583 executions.

The repeated fuzzers completed 7,900,796 executions and wrote no failing
corpus entry.

The vulnerability scan reported zero reachable vulnerabilities. It found one
imported and one required vulnerability with no reachable call path.

The raw OpenRouter-compatible smoke checks passed for chat, stream, models,
and embeddings. Python, TypeScript, and Go OpenRouter SDK checks are
`UNVERIFIED` because those SDK packages are not installed or part of this Go
module. This proof does not count an absent SDK as compatible.

## Isolated Pre-PR Review

The repository-wide review used Claude Opus 5 with high reasoning. Ten bounded
commits exposed the complete original tree because deletion-only legacy test
fixtures made one monolithic secret scan fail closed. Two exact-tree correction
deltas then reviewed all accepted fixes. Every synthetic final tree matched the
real commit tree.

The review repairs include:

- Atomic multi-key compare-and-swap for identity creation, deletion, and rate
  limits across memory, Badger, and Valkey.
- Fail-closed embedding identity policy and tenant-safe cache identity.
- Exact OpenAI extension placement, response log probabilities, and
  OpenAI/OpenRouter stream usage shapes.
- Bounded credential compare-and-swap retries and sticky-session storage.
- Exact Vertex Anthropic request bodies and exact Starmap provider IDs in the
  Chat UI.
- Explicit, resilient startup catalog refresh and nil-safe bootstrap factories.
- Wildcard CORS without credentials.
- Fail-closed negative verifiers and a leak-free raw protocol smoke process.

The first correction review reported four findings. It accepted the Valkey
unbounded-scan and identity hash-index findings. It rejected the registry map
and log-probability findings because adjacent source and tests already proved
those contracts. A follow-up review rated the correction correct. Its one P3
finding describes a pre-existing operator-repair gap: a corrupt or foreign
identity hash index still needs direct storage repair before identity deletion.

Authorization already fails closed in that state. The review workflow stopped
scope expansion after two correction cycles, so this gap is not part of SVA16.

## Worktree State

The reviewed Starport code tree is commit `ee252ff` on branch
`codex/starport-v1-starmap`. This evidence update follows that code commit.
The branch is ready for the protected pull request. Starmap maintainers merged
PRs #64 and #65. Starmap v0.3.0 is public and immutable.

## Completion Audit

The requirement-by-requirement audit found four defects after the first
closeout:

1. `docs/ARCHITECTURE.md` still assigned model discovery and health probes to
   inference connectors.
2. `internal/registry` retained an unused model-prefix resolver that could
   bypass the exact Starmap route identity.
3. model listing read a routable snapshot and then read `Current()` again for
   each model. One response could therefore combine catalog generations.
4. V01 accepted any Starmap module declaration, including a local replacement.

The current worktree fixes all four defects. Model listing now retains one
`RoutableSnapshot` for the complete response. O05 requires
`TestModelDiscoveryRetainsOneCatalogGeneration`. O08 rejects the stale
documentation and registry fallback. The ownership verifier reports
`Summary: 12 passed, 0 failed`.

The final Starmap `make verify` run passes the full suite, short race suite,
pure-Go consumers, vet, performance, zero-issue lint, coverage, documentation,
diff, build, and catalog validation gates. The final Starport full suite,
focused race suite, fuzz, vulnerability, vet, zero-issue lint, build, protocol,
writing, and diff checks pass.

SVA11 remains `todo` until the resulting final pull request merges.
