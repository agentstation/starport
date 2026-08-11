# Catalog-driven provider runtime review disposition

Status: accepted for plan activation on 2026-08-10. Later policy corrections
in this file govern active execution.

## Review basis

- Starport: `101c32d8fd6991586e8ae7003baf199cef651844`.
- Starmap: `7f53767dfb68efbde2cec80c3d739f5badb43230`.
- Independent GPT-5.6-sol xhigh verdict from the review prompt: `approve with
  changes`.
- Claude supplied three follow-up reviews. Codex checked their source claims
  against both pinned trees.
- Network research used official project sources. Live secret-store behavior
  and adapter build deltas remain unverified.

## Independent review blocking findings

| ID | Disposition | Plan route |
|---|---|---|
| B1 | Accepted | CDP1 defines profiles, plane references, terminal authentication alternatives, and typed protocol options. |
| B2 | Accepted with a corrected value contract | D5 defines secret references. D8 and CDP5.1 define multi-field credential material instead of raw bytes. |
| B3 | Accepted and strengthened | D2 and CDP1 validate provider, profile, field, and whole-catalog alias collisions before environment access. |
| B4 | Accepted with narrower scope | Starmap already has `Client.CurrentCatalogState`. CDP4 extends the remote subscriber and caller-owned storage. |
| B5 | Accepted | D10 and CDP6.1 define complete runtime candidate construction, atomic publication, rollback, and connector draining. |
| B6 | Accepted and strengthened | CDP-V01 through CDP-V19 freeze the terminal verifier contract. CDP-V19 proves BYOK order and `user_only` noninterference. |
| B7 | Accepted | CDP2, CDP3, CDP4, CDP6, CDP7, and CDP9 now have smaller follow-on tasks. |
| B8 | Accepted with a policy correction | D6 and the dependency policy separate hard budgets, review thresholds, and hard safety, correctness, and compatibility gates. Numeric heuristics cannot reject an adapter by themselves. |

## Independent review non-blocking findings

| ID | Disposition | Plan route |
|---|---|---|
| N1 | Accepted | CDP2.1 uses durable reconciliation issues for review candidates and names their required evidence. |
| N2 | Accepted | CDP2 permits provider constants only when production selection cannot enumerate or switch on them. |
| N3 | Accepted | D10, CDP6, and CDP-V12 include operation support in the runtime intersection. |
| N4 | Accepted | CDP1 validates query placement. CDP3, CDP5.1, and CDP7 require URL and query redaction. |
| N5 | Accepted | CDP0 freezes prohibited production zones and permits exact codec, test, fixture, and documentation cases. |

## Claude follow-up findings

| ID | Disposition | Evidence and plan route |
|---|---|---|
| G1 | Accepted after Claude withdrew the universal `providerauth.Source` proposal | `Token` holds one value, requires expiry in its refreshing path, and exposes `QuotaProjectID`. D8 and CDP5.1 move caching and single-flight work to a general multi-field credential layer. Bearer authentication becomes a thin projection. |
| G2 | Accepted | Core V1 includes ambient, explicit `env:`, explicit `file:`, and cloud default sources. File change identity cannot rely on modification time alone. |
| G3 | Accepted and strengthened | Product aliases derive from provider and field IDs. The validator also covers profiles and cross-pair collisions. |
| G4 | Accepted | D4 separates tenant policy from operator source resolution. CDP7 integrates all three BYOK strategies with inference attempts. |
| G5 | Accepted | CDP6.1 publishes one complete runtime generation and drains old connectors. |
| G6 | Accepted as a new blocker | Connectors store `ProviderConfig.APIKey` and apply it to every request. CDP6 makes connectors credential-free and carries selected material explicitly for each request. |

## Claude activation review findings

| ID | Disposition | Evidence and plan route |
|---|---|---|
| A1 | Accepted with a later policy correction | Raw module, package, owner, and binary counts are review evidence. More than five owners and the 8 or 15 percent size deltas trigger review. They do not reject an adapter. Hard safety, correctness, compatibility, provenance, license, vulnerability, and approved operational gates remain. |
| A2 | Accepted | CDP-V19 freezes BYOK order, forbids operator use under `user_only`, and requires the same external error whether operator material exists. |
| A3 | Accepted as a factual correction | GPT-5.6-sol xhigh ran the independent review prompt. The plan no longer attributes the completed review to a different model. |
| A4 | Accepted | CDP9 documents strict ambient precedence, explicit `env:` override, wrapper quickstarts, and projected or mounted file rotation. |
| A5 | Accepted with narrower language | CDP3 and CDP5.1 test in-place rewrite, atomic replacement, symlink target swap, mounted-content replacement, and agent file rerender. The plan does not claim that all systems use the Kubernetes `..data` implementation. |
| A6 | Accepted | CDP-V07 and CDP9 require both product-owned source implementations to run the same named conformance vectors. No shared runtime secret package is added. |

## Operator and rotation source evidence

- [Doppler CLI](https://docs.doppler.com/docs/cli) documents
  `doppler run -- your-command-here` and environment injection.
- [1Password CLI](https://www.1password.dev/cli/reference/commands/run)
  documents `op run -- <command>` and environment injection.
- [Infisical CLI](https://infisical.com/docs/cli/commands/run) documents
  `infisical run -- <command>` and environment injection.
- [Kubernetes projected volumes](https://kubernetes.io/docs/concepts/storage/projected-volumes/)
  document atomic writes based on symlinks.
- [Vault Agent templates](https://developer.hashicorp.com/vault/docs/agent-and-proxy/agent/template)
  document file destinations, renewal, and rerender behavior.

## Corrections and qualifications

The independent review recommendation for
`Resolve(context.Context, SecretRef) ([]byte, error)` was incomplete. Raw bytes
cannot represent multi-field material, versions, expiry, or leases. The
repository-owned contract returns named credential material with opaque
lifecycle metadata.

The credential layer owns the only cache and single-flight mechanism.
`providerauth.Source` cannot add a second cache. Static credentials do not need
a fabricated expiry. Google quota-project handling stays in the Google
authentication primitive.

The file source uses content identity and file identity to detect in-place and
atomic replacement. It does not expose a raw secret digest, inode, or sensitive
path through logs, metrics, cache keys, or errors.

Starmap `Client.CurrentCatalogState` already returns one atomic pair. The
remote subscriber does not expose that pair and advances its own identity for
a digest-equal generation without republishing the client catalog. CDP4 owns
this semantic and a caller-owned durable store.

Starport's module graph contains AWS SDK modules through Starmap. The current
Starport release binary does not list an AWS SDK module in `go version -m`.
The direct AWS adapter therefore has a real linked-binary cost. Its exact cost
remains unverified until CDP7.1.

Claude reported the following direct-adapter deltas. Claude did not attach a
command log, source harness, module graph, or binary. These values are
`UNVERIFIED` until CDP3.1 and CDP7.1 reproduce them with retained raw evidence.

| Adapter | Reported binary delta | Reported module delta | Reported package delta |
|---|---:|---:|---:|
| Azure Key Vault | 90,112 bytes | 2 | 3 |
| HashiCorp Vault | 491,520 bytes | 14 | 26 |
| Google Secret Manager | 3,309,568 bytes | 8 | 128 |
| AWS Secrets Manager | 3,358,720 bytes | 15 | 84 |
| All four | 7,208,960 bytes | 70 total modules | 240 |

The reported data supports one structural correction: raw module and package
counts do not measure runtime cost by themselves. The policy keeps complete
graph evidence. More than five owners requires dependency and security review.
The 8 percent per-adapter and 15 percent aggregate binary deltas require
release review. These thresholds do not select the verdict. CDP3.1 and CDP7.1
repeat the aggregate build after every accepted adapter.

OpenBao passed its
mandatory review because the six-owner count was its only prior rejection
reason.

## Library disposition

- Reject Helmfile vals. Version 0.45.0 requires Go 1.26, lacks one suitable
  context-aware source contract, and has a broad runtime closure.
- Keep Go CDK runtimevar as a comparator. Version 0.46 includes AWS Secrets
  Manager, AWS Parameter Store, Google Cloud Secret Manager, filevar, and
  HashiCorp Vault. It has no equivalent Azure runtimevar package.
- Prefer official service clients. Evaluate maintained protocol-native clients
  with the same evidence.
- Prefer `github.com/hashicorp/vault/api` for Vault.
- Use the official `github.com/openbao/openbao/api/v2` client for the separate
  OpenBao adapter and conformance target.

## Measured baseline

The controlled build used Go 1.26.5 and this command shape:

```text
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w'
```

| Repository | Binary bytes | Modules | Compiled packages |
|---|---:|---:|---:|
| Starport | 49,791,138 | 157 | 609 |
| Starmap | 36,769,954 | 126 | 570 |

The Starmap local head had the same tree as the pinned baseline. Existing
`dist` binaries were not accepted as the Starmap baseline because their
embedded module versions did not match the pinned source tree.

## Activation disposition

The plan can move to `active` after the owner approves the whole-plan goal.
CDP0 must author the 19-condition red verifier before a production change.
G2, G4, G5, G6, A1, and A2 are activation requirements. B4 requires a precise
task before activation, not completed implementation.
