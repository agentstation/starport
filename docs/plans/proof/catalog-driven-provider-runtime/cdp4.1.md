# CDP4.1 Starmap release and catalog publication

Status: `done`

Starport consumer commit: `dac601b120114fd4ff00db6f0ba9e8ed89e14a20`

Starmap release source: `47edede01dfb494343ca553c78a273940efa9b42`

Starmap catalog workflow correction: PR
[#72](https://github.com/agentstation/starmap/pull/72), merge commit
`82a82dc644d7377f18f14b19b554a3a604d17e62`

## Release prerequisites and pending pull requests

CDP4.1 reviewed all pending Starmap changes that the release required. It
repaired defects and ran the GitHub-hosted checks before each merge:

| PR | Purpose | Merge commit | Hosted result |
|---|---|---|---|
| [#68](https://github.com/agentstation/starmap/pull/68) | Attestation action correction | `fa4f157fde1839c33687d3e37633fec2b44a3a9b` | All three checks passed in run `31463070157`. |
| [#70](https://github.com/agentstation/starmap/pull/70) | Catalog-driven architecture | `5db5913e43db9c1e76a7996fb7096cc46605702a` | All three checks passed in run `31461468872`. |
| [#71](https://github.com/agentstation/starmap/pull/71) | S3 dependency correction | `47edede01dfb494343ca553c78a273940efa9b42` | All three checks passed in run `31465040541`. |
| [#72](https://github.com/agentstation/starmap/pull/72) | Compatible catalog rollback selection | `82a82dc644d7377f18f14b19b554a3a604d17e62` | Verification Gate, Security & Reliability, and Action Pin Provenance passed in run `31470721131`. |

The Starmap open pull-request query returned an empty list after PR #72 merged.

## Starmap v0.4.0

The immutable public
[v0.4.0 release](https://github.com/agentstation/starmap/releases/tag/v0.4.0)
resolves to exact source commit
`47edede01dfb494343ca553c78a273940efa9b42`. Release workflow
[run 31466577256](https://github.com/agentstation/starmap/actions/runs/31466577256)
passed its test, release, and Homebrew verification jobs.

The release contains six platform archives, six SBOMs, one checksum manifest,
and one checksum signature. Independent public readback verified all 12 archive
and SBOM entries against the SHA-256 manifest. GitHub SLSA provenance covers
all 12 files. The release is public, immutable, not a prerelease, and marked as
latest.

The AgentStation Homebrew cask uses version 0.4.0 and the published hashes. It
uses the `main` tap branch and the scoped quarantine-removal hook. The hosted
Homebrew job installed and verified the exact release.

## Starport consumer migration

The fail-before consumer selected newer Google authentication, gRPC, and
Google API modules than Starmap tested directly. The Starmap dependency update
and release verification aligned the tested graph before publication.

Starport commit `dac601b120114fd4ff00db6f0ba9e8ed89e14a20` now requires
`github.com/agentstation/starmap v0.4.0`. A clean consumer clone had no
`replace` directive and resolved these public module checksums:

```text
github.com/agentstation/starmap v0.4.0
h1:Gpy/PUvUI9EtuD5/g26UuuGQQtHZqM5YruXwOfBtvlE=

github.com/agentstation/starmap v0.4.0/go.mod
h1:PpHoL/VXQr1k6cfaaixJwou4DnAOYw9a+PCLwtaz86I=
```

The clean clone passed `go test ./...`. The Starport branch also passed:

- The focused catalog, application, proxy, and router tests.
- `go test ./...`.
- The repository integration race gate with normal uncapped scheduling.
- `make verify`.
- `go vet ./...`.
- `make lint` with zero issues.
- `make build`.
- Raw, Python, TypeScript, and Go OpenRouter SDK smoke tests.
- `make release-check` with the pinned GoReleaser 2.17.1.

## Catalog publication correction

Initial catalog run
[31468600163](https://github.com/agentstation/starmap/actions/runs/31468600163)
published and verified the first schema-v5 generation. Its last rollback check
then failed because the workflow selected the newest older prerelease without
checking its declared schema compatibility. That immutable release used schema
3. The strict schema-v5 decoder correctly rejected it. This was a workflow
selection defect, not a reason to add a runtime compatibility path.

PR #72 added schema-independent envelope inspection for rollback selection.
The public `Open` path remains strict and still rejects an incompatible payload.
The workflow now verifies provenance before it selects the newest release that
declares support for the current schema. If no compatible release exists, the
first publication for a new schema has no rollback target.

The exact PR #72 commit passed these local gates before publication:

- `GOTOOLCHAIN=go1.26.5 make verify`.
- `GOTOOLCHAIN=go1.26.5 make release-check`.
- `actionlint .github/workflows/catalog-generation.yaml`.
- The pre-PR autoreview with GPT-5.6-sol at high reasoning. It reported no
  accepted or actionable findings. The configured Claude reviewer failed
  before inference because its API route rejected a self-signed certificate.

The full uncapped race suite passed in the verification gate. No command used
`GOFLAGS`, `-p`, a scheduler cap, or a timeout change.

## Verified public schema-v5 generation

Recovery run
[31471995383](https://github.com/agentstation/starmap/actions/runs/31471995383)
ran from exact `main` commit
`82a82dc644d7377f18f14b19b554a3a604d17e62`. It refreshed providers and
classified a semantic change. It then validated and staged the generation,
created and verified provenance, and published the immutable release. It
downloaded and verified that release and the prior compatible schema-v5
release. The workflow skipped the optional OCI mirror because the mirror has no
configuration.

The new immutable prerelease is
[`catalog-semantic-0da6f0bfefb6b3dbb442a13d776f73c8025e286cb43628c0d2261b8631f23c4a`](https://github.com/agentstation/starmap/releases/tag/catalog-semantic-0da6f0bfefb6b3dbb442a13d776f73c8025e286cb43628c0d2261b8631f23c4a).
Its release tag resolves to the same exact `main` commit. Public download and
strict verification returned:

```text
generation_id: 29f0711a-39c1-435e-9d56-9552c0f84b59
semantic_checksum: sha256:0da6f0bfefb6b3dbb442a13d776f73c8025e286cb43628c0d2261b8631f23c4a
payload_checksum: sha256:cd8210c1b5a58d8feaf7eece50333d6d9ea83c7a0172123df1dd846786922a4e
archive_checksum: sha256:bdb3b786106336f1483f490c64272bc903bca4fea1bcd5bba5443a09fa964b03
```

The release has exactly three uploaded assets: the catalog archive, the
SHA-256 file, and the detached in-toto statement. The release is public,
immutable, and a prerelease. A separate `gh attestation verify` command passed
with the catalog workflow as signer and self-hosted runners denied.

The rollback check selected the preceding compatible schema-v5 release,
`catalog-semantic-a6da80f611d9c5342352c7edebe7e4baf9c27715631d12e6fd52549ec533a36f`.
It did not select the immutable but incompatible schema-v3 release.

The cross-repository campaign verifier reported `3 passed, 16 failed`. CDP5
and later tasks own the remaining red conditions.

CDP4.1 acceptance is complete. A clean Starport consumer resolves the exact
public Starmap release and module graph. The public catalog generation passes
artifact, provenance, remote, and compatible rollback verification.
