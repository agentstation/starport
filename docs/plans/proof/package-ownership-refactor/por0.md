# POR0 baseline and proof contract

POR0 pins the campaign inputs before a package move changes product behavior.

## Baselines

| Repository | Branch | Commit | Working tree |
|---|---|---|---|
| Starport | `main` | `1243849111f5814d0e3418ac7920900fc4a0ea98` | clean |
| Starmap | `main` | `91319bc91422685b5e2937646f7efa5dea2fe371` | clean |

Starport plan pull request [#105](https://github.com/agentstation/starport/pull/105)
merged as the pinned Starport commit. Ten hosted checks passed on the exact
reviewed head `e09ff68cbf94fbf8ae98c51773476aa5c2f509a4`.

The open-pull-request query returned no open pull request in either product
repository before the control branch started. See `open-prs.json`.

## Package graphs and tests

`package-graphs.tsv` records the package count, local import-edge count, and
SHA-256 digest of the sorted local adjacency graph. The graph command is:

```bash
go list -json ./... | jq -c -s --arg p "$MODULE" \
  'map(select(.ImportPath|startswith($p))) as $pkgs |
   ($pkgs | map(.ImportPath) | INDEX(.)) as $idx |
   $pkgs | map({package:.ImportPath,
   imports:([.Imports[]? | select($idx[.] != null)]|sort)}) |
   sort_by(.package)' | shasum -a 256
```

`test-inventory.tsv` records 548 Starport tests, 3 Starport fuzz tests, 1,015
Starmap tests, 19 Starmap examples, and 7 Starmap fuzz tests. These baseline
commands passed with normal uncapped Go scheduling:

```bash
go test -count=1 ./...
go test -race -count=1 ./...
```

The ordinary and race runs covered 42 Starport package suites and 85 Starmap
package suites. Neither run used `GOFLAGS`, `-p`, or another scheduler cap.

## Fail-before evidence

The scoped stale-path inventory found eight old Starport packages across 48
current-authority files. It found six old Starmap support packages across 49
current-authority files. See `stale-path-inventory.tsv`.

Starport `internal/providerauth` contains both request mutation and renewable
Google and Azure credential lifecycles. Starport `internal/httpclient` exports
middleware, metrics, and mutation APIs, but production outside that package
uses only `DefaultConfig`, `New`, and `GetHTTPClient` from one connector file.

Four Starmap provider test packages construct provider IDs, endpoints,
credential names, field mappings, or author mappings in Go. The shared fixture
helper also derives provider identity from its directory. These facts duplicate
the embedded provider YAML and make fixture movement depend on path shape.

The strict Starport writing baseline has 59 diagnostics in files that POR4
already owns: 1 in `AGENTS.md`, 56 in
`docs/ARCHITECTURE_CONTROL_PLANE.md`, and 2 in
`internal/storage/README.md`.

## Budget measurement

`starmap-budget.json` contains the raw current report. The report passes its
existing numeric checks. It measured 8,110,865 uncompressed bytes, 298,892
compressed bytes, 14 providers, 590 models, and a 137,108-second generation age.
POR2 must still classify each number from an approved product objective.

## Campaign verifier

`verify.sh` implements POR-V01 through POR-V09. `test-verifier.sh` checks the
nine-result output shape, result count, exit status, and invalid-root failure.
The verifier is intentionally red on these baselines. Later task proofs record
which named assertion changed after each merge. The first corrected baseline
run reported `Summary: 0 passed, 9 failed` and exited with status 1.
