# Independent review prompt: catalog-driven provider runtime

Copy the prompt below into a fresh independent model session. Start the session
with a new context window. Run it from a parent directory that contains both
repositories.

---

You are an independent architecture reviewer. Review two Go repositories and
one proposed execution plan. Start with no assumptions from prior
conversations. Do not implement changes and do not edit files.

Repositories:

- Starport: `https://github.com/agentstation/starport`
- Starmap: `https://github.com/agentstation/starmap`

Pinned review baselines:

- Starport `main`: `101c32d8fd6991586e8ae7003baf199cef651844`
- Starmap `main`: `7f53767dfb68efbde2cec80c3d739f5badb43230`

Primary plan:

- `starport/docs/plans/catalog-driven-provider-runtime-plan.html`

Baseline proof:

- `starport/docs/plans/proof/catalog-driven-provider-runtime/README.md`

## Read these sources completely

Read the sources in this order:

1. `starport/AGENTS.md`
2. `starport/docs/TASKS.md`
3. `starport/docs/ARCHITECTURE.md`
4. `starport/docs/plans/catalog-driven-provider-runtime-plan.html`
5. `starport/docs/plans/proof/catalog-driven-provider-runtime/README.md`
6. `starmap/AGENTS.md`
7. `starmap/docs/ARCHITECTURE.md`
8. `starmap/docs/REMOTE_CATALOG_PROTOCOL.md`
9. `starmap/docs/TESTING.md`
10. Both repository `go.mod` files.

Then inspect every implementation and test path that the plan names. Search
both repositories for provider IDs, model IDs, endpoint URLs, credential field
names, environment variable names, header names, schemes, and provider switches.

## Main goal

Adding valid provider YAML to Starmap must propagate provider and model facts
into an immutable catalog generation. Starport must derive runtime provider
support from that generation when it supports the declared transport and
authentication primitives.

Starport must not contain a compiled provider roster. Starmap and Starport can
compile transport codecs and cloud authentication implementations. Examples
include OpenAI, Anthropic, Google, Ollama, Google default credentials, Azure
default credentials, and AWS default credentials.

The intended runtime condition is:

```text
routable providers =
    catalog providers
  intersection compiled transport primitives
  intersection compiled authentication primitives
  intersection operator configuration
```

Provider IDs, model IDs, offerings, prices, limits, and capabilities must come
from Starmap data or verified provider evidence. The same rule covers endpoint
bindings, credential names, request placement, and status facts.

## Credential rules to review

Starmap owns serializable credential metadata. It never serializes secret
values. Starmap and Starport resolve secret values independently for their own
credential planes.

For ambient API-key discovery, conventional provider names come first. Product
aliases come second. The first non-empty value wins. An invalid selected value
fails closed and does not fall through.

Required examples:

| Process | First candidate | Second candidate |
|---|---|---|
| Starmap catalog acquisition | `OPENAI_API_KEY` | `STARMAP_OPENAI_API_KEY` |
| Starport inference | `OPENAI_API_KEY` | `STARPORT_OPENAI_API_KEY` |

The same rule applies to catalog-declared keys such as `FIREWORKS_API_KEY`.
Starport removes `STARPORT_PROVIDERS_OPENAI_API_KEY`. This pre-release change
adds no compatibility path.

An explicit secret reference takes precedence over ambient discovery. Its
failure is terminal unless the operator configures a fallback. Workload
identity remains an authentication method and not a secret string.

Review the future secret-source seam for these stores:

- AWS Secrets Manager and AWS Parameter Store
- Google Cloud Secret Manager
- Azure Key Vault
- HashiCorp Vault and OpenBao

Compare Helmfile vals, Go CDK runtimevar, official cloud clients, and the
official HashiCorp client. Review maintenance, API stability, licenses,
dependency closure, binary size, cancellation, workload identity, caching,
version selection, redaction, and testability. Do not select a project from
feature count alone.

## Required review questions

1. Does the plan remove every compiled provider roster from Starport?
2. Does the Starmap schema keep acquisition and inference values isolated?
3. Does the schema keep repeated provider credential facts dry?
4. Can one new OpenAI-compatible provider work without Starport source changes?
5. Do compiled registries use transport and authentication primitives only?
6. Do model facts leave Google, Anthropic, and OpenAI acquisition Go code?
7. Does environment discovery follow the stated precedence for every provider?
8. Can explicit secret references fail without leaking or silently falling back?
9. Can cloud default credentials and workload identity remain renewable?
10. Can Starport consume a verified catalog update without a binary release?
11. Does the synthetic unknown-provider test prove the complete contract?
12. Does each task fit one coherent pull request and preserve dependency order?
13. Do task checks cover success, invalid data, failure, refresh, and recovery?
14. Does the plan preserve exact opaque provider model IDs?
15. Does any YAML field encode Starport policy that belongs in Starport?

## Architecture review rules

- Distinguish a provider fact from a transport primitive.
- Distinguish credential metadata from a credential value.
- Keep policy separate from transport, storage, framework, and vendor details.
- Name each concept seam and the contract it owns.
- Reject a generic helper package without one owned concept.
- Prefer direct pre-release changes. Do not recommend legacy aliases.
- Preserve reviewed model identity links and last-known-good catalog behavior.
- Treat remote catalog notifications as hints for verified immutable fetches.
- Check second-order effects in setup, diagnosis, BYOK, docs, release, and tests.
- Check third-order effects in caching, refresh, routing, and credential renewal.

## Required output

Start with one verdict: `approve`, `approve with changes`, or `reject`.

Then provide these sections:

1. **Blocking findings** with severity, confidence, file path, line, evidence,
   consequence, and exact plan change.
2. **Non-blocking findings** in the same format.
3. **Hardcode inventory** grouped by allowed transport primitive, prohibited
   provider fact, prohibited model fact, and uncertain case.
4. **Credential contract review** with the resolved precedence and security
   failure behavior.
5. **Secret library verdict** with a comparison table and one recommendation.
6. **Task-order review** with every plan task marked keep, split, merge, reorder,
   or remove.
7. **Missing tests** with the exact observable contract for each test.
8. **Plan edits** as a concise list that maps each change to a task ID.
9. **Residual risks** that remain after all proposed work completes.

Support each finding with source evidence. Mark an inference as an inference.
State any file or network access limitation. Do not praise detail. Review the
actual source and executable contracts.

---
