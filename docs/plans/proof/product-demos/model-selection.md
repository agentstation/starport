# Demo model selection

Date: 2026-10-07.
Selected model: GPT-6.1 Sol.
Exact provider model ID: `gpt-6.1-sol`.

The user selected this model for the current CLI, Console, and Starmap stories.
The released first-request recording keeps its original model and evidence.

## Official contract

[OpenAI's model documentation](https://developers.openai.com/api/docs/models/gpt-6.1-sol)
confirms the exact provider ID. It supports Chat Completions without tool
calling. Tool calling requires the Responses API. The current demo stories
inspect the catalog and credential state. They do not call provider inference.

The documented context window is 1,050,000 tokens. Maximum output is 128,000
tokens. Standard text prices per million tokens are $2 input, $0.10 cached
input, $2.50 cache writes, and $10 output. Long-context, service-tier, and
regional prices have separate conditions. A catalog update must preserve
those conditions instead of treating the base prices as universal.

## Source evidence before the update

The original Starport source binary returned no GPT-6 match from its embedded catalog.
Starmap's embedded generation contains an OpenAI review candidate for
`gpt-6.1-sol`. Its code is `unresolved_model_reference`. The provider observation
already contains the exact ID, but the authored model and provider offering
records are absent.

[Starmap's authoring rules](../../../../../starmap-product-demos/README.md)
require an explicit canonical model link before an offering can publish.
Generated endpoint projections are not authoring sources. A bootstrap manifest
must match its committed generation and membership evidence.

Starport's `models search` and `models show` commands read the embedded Starmap
generation. A gateway-only remote source change cannot update those commands.
The CLI recording needs a rebuilt binary that contains the reviewed generation.

## Catalog preservation checks

The public checkpoint passed native GitHub attestation checks. Its SHA-256 is
`b9f137267c51c11ed7da98c47b25d5c4b74deaacc84b03c5a98b63d21a267f6c`.
The accepted catalog includes 260 unresolved review candidates.

The first local update replaced 32,874 existing provenance roots and removed
review candidates. We rejected that output. An authenticated replay retained
candidate evidence but changed 180 existing offerings and 17,438 provenance
roots. A partial author workspace also removed 42 logos. We rejected that output.

The user approved diagnosis and repair of the catalog path before capture.
The acceptance check requires the new authored model and exact OpenAI offering.
Existing facts, membership, original receipts, and unrelated reviews must survive.
Only the resolved OpenAI candidate may disappear. The Azure candidate remains
unresolved because we did not author an Azure offering.

Claude owns production history compaction and workflow error retention in
`/private/tmp/starmap-compaction-20261007`. This plan does not edit those files.
Our catalog changes address partial author metadata and baseline field-receipt
carriers. Root checks the accepted history without new model input separately.

An independent replay of all 86 accepted observations produced exact
catalog payload bytes and source-observation links. Payload SHA-256 is
`ced7500b3f6cd44907c382b2b1750982edeebd6adc528bcf12af19d8a94315a1`.
All 42,477 provenance roots match. The audit adds no local observation.
The native test passed in 103.52 seconds. Its temporary test file is absent.

Raw replay retains 904 historical reviews. Preparation selects current reviews
separately. The accepted artifact has 260 current reviews.

The first repaired candidate preserves all existing catalog facts and membership.
It retains all 34 original source links and all 259 unrelated current reviews.
Its remaining defect changes 161 provenance roots. These include provider
metadata receipts and two price rejection records. We rejected this candidate.

The accepted preparation uses identity-only OpenAI declarations. Its repairs apply
only to real local deltas that omit existing records. Normal source updates retain
their existing policy. The final original-history replay passed in 121.88 seconds
and reproduced exact payload bytes and source links.

The final candidate preserves every prior catalog fact, membership scope, and
all 42,477 existing provenance roots. It retains all 34 original source links
and all 259 unrelated current reviews. It adds one canonical model, one OpenAI
offering, 62 model-specific provenance roots, and one actual local source link.
Only the resolved OpenAI review disappears. The Azure review remains.

Candidate payload SHA-256 is
`ad6b71e899dd9b5e0f4ee4195cdc4b07117447479e7f6b11117245ef33a68eb9`.
Native offline preparation passed in 207.61 seconds within the existing bounds.
Root's independent comparison passed.

Native staging passed with exactly seven catalog file changes. Catalog-generation
checks and all 152 bootstrap cases pass. Runtime preservation checks pass 70
functions and 198 cases. Original bounds and source observation dates remain exact.

## Consumer publication repair

A native development app rejected the same payload during startup. Starmap
returned a rebuilt upstream timestamp for an unchanged derived generation.
The durable store retained its committed timestamp. Starport correctly rejected
this mismatch under its strict acceptance contract.

The two Starmap no-op paths now return committed client state. Four focused
runtime race tests pass. The regression covers startup, repeated source replay,
and restart. It checks immutable effective timestamps and original upstream evidence.

The native Starport development-app race regression passes with the production
Starmap HTTP handler. The old runtime reproduces the exact timestamp conflict.
Starport acceptance checks remain unchanged. Read the
[consumer timestamp evidence](consumer-timestamp-check.json).

Root rebuilt both demo binaries against this final source. The Console capture
passes at 35.206 seconds with the exact reviewed payload, current generation,
source health, and no fallback. The CLI and Starmap SVGs pass at 54.26 and 54.37 seconds. All 84 website
tests and six final browser cases pass.
