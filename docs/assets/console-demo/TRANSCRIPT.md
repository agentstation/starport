# Choose a model in the Console

This recording uses the current Starport Console and an isolated development gateway.
It accepts the reviewed local Starmap payload before capture.
The gateway can compose a separate generation ID. Its accepted payload checksum must match the Starmap manifest.
The selected model is GPT-6.1 Sol (`openai/gpt-6.1-sol`).

The accepted generation must be fresh, validated, and free of fallback or degradation.
The gateway reads a local Starmap server. All capture traffic stays on loopback.
The capture has no provider inference credentials and sends no inference requests.

## Story

The goal is to choose a model for an application and locate its provider credential setup.
The opening states that goal before the first action.

1. **Start with a current catalog.** The overview shows gateway readiness and catalog freshness.
2. **Find a model.** Open Models and type `gpt-6.1-sol`.
3. **Check model requirements.** Read capabilities, prices, provider offerings, and credential state.
4. **Locate the provider credential.** Follow the OpenAI offering to its provider page.
5. **Open credential setup.** Read the missing-credential message and open **Set shared credential**.

The result is a model inspection and an empty credential form for the selected provider.
The closing states that OpenAI still needs a provider credential.
The next step is to set a shared OpenAI credential and connect a client with the gateway API key.
The form stays empty, and **Apply credential** stays disabled.
The recorder checks that it makes no credential write.

A short title introduces each action. Titles fade before the pointer moves or the UI changes.
The unfiltered model list holds for 0.6 seconds before search.

Filtered results hold for 1.8 seconds, and model details hold for 3.5 seconds.
The provider credential message holds for 3.2 seconds, and the empty form holds for 2.5 seconds.
Search waits for the actual filter update. It does not use a fixed loading pause.

The opening holds for 2.4 seconds.
The result and next-step card holds for 3.5 seconds and stays visible at the end.

Gateway readiness means that the gateway can accept requests.
Catalog presence does not prove that a provider will accept inference.
A provider with anonymous authentication can count as credentialed without an API key.

The capture shows actual catalog status and preserves source observation dates in its record.
The catalog age is the accepted generation age. Providers retain their original observation dates.
The recorder requires a current generation less than one hour old.
It waits for the actual catalog API before it records the Console.
It verifies successful startup adoption and a source check within two minutes.

The visible pointer follows browser mouse events. Clicks show a short highlight.
The search types one character every 110 milliseconds.
The recording adds no traffic or account usage.

The [capture record](record.json) lists the source commit, binary hash,
recorder hash, browser version, chapter times, scene holds, and artifact hashes.
It also binds the reviewed catalog input files and the Starmap server binary.
The [title preview](title-preview.png) shows the opening goal.
The [video](console.mp4) has the same story as the [GIF](console.gif).

The video keeps the 1280-by-800 capture size. The GIF uses 960 by 600 pixels.
The [static preview](poster.png) shows the first scene.
The website uses playback controls and does not autoplay this recording.

## Reproduce

Use macOS, Go, pnpm, FFmpeg, Google Chrome, and Playwright.
Build the Console before the binary so the binary embeds the current frontend:

```sh
pnpm -C console install --frozen-lockfile
pnpm -C console lint
pnpm -C console build
# Use the reviewed Starmap worktree for both source builds.
# Keep this workspace file outside both repositories.
demo_work=$(mktemp -d)
cd "$demo_work"
go work init /path/to/starport /path/to/starmap
cd /path/to/starmap
GOWORK="$demo_work/go.work" go build -o /tmp/starmap-console-demo ./cmd/starmap
cd /path/to/starport
GOWORK="$demo_work/go.work" go build -o /tmp/starport-console-demo ./cmd/starport
```

Record into a new output directory. Set `--playwright-module` to an installed
Playwright package, or omit it when Node can resolve `playwright`:

```sh
node scripts/console-demo/record.mjs \
  --binary /tmp/starport-console-demo \
  --starmap-binary /tmp/starmap-console-demo \
  --starmap-source /path/to/starmap \
  --browser-path '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' \
  --output /tmp/starport-console-capture
```

The recorder clears provider credentials and uses a temporary home.
It disables provider acquisition and starts an isolated Starmap catalog server.
That server uses the reviewed embedded generation in its source build.
It checks catalog freshness, validation, fallback, and degradation before capture.

It consumes the launch ticket in memory and does not save it.
It records the actual Console, checks browser errors, stops both servers,
and removes temporary state. It refuses a nonempty output directory.

The browser interface needs a browser recording. A terminal SVG cannot capture it.
