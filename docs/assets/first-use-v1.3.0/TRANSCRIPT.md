# README demonstration of release v1.3.0

This record is the README demonstration of Starport v1.3.0. The real OpenAI provider answered one streamed request through the gateway. The record binds the attested release archive, so no later source change invalidates it.

The [edited animation](first-use.gif) runs 41.1 seconds. The [static preview](poster.png) and this transcript provide alternatives to animation. The [uncut capture](first-use-uncut.gif) keeps the original output timing.

## Release

- Source: GitHub release `v1.3.0` of `agentstation/starport`.
- Tag commit: `b649dbf5184e53b2c13449b96fc887bcb144e18c`.
- Version: `starport version 1.3.0`.
- Archive: `starport_1.3.0_darwin_arm64.tar.gz`, SHA-256 `3b6dd19bb7e9e88de84ac6c795128f06e96cfb7eaf50f62f784c6f1a849d96ca`, verified against `checksums.txt`.
- Provenance: verified with `gh attestation verify` before the capture.
- Binary SHA-256: `b17f6cf44cf740a4d3ed74add310a2920032aa798aa787a143af8825c4e56b2d`.

## Provider

- The answer scene calls the real `openai` provider through the gateway. No base URL override and no local upstream took part.
- The provider credential came from `OPENAI_API_KEY` in the capture environment. No output keeps the value.
- The provider answered: "Hello from Starport! How can I assist you today?"

## What the recording shows

1. `install`: Verify the archive digest, extract the archive, and run `starport --version`.
2. `catalog`: Run `starport models show openai/gpt-4o-mini --json` with no provider key in the environment.
3. `setup`: Start `starport dev --no-open` with the provider credential in the environment. The provider credential and the gateway key stay hidden. They serve separate roles.
4. `answer`: Stream one request to `/api/v1/chat/completions`. The provider answers with the original timing. The stream ends with `[DONE]`.
5. `next`: Show the client base URLs and the persistent setup path.

Starport reported provider `openai` and model `gpt-4o-mini-2024-07-18`. The provider stream took 0.866 seconds. This is not a latency benchmark.

## Editing

The edited animation sets the pause at each scene boundary:

- `install/catalog`: 1.001 seconds to 6 seconds.
- `catalog/setup`: 1.000 seconds to 8 seconds.
- `setup/answer`: 1.019 seconds to 6 seconds.
- `answer/next`: 1.002 seconds to 7 seconds.

No edit is inside the inference interval. GIF frame timing rounds cumulative timestamps to centiseconds. The final frame holds for 8 seconds.
The [render record](render.json) holds the cut points and the output hashes. The [events](events.json) hold the captured output.

## Capture environment

The capture used a temporary home without a persistent catalog-state or object-store selector. After shutdown, the temporary home held no files.
The capture ran with network access, because the real provider needs it.

The renderer used Menlo at 26 pixels on a 1280 by 800 frame.
