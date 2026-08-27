# MOD0 baseline

The record the model modalities plan measures against. Every later task states
what changed from a number or a body on this page.

## Pinned commits

| Repository | Commit | Tag |
| --- | --- | --- |
| starport | `29a134c` on `main` | none |
| starmap | `ce19e93994aea601ec5209743161af0e472987a5` | `v0.8.0` |

`go.mod:13` pins `github.com/agentstation/starmap v0.8.0`, and the local
Starmap tree sits on the same commit that tag names. Starmap pull request 102
corrected a derivation that read the pricing shape. MOD12 must not restore it.

## Offering census

[`mod0-census.md`](mod0-census.md) holds the model and offering counts by
modality, and the operation counts. The three numbers that later tasks read
are 613 shipped offerings, 512 that serve chat completions, and 63 that serve
no operation at all. Decision MOD-D1 cites the modality rows.

## Refusal bodies

Captured against a loopback gateway built from the pinned Starport commit and
started with `serve --no-auth`. Both requests name a model the catalog serves,
and both fail in the codec before any route runs.

An audio part:

```
POST /v1/chat/completions
{"model":"google/gemini-1.5-flash","messages":[{"role":"user","content":[
  {"type":"text","text":"transcribe this"},
  {"type":"input_audio","input_audio":{"data":"<base64 wav>","format":"wav"}}]}]}

HTTP/1.1 400 Bad Request
{"error":{"message":"Invalid request body: messages[0]: content[1].type \"input_audio\" is not supported","type":"invalid_request_error"}}
```

A file part:

```
POST /v1/chat/completions
{"model":"google/gemini-1.5-flash","messages":[{"role":"user","content":[
  {"type":"text","text":"summarize"},
  {"type":"file","file":{"filename":"a.pdf","file_data":"data:application/pdf;base64,<base64 pdf>"}}]}]}

HTTP/1.1 400 Bad Request
{"error":{"message":"Invalid request body: messages[0]: content[1].type \"file\" is not supported","type":"invalid_request_error"}}
```

MOD2 turns both bodies into a successful decode.

## Media route baseline

The same gateway answers every dedicated media path with 404.

| Path | Status |
| --- | --- |
| `POST /v1/images/generations` | 404 |
| `POST /v1/images/edits` | 404 |
| `POST /v1/audio/speech` | 404 |
| `POST /v1/audio/transcriptions` | 404 |
| `POST /v1/audio/translations` | 404 |
| `POST /api/v1/images` | 404 |
| `POST /api/v1/audio/speech` | 404 |
| `POST /api/v1/audio/transcriptions` | 404 |

MOD14 registers all eight.

## The three OpenRouter media paths

Read from the live OpenRouter OpenAPI document at
`https://openrouter.ai/openapi.json`, fetched 2026-08-26. Paths are relative
to the `https://openrouter.ai/api/v1` base.

| Operation | Method and path | Operation identifier |
| --- | --- | --- |
| Image generation | `POST /images` | `createImages` |
| Speech | `POST /audio/speech` | `createAudioSpeech` |
| Transcription | `POST /audio/transcriptions` | `createAudioTranscriptions` |

OpenRouter publishes no image edit path. It folds an edit into `POST /images`
through reference images, so MOD14 registers `/v1/images/edits` on the OpenAI
family alone. OpenRouter also publishes no translation path, which leaves
`/v1/audio/translations` in the same position.

The same document names the routes the follow-up plans own, and each one
confirms the shape those plans assume.

| Plan | Method and path |
| --- | --- |
| Async media jobs | `POST /videos` |
| Async media jobs | `GET /videos/{jobId}` |
| Async media jobs | `GET /videos/{jobId}/content` |
| Reranking | `POST /rerank` |
| Files API | `GET /files` |
| Files API | `POST /files` |
| Files API | `GET /files/{file_id}` |
| Files API | `DELETE /files/{file_id}` |
| Files API | `GET /files/{file_id}/content` |

The files API plan registers the same five routes the last five rows name.

## Verifier

`scripts/verify-model-modalities.sh` reports `Summary: 0 passed, 26 failed`
against the pinned commit. Condition MMD-V24 needed one revision to reach red:
the first spelling looked for `output_modalities` anywhere under `console/src`,
and the model detail view already prints it. The condition now reads
`console/src/lib/modelFilter.ts:88`, which filters on input modalities alone.
