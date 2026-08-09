# SPR1 Release Candidate Evidence

Date: 2026-08-09

## Result

- Pull request #73 merged by rebase at 2026-08-09T18:43:23Z.
- Protected `master` identified `ee8c34e6f0bbe6fc4bb5506da447388bfdffb4b3`
  after the merge.
- The merged work spans commits `2a66b22` through `ee8c34e` after the plan
  activation commit.
- GitHub reported zero open pull requests after the merge. It reports all ten
  baseline Dependabot pull requests as closed.
- Branch protection requires current branches, pull requests, linear history,
  and administrator enforcement.
- It requires the Build, Security Scan, Release Snapshot, and Action Pin
  Provenance checks.
- It disables force pushes and branch deletion.
- The repository immutable-release setting is on.

## Candidate contract

- Starmap `v0.3.0` is the published catalog dependency. The Go module has no
  local replacement.
- The ownership verifier passed 12 of 12 Starmap boundary checks.
- The architecture verifier passed 12 of 12 concept-seam checks.
- The release verifier passed 14 of 14 distribution checks.
- All 15 third-party GitHub Action references matched the release tags in
  their pin comments.
- Raw chat, streaming chat, models, and embeddings smoke checks passed.
- The official OpenRouter Python, TypeScript, and Go SDK smoke checks passed.
- The release snapshot produced the six declared platform archives, checksum
  manifest, and SPDX SBOMs. Binary and archive verification passed.
- The release workflow is draft-first. It verifies assets, checksums,
  provenance, container identity, and recovery evidence before publication.

## Verification

GitHub Actions run 31329415791 tested pull-request head
`6a8e31b10e9d151b1a29f2f4d9f4540f0744b9bf`. All 10 jobs passed:

- Lint.
- Security Scan.
- Test on Ubuntu, macOS, and Windows with the race detector.
- Action Pin Provenance.
- OpenRouter SDK Compatibility.
- Release Snapshot.
- Release Contract.
- Build.

The local release gate also passed:

```text
bash scripts/verify-starmap-ownership.sh  # 12 passed, 0 failed
bash scripts/verify-v1-architecture.sh    # 12 passed, 0 failed
bash scripts/verify-v1-release.sh         # 14 passed, 0 failed
bash scripts/verify-action-pins.sh        # 15 references verified
bash scripts/verify-release-workflow.sh   # passed
go test ./...                             # passed
go vet ./...                              # passed
make lint                                 # 0 issues
make build                                # passed
bash scripts/smoke-openrouter-sdks.sh     # 4 raw and 3 SDK checks passed
actionlint                                # passed
```

A separate writable Linux container ran `go test ./...` successfully. The
atomic hot-reload regression passed 30 repeated Linux runs and five Linux race
runs before the full matrix.

The final pre-PR Sol autoreview scanned the complete branch and its credentials.
TruffleHog was clean. The reviewer reported no accepted or actionable findings.
