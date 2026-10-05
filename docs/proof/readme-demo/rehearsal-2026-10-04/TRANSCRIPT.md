# README demonstration rehearsal

This record is a rehearsal. A local fixture upstream supplies the answer, so the record cannot qualify a release case. The README does not link this record.

The [edited animation](first-use.gif) runs 40.8 seconds. The [static preview](poster.png) and this transcript provide alternatives to animation. The [uncut capture](first-use-uncut.gif) keeps the original output timing.

## Candidate

- Source: CI run 37255309275 of pull request 418.
- Head commit: `b64d3682f11cfa30abc5e589a3015055ccf77b33`.
- Version: `starport version 1.2.2-next`.
- Archive: `starport_1.2.2-next_darwin_arm64.tar.gz`, SHA-256 `a3417f22b74ad57071c98470eb27d6bbf32f56794f512a50863ac885c19d4644`.
- Binary SHA-256: `8888b75e72834ba913c99dba5b8598d54b9ac6bc97a43cc982b9520aa8a99042`.

## Fixtures

- `fixture_upstream` is a local OpenAI-compatible upstream. It serves the model `gpt-4o-mini-rehearsal-fixture` and streams the answer "Rehearsal fixture: Hello from Starport!".
- The provider credential is a throwaway token. The capture generates a new token for each run. No output keeps the token value.
- `STARPORT_OPENAI_INFERENCE_BASE_URL` routes the `openai` provider to the fixture. No request goes to OpenAI.

## What the recording shows

1. `install`: Verify the archive digest, extract the archive, and run `starport --version`.
2. `catalog`: Run `starport models show openai/gpt-4o-mini --json` with no provider key in the environment.
3. `setup`: Start the fixture upstream and `starport dev --no-open`. The provider credential and the gateway key stay hidden. They serve separate roles.
4. `answer`: Stream one request to `/api/v1/chat/completions`. The screen shows the fixture notice. The stream ends with `[DONE]`.
5. `next`: Show the client base URLs and the persistent setup path.

Starport reported provider `openai` and model `gpt-4o-mini-rehearsal-fixture`. The fixture stream took 0.757 seconds. This is not a latency benchmark.

## Editing

The edited animation sets the pause at each scene boundary:

- `install/catalog`: 1.005 seconds to 6 seconds.
- `catalog/setup`: 1.262 seconds to 8 seconds.
- `setup/answer`: 1.010 seconds to 6 seconds.
- `answer/next`: 1.005 seconds to 7 seconds.

No edit is inside the inference interval. GIF frame timing rounds cumulative timestamps to centiseconds. The final frame holds for 8 seconds.
The [render record](render.json) holds the cut points and the output hashes. The [events](events.json) hold the captured output.

## Capture environment

The capture used a temporary home without a persistent catalog-state or object-store selector. After shutdown, the temporary home held no files.
The sandbox refused network access except loopback during the capture.

The renderer used Menlo at 26 pixels on a 1280 by 800 frame.
