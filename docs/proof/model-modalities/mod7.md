# MOD7 console composer

Condition MMD-V10. Commit `e020792`.

## What changed

The composer had one control. It read the image modality alone, and it sent
every file as an `image_url` part. A model that reads audio or a PDF could not
receive one from the console.

The composer now has three controls, one for each kind. A new module,
`console/src/lib/attachments.ts`, owns the words the composer and the
conversation store share.

## The seam

Two files needed the same four facts. Which kinds exist. What the catalog
calls each kind. What a file picker accepts for each kind. What each kind
becomes on the wire. Two copies of those facts drift, so the new module holds
one copy.

## The word the catalog uses

The models API answers with `pdf` for a document. It does not answer with
`document`. `CATALOG_MODALITY` holds the translation, and it holds it once.
This mirrors `modelInputModalities` in `internal/router`, which does the same
job on the Go side.

A control that looked for `document` would refuse every model that reads
one. The test model `google/gemini-2.5-flash` carries `pdf` in its
modality list for exactly this reason.

## The three wire shapes

The shapes disagree, so `attachmentPart` encodes each one on its own terms.

| Kind | Part type | How it carries bytes |
| --- | --- | --- |
| image | `image_url` | data URL under `image_url.url` |
| audio | `input_audio` | raw base64 under `data`, format word under `format` |
| document | `file` | data URL under `file.file_data`, name under `file.filename` |

Audio is the one that a shared encoder would break. Its shape names the format
in its own field and expects the base64 alone. A data URL header left in place
reaches the provider as part of the bytes.

The format word comes from the filename extension first, because that is what
the reader chose. A file that arrives without an extension falls back to the
media type. A browser reports an MP3 as `audio/mpeg`, and no provider accepts
`mpeg` as a format word, so `AUDIO_FORMAT_ALIASES` maps it to `mp3`.

## Stored conversations

A conversation written before an attachment carried a kind holds an `images`
array of data URLs and nothing else. Those records are the reader's own
history. `turnAttachments` reads either shape, so an old turn still renders and
still replays on a retry.

## Verification

`pnpm --dir console check` passed: build, typecheck, and 115 tests in 19 files.

`scripts/verify-model-modalities.sh` reports 11 passed and 15 failed, with
MMD-V10 passing. The 15 remaining conditions belong to MOD8 through MOD16.

`go test ./...` passed and `go vet ./...` reported nothing. This commit
changes no Go file, and the run proves it.

## Fail-before

Five mutations, each against a named test.

| Mutation | Test that fails |
| --- | --- |
| `CATALOG_MODALITY.document` from `pdf` to `document` | the audio modality test and the document attach test |
| audio `data` keeps the data URL header | the audio part test and the media type fallback test |
| drop the `mpeg` to `mp3` alias | the audio format falls back to the media type |
| `turnAttachments` drops the legacy `images` branch | messageContent still reads a stored images record |
| the model switch keeps every attachment | a model switch drops the attachments the new model cannot read |

## Live gateway

Checked against a gateway on `127.0.0.1:8080` built from this commit, with a
console session opened from the local admin token.

| Model | Image | Audio | Document |
| --- | --- | --- | --- |
| `google/gemini-2.5-flash` | active | active | active |
| `groq/llama-3.1-8b-instant` | disabled | disabled | disabled |

Each disabled control carried the label `This model does not accept <kind>
input`.

## Observed, not fixed

Two defects surfaced during this check. Neither comes from this commit, and
neither belongs to this task.

`pnpm --dir console dev` cannot resolve the `@/` alias. The dev server reports
`Failed to resolve import "@/components/auth/destination"` and answers 500.
`vite.config.ts` declares no `resolve.alias`, and the build reads the paths
from `tsconfig.json` while the dev server does not. The build and the
typecheck both pass, so no gate catches it.

The console CSP blocks its own font. A page load reports that a
`data:font/woff2` source violates `font-src 'self'`, so the console renders in
a fallback face. The build inlines the font as a data URI, and the policy
admits `'self'` alone.
