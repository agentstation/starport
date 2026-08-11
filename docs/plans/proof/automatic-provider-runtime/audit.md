# Automatic provider runtime plan audit

Date: 2026-08-11  
Baseline: `b52cd7e286a9a870293155638392ed514b630a47`

## Disposition

The plan is ready for activation after the corrections recorded in this audit.
The review changed the design at one important seam: Starport must select
request credential material before it binds a catalog endpoint template.
Without this order, tenant-only Azure and Vertex requests cannot supply their
own endpoint fields.

The audit also replaced vague or arbitrary acceptance rules. The plan now has
12 named verifier assertions, exact admin routes, exact refresh triggers, a
typed state contract, and evidence-based failure scopes. It has no numeric
dependency rejection threshold. A numeric measurement can reject a design only
when an approved product or operational budget defines its effect and exception
process.

## Verified current behavior

| Area | Baseline behavior | Required plan correction |
|---|---|---|
| Catalog authority | Starmap supplies provider, offering, endpoint, and inference credential metadata. | Preserve Starmap as the only provider authentication contract. |
| Credential planes | The resolver reads `credentials.inference` and not catalog-acquisition profiles. | Preserve this isolation. |
| Provider membership | Configuration includes only providers with resolved operator material. Activation iterates that filtered map. | Register every executable catalog provider without operator material. |
| Endpoint binding | Activation and catalog projection bind endpoint templates from operator configuration. | Bind after request policy selects operator or tenant material. |
| Empty material | Application composition rejects an empty production provider set. | Keep the gateway ready and report provider credential state. |
| Cloud chains | Google and Azure default chains are compiled and keyed by authentication primitive. | Attempt only catalog-declared profiles during bounded background work. |
| Runtime publication | The registry already supports atomic generations, request leases, and drain. | Reuse this owner for reconciled runtime publication. |
| Offering health | `internal/availability` owns `healthy`, `open`, `half_open`, and `unavailable`. | Do not duplicate this state machine. Project it into admin status. |
| Administration | The existing admin group has authentication, but the planned provider routes do not exist. | Add the two exact authenticated routes named in APR5. |
| Local storage | Production Badger storage is persistent. There is no explicit in-memory development composition. | Use Badger in-memory mode for `starport dev`. |

The current request planner selects a concrete route before credential policy
selects tenant or operator material. The implementation must keep route
identity separate from endpoint material. It must bind the chosen route's
Starmap template after credential selection and before connector execution.

## Dependency and platform review

The audit ran:

```text
go list -m -u -f '{{if not .Indirect}}{{.Path}} {{.Version}} {{with .Update}}update={{.Version}}{{else}}current{{end}}{{end}}' all
go version
```

Every direct module reported `current`. The complete command output is in
[`dependencies.txt`](dependencies.txt). Relevant versions are:

| Module | Version | Planned use |
|---|---:|---|
| `github.com/agentstation/starmap` | `v0.4.1` | Catalog and inference authentication contract |
| `cloud.google.com/go/auth` | `v0.23.0` | Google default credentials and project identity |
| `github.com/Azure/azure-sdk-for-go/sdk/azidentity` | `v1.14.0` | Azure default credentials |
| `github.com/dgraph-io/badger/v4` | `v4.9.6` | In-memory development storage |
| `github.com/urfave/cli/v3` | `v3.10.1` | Context-bound `dev` command lifecycle |
| `github.com/fsnotify/fsnotify` | `v1.10.1` | Existing file-source events where applicable |

The local toolchain was `go1.26.5 darwin/arm64`. The module keeps its published
Go 1.25 language contract. This campaign does not change that contract.

Official API documentation supports the planned use:

- [Google `DetectDefault`](https://pkg.go.dev/cloud.google.com/go/auth/credentials#DetectDefault)
  searches the documented application-default sources. The returned
  [credentials can supply a project ID](https://pkg.go.dev/cloud.google.com/go/auth#Credentials.ProjectID).
- [Azure `DefaultAzureCredential`](https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azidentity#DefaultAzureCredential)
  is an ordered chain. The chain can contact local tools or platform services,
  so Starport must run it with deadlines outside the inference hot path.
- [Badger `WithInMemory`](https://pkg.go.dev/github.com/dgraph-io/badger/v4#Options.WithInMemory)
  stores all data in memory and creates no value or SST files.
- [urfave/cli `ActionFunc`](https://pkg.go.dev/github.com/urfave/cli/v3#ActionFunc)
  receives a `context.Context`, which is sufficient for cancellation and
  shutdown of `starport dev`.

No new runtime dependency is required. Transitive updates alone do not justify
a direct dependency change. If implementation finds a missing capability, the
owning task must record the specific need and the dependency evidence required
by decision D11.

## Assumptions accepted for activation

1. A catalog provider can be represented without operator material when its
   transport and inference authentication primitive are compiled.
2. Endpoint templates are provider facts. Their resolved values are selected
   request credential material and must not enter the shared route generation.
3. Provider reconciliation has three membership triggers: startup, a configured
   interval, and an authenticated manual request. Credential renewal stays in
   the credential resolver.
4. Process environment changes require restart. File and remote secret sources
   can refresh through their supported lifecycle.
5. Readiness reports internal gateway operation. Provider credential
   availability is an admin-state concern.
6. The current offering circuit policy is outside this campaign.
7. Direct breaking changes follow the repository's published
   no-compatibility-window policy. Starport is already publicly released, so
   the plan does not call these changes prelaunch changes.

APR0 must preserve this audit as baseline evidence and create the red verifier
before production behavior changes.
