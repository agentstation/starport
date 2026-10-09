# Product demo audit

Reviewed on 2026-10-07 against Starport `c63b6284`, Starmap `c3e38970d`, and VHS `038df4f`.
Local changes repair sparse catalog preservation and add the reviewed GPT-6.1 Sol model.
The repair preserves normal source-update policy and original-history replay bytes.
The recordings execute current product code in isolated temporary state.

## Decision

Use AgentStation VHS for terminal SVG capture.
The fork already supports SVG. Charm VHS v0.12.1 does not support native SVG output.
The fork needs timing, error propagation, and exact font embedding fixes before a new release.
The local recorder fixes preserve visible elapsed time in SVG and raster output.
The generated terminal SVGs embed the licensed Geist Mono font used during capture.

Read the [VHS comparison](vhs-comparison.md) for pinned revisions, upstream changes, and PR findings.
This copy preserves the research from the VHS worktree.
The upstream Brave issue contains mixed reports about selecting Chrome.
This rollout selects Chrome and verifies elapsed playback time.
Browser selection alone does not prove correct timing.

AgentStation PR #6 owns an existing timing proposal.
Its contributor branch permits maintainer edits.
A reviewed revision can extend that proposal without rewriting its published commits.
Review the exact font option alongside PR #8.
Port upstream PR #781 before a Windows release.
A complete upstream merge is not a prerequisite for these macOS captures.

## Tape and story review

| Recording | Finding | Result |
| --- | --- | --- |
| Starmap `scripts/demo.tape` | Mixed local and PATH binaries, truncated catalog lists, no server story, and compressed playback. | An opening goal leads to server readiness, an HTTP model read, a pinned payload, checksum verification, and a subscriber next step. |
| Starport CLI | Existing first-request tools own release qualification and provider inference. | A separate CLI story selects one model, checks a live authenticated gateway, and ends with provider setup and restart instructions. |
| Starport Console | Historical Console GIF shows an earlier interface. | A browser story follows a selected model to its provider and empty credential setup, then states the result and next step. |
| Starport first request | Released v1.3.0 recording proves a real provider request. | Preserved its media, release binding, transcript, and timing. |
| VHS examples | 103 non-error tapes parse. Current Gum rejects the welcome tape's old spinner command. | Corrected `gum spin` and declared welcome/meta dependencies. |

The Starmap recording proves HTTP catalog distribution and payload integrity.
It does not claim authenticated subscriber activation.
The CLI uses reviewed embedded catalog data. The Console reads a loopback Starmap server.
Neither recording uses provider inference credentials.
They make no provider inference request and add no simulated usage.

The Console requires a fresh accepted generation with passed validation and no fallback or degradation.
It records the actual source observation dates. Generation age does not imply new provider observations.
Its visible pointer follows browser mouse events, and search text uses character typing.

The new demo source selects `openai/gpt-6.1-sol`. Its OpenAI credential must be `not_configured`.
All three final captures pass with the reviewed local catalog and final paired source.
The capture checks empty secret fields, disabled credential apply, and zero credential writes.

Both terminal tapes use real VHS shell commands with 35-millisecond typing and the same Dracula and Geist Mono style.
Cyan chapter headings introduce terminal actions. Console title cards fade before each action.
Read the [video direction review](video-direction.md) for viewer goals, verified results, next steps, and pause decisions.

The prior Starport rehearsal at `2026-10-04` remains invalid against current product code.
Its invalidation records changed product paths. The verifier remains unchanged.
A historical rehearsal does not replace the released first-request proof.
Inherited VHS examples did not receive a complete runtime audit of external programs.

## Media and website

The Starport README shows separate CLI and Console GIFs.
It links the CLI SVG, videos, static previews, transcripts, and reproduction steps.
The Starmap README shows a GIF and links the animated SVG and command evidence.
GIF fallbacks remain until GitHub renders the published assets through its image proxy.

The website Console chapter shows the recorded interface with native controls.
It loads a static poster and does not autoplay or loop.
It preserves the eleven storyboard canvases and their chapter order.
Reduced motion uses the static storyboard with the same controlled player.
The animated chapter unmounts its player when the reader leaves it.
The old schematic disappears while the Console recording is the scene.

## Acceptance evidence

- Website: `pnpm -C website lint` and `pnpm -C website test` pass. All 84 tests pass, including 51 exported pages and exact Console copies.
- README: all 10 `TestReadme` tests and the quick-start verifier pass. Both precede the final SDK repair.
- Documentation links: README, both Starport transcripts, and plan index pass after the final captures.
- Released first request: the unchanged v1.3.0 verifier passes all 15 conditions.
- CLI tooling: 23 regression tests and 42 legacy capture tests pass. The frozen sources remain unchanged through the final capture.
- Starmap tooling: 9 regression tests, 10 source checks, 10 output checks, and 11 SVG story checks pass.
- Final payload: exact 26,776,618-byte verification passes.
- Catalog preservation: all existing facts, membership, 42,477 provenance roots, 34 original source links, and 259 unrelated reviews remain exact.
- Original replay: all 86 accepted observations reproduce the exact original payload and source links after the reconciler repair.
- Reconciler race checks: 165 functions and 733 cases pass. Bootstrap: 51 functions and 152 cases pass.
- Runtime preservation race checks: 70 functions and 198 cases pass before the final no-op repair.
- Timestamp repair: four focused runtime race tests pass. Startup, replay, restart, immutable timestamps, sequence, and upstream evidence remain exact.
- Consumer regression: the actual development app accepts the unchanged derived publication through the production Starmap HTTP handler. The old runtime reproduces the conflict.
- Catalog-generation gates, embedded validation, budget checks, goago, and owning-package vet pass.
- Console: six actual scenes, both server exits zero, no browser errors, and matching artifact and recorder hashes.
- Console catalog: reviewed local Starmap source, current accepted generation, passed validation, no fallback or degradation, and acquisition disabled.
- Console freshness: effective generation six seconds old, source check three seconds old. Original source observation dates remain unchanged.
- Console pointer: five mouse targets, click highlights, and 110-millisecond character typing.
- CLI typing: 8 search, 14 model-inspection, and 22 gateway-startup prefixes prove visible typing.
- Media duration: raster and SVG recordings differ from measured capture by less than 0.3 seconds.
- Terminal assets: SVG XML, licensed embedded font bytes, media hashes, scene text, and elapsed duration parity pass.
- Titles: each terminal opening combines its goal and first chapter. Three chapters precede the actual commands.
- Console direction: a goal and three chapters precede the actions. Its stable result card holds for 3.5 seconds.
- Final duration: CLI SVG 54.26 seconds, Starmap SVG 54.37 seconds, and Console capture 35.206 seconds.
- VHS: 190 race cases, vet, and changed-code lint pass. All 103 non-error example tapes pass syntax validation.
- Browser timing: Brave and Chrome each produce 4.69-second SVG and 4.64-second GIF playback, excluding a two-second hidden span.

The [catalog comparison](catalog-preservation-check.json) records the independent preservation check.
The [consumer timestamp proof](consumer-timestamp-check.json) records fail-before and final race results.
The [Console startup proof](console-startup-check.json) records the final accepted payload and fresh source check.
Claude's separate production compaction work remains outside this task.

The [CLI buffer review](cli-buffer-review.json) records a rejected capture with a visible setup command.
Relative cursor erasure left one command in the viewport. Hidden settling delays did not remove it.
The corrected tape clears the screen and restores only the editorial heading before actual banner and readiness output.

Inspect the [Console storyboard](console-storyboard.png), [closing card](console-closing.png), and [pointer and partial search text](console-pointer-typing.png).
Read the [browser timing evidence](browser-timing.json), [playback evidence](browser-checks.json), and [final media bindings](media-checks.json).

The VHS full lint reports 51 preexisting findings.
The installed golangci-lint version is `v2.13.1`. The repository CI pins `v2.3.0`.
This task does not weaken lint checks.
The full Starport task status file has six existing prose findings. Its new plan and audit references pass prose checks.

The VHS README has 32 existing prose diagnostics. Its new paragraphs pass and add no diagnostics.

Windows browser startup, final GitHub image proxy rendering, and hosted website deployment remain UNVERIFIED.
The full pre-PR gates and structured autoreview await publication authority.

## Local state

Changes remain uncommitted in three isolated worktrees.
The original Starport, Starmap, and VHS checkouts retain their prior work.
No branch push, PR update, merge, release, or deployment occurred.
Keep the worktrees and plan until publication and merge.

## Reproduce focused acceptance checks

Run Go checks from their owning repository. The temporary paired workspace selects the reviewed Starmap source for Starport.

| Owner | Command | Recorded result |
| --- | --- | --- |
| Starport website | `pnpm -C website lint` | Pass, zero errors. |
| Starport website | `pnpm -C website test` | 84 tests pass across four files. |
| Starport CLI recorder | `python3 scripts/test-cli-demo.py` | 23 tests pass. |
| Starmap recorder | `python3 scripts/test_record_demo.py` | Nine tests pass. |
| Starmap runtime | `GOWORK=off go test -race ./runtime -run 'TestOrdinaryNoopRetainsCommittedEffectiveState|TestProviderPolicyStartupAlignsHTTPGeneration|TestProviderPolicyReactivationPreservesImmutableGeneration|TestAcquisitionSourceSelectionRefusesFailedStartupPublication' -count=1 -timeout 15m` | Four focused functions pass. |
| Starport consumer | `GOWORK=/tmp/agentstation-product-demo-go.work go test -race ./internal/app -run '^TestDevelopmentCatalogAcceptsUnchangedDerivedPublication$' -count=1 -timeout 15m` | Native regression passes. The old runtime fails with the timestamp conflict. |
| Starport links | `GOWORK=/tmp/agentstation-product-demo-go.work bash scripts/verify-doc-links.sh README.md docs/assets/cli-current/TRANSCRIPT.md docs/assets/console-demo/TRANSCRIPT.md docs/plans/README.md` | Pass. |
| Starmap catalog tools | `GOWORK=off make catalog-generation-check` | Nine Go package gates, 67 Python tests, and provider-refresh shell tests pass. |

The commands above reproduce the recorded focused checks. The JSON proof files retain exact case names, timings, and hashes.
Full pre-PR checks and structured autoreview remain pending. No PR publication occurs in this task.
