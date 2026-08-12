# POR8 cross-repository closeout proof

POR8 started from clean protected main branches:

- Starport `924855c9d6cda6c12060cad91cd99bf3d21094a8`.
- Starmap `3a029796f223db224e6147003c044f99a2f3f2bf`.

## Current-authority readback

The readback covered source, tests, scripts, workflows, current documentation,
generated package documentation, and Make targets. The approved package paths
are current. The old `pkg/httpclient` text exists only in the immutable
`docs/ARCHITECTURE_CONTROL_PLANE.md` history. The layout verifier excludes this
historical record by design.

The readback found one product correction. Starport `docs/TASKS.md` still
reported POR7 as active after pull request #109 merged. Commit `c72e490` records
POR7 as complete and POR8 as active. Starport pull request #110 publishes this
one-line correction. Starmap needs no closeout correction.

Both repositories reported no open pull request before pull request #110 was
created.

## Local verification

Starport passed these checks on branch
`codex/por8-cross-repository-closeout` at `c72e490`:

- `bash scripts/verify-package-layout.sh`.
- `make verify`, including ownership 12/12, architecture 12/12, release 15/15,
  release workflow, developer experience 47/47, and documentation checks.
- Ordinary and uncapped race tests for all 41 packages.
- `go vet ./...`, lint with 0 issues, and `make build`.
- All 7 OpenRouter SDK smoke cases.
- A release snapshot with GoReleaser 2.17.1 and Syft 1.51.0. The snapshot
  verified 6 binaries, 6 archives, 6 SBOMs, checksums, and the Homebrew cask.
- `go mod tidy`, clean-diff checks, and strict writing lint for all 17 required
  files.

The machine defaults were GoReleaser 2.15.3 and Syft 1.42.4. The snapshot used
checksum-verified temporary copies of the repository-required versions. It did
not change the machine-wide tools.

Starmap passed these checks on clean `main` at `3a029796`:

- `bash scripts/verify-package-layout.sh` and `make verify`.
- Ordinary and uncapped race tests for all 82 packages.
- `go vet ./...`, lint with 0 issues, and `make build`.
- Catalog generation, embedded catalog budget, generated documentation,
  release-readiness, and release-snapshot checks.
- A six-platform snapshot with 6 archives and 6 SBOMs.
- `go mod tidy` and all clean-diff checks.

All Go commands used normal scheduling. No command set `GOFLAGS` or a package
parallelism cap.

## Hosted evidence

Starport pull request #109 passed all 10 hosted checks on head `15d0acc`. Its
tree `e3251d3a585d3bda00311d0d96523b957cb60c1b` equals the protected main tree
at `924855c9`. The protected main CI run also passed on that exact merge.

Starmap pull request #76 passed Verification Gate, Security & Reliability, and
Action Pin Provenance on head `c599ba36`. Its tree
`550be250486aa20d862f8b0a4bcffe3d2e62d33b` equals the protected main tree at
`3a029796`.

The pre-PR autoreview gate skipped a model call for Starport commit `c72e490`
because the branch contains no substantive code change.

## Final gate

Starport pull request #110 hosted checks and exact-head merge remain.
