# MOD15 the console reads the media catalog

MOD12 made the media models routable. MOD14 published the paths that reach
them. A reader of the console still could not find those models, and a
generated picture still arrived as a base64 blob. MOD15 closes both gaps.

## What a reader can now see

| Surface | Before | After |
| --- | --- | --- |
| Models filter | input modality only | an output facet beside it |
| Models table badges | a fixed list of three kinds | every modality the catalog names |
| Model detail | modalities, capabilities, parameters | the served operations too |
| Chat transcript | text only | a picture renders, a spoken answer plays |

## The output modality is its own facet

An image model accepts text and returns a picture. A chat model accepts text
and returns text. Their input modalities read the same, so a facet over the
input half cannot separate them. The output facet asks the other question.

A catalog entry that states no output modality answers neither `text` nor
`image`. A missing fact and a text-only model are different answers, and the
facet keeps them apart rather than guessing.

## The badges read the catalog

The table filtered its modality badges through a list of three kinds written
in the component. That list already omitted video, so a video model read as a
text model. The badges now read the catalog directly, in both directions.

Text stays out of both halves. Every model handles text, and a badge that
every row carries tells a reader nothing.

## The operations name the path

Modalities alone leave the path open. A model that emits a picture may serve
it on the chat path or on the image path, and a caller has to pick one.

The catalog projection now names what each offering serves. The list comes
from the route the control plane built, which the catalog already narrowed to
the operations the compiled adapter implements. A reader therefore sees what
they can reach, not what the provider advertises.

The model detail page renders that list as a fourth capability tier.

## A picture and a spoken answer reach the transcript

An assistant turn carries its generated media beside its text rather than
inside it. Both kinds hold a data URL, so a stored conversation replays them
after a reload the way an attachment does. A provider link would expire.

The picture renders as an image. The spoken answer gets a player, and its
transcript prints underneath, so a reader who cannot listen still reads what
the model said.

## Audio arrives in pieces

A picture arrives whole in one delta. A spoken answer arrives in chunks, and
each chunk carries its own base64 padding. Joining the encoded strings
produces a payload no decoder accepts.

The console decodes each chunk on arrival, joins the bytes, and encodes once
at the end. A chunk that fails to decode drops itself rather than the whole
answer, because the text of the same turn is already on screen.

## A gap this task found

The catalog projects media offerings for `deepinfra` and `groq`. It projects
none for `openai`, although the same generation names image and audio
offerings there. The media offerings of that provider carry no model
definition, and the model projection lists definitions.

MOD12 recorded the residual offerings and their reason. This is one more
reading of the same fact, and it belongs to the catalog seam rather than to
the console. The facets work on the providers that do project.

## Acceptance

| Condition | Test |
| --- | --- |
| MMD-V24 | `output facet selects a model by what it produces` |
| MMD-V24 | `a turn holding a generated image renders an image element` |

Seven more tests hold the rest:

- `a model with no stated output modality says nothing rather than text`
  requires an entry without the field to answer neither question.
- `operations gather across the offerings that serve a model` requires the
  sorted union, and an empty answer for a model that names none.
- `a spoken answer gets a player and prints its transcript` requires both
  halves of the audio turn.
- `TestModelOfferingsNameTheirOperations` requires every routable offering to
  name at least one operation, and requires a media one to reach the reader.
- `a chunked spoken answer joins its bytes, not its base64` feeds two audio
  chunks through the stream reader and requires one joined payload.
- `an unreadable audio chunk drops that chunk alone` requires the neighbouring
  chunk to survive.
- `a text answer carries no media` requires the common turn to stay empty.

## Fail-before

Both facet tests call a function that did not exist before this task, so the
test file failed to load. The image test rendered an assistant turn that
carried no media field.

## Verification

- `pnpm --dir console check` reports 20 test files and 125 tests passed.
- `scripts/verify-console-modernization.sh` reports 21 passed and 0 failed.
- `scripts/verify-model-modalities.sh` reports 24 passed and 2 failed. The two
  that remain are MMD-V25 and MMD-V26, which MOD16 owns.
