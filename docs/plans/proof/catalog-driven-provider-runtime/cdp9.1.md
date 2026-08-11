# CDP9.1 merge, release, and public readback

Status: `done`

## Architecture disposition

Repository history did not connect the former 52,428,800-byte release limit
to an approved distribution, startup, memory, storage, or operating
requirement. It was a dependency-growth review signal, not a hard product
budget.

The final architecture preserves Starmap-owned catalog acquisition:

- Local operation can compose Starmap acquisition in the Starport process for
  first-run and scheduled refresh.
- Enterprise operation can publish Starmap generations from a separate
  service.
- Both modes produce the same immutable generation and use the same atomic
  Starport activation transaction.
- Catalog-acquisition credentials and inference credentials remain separate.

Starport now reports every release executable size. The verifier enforces a
future hard limit only when an operator supplies an approved value through
`APPROVED_MAX_RELEASE_BINARY_SIZE_BYTES`.

## Release-size evidence

The final v1.0.2 snapshot reported these executable sizes:

| Target | Bytes |
|---|---:|
| Darwin amd64 | 57,234,720 |
| Darwin arm64 | 54,672,162 |
| Linux amd64 | 55,963,810 |
| Linux arm64 | 53,084,322 |
| Windows amd64 | 57,233,408 |
| Windows arm64 | 53,639,680 |

The Linux amd64 baseline was 49,791,138 bytes. The final delta was 6,172,672
bytes, or 12.3971 percent. It remained below the plan's 15 percent aggregate
review threshold. The review also confirmed static Linux binaries,
cgo-disabled builds, the complete platform matrix, SBOM generation, and the
accepted Starmap acquisition closure. Size did not change the architecture.

The optional approved-budget input passed at 100,000,000 bytes and failed
closed for a value of 1 byte and for a nonnumeric value.

## Starmap publication

- Pull request [#73](https://github.com/agentstation/starmap/pull/73) merged as
  `91319bc91422685b5e2937646f7efa5dea2fe371` after all hosted checks passed.
- Immutable release
  [v0.4.1](https://github.com/agentstation/starmap/releases/tag/v0.4.1)
  published through run
  [31503406666](https://github.com/agentstation/starmap/actions/runs/31503406666).
- All 12 archives and SBOMs matched `checksums.txt`. The AgentStation GPG
  signature was valid. Every listed asset passed GitHub provenance
  verification.
- The exact-version Homebrew install passed from the AgentStation tap.
- Starport resolved the public Go module as
  `github.com/agentstation/starmap v0.4.1` without a replacement directive.

Post-release catalog run
[31505690865](https://github.com/agentstation/starmap/actions/runs/31505690865)
published immutable generation `81d1cebe-53e3-4dc0-9ce6-f4f8f0e65b3f`:

```text
semantic: sha256:b6fd0461eb782fe80fef540f59e409c7ec4dc1791696f2a4cdf3157425975e21
payload:  sha256:d3acf6e69534f640973f59cc3361690c4e9d5ed7cf36447b6cd0cb3ae4ceacf1
archive:  sha256:4329847d7fbe0143086675a8cf16201f711003bb7c05edb26c373ad1d38b2320
```

The workflow and an independent download verified its checksum, provenance,
and public payload. The prior schema-v5 generation
`29f0711a-39c1-435e-9d56-9552c0f84b59` remained readable.

## Starport publication

- Pull request [#91](https://github.com/agentstation/starport/pull/91) passed
  all 10 hosted checks on head
  `7a8fc83ceb96a9c79fdab1fe6d25213157f2dc34` and merged as
  `a13e47dd547a0f46bcdb17ed5ab2f41207bd2df2`.
- Immutable release
  [v1.0.2](https://github.com/agentstation/starport/releases/tag/v1.0.2)
  published through run
  [31506426078](https://github.com/agentstation/starport/actions/runs/31506426078).
- The 12 archives and SBOMs matched the public checksum manifest. The
  manifest and every listed asset passed GitHub provenance verification.
- The public Darwin arm64 binary reported version `1.0.2`, commit `a13e47d`,
  Go `1.26.5`, and the expected target.
- The attested two-platform container published at
  `sha256:818d6e189d144143fbe57f2e09ffd9bb356770670ca3221344ff26d4da5d30bc`.
- Exact v1.0.2 Homebrew installs and developer artifacts passed on macOS and
  Linux. The macOS installation did not retain quarantine metadata.

The release workflow emitted one actionable warning: the container
attestation could not create its linked-artifact storage record without
`artifact-metadata: write`. Pull request
[#92](https://github.com/agentstation/starport/pull/92) added that permission
only to the release and recovery publication jobs. It also added a regression
check. All hosted gates passed before merge as
`7a2c66680049a565e3c42392ba8cd8f927cf6be1`.

Homebrew automatically trusted only the fully qualified
`agentstation/tap/starport` cask. The remaining `aws/tap` warning came from the
hosted macOS image and did not affect the Starport cask or exact-version test.

## CDP10 handoff

The project closed pull request
[#86](https://github.com/agentstation/starport/pull/86).
Pull request #92 merged its exact
`Homebrew/actions/setup-homebrew@c8707045ccae42888fe98e86f2ee8938bc7cc193`
update and recorded the supersession on #86. CDP10 can now remove the completed
control plane after this evidence merges.

No verification command used `GOFLAGS`, `-p`, or another scheduler cap.
