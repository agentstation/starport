# Demo direction

The demos serve four distinct first-use goals.
Each story connects its opening goal to observed product output and a specific next step.
The current recordings make no provider inference request.
The released first-request recording supplies the real inference example.

## Viewer goals and results

| Demo | Viewer and goal | Visible journey | Result and next step |
| --- | --- | --- | --- |
| First request, release v1.3.0 | A developer wants to try inference through Starport. | Verify and install the archive, inspect the catalog, start a temporary gateway, and stream a real provider answer. | The stream completes. The ending shows client base URLs, the gateway API key role, and persistent setup. |
| CLI, current source | A developer wants to choose a chat model and check a local gateway before configuring inference. | Inspect `openai/gpt-6.1-sol`, start an authenticated gateway, read readiness, and inspect that model through the keyed catalog API. | The model exists and the gateway accepts authenticated catalog reads. Stop the session, set the provider credential, restart, and use the new gateway URL and key. |
| Console, current source | A developer wants to inspect a model and find the credential setup for its provider. | Check the current catalog, search `gpt-6.1-sol`, inspect its offerings, and follow the OpenAI offering to its empty credential form. | The model was inspected. OpenAI needs a provider credential. Set the shared credential, then use the gateway API key in the client. |
| Starmap, current source | A catalog operator wants to serve model metadata and a verified catalog generation to clients. | Start a local server, read readiness over HTTP, read `openai/gpt-6.1-sol`, and fetch and verify a generation payload. | The client read the model and verified the payload checksum. Configure a catalog subscriber as the next step. |

The first-request recording already has five ordered scenes and a resolved ending.
Its release archive, answer timing, media, and evidence remain unchanged.
The release verifier passes all 15 conditions.
The older source rehearsal remains invalid and is not a README example.

The three current stories inspect GPT-6.1 Sol.
They use a reviewed local catalog and preserve the original provider observation dates.
The Console follows one model's offering instead of opening the unrelated provider grid.
It reaches the actual **Set shared credential** form.
The form stays empty, and **Apply credential** stays disabled.
The capture checks the missing OpenAI credential and the absence of credential writes.

The Starmap story starts with the server use case.
The local model list duplicates the later HTTP model read, so the recording omits that list.
Readiness, model lookup, and payload verification form one client journey.
The ending uses the observed model and generation.
It does not claim that an authenticated subscriber adopted the generation.
The transcript and README link the Go subscriber and Starport source configuration contracts.

## Openings and endings

The terminal opening combines the viewer goal and first chapter on one screen.
One orientation hold precedes the first command.
The Console opening states the goal, then reveals the actual overview.
No recording inserts two title holds without a product action between them.

Each chapter introduces the purpose of the next action.
Editorial titles remain separate from actual command output and UI state.
The terminal recordings keep real VHS shell commands, character typing, and the same capture font and theme.
The Console keeps its actual UI, mouse movements, click highlights, and character typing.

The closing names the result that the capture checked.
The CLI gateway remains live through its closing hold.
The capture then stops its owned process outside the visible story.
The Console closing stays visible through the final frame.
The Starmap closing connects the verified fetch to subscriber configuration.

## Pause decisions

Opening holds allow time to read the goal and first action.
Later chapter holds allow time to read the next action.
Output holds follow command completion or actual UI readiness.
The CLI launch command holds for 1 second before the capture shows the actual sanitized banner.
Startup probes stop at their configured time limits.
The visible Starmap readiness request saves its response before `jq` displays the status.

The Console unfiltered list holds for 0.6 seconds before the search starts.
Filtered results hold for 1.8 seconds, and model details hold for 3.5 seconds.
The provider credential message holds for 3.2 seconds.
The empty form holds for 2.5 seconds, and the closing holds for 3.5 seconds.
These longer holds allow time to read capabilities, prices, credential roles, and the next step.
Search waits for the actual filter update rather than an arbitrary loading delay.

The CLI closing contains restart instructions and credential roles, so it holds for 8 seconds.
The Starmap closing contains a short result and subscriber next step, so it holds for 5 seconds.
Shutdown adds no visible pause.

Capture records own exact timing, source hashes, artifact hashes, and observed results.
The final review checks opening and title order, intermediate typing, result readability, closing visibility, source bindings, and native website playback.

## Final Sol recordings

| Demo | Duration | Closing evidence |
| --- | --- | --- |
| CLI | SVG 54.26 seconds, GIF and MP4 54.28 seconds | Live readiness and keyed catalog return 200. Anonymous catalog access returns 401. Cleanup follows the eight-second result. |
| Console | Capture 35.206 seconds, MP4 and WebM 35.24 seconds, GIF 35.20 seconds | Exact model, missing OpenAI credential, empty secret fields, disabled apply, and zero credential writes. |
| Starmap | SVG 54.37 seconds, GIF 54.40 seconds | Observed model and generation, verified payload checksum, and zero visible capture commands. |

All three recordings use the final source with the no-op timestamp repair.
The accepted payload contains 26,776,618 bytes. Source hashes and original provider observation dates remain exact.
The Console checks the upstream generation separately from the accepted effective generation.
Their payload checksums match. Its source check is three seconds old and its effective catalog is six seconds old.

Startup checks finish before the visible story.

All asset, recorder, transcript, font, story, and duration bindings pass.
The CLI has 8 search, 14 model-inspection, and 22 gateway-startup prefixes.
All 23 CLI tests, 42 legacy capture tests, and 9 Starmap regression tests pass.
The Starmap recorder checks 10 source, 10 output, and 11 story conditions.

All 84 website tests pass, including the production export and exact Console asset copies.
Four browser layouts pass with native playback controls and no browser errors.
Final SVG browser checks verify fonts, animation, and image embeds.
