# SPR2 Publication Evidence

Date: 2026-08-09

## Release identity

- Pull request #74 merged the SPR1 proof into protected `master` after all 10
  CI jobs passed.
- Annotated tag `v1.0.0` peels to
  `fca912fe8efca57fd6ad048b005524114c7806ca`.
- The tag was the exact protected `master` head at publication.
- Source run 31330494508 preserved the verified distribution. Recovery run
  31331662248 reused it and passed all steps in 2 minutes 6 seconds.
- Release `RE_kwDOPH3H-c4V6I7F` is public, non-draft, non-prerelease, and
  immutable.
- The release is at
  <https://github.com/agentstation/starport/releases/tag/v1.0.0>.

## Public assets and provenance

The release contains one checksum manifest, six platform archives, and six
SPDX JSON SBOMs. Independent public download produced these results:

```text
PASS 6 release archives, 6 Syft SBOMs, and the checksum manifest
PASS 6 version-exact cgo-disabled release binaries
PASS 12 release asset attestations plus checksum-manifest attestation
```

All file attestations identify `.github/workflows/release.yaml` at source
commit `fca912f`. OCI attestation 39697020 identifies the same workflow and
source commit.

## Public container

The GHCR package is public. The `1.0.0`, `v1.0.0`, and `latest` tags resolve
to one digest:

```text
sha256:f4230687fdf664022e4be80031c4145ff2eb795ff200489216ea76ba4b64bc24
```

A fresh Docker configuration pulled the digest without credentials. The
manifest has Linux amd64 and arm64 images plus at least two SBOM attestation
manifests. Both platform images use `65532:65532` and identify source revision
`fca912f`. The amd64 image reports `starport version 1.0.0`.

## Repository controls

Final publication readback found no open pull request. Protected `master`
requires Build, Security Scan, Release Snapshot, and Action Pin Provenance. It
also enforces administrators and linear history, and it disables force pushes
and deletions.
