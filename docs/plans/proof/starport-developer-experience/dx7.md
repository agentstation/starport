# DX7 Homebrew distribution proof

Date: 2026-08-10

## Scope

DX7 completes the Starport Homebrew cask contract. It removes Apple release
credentials as a publication gate while it keeps optional macOS signing and
notarization. It also keeps SHA-256 checksums, SBOMs, GitHub attestations,
immutable artifacts, stable-tag controls, cask audits, and exact-version
installation tests.

Implementation commit: `b4ff9b4`.

## Release contract

- GoReleaser targets `agentstation/homebrew-tap` on `main`.
- GitHub reports `main` as the tap default branch and `refs/heads/main` as its
  only branch.
- The release workflow passes Apple secrets to GoReleaser when they exist. It
  has no mandatory Apple secret check.
- GoReleaser enables signing and notarization only when all five Apple
  credentials exist.
- The cask hook runs only on macOS. It removes `com.apple.quarantine` only
  from `#{staged_path}/starport` and does not use `sudo`.
- The macOS installation job resolves the installed binary and fails if the
  quarantine attribute remains. It verifies Developer ID and Gatekeeper
  metadata when a Developer ID signature exists.
- The release and recovery paths publish a stable cask only after immutable
  artifact and provenance verification.
- macOS and Linux jobs install the cask and verify the exact Starport version,
  completions, and manual page.

The cask verifier has regression scenes for a broad staged path, a privileged
hook, and a cross-platform `xattr` hook. Each invalid fixture fails.

## Release snapshot

GoReleaser 2.17.1 and Syft 1.50.0 produced a clean `v1.0.1-next` snapshot.
These checks passed:

- Six exact-version, CGO-disabled binaries for macOS, Linux, and Windows on
  AMD64 and ARM64.
- Six release archives, six Syft SBOMs, and the SHA-256 checksum manifest.
- Generated cask syntax, platform blocks, checksums, installed artifacts, and
  the scoped hook.
- A strict Homebrew cask audit in a repository-free temporary tap.
- Sixteen pinned GitHub Action references.

The first snapshot attempt used a modified worktree. The release binary
verifier rejected all resulting binaries. The clean committed-worktree rerun
passed. This result proves that the release provenance check fails closed.

## Required checks

These commands passed after the final implementation commit:

```bash
bash scripts/verify-starmap-ownership.sh
bash scripts/verify-v1-architecture.sh
go test ./...
go vet ./...
make lint
make build
bash scripts/smoke-openrouter-sdks.sh
bash scripts/smoke-first-run.sh
bash scripts/verify-v1-release.sh
bash scripts/verify-release-workflow.sh
bash scripts/verify-developer-experience.sh
bash scripts/verify-doc-links.sh
bash scripts/test-doc-link-verifier.sh
make release-snapshot
```

The ownership verifier passed 12 checks. The architecture verifier passed 12
checks. The release verifier passed 14 checks. The developer-experience
verifier passed all 41 checks. The Go linter reported zero issues. The raw
HTTP and official Python, TypeScript, and Go OpenRouter SDK scenes passed.

Strict technical-writing lint passed 13 current documents with zero
diagnostics. The glossary check passed 15 terms with zero errors.

## Review

The configured Claude reviewer could not connect through the local
self-signed certificate proxy. The isolated Codex reviewer then ran at the
pre-pull-request gate with P0 through P3 findings enabled.

Its first pass found three items. The implementation accepted the macOS-only
hook and complete-credential conditions. A live GitHub readback rejected the
tap-branch item because the tap uses only `main`. The final review reported no
actionable findings and rated the patch correct with 0.97 confidence. Secret
scanning was clean.

## Publication state

- `UNVERIFIED`: the exact stable Homebrew installation remains unavailable
  until this change merges and the next stable release updates the tap.
- `UNVERIFIED`: optional Developer ID signing and notarization need the five
  Apple credentials. Their absence does not block CLI publication.

No unavailable scene weakens a required release assertion.
