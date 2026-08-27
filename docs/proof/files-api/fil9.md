# FIL9, documentation and the gate

FIL9 writes down the file store and puts its verifier in CI. A gate no workflow
runs cannot report a regression, and a surface no document names is one an
operator finds by reading source.

## The route table names the scope beside the route

`docs/OPERATOR-GUIDE.md` gained a File Storage section. Its table pairs each of
the five routes with the scope it reads.

The pairing is the point. A reader who sees only a route list has to guess
which routes a `files:read` key can call. The table answers that in one place,
and the answer matches `internal/server/routes.go`.

The same section states the rule the routes already hold. Each route scopes its
answer to the calling credential, and no route lists across accounts.

## The architecture document owns the split, not the operator guide

Two documents describe the same store, and each says a different thing.

| Document | What it states |
| --- | --- |
| `docs/ARCHITECTURE.md` | why the record and the bytes live apart |
| `docs/OPERATOR-GUIDE.md` | which values select a backend and what they bound |

The architecture section explains the split by size. A record is small,
indexed, and read by a prefix scan. A byte range is large, written once, and
read whole. A `KVStore` value holding a 512 MiB upload would put that payload
in every prefix scan of `files:v1:tenant:`.

The operator guide does not repeat that reasoning. It gives the environment
values and the defaults.

## Retention has a floor and a ceiling, and both belong in the guide

The deployment sets `STARPORT_FILES_RETENTION`, and an upload can ask for less
with `expires_after[anchor]` and `expires_after[seconds]`. Two refusals bound
that request: `MinRetention` is one hour, and the deployment window is the
ceiling.

A caller therefore shortens retention and never extends it. That single
sentence is what an operator needs, so the guide states it rather than listing
two error identifiers.

## The stored-byte bound is a level

The guide says so directly. Nothing resets a stored-byte bound at an interval
boundary: an upload raises it and a delete lowers it. It reads unlike the
request, spend, and token limits beside it in `internal/limits`, and a reader
who assumed a window would plan a wrong recovery.

The guide also names the refusal an operator will see, HTTP 413, and the repair
the gateway states with it.

## The configuration reference lists every field

`.env.example` is the field list `docs/README.md` points a reader to. It gained
the seven `STARPORT_FILES_` values, with the object-store block commented out
below the filesystem default.

An incomplete object-store selection refuses startup. Both `.env.example` and
the guide say so. The alternative reading is a silent fallback. An operator who
expected a shared bucket and got a local directory would lose a file the moment
a second node answered.

## The seam rule bounds where a backend name appears

The seam list in `AGENTS.md` gained one bullet. The `internal/files` package
owns the record, the purposes, the retention window, and the stored-byte bound.
Its sibling `internal/blob` owns the bytes, the two backends, and the name of
the selected one. No other package names a backend, a bucket, or a storage
path.

The last sentence is the enforceable half. Without it the rule reads as a
suggestion about where new code should go, rather than a bound on where a
backend name may appear.

## The gate runs where the other gates run

`.github/workflows/ci.yml` runs `scripts/verify-files-api.sh` in the same step
as the other verifiers, after `verify-model-modalities.sh`. The required
evidence list in `AGENTS.md` names it in the same position, so the local
command list and the CI job stay in step.

The evidence paragraph states the terminal count. The gate is terminal at 22
conditions, `FIL-V01` through `FIL-V22`. A later file feature changes a
condition body or replaces the gate. It does not raise the count.

## Both follow-on plans are active

The async media jobs plan and the document parser plan both waited on this one.
The async plan stores its assets through `internal/blob`, and the parser plan
reads bytes the file store holds. Both now read `active`, and `docs/TASKS.md`
records the same change.

## Acceptance

| Condition | Statement | Held by |
| --- | --- | --- |
| FIL-V21 | CI runs this gate | `.github/workflows/ci.yml` |
| FIL-V22 | the required evidence list names this gate and its terminal count | `AGENTS.md` |

## Verification

Fail-before: no workflow named the verifier, and the gate reported
`20 passed, 2 failed`.

After: `bash scripts/verify-files-api.sh` reports `Summary: 22 passed, 0
failed`. `bash scripts/verify-doc-links.sh` reports `PASS documentation links`.
`bash scripts/verify-developer-experience.sh` reports `47 passed, 0 failed`.
`bash scripts/verify-readme-quickstart.sh` and
`bash scripts/verify-v1-architecture.sh` pass.

The technical writing linter reports no diagnostic in the added ranges of
`docs/ARCHITECTURE.md`, `docs/OPERATOR-GUIDE.md`, and `README.md`. It reports
`0 diagnostic(s)` for `docs/TASKS.md` and for this file. `AGENTS.md` carries one
long-sentence diagnostic on the added gate paragraph. Every gate paragraph
around it is a list of the same shape and length, and a shorter form would
describe less than the gate covers.
