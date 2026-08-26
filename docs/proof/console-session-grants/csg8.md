# CSG8 — the words "sign in" belong to the identity grant

## Problem

The campaign registered three grants, and only one of them is about a person.
A launch ticket and a pasted local admin token both answer *where you are*: you
are at the machine that printed the secret. An identity provider answers *who
you are*, and it is the only grant that ships nothing yet.

The shipping copy did not hold that line. `starport dev` printed
`Console (one-time sign-in link)`, the route table was headed
`Console Sign-In`, and roughly twenty comments across `internal/server`,
`internal/localauth`, and `console/src` described a browser holding a cookie as
`signed in`. That copy is wrong twice over. It tells an operator the gateway
authenticated them when it did no such thing, and it spends the words the
enterprise grant will need on the two grants that must not claim them. When an
identity provider does land, `Sign in with your provider` has to be new
language on the page, not a phrase already worn out by a link.

The old verifier condition was
`absent 'sign-in link' internal/cli` — one literal string in one directory. It
would have passed on a rename, and it never looked at the console at all.

## Change

| Surface | Was | Is |
| --- | --- | --- |
| `internal/cli/development.go` | `Console (one-time sign-in link)` | `Console (one-time launch link)` |
| `cmd/starport/run.go` | `Could not mint a console sign-in link…` | `…a console launch link…` |
| `internal/server/routes.go` | `// Console Sign-In:` | `// Console Session Grants:` |
| `internal/cli/ui.go`, `README.md`, `docs/OPERATOR-GUIDE.md` (5 lines) | `sign-in link`, `sign-in URL` | `launch link`, `launch URL` |
| comments in `internal/server`, `internal/localauth`, `console/src/lib/api.ts` | `a signed-in shell`, `claiming to be signed in`, `has not signed in` | `the shell`, `claiming to hold a session`, `holds no session` |
| `internal/authmode/mode.go` + its test | `a gateway they cannot log in to change` | `a gateway they cannot reach to change` |

The two `authmode` lines are the same error in a different tense: reaching a
gateway with a gateway API key is not logging in either.

Nothing about behaviour moved. Every edit is a string an operator reads or a
comment a maintainer reads.

## Why the route heading changed too

`// Console Sign-In:` headed a block that now registers two routes. The
replacement names the concept the campaign introduced, and the sentence under
it now covers both routes at once rather than repeating the same rationale
twice — the second comment keeps only what is unique to it, that the path is
written out because Go uses it once and the console reaches it from
TypeScript.

## Verifier condition

CSG-V16 was rewritten from a single-literal grep into a reservation:

```bash
! grep -RniE '\b(sign|signed|log|logged)[ -]?in\b' \
    --include='*.go' --include='*.ts' --include='*.tsx' --include='*.md' \
    --exclude='routeTree.gen.ts' \
    internal cmd console/src README.md docs/OPERATOR-GUIDE.md \
  | grep -vE 'internal/localauth/(grant_identity|grant)\.go' \
  | grep -q .
```

It scans every shipping surface — Go, TypeScript, the README, and the operator
guide — and carves out exactly the two files that own the reservation:
`grant_identity.go`, which is the grant those words belong to, and the line in
`grant.go` that states the rule. A condition that only forbade the words would
be wrong; the point is that one grant may spend them and the others may not.

The trailing word boundary is what makes it usable. Without it, six lines
report as logins: five `signingKey` / `signing key` references in
`internal/localauth`, which is an unrelated homonym, and one sentence in the
operator guide about a `catalog intersection`. The leading boundary catches
nothing in the tree today and is kept for the other direction — `redesign in`,
`assign in`, and `catalog in the console` all read as sign-in to a pattern that
never asks where the word starts. That asymmetry is recorded in the script
rather than left for the next reader to rediscover.

## Fail-before

Four controls, each reverted immediately afterwards:

| # | Mutation | CSG-V16 |
| --- | --- | --- |
| 1 | `internal/cli/development.go` prints `Console (one-time sign-in link)` again | FAIL |
| 2 | a comment in `console/src/lib/api.ts` says `a reader who is logged in` | FAIL |
| 3 | the `grant_identity`/`grant` exception is dropped from the pipeline | FAIL |
| 4a | the leading `\b` is removed | PASS |
| 4b | the trailing `\b` is removed | FAIL |

Control 1 proves the condition still covers the case the old one covered.
Control 2 proves it now covers the console, which the old one never read.
Control 3 proves the identity grant really does spend the words, so the
exception is load-bearing rather than defensive. Control 4b proves the trailing
boundary is doing work; 4a is reported as it ran — the leading boundary is a
guard against a phrasing the tree does not currently contain, not a fix for a
current hit.

An earlier run of controls 1–3 reverted with `git checkout <file>`, which
discarded the uncommitted CSG8 edits in those same files along with the
mutation. The controls were rerun against file copies, and the two edits lost
that way (`api.ts` line 47, `development.go` line 88) were restored and are in
the change set above.

## Checks

```
bash scripts/verify-console-session-grants.sh   Summary: 16 passed, 0 failed
go build ./...                                  clean
go test ./internal/...                          all packages ok
gofmt -l internal cmd                           no output
console: pnpm typecheck                         clean
console: pnpm test                              19 files, 107 tests passed
```
