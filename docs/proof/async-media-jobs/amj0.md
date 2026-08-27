# AMJ0, baseline

AMJ0 pins what both repositories hold today, records the provider routes and
state words read from live documentation, and writes the verifier red. It
changes no production file.

## Both baselines

| Repository | Baseline |
| --- | --- |
| Starport | the merge of the files API cleanup, `docs/plans/files-api-plan.html` absent |
| Starmap | `b1e6ad43`, `feat(catalogs): derive five dedicated media operations (#104)` |
| Starport dependency | `github.com/agentstation/starmap v0.10.0` |
| Shipped catalog generation | `catalog-20260827T064147Z-937f0bf70acd` |

## The OpenAI video routes, read 2026-08-27

The OpenAI platform documentation lists six routes under `/videos`.

| Method | Path | What it does |
| --- | --- | --- |
| POST | `/v1/videos` | starts a generation |
| GET | `/v1/videos/{video_id}` | reads one job |
| GET | `/v1/videos` | lists jobs |
| DELETE | `/v1/videos/{video_id}` | deletes a job |
| GET | `/v1/videos/{video_id}/content` | reads the finished asset |
| POST | `/v1/videos/{video_id}/remix` | starts a generation from a finished one |

The response carries `id`, `status`, `progress`, `model`, `seconds`, `size`,
`error`, `expires_at`, `created_at`, `completed_at`, `prompt`, and
`remixed_from_video_id`. The request carries `prompt`, an optional
`input_reference` naming a stored file or an image URL, `model`, `seconds`, and
`size`.

Two facts matter for AMJ5. There is no cancel route: OpenAI stops a job through
`DELETE`. And the path parameter is `video_id`, not `id`. The plan text writes
`/v1/videos/{id}`, so AMJ5 registers `{video_id}` instead and matches both the
vendor and the file routes that already use `{file_id}`.

## The OpenRouter video routes, read 2026-08-27

| Method | Path |
| --- | --- |
| POST | `/api/v1/videos` |
| GET | `/api/v1/videos/{jobId}` |
| GET | `/api/v1/videos/{jobId}/content?index=0` |
| GET | `/api/v1/videos/models` |

A submit answers 202 with a `polling_url`. The poll response carries `id`,
`generation_id`, `polling_url`, `status`, `unsigned_urls`, `usage`, and
`error`. A caller can pass `callback_url` and receive a webhook rather than
poll. OpenRouter documents no cancel route either.

## The state words, and the set Starport keeps

| Source | Words |
| --- | --- |
| OpenAI | `queued`, `in_progress`, `completed`, `failed` |
| OpenRouter | `pending`, `in_progress`, `completed`, `failed`, `cancelled`, `expired` |

Starport keeps five states, of which three are terminal.

| Starport state | Terminal | OpenAI word | OpenRouter word |
| --- | --- | --- | --- |
| `queued` | no | `queued` | `pending` |
| `running` | no | `in_progress` | `in_progress` |
| `completed` | yes | `completed` | `completed` |
| `failed` | yes | `failed` | `failed`, `expired` |
| `cancelled` | yes | absent | `cancelled` |

Three mappings need a reason.

`cancelled` stays a state although OpenAI reports no such word. A caller that
stops its own job did not fail. One usage rule reads off the state. A failed
job and a cancelled job both draw no cost, and a completed one draws exactly
one record. Folding cancellation into `failed` would still serve accounting and
would lie to the caller.

OpenRouter's `expired` maps onto `failed` with a stated reason rather than onto
a sixth state. A provider that expired a job produced no asset, which is what
`failed` means here.

Starport's own asset expiry is a different fact and never a state. A completed
job stays completed after its bytes go, because the job did produce an asset.
AMJ6 therefore marks the record and answers 410 on the content route, and the
state the caller reads does not change.

## The census, from the shipped endpoint facts

`internal/embedded/catalog/endpoints.yaml` in `starmap v0.10.0` holds 613
endpoint facts. Their operations divide as follows.

| Operation | Endpoint facts |
| --- | --- |
| `chat-completions` | 512 |
| `embeddings` | 38 |
| `images-generations` | 26 |
| `audio-speech` | 14 |
| `audio-transcriptions` | 7 |
| `audio-translations` | 7 |

Thirteen model definitions declare `video` among their output modalities, and
each has exactly one endpoint fact. Every one of them names `deepinfra` as its
provider, and every one carries no operation at all.

| Model | Provider model identifier |
| --- | --- |
| `alibaba/wan2.2-t2v-a14b` | `Wan-AI/Wan2.2-T2V-A14B` |
| `alibaba/wan2.6-t2v` | `Wan-AI/Wan2.6-T2V` |
| `bytedance/seedance-1.5-pro` | `ByteDance/Seedance-1.5-Pro` |
| `bytedance/seedance-2.0` | `ByteDance/Seedance-2.0` |
| `google/veo-3.1` | `google/veo-3.1` |
| `google/veo-3.1-fast` | `google/veo-3.1-fast` |
| `lightricks/ltx-2.3-distilled-diffusers` | `FastVideo/LTX-2.3-Distilled-Diffusers` |
| `nvidia/cosmos3-nano` | `nvidia/Cosmos3-Nano` |
| `nvidia/cosmos3-super` | `nvidia/Cosmos3-Super` |
| `pixverse/pixverse-6-t2v` | `Pixverse/Pixverse-6-T2V` |
| `pixverse/pixverse-t2v` | `Pixverse/Pixverse-T2V` |
| `pixverse/pixverse-t2v-hd` | `Pixverse/Pixverse-T2V-HD` |
| `pruna-ai/p-video` | `PrunaAI/p-video` |

The count is 13 and not the 17 the plan states. The plan reads the catalog of
2026-08-26. The model modalities campaign then corrected 28 model files and the
routability predicate. AMJ3 works from 13, and `amj3.md` reports how many of
the 13 gained the operation.

The single-provider result also sets what AMJ4 must build. One connector serves
every video offering the shipped catalog holds. The narrow interface therefore
has one implementation, and the registry refusal stops a second provider from
claiming the operation without one.

Each of the 13 also reports `lifecycle: unknown` and `availability: unknown`.
AMJ3 sets those alongside the operation, because an offering that reports an
operation and an unknown lifecycle tells a router two different things.

## Fail-before

No route under `/v1/videos` exists, and `git grep -l '/v1/videos' internal`
returns nothing. The package `internal/jobs` does not exist. `internal/routing`
names seven operations, and none of them is a video operation.
`internal/limits` bounds requests, spend, tokens, and stored bytes. Nothing
bounds how many jobs an account holds open.

## The verifier

`scripts/verify-async-media-jobs.sh` holds all 18 conditions and reports
`Summary: 0 passed, 18 failed`.

| Condition | Statement | Owner |
| --- | --- | --- |
| AMJ-V01 | one transition table names every legal state change | AMJ1 |
| AMJ-V02 | the record exposes no provider job identifier to a caller | AMJ1 |
| AMJ-V03 | a job written by one account is unreadable by another | AMJ2 |
| AMJ-V04 | the import graph test names the package and bounds its imports | AMJ2 |
| AMJ-V05 | the catalog projects the video generation operation | AMJ3 |
| AMJ-V06 | the named operation set holds the video operation | AMJ3 |
| AMJ-V07 | a descriptor claiming the operation with no interface fails activation | AMJ4 |
| AMJ-V08 | an unknown provider state word and a spent lifetime both fail loudly | AMJ4 |
| AMJ-V09 | a route test walks the router and names the four video paths | AMJ5 |
| AMJ-V10 | a key holding no `videos:write` scope cannot submit a job | AMJ5 |
| AMJ-V11 | another account's job answers not found rather than forbidden | AMJ5 |
| AMJ-V12 | the content route serves Starport bytes and never a provider URL | AMJ6 |
| AMJ-V13 | an expired asset answers 410 and its job keeps an expired marker | AMJ6 |
| AMJ-V14 | a completed job draws exactly one usage record however often a caller polls | AMJ7 |
| AMJ-V15 | a failed job and a cancelled job draw no cost | AMJ7 |
| AMJ-V16 | an account at its outstanding job limit draws a refusal, and a terminal job frees a slot | AMJ7 |
| AMJ-V17 | the console submits a job, names a failure, and marks an expired asset | AMJ8 |
| AMJ-V18 | CI runs this gate and the evidence list names its terminal count | AMJ9 |

Two condition bodies deserve a note.

AMJ-V15 reads `internal/jobs/accounting_test.go` rather than `internal/usage`.
The accounting rule is a job rule: the terminal state decides the cost. Putting
the assertion in `internal/usage` would pull the job state vocabulary across a
seam that owns neither the states nor the transition.

AMJ-V18 carries both close statements, because this plan has one close
condition where the files API plan had two. It asserts the gate in
`.github/workflows` and in the required evidence list of `AGENTS.md` together.

## Verification

`bash scripts/verify-async-media-jobs.sh` reports
`Summary: 0 passed, 18 failed`. `go build ./...` passes.
