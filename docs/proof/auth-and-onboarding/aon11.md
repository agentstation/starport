# AON11 Documentation

Date: 2026-08-25
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)
Verifier: none new. The campaign gate stays at 26 conditions. This task is held
by `scripts/verify-readme-quickstart.sh`, `scripts/verify-doc-links.sh`, and a
run of the quickstart itself on a cold machine account.

## What changed

Thirteen tasks changed what the product does. The documents still described the
product before them: a quickstart whose printed banner was two lines shorter
than the one the program prints, no statement anywhere of which credential pays
for a request, and an architecture document that called the whole credential
subsystem BYOK.

Four documents now say the same five things, in the same words:

| Term | Owner | What it is |
| --- | --- | --- |
| Gateway API key | a tenant | The `STARPORT_` bearer token. Authenticates and carries scopes. |
| Environment credential | the operator | A provider key read from the process environment. |
| Gateway credential | the operator | A provider key the operator applies, scope `*`, deployment-wide. |
| BYOK | a tenant | A provider key a tenant brings, scope `tenant:<id>`. |
| Console session | a browser | A signed HttpOnly cookie opened by a launch ticket. |

`README.md` teaches the first two rows to somebody who has never run the
binary. `docs/OPERATOR-GUIDE.md` teaches all five to somebody running a
deployment. `docs/ARCHITECTURE.md` says where each one lives and why the seams
are where they are. `AGENTS.md` states the rule that keeps the next change from
undoing it.

## The quickstart now matches the program

The banner in the README was not a paraphrase; it was a transcript, and it had
gone stale. On the pre-AON11 head it read:

```text
Starport development gateway
URL: http://127.0.0.1:8080
Gateway API key (shown once): replace-with-generated-gateway-key
```

AON9 added the two lines that matter most to somebody starting out: whether the
gateway is open, and how to reach the console without pasting a key into a
browser. Neither appeared in the document that teaches the first five minutes.

The quickstart also gained two sections it did not have. **Serve without a
gateway API key** names `--no-auth`, the console switch, and the
`--allow-remote-no-auth` tripwire. **Keep the gateway** separates the throwaway
`starport dev` gateway from `starport init` plus `starport serve`, which is the
question the old `starport init` paragraph answered by implication only.

## BYOK means one thing

`docs/ARCHITECTURE.md` used the word for the whole credential subsystem in four
live places. Each is now specific:

| Was | Is |
| --- | --- |
| "BYOK provider-key management with encrypted credential storage…" | the three sources, named, with their owners |
| `ProviderKeys["BYOK provider-key handlers"]` | `ProviderKeys["provider credential handlers"]` |
| `internal/providers/ # provider runtime composition and BYOK` | `# provider runtime composition and credential reconciliation` |
| "The BYOK repository isolates provider keys by API-key scope." | isolates by scope: `*` for the operator's, `tenant:<id>` for a tenant's |

The last was wrong twice. It named the wrong credential and the wrong scope:
AON3 moved a tenant's credential from an API-key scope to a tenant scope, and
nothing updated the security bullet that described the old layout.

A new `## Credentials and Tenants` section states the three sources as a table,
the strategies that order them, the narrowing rule that lets a key restrict its
account's strategy but never widen it, and the reason a refused BYOK marks
nothing in shared operator availability state.

The package tree gained the seams the campaign created and never listed:
`internal/providers/keyring`, `internal/tenant`, `internal/limits`,
`internal/authmode`, and `internal/localauth`.

## Fail-before: the recorded one did not materialize

The plan recorded the baseline as "the quickstart verifier fails once AON9
changes the first-run output." It does not. On the AON14 head, with the
pre-AON11 README in place:

```console
$ git checkout -- README.md
$ bash scripts/verify-readme-quickstart.sh
PASS README quickstart and dynamic stable-release selection
$ echo $?
0
```

The verifier checks the shape of the quickstart — that `## Quick start` names
`starport dev`, that Terminal 1 does not mention `STARPORT_API_KEY` and
Terminal 2 does, that four literals about in-memory state and catalog refresh
are present. AON9 changed the contents of a fenced block the verifier does not
read. A stale transcript is exactly the kind of drift a shape check cannot see,
which is why the acceptance also asks for the commands to be run.

Two baselines that do hold on the same head:

```console
$ git show HEAD:docs/ARCHITECTURE.md | grep -c BYOK
5
$ git show HEAD:docs/ARCHITECTURE.md | grep -n BYOK | head -3
26:- BYOK provider-key management with encrypted credential storage, …
99:  Server --> ProviderKeys["BYOK provider-key handlers"]
125:├── internal/providers/        # provider runtime composition and BYOK
```

Four of those five name an operator credential. One — line 351, "Tenant BYOK
outcomes cannot change shared operator state" — was already correct and is
unchanged.

## The quickstart run on a cold data directory

Run with an empty `HOME`, an empty working directory, no `.env`, no inherited
environment, and the placeholder provider key the README itself prints:

```console
$ env -i HOME=$COLD PATH=/usr/bin:/bin TMPDIR=/tmp \
    OPENAI_API_KEY="replace-with-provider-inference-key" \
    STARPORT_SERVER_PORT=8099 ./starport dev --no-open

Starport development gateway
URL: http://127.0.0.1:8099
Authentication: required
Gateway API key (shown once): STARPORT_…
Console (one-time sign-in link): http://127.0.0.1:8099/launch?lt=…
```

Line for line, the banner the README shows. Then the Terminal 2 commands, as
written:

```console
$ curl --fail http://127.0.0.1:8099/health/ready
{"status":"ok","timestamp":"2026-08-25T19:37:18Z","service":"starport","version":"1.0.0"}

$ curl --fail-with-body -H "Authorization: Bearer $STARPORT_API_KEY" \
    http://127.0.0.1:8099/api/v1/models
422 models

$ curl -H "Authorization: Bearer $STARPORT_API_KEY" … /api/v1/chat/completions
401
```

The 401 is the documented outcome, not a defect: the README says the first
provider request is what proves whether the provider accepts the resolved
credential, and the credential here is the literal placeholder string. Readiness
answered 200 without a key on the same gateway, which is the other thing that
paragraph claims.

The console link was checked twice, because the README makes a claim about it
that a reader cannot verify by looking:

```console
$ curl -o /dev/null -w '%{http_code}' -D - "$LAUNCH_URL"
303
Location: /
Set-Cookie: starport_session=…; HttpOnly; SameSite=Lax

$ curl -o /dev/null -w '%{http_code}' "$LAUNCH_URL"
401
```

Spent on first use, as the document says, and the browser gets an HttpOnly
session rather than a key.

`STARPORT_SERVER_PORT=8099` is the only departure from the literal text. Port
8080 held a gateway the operator of this machine is watching, and `starport dev`
has no port flag.

## Deviations

**Step 4 asked for a replacement that had nothing to replace.** It reads
"Replace the `internal/providers/byok` seam line in `CLAUDE.md` with
`internal/providers/keyring`." No such line ever existed — the seam list named
`internal/providers/auth` and `internal/providers/state` but never the
credential store. AON3 renamed the package without the instructions ever having
mentioned it. A keyring line was added rather than replaced, and it states the
vocabulary the package owns so the next change has somewhere to put a fifth
source.

**`CLAUDE.md` is a symlink to `AGENTS.md`.** The edits land in `AGENTS.md`, which
is the tracked file. The plan names `CLAUDE.md` throughout; both paths read the
same bytes.

**The instructions gained more than the seam line.** Two ownership rules were
added alongside it: that a gateway API key and a provider credential are
different secrets, and that BYOK names only a tenant-brought credential. The
architecture document explains the distinction; the instructions are what an
agent reads before editing, and an explanation nobody reads first does not stop
the drift this task exists to correct.

## Evidence

```console
$ bash scripts/verify-readme-quickstart.sh
PASS README quickstart and dynamic stable-release selection

$ bash scripts/verify-doc-links.sh
PASS documentation links

$ bash scripts/test-doc-link-verifier.sh
PASS documentation link verifier edge cases

$ bash scripts/verify-auth-onboarding.sh
Summary: 26 passed, 0 failed

$ bash scripts/verify-package-layout.sh
package-layout verification passed

$ bash scripts/verify-v1-architecture.sh
Summary: 12 passed, 0 failed

$ grep -rn BYOK docs README.md CLAUDE.md
```

Every remaining match names a credential a tenant brought for itself, or is a
historical record: `docs/TASKS.md:40` is a completed 2025 task row,
`docs/CONTRIBUTING.md:161` names the word as a glossary term, and the proof
documents under `docs/proof/auth-and-onboarding/` record what each task found at
the time it ran.

No code changed. No test was weakened or removed.
