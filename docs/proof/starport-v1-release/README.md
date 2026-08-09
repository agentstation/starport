# Starport v1 Release Closeout

Date: 2026-08-09

Starport v1.0.0 is public, immutable, and independently verified. The release
is at <https://github.com/agentstation/starport/releases/tag/v1.0.0>.

## Terminal ledger

| Item | Status | Evidence |
| --- | --- | --- |
| SPR0 | `done` | Baseline `ff293dd`; 10 stale Dependabot pull requests; release verifier reported 2 passed and 11 failed; [SPR0 evidence](spr0.md). |
| SPR1 | `done` | PR #73; protected candidate `ee8c34e`; CI run 31329415791 passed all 10 jobs; [SPR1 evidence](spr1.md). |
| SPR2 | `done` | Tag commit `fca912f`; release run 31331662248; immutable release `RE_kwDOPH3H-c4V6I7F`; [SPR2 evidence](spr2.md). |
| SPR3 | `done` | Closeout PR #75; this proof survives active-plan removal. |

## Verified release

- Tag `v1.0.0` identified the exact protected `master` head at publication.
- The release contains 13 checksum-verified assets: six archives, six SPDX
  JSON SBOMs, and one checksum manifest.
- All 13 file attestations and the OCI attestation identify release workflow
  `.github/workflows/release.yaml` at source commit `fca912f`.
- The six static binaries cover Linux, macOS, and Windows on amd64 and arm64.
  The native binary reports `starport version 1.0.0`.
- The public GHCR tags `1.0.0`, `v1.0.0`, and `latest` resolve to
  `sha256:f4230687fdf664022e4be80031c4145ff2eb795ff200489216ea76ba4b64bc24`.
- An anonymous pull verified two non-root Linux platform images and the exact
  version, source revision, and SBOM attestation manifests.

## Candidate quality

- The Starmap ownership and v1 architecture verifiers each passed 12 checks.
- The release verifier passed 14 checks, and all 15 action pins matched their
  reviewed release tags.
- Raw OpenRouter routes and the official Python, TypeScript, and Go SDKs
  passed their smoke tests.
- Pull request #73 passed all 10 protected checks. The final pre-PR Sol review
  reported no accepted or actionable finding.
- A full-history TruffleHog 3.96.0 scan reported no verified or unverified
  secret.

The release campaign is terminal. Future product work needs a separate plan.
