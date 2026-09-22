# First request through Starport

The [38-second demonstration](first-use.gif) shows Starport v1.2.0 on a Mac with an ARM64 processor.
It installs a release archive, reads the catalog before provider setup, and returns a real streamed OpenAI answer.
The [static preview](poster.png) and this transcript provide alternatives to animation.
The [uncut output capture](first-use-uncut.gif) retains the original output timing, including the hidden credential-entry wait.

## What the recording shows

1. Download the release archive and checksum file from GitHub. Verify the checksum, extract the archive, and run `starport --version`.
2. Read `openai/gpt-4o-mini` from the embedded catalog. The captured result has a 128,000-token context window.
3. Supply `OPENAI_API_KEY` through hidden input and start `starport dev --no-open`. Keep its generated gateway key separate from the provider credential.
4. Send a streamed request to `/api/v1/chat/completions`. The request uses `openai/gpt-4o-mini`, `max_tokens: 32`, and the message “Say: Hello from Starport!”
5. Show the client base URLs and the persistent setup path.

OpenAI returned “Hello from Starport! How can I assist you today?”
Starport reported provider `openai`, model `gpt-4o-mini-2024-07-18`, and the terminal `[DONE]` event.
The request took 1.700 seconds, including provider time. This is not a gateway overhead benchmark.

## Editing and scope

The animation renders timestamped output from actual commands and the actual HTTP stream.
The capture tool verifies and extracts the archive. It formats selected catalog JSON through `jq` and displays the decoded stream content.
It hides both credential values and omits startup logs. The recorded process uses port 19325 to isolate it from other local services.
The README uses the normal default port, 8080.

The edited animation reduces the 40.284-second credential-entry gap to eight seconds.
It extends the installed-version pause to six seconds and the final frame to eight seconds for reading.
These edits occur outside inference. GIF frame timing rounds cumulative timestamps to centiseconds.
The [render record](render.json) contains the exact cut points and artifact hashes.

The source capture uses a temporary home with no persistent catalog-state or object-store selector.
It reads the catalog before loading the provider credential. After shutdown, the temporary home contains no files, and the scratch directory no longer exists.
The capture does not modify an existing Starport installation. It does not qualify replicated operation or disaster recovery.

## Repeat the capture

The capture needs macOS ARM64, Python 3.12 or later, `gh`, `jq`, and authorized OpenAI inference access.
It makes one provider request with at most 32 output tokens. Supply the provider key only at the hidden prompt.
Choose a new output directory for each run.

```bash
python3 capture.py --output /tmp/starport-first-use-capture
```

The capture record keeps nonsecret outputs, timestamps, release hashes, and cleanup results.
The renderer needs Pillow and the macOS Menlo font. Rendering does not repeat the provider request.

```bash
python3 render.py /tmp/starport-first-use-capture/capture.json /tmp/starport-first-use-render
```

The checked-in [events](events.json) omit local paths and process command arguments.
Use them with [replay.py](replay.py) to inspect the captured output in a terminal.
The [capture source](capture.py) and [renderer source](render.py) define the remaining reproduction details.

For copyable installation and request commands, return to the [README](../../../README.md).
For persistent storage and team deployment, read the [operator guide](../../OPERATOR-GUIDE.md).
