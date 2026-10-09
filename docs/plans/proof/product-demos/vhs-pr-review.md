# VHS open PR review

Do not merge any of the four reviewed contributor heads. Published main supersedes PR #6. PR #8 has the same core goal as main, but its remaining resolver and subset behavior needs changes. PRs #7 and #9 have useful separate goals and need export fixes before merge.

Reviewed on 2026-10-08 against `346bbb1e1f769d8d8d789eca87b4aa8a108bb3d1`. Reviewers preserved published history. No user source, contributor branch, GitHub comment, or PR state changed.

## Revisions and recommendations

All four PRs remain open. Their common ancestor with current main is `36343ddb468785b82ea92f17db841eae7cfe3e2f`.

| PR | Exact head | Merge check against current main | Recommendation |
| --- | --- | --- | --- |
| [#6: timing](https://github.com/agentstation/vhs/pull/6) | `1e430f095a75a8dce41ed8f3b37d076a93b697df` | Conflicts in `svg.go`, `vhs.go`, `video.go` | Superseded. Keep main's visible timeline, raster repetition, and error propagation. |
| [#7: dimensions](https://github.com/agentstation/vhs/pull/7) | `ee7844d7d6516a95dc6998387d87ebb75b43eaab` | Clean merge, tree `4cd1b1095c5c72c0f2f234eac9c89445ad922204` | Changes required. Preserve rounded cutouts and avoid caller style mutation. |
| [#8: fonts](https://github.com/agentstation/vhs/pull/8) | `fb3666d439234c95a7804703a1214bfc4fd16829` | Conflicts in `svg.go`, `video.go` | Core superseded. Revise optional resolution and subsetting as a separate change. |
| [#9: progress](https://github.com/agentstation/vhs/pull/9) | `3b7b233e9eb594e9804d8e7b748f224147a6db93` | Clean merge, tree `df3cf532e42b954afeac03d4d0d3c83f99ec3532` | Changes required. Validate color input and preserve valid XML. |

A clean textual merge does not establish correct behavior. The probes for #7 and #9 ran on their clean merge trees with current main.

## Findings

### PR #6: P2: the last capture time is not the visible recording duration

[Diff: video.go:199](codex://review?pr=https%3A%2F%2Fgithub.com%2Fagentstation%2Fvhs%2Fpull%2F6&path=video.go&line=199&side=right)

Trigger: record `Sleep 2500ms` at one frame per second, with SVG output. The first capture starts the elapsed clock at zero. `MakeSVG` then uses the final capture timestamp as the full duration. The initial visible interval and final hold disappear.

A native Chrome and ttyd test produced a 1.0-second SVG for this 2.5-second hold. The same test on published main produced 2.5 seconds. This is a reproduced failure of the PR's timing contract. Main already fixes it through the visible timeline and explicit recording stop time.

Evidence: [PR probe log](vhs-pr-review/pr6-probes.json), [main control](vhs-pr-review/main-control.json), [probe source](vhs-pr-review/review_timing_test.go.txt).

### PR #6: P2: raster timing still depends on requesting SVG

[Diff: vhs.go:267](codex://review?pr=https%3A%2F%2Fgithub.com%2Fagentstation%2Fvhs%2Fpull%2F6&path=vhs.go&line=267&side=right)

Trigger: slow capture with only MP4, GIF, or WebM output. Render sets `ActualFramerate` only when `svgFrames` is nonempty. A raster-only recording therefore retains the requested rate and the original shortened playback behavior.

The Render policy probe confirmed `-r 10` for raster-only output. The test uses an encoder substitute to isolate the rate decision. It does not claim to measure a real video duration.

For mixed output, one average rate still assigns equal intervals to every PNG. Uneven capture times such as 0, 0.1, and 1.5 seconds cannot retain their individual transition times through one average rate. This follows from `buildFFopts` consuming only `ActualFramerate`, without per-frame times. Main's repeated-state playback sequence addresses that contract.

The PR test constructs timestamps and asserts an SVG duration string. It does not exercise recording stop time, raster-only output, hidden command transitions, capture failures, or nonuniform raster playback. The old recording loop also retains raster work for SVG-only output. These are reasons to preserve main's implementation when handling the conflicts.

### PR #7: P2: the root background fills rounded corner cutouts

[Diff: svg.go:258](codex://review?pr=https%3A%2F%2Fgithub.com%2Fagentstation%2Fvhs%2Fpull%2F7&path=svg.go&line=258&side=right)

Trigger: set a nonzero `BorderRadius`. The new opaque root SVG background fills the area outside the rounded terminal window. The exported window fills the corners where the rounded shape left cutouts.

The clean merge-tree probe reproduced the root background rule with `BorderRadius=20`. Native Chrome rendered corner pixel RGBA 23,23,23,255. The main control rendered 0,0,0,0, which preserves transparency. Remove that unconditional fill or constrain it to the rounded window geometry. Preserve the separate margin color and transparent edge behavior.

A second probe showed `Generate` mutating the caller's style from 1200x600 to 736x520 through `style.Width` and `style.Height` at lines 235 and 236. Compute a local style copy. Current raster commands render before SVG, so this review does not claim that the mutation corrupts current raster output.

The PR adds only explanatory test comments. It has no new assertions with nonzero `TermCols` or `TermRows`. Add grid, margin, bar, fractional metric, and rounded-edge cases.

Evidence: [browser pixel probe](vhs-pr-review/pr7-corner-browser.log.txt), [main control](vhs-pr-review/main-corner-browser.log.txt), [browser source](vhs-pr-review/review_corner_test.go.txt), [probe log](vhs-pr-review/pr7-probes.log.txt), [probe source](vhs-pr-review/pr7-probe.go.txt). The root reviewer ran these probes. This review inspected their sources and results.

### PR #8: P2: fontconfig fallback can change the captured font metrics

[Diff: video.go:241](codex://review?pr=https%3A%2F%2Fgithub.com%2Fagentstation%2Fvhs%2Fpull%2F8&path=video.go&line=241&side=right)

Trigger: request a font family absent from the system. `fc-match` returns a fallback file, but the PR embeds that file under the requested family name. Capture has already used Chrome's own fallback rules. The two fallback choices need not match.

On this macOS host, `ZZZNonexistentFont999` resolved through fontconfig to Verdana. Native Chrome measured `MMMMiiii` at 63.9375 pixels with the capture font stack and 98.3125 pixels with the embedded fallback. This changes text width relative to the captured cell dimensions.

Resolve and load the selected bytes into capture before recording, or reject a mismatched fallback. A rebase must preserve main's explicit font-file priority. Its `VHS Capture Font` family is a browser alias, not an installed fontconfig family. Resolving that alias again can replace the selected bytes with an unrelated fallback.

The existing nonexistent-font test permits any fallback and checks only valid base64. It does not establish capture and export metric equality.

### PR #8: P2: the subset omits window title glyphs

[Diff: video.go:294](codex://review?pr=https%3A%2F%2Fgithub.com%2Fagentstation%2Fvhs%2Fpull%2F8&path=video.go&line=294&side=right)

Trigger: use JetBrains Mono, terminal text `123`, and `WindowBarTitle=ZZZ`. The subset collects frame lines and cursor characters, but excludes the title. With the default title family, the title uses the same embedded face.

Real `pyftsubset` output contained the terminal glyphs but no `Z` in its character map. The generated SVG still contained `ZZZ`. That title must then use fallback glyphs, which removes the intended font portability and can change title metrics.

Include every string that uses the embedded face, including the window title. Add a native subset character-map regression and a title rendering check. Preserve a separately selected title font when configured.

Evidence for both font findings: [probe log](vhs-pr-review/pr8-probes.json), [probe source](vhs-pr-review/review_font_test.go.txt).

### PR #9: P2: accepted color input can produce malformed SVG

[Diff: parser/parser.go:515](codex://review?pr=https%3A%2F%2Fgithub.com%2Fagentstation%2Fvhs%2Fpull%2F9&path=parser%2Fparser.go&line=515&side=right)

[Diff: svg.go:318](codex://review?pr=https%3A%2F%2Fgithub.com%2Fagentstation%2Fvhs%2Fpull%2F9&path=svg.go&line=318&side=right)

Trigger: the tape uses a quoted string: `Set ProgressBar 'red"broken'`. The parser validates only inputs that start with `#`. It accepts this string, and the generator inserts it directly into a double-quoted `fill` attribute.

The clean merge-tree probe produced an XML parse failure: attribute name without `=` on line 36. The parser also accepted `not-a-color`. Validate the permitted color forms and escape values at the XML boundary. Add accepted-input-to-valid-XML tests.

The animation test asserts that the progress and slide rules exist. It does not compare their duration or delay values. Add PlaybackSpeed and LoopOffset assertions, plus an off-by-default case through the command execution path.

Evidence: [probe log](vhs-pr-review/pr9-probes.log.txt), [probe source](vhs-pr-review/pr9-probe.go.txt). The root reviewer ran these probes. This review inspected their sources and results.

## Checks and limits

| Check | Result |
| --- | --- |
| PR #6 exact-head `go test -race -count=1 -json ./...` | Passed: 56 top-level tests, 167 cases, no failures or skips. |
| PR #8 exact-head `go test -race -count=1 -json ./...` | Passed: 60 top-level tests, 193 cases, no failures or skips. |
| PR #7 exact-head `go test -race ./...` | Passed in the root review. |
| PR #9 exact-head `go test -race ./...` | Passed in the root review. |
| PR #6 and #8 `go vet ./...` | Passed. |
| PR #6 targeted timing probes | Both contract probes failed on the PR head. Native visible-hold control passed on main. |
| PR #8 targeted font probes | Two contract probes failed. Font collection loading passed in Chrome. |
| PR #7 and #9 clean merge-tree probes | Failed on the export contracts above. |

Exact-head race suites ran before reviewers added the separate review probe files. The logs and sources are in `/tmp/vhs-open-pr-review-20261008`. The root's full-suite logs are `/tmp/vhs-pr7-head-suite.log` and `/tmp/vhs-pr9-head-suite.log`.

Go compatibility remains 1.24.1. None of these PRs changes module dependencies or the declared Go version. Local tests used the installed Go toolchain. Reviewers did not test Linux or Windows browser behavior. No structured autoreview helper ran.

PR #8 depends on fontconfig for resolution and optionally FontTools plus WOFF2 support for subsetting. Missing fontconfig returns empty embedding data. Subsetting failure falls back to the full file.

On this host, Menlo's TTC collection failed subsetting because the command omitted its face index. The fallback embedded the 2.1 MB full collection. Chrome loaded that result successfully. This is a size and platform coverage limitation, not a confirmed broken-font finding.

The font tests in the PR check CSS strings, fake font bytes, MIME hints, and base64 encoding. They do not prove browser metric equality or title subset coverage. They also omit checks with both optional tools absent.

PR #7 changes the viewport extent. It cannot repair the font metric mismatch in #8. PR #9 uses the shared animation duration and delay. A rebase must retain main's visible timing and percentage-based LoopOffset. Its feature affects SVG only. GIF, MP4, and WebM do not receive a progress bar.

The root reviewer owns final merge judgment. This report recommends changes and does not authorize a merge, closure, contributor branch rewrite, or GitHub comment.
