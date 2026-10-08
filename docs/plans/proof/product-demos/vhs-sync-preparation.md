# VHS upstream sync evidence

The local merge is ready for review and commit. All required checks pass on the final source. This task made no commit, push, pull request, release, or contributor branch change.

## Source

- Worktree: `/Users/jack/src/github.com/agentstation/vhs-demo-capture-timing`.
- Branch: `codex/sync-vhs-upstream`.
- Fork parent: `346bbb1e1f769d8d8d789eca87b4aa8a108bb3d1`.
- Frozen upstream parent: `24fa2254a9806091e6ee6a980e9f3bcfe0a9ba53` (Charmbracelet v0.12.1).
- Upstream commits included: 31. Fork commits ahead before the merge: 18.
- Merge command: `git merge --no-commit --no-ff 24fa2254a9806091e6ee6a980e9f3bcfe0a9ba53`.
- Pending merge index tree: `56f587d30ad18f3090e8e6d543676f963c12f1fc`.
- Final source-input digest: `738f4aeace8ecc162680e9a54b13a98e1680c8087f7507e2f0b2afbfe405c9fc`.
- Final input hashes: `/tmp/vhs-sync-source-inputs.json`.
- Full staged patch: `/tmp/vhs-sync-final.diff`.
- Staged file list and summary: `/tmp/vhs-sync-final-status.txt`, `/tmp/vhs-sync-final-stat.txt`.

The index has no unresolved merge entries. HEAD and MERGE_HEAD remain the two stated parents. The final worktree has no unstaged source changes.

## Behavior

The merge includes the upstream Windows browser startup fix, bounded DevTools startup polling, render context propagation, and encoder errors. It also includes Rows/Columns sizing, scroll commands, Ctrl+arrow handling, assumed `.tape` extensions, documentation, and dependency updates. The required Go version is 1.26.7.

The fork retains its module identity, release configuration, and Homebrew package trust checks. It retains SVG output, exact font file options, browser path options, visible recording timing, nonuniform raster frame repetition, screenshots, and capture error propagation. The browser path option and environment variable also apply to the new upstream browser startup transport.

VHS loads the exact font before grid measurement. Native tests prove requested terminal rows and columns, mixed grid/pixel sizing, window bar and padding handling, and unchanged explicit pixel axes. They also prove SVG dimensions, exact embedded font bytes, and no raster capture for SVG-only output. Existing slow capture, hidden span, capture error, timing boundary, and raster repetition tests remain active.

Cancellation tests found two integration defects. A canceled SVG-only render wrote output and returned success. Canceling before Unicode input left terminal element retries active after Chrome exited. Render now checks cancellation before work and before SVG output. Encoder failures retain their diagnostic text and cancellation cause. The browser client uses the caller's context.

Unicode input, viewport setup, and grid measurements return errors through ordinary rod calls. They do not use panic-based rod methods. Native regression tests cover these paths.

The existing SVG renderer remains in its current file. The changes in that file only replace formatted builder writes and repeated font literals. A renderer redesign is outside the sync. This preserves the existing state, coordinate, cursor, text, and timeline contracts.

## CI and security

The fork-owned build and lint workflows remain in place. The Go matrix uses 1.26.7. The lint workflow pins golangci-lint v2.13.1. The official release page is [golangci-lint v2.13.1](https://github.com/golangci/golangci-lint/releases/tag/v2.13.1).

The merge retains the original fork `.golangci.yml` byte for byte. It already excludes gosec and nestif. The upstream configuration reports 65 issues on the fork parent, 48 on the merged source before mechanical repairs, and zero on pristine upstream. All 48 merged issue identities exist on the fork parent. The identity comparison is `/tmp/vhs-sync-lint-comparison.json`.

The final fork configuration reports zero issues after formatted write, constant, allocation, and formatting repairs. The changes add no suppression.

The upstream commit titled `chore: lint and govuln` changes dependencies and source. Neither the frozen upstream VHS lint workflow nor the current Charm reusable lint workflow includes a vulnerability scan job. The changes add no scan job to the fork.

The local scan found a reachable font decoder defect in the fork's x/image dependency at version v0.29.0.

The module now requires v0.39.0, the first fixed version. The [official Go vulnerability record](https://pkg.go.dev/vuln/GO-2026-4962) identifies the defect and fixed version. Final govulncheck output reports zero reachable vulnerabilities. It reports eight required-module vulnerabilities that the code does not call. The scanner's call graph and database state limit this result.

The scanner is govulncheck v1.7.0, built with Go 1.27.1. Its database timestamp is 2026-10-07 14:10:51 UTC. The analysis commands use `GOTOOLCHAIN=go1.26.7`. The final application binary records Go 1.26.7.

## Checks

All commands below ran in the owned worktree. Each listed final command exited zero. The race suite used the installed Google Chrome, ttyd, and ffmpeg.

| Check | Command and result |
| --- | --- |
| Native race | `VHS_TEST_BROWSER=1 VHS_BROWSER_PATH='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' GOTOOLCHAIN=go1.26.7 go test -race -count=1 -json ./...`: 82 top-level tests, 230 cases, four packages, zero failures or skips. |
| Vet | `GOTOOLCHAIN=go1.26.7 go vet ./...` |
| Native build | `GOTOOLCHAIN=go1.26.7 go build -o /tmp/vhs-sync-binary .` |
| Linux build | `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=go1.26.7 go build -o /tmp/vhs-sync-linux .` |
| Windows build | `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=go1.26.7 go build -o /tmp/vhs-sync-windows.exe .` |
| Module state | `GOTOOLCHAIN=go1.26.7 go mod tidy -diff`: no diff. |
| Full lint | `GOTOOLCHAIN=go1.26.7 golangci-lint run --timeout=5m`: zero issues with the retained fork configuration. |
| Vulnerability scan | `GOTOOLCHAIN=go1.26.7 govulncheck ./...`: zero reachable findings. |
| Formatting | `GOTOOLCHAIN=go1.26.7 gofmt -l` on every changed Go file: no output. |
| Staged patch | `git diff --cached --check`: no findings. |
| Tape syntax | `/tmp/vhs-sync-binary validate` with all 106 maintained example tapes outside `examples/errors`: all pass. The list is `/tmp/vhs-sync-tapes-accepted.json`. |
| Upstream media | Exact LFS SHA/size checks, `ffprobe`, and full `ffmpeg -f null -` decode: all three files pass. |

Race results are `/tmp/vhs-sync-race-accepted.json`. Other final logs use `/tmp/vhs-sync-*-accepted.log`. The canceled SVG failure is in `/tmp/vhs-sync-contract-before.log`. The canceled Unicode retry dump is in `/tmp/vhs-sync-unicode-before.log`. The repaired Unicode probe is in `/tmp/vhs-sync-unicode-after.log`.

Linux and Windows builds prove compilation only. This task did not run native browsers on those platforms. The merge retains the Windows browser fix. Portable startup tests cover its transport contract. The macOS native tests do not prove Windows process behavior. This task did not run Docker or hosted CI.

## Artifacts

The native binary is `/tmp/vhs-sync-binary`. Its SHA-256 is `fc2aab1467e9e28b044d1a1394ae3ab68c925b6948a7f6fcfcad6f740f862fb5`.

The merge uses the original example GIF bytes. Each file matches its frozen upstream LFS pointer and decodes as GIF89a. Detailed metadata is `/tmp/vhs-sync-media-proof.json`.

| File | Dimensions | Duration | SHA-256 |
| --- | --- | --- | --- |
| `examples/settings/rows.gif` | 475 × 504 | 2.96 seconds | `72544dc67afc8fc3bb3c009c2a2d65c4430db94fcbeaec996d636ee0ab676bf7` |
| `examples/settings/columns.gif` | 764 × 400 | 2.96 seconds | `c98b0854ff508a39b320b149011a8a1b68da5a3401c5f1cb6c5ceef41271f63b` |
| `examples/settings/rows-columns.gif` | 951 × 600 | 2.96 seconds | `76b3af2510614f13779de30c65e15cbfe2299cbecd814a1e939be3d796e1ff19` |

## Proposed pull request

Title: Sync Charmbracelet VHS v0.12.1 and preserve SVG capture

Body: Merge all 31 commits in the upstream v0.12.1 release. This adds the Windows browser startup fix, render cancellation and encoder errors, terminal grid sizing, scroll commands, and dependency updates. It retains AgentStation SVG, exact font files, browser selection, capture timing, and package identity. The integration also returns cancellation through the SVG and Unicode input paths and fixes the reachable x/image font decoder vulnerability.

Validation: Go 1.26.7 native Chrome race suite passes all 230 cases. Vet, full fork-policy lint, module tidy, formatting, 106 tape syntax checks, and native/Linux/Windows builds pass. The local vulnerability scan reports zero reachable findings. All new upstream GIFs match their LFS hashes and decode correctly. Linux and Windows browser runtime behavior remains unverified locally.
