# SVA12 Hardcoded Catalog-Fact Audit

Date: 2026-08-03

Status: fail-before evidence complete

Starport baseline: `bf6b09e1136913da87d815abfa94b975927041a0`

Starmap baseline: `918bb3489431b600bd66930d904ef4c7c7b0e651`

## Outcome

The audit found 13 ownership defects. Eleven defects can change public behavior.
One defect can expose a provider key through URLs. The existing architecture
verifier does not detect any defect.

The review also confirmed an important existing asset. Starmap already owns
provider credential discovery and live model acquisition. Starport does not
use that acquisition path.

## Method

The audit inspected all 122 production Go files under `internal` and `cmd`.
It searched provider identifiers, URLs, model names, capabilities, prices,
limits, lifecycle values, and availability values. It then read each owning
call path and its tests.

Seventeen production files contain supported-provider literals. Eight files
contain provider URLs. Six files contain recognizable model names or family
conditions.

The review compared those values with the exact Starmap v0.2.0 schema and the
current Starmap source. It also inspected Starmap acquisition, provider clients,
credential loading, reconciliation, and atomic publication.

## Authentication boundary

There are two independent credential planes.

| Plane | Owner | Purpose | Secret storage |
|---|---|---|---|
| Catalog acquisition | Starmap | Fetch provider model facts and build catalog generations. | Starmap runtime credential resolvers. Never serialized. |
| Gateway and inference | Starport | Authenticate callers and call provider inference APIs. | Starport encrypted credential repositories and runtime adapter state. |

Starmap uses API keys, Google ADC, cloud credential chains, and workload
identity to build the map. Starport must not reuse those values for inference.
Starmap must not read Starport BYOK or gateway credentials.

## Ownership classification

| Concept | Canonical owner | Required treatment |
|---|---|---|
| Provider identity, aliases, and display metadata | Starmap | Read from one catalog generation. |
| Catalog endpoints and acquisition auth | Starmap | Use Starmap acquisition and credential resolvers. |
| Inference endpoints and provider service facts | Starmap | Read exact provider or offering facts. |
| Status pages, privacy, retention, and governance | Starmap | Project the provider record. |
| Model definitions and exact provider offerings | Starmap | Use exact opaque offering identities. |
| Price, limits, lifecycle, regions, and availability evidence | Starmap | Read the exact offering. |
| Provider-scoped service capabilities | Starmap | Add an offering fact when the current schema cannot express it. |
| Compiled inference adapter constructors | Starport | Keep one typed adapter registry. |
| Inference credential fields and validation | Starport | Keep with the adapter registry. |
| Encrypted provider inference secrets and BYOK | Starport | Keep in the credential repository. |
| Routing, retries, latency, circuit state, and affinity | Starport | Keep as runtime policy and state. |
| Azure deployments and local Ollama inventory | Operator and runtime observation | Normalize into one Starmap generation. |
| OpenAI and OpenRouter wire paths and field names | Starport | Keep in protocol adapters. |

## Findings

### A01 Provider universes diverge

Severity: P1 blocker

Starport compiles eight production adapters. `providerSpecs` configures Mistral,
Azure OpenAI, and Ollama at `internal/app/providers.go:26`. Starmap v0.2.0 has
no provider record for those three IDs.

The routable snapshot iterates Starmap providers first at
`internal/catalog/control_plane.go:319`. A configured adapter without a catalog
provider cannot produce a route. The code can therefore register an adapter
that discovery and routing can never use.

Target contract: active provider equals catalog provider, compiled adapter,
and valid operator configuration. Startup fails when a configured provider
lacks either static contract.

Owner: SVA13 and SVA14.

### A02 Starport duplicates Starmap model acquisition

Severity: P1 architecture

Starmap loads provider credentials in `pkg/sources/providers.go:368`. Its
provider clients fetch model data. `acquisition.Syncer.Sync` reconciles facts
and publishes an atomic generation at `acquisition/syncer.go:91`.

Starport defines `Connector.Models` at
`internal/providers/connectors/interface.go:27`. The registry calls it for
validation at `internal/registry/registry.go:494`. Proxy discovery also calls
it during requests at `internal/proxy/proxy.go:832`.

Target contract: Starmap acquisition is the only dynamic model-update path.
Inference adapters do not own model caches or catalog discovery.

Owner: SVA13 through SVA15.

### A03 Inference validation mixes two concerns

Severity: P1 blocker

`keyManager.ValidateKey` has a provider switch at
`internal/providers/byok/validation.go:14`. The switch mixes provider membership
with Starport inference credential validation. It also accepts `aws-bedrock`,
but Starport has no Bedrock inference adapter.

The validator dispatch itself does not belong in Starmap. It is executable
Starport adapter behavior. Provider membership must come from the active
provider intersection.

Target contract: one Starport adapter descriptor owns construction, supported
operations, inference credential fields, local checks, signing, and probes.

Owner: SVA14.

### A04 Google places an inference key in the URL

Severity: P1 security

`GoogleAIStudioConnector.getEndpoint` appends `?key=` at
`internal/providers/connectors/google_aistudio.go:145`. URLs can enter access
logs, proxies, traces, metrics, and transport errors.

Starmap's provider metadata confirms the service supports the
`x-goog-api-key` header. Starport still owns inference authentication and must
apply that header in its adapter.

Target contract: no provider secret enters a URL, error, log, fixture, or
proof file.

Owner: SVA14.

### A05 Embedding routing assumes capability

Severity: P1 blocker

`findEmbeddingsProvider` calls every connector model API at
`internal/proxy/proxy.go:886`. It returns the first matching model and assumes
embedding support at line 900.

This can select a connector that does not support embeddings. It can
also miss a valid Starmap embedding offering.

Target contract: route selection intersects Starmap definition capability,
offering service capability, and compiled adapter operation support.

Owner: SVA15.

### A06 Endpoint discovery bypasses the snapshot

Severity: P1 blocker

`GetModelEndpoints` calls provider APIs during the request at
`internal/proxy/proxy.go:832`. It ignores discovery errors and returns
`available: true` with a hardcoded gateway path.

The result can combine facts from different moments. It also bypasses offering
price, limits, lifecycle, endpoint, and runtime availability.

Target contract: provider, model, and endpoint discovery project one retained
routable snapshot.

Owner: SVA15.

### A07 Adapters alter or infer opaque offering identity

Severity: P1 blocker

Starmap defines `ProviderModelID` as an exact opaque value in
`pkg/catalogs/provider_offering.go:18`. Anthropic rewrites dots in that value at
`internal/providers/connectors/anthropic.go:246`.

Vertex selects an Anthropic publisher by searching for `claude` at
`internal/providers/connectors/vertex_ai.go:179`. Starmap already has exact
offering endpoints and author identity.

Target contract: adapters pass exact provider model IDs unchanged. They use an
offering endpoint or a typed adapter protocol field.

Owner: SVA13 and SVA15.

### A08 Static model facts can become false

Severity: P1 reliability

Anthropic, Google AI Studio, Groq, and Vertex health checks contain fixed model
IDs. These checks fail when a provider retires a model.

Google AI Studio discovery removes every model without `gemini` in its ID at
`internal/providers/connectors/google_aistudio.go:94`. Azure returns fake
deployments and timestamps at `internal/providers/connectors/azure.go:156`.

Target contract: probes use a current route or provider status signal. Azure
and Ollama use tenant or local observations. No adapter fabricates models.

Owner: SVA14 and SVA15.

### A09 Provider metadata and endpoint defaults have several owners

Severity: P2 reliability

`Loader.postProcess` repeats provider base URLs at
`internal/config/loader.go:113`. Several connector constructors repeat the same
URLs. BYOK validation repeats provider endpoints again.

`ListProviders` hardcodes `RequiresAuth: true` at
`internal/proxy/proxy.go:778`. It also invents the provider description instead
of projecting Starmap metadata.

Target contract: Starmap supplies factual defaults and metadata. Starport
configuration supplies only operator overrides. Inference authentication
remains a Starport adapter concern.

Owner: SVA13 through SVA15.

### A10 Cache support and billing use provider-wide guesses

Severity: P2 reliability and financial

`ProviderSupportsCacheControl` contains a provider list at
`internal/proxy/cache_control.go:3`. Actual service support can differ by
offering. Starport adapter encoding support is a separate condition.

`getStandardCost` contains sample provider prices and a default at
`internal/providers/byok/provider_keys.go:473`. The production method has no
consumer outside its interface and tests.

Target contract: prompt-cache behavior intersects an offering fact and adapter
support. V1 deletes unused billing. Any later billing requires an exact
offering, price unit, and effective price.

Owner: SVA13 and SVA15.

### A11 Code infers model ordering and representative offerings

Severity: P2 correctness

Model listing parses Gemini versions from opaque names at
`internal/proxy/proxy.go:519`. It also uses the first alphabetical route as the
top provider at line 675.

Alphabetical order does not select the cheapest, fastest, or policy-preferred
offering. Starmap release metadata and explicit route policy already provide
stable facts.

Target contract: display ordering uses catalog metadata or protocol-stable
lexical order. A response never labels an arbitrary route as the top provider.

Owner: SVA15.

### A12 Endpoint type controls Starmap cloud auth

Severity: P2 architecture

Starmap correctly supports Vertex ADC. Its checker selects Google Cloud auth
from `EndpointTypeGoogleCloud` at `internal/auth/checker.go:18`.

That condition does not name the stable concept. Azure, AWS, and other clouds
can use API keys, default chains, workload identity, or managed identity.

Target contract: typed catalog-acquisition auth metadata selects a resolver.
Endpoint protocol selection remains independent.

Owner: SVA13.

### A13 The prior verifier can report a false closeout

Severity: P1 verification

`scripts/verify-v1-architecture.sh` reports `Summary: 12 passed, 0 failed` on
this baseline. The new ownership verifier reports
`Summary: 0 passed, 12 failed`.

The existing focused tests also pass across six packages. They do not mutate a
provider fact or prove that a new fact flows without another conditional.

Target contract: fixed ownership checks and a synthetic Starmap fact mutation
must pass before closeout.

Owner: SVA12 and SVA16.

## Valid Starport-owned constants

The audit does not require every string to move into Starmap. These values stay
in Starport:

- OpenAI and OpenRouter HTTP paths and field names.
- `openrouter/auto` as a Starport wire alias.
- Adapter constructor functions and provider wire codecs.
- Inference credential field names, signing, validation, and active probes.
- Retry budgets, timeouts, rate limits, cache policy, and routing weights.
- Runtime latency, health, circuit, affinity, and tenant policy.
- Operator endpoint overrides and tenant-specific deployment names.
- The Starport gateway's own API-key and authorization contracts.

## Fail-before verification

Command:

```text
bash scripts/verify-starmap-ownership.sh
```

Result:

```text
FAIL O01 through O12
Summary: 0 passed, 12 failed
```

Control command:

```text
bash scripts/verify-v1-architecture.sh
```

Control result:

```text
PASS V01 through V12
Summary: 12 passed, 0 failed
```

The fail-before evidence changes no production behavior.
