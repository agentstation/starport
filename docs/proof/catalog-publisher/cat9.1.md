# CAT9.1 The Starport operator documentation

Starport documentation now names the catalog settings that CAT8 shipped. A new
topology guide gives five deployment topologies and one replicated variant. The
operator guide holds the complete catalog reference. The README names the
central Starmap server topology. The environment example lists every canonical
name and holds no removed name.

This record covers conditions CAT-V56, CAT-V57, and CAT-V58.

## Fail before

The base commit is `9081ca4efbec995fdd77ac78bcfb355c1699f8bb`.

| Condition | State before | Command |
| --- | --- | --- |
| CAT-V56 | the topology guide did not exist | `git show 9081ca4:docs/DEPLOYMENT-TOPOLOGIES.md` reported that the path does not exist |
| CAT-V57 | the README named no central Starmap server and linked no guide | `git show 9081ca4:README.md \| grep -ci 'central starmap'` printed `0` |
| CAT-V58 | the environment example held five removed names | `git show 9081ca4:.env.example \| grep -nE 'STARPORT_CATALOG_(REMOTE_URL\|REMOTE_API_KEY\|REMOTE_ACTIVATION_INTERVAL\|REFRESH_ON_START\|REFRESH_INTERVAL)'` printed five lines |
| CAT-V58 | the operator guide named no source setting | `grep -c 'STARPORT_CATALOG_SOURCE'` printed `0` on the base guide |

The base README described the removed `STARPORT_CATALOG_REMOTE_URL` and
`STARPORT_CATALOG_REMOTE_API_KEY` settings. It linked the anchor
`docs/OPERATOR-GUIDE.md#remote-starmap-catalogs`. The base operator guide held a
`## Remote Starmap Catalogs` section at line 706. The base environment example
held two canonical names of the nineteen: `STARPORT_CATALOG_WORKSPACE_PATH` and
`STARPORT_CATALOG_REFRESH_TIMEOUT`.

## What this task wrote

**The topology guide.** `docs/DEPLOYMENT-TOPOLOGIES.md` opens with a decision
table and four selection questions. It then gives one section for each
topology. The five topologies are:

- a single Starport with direct GitHub
- a Starport fleet with direct GitHub
- a central Starmap server with replica acquisition
- restricted replica egress
- an air-gapped mirror

A sixth section gives the replicated central tier. Each section holds one mermaid diagram and five labeled paragraphs:
settings, request budget, freshness age, egress, and failure behavior.

**The fleet thresholds.** The fleet section states that a direct consumer
budgets from the four GitHub rate-limit headers. It gives the request rate for
100, 10,000, and 100,000 replicas at the startup spread, the poll interval, and
the acquisition interval. It names three points that move a fleet to a central
Starmap server. The three points are:

- 60 replicas behind one address with no token
- about 5,000 replicas that share one token
- 10,000 replicas against the secondary limit

**The boundary statement.** The restricted section states that restricted
egress is not air-gapped. The central server still reaches GitHub and the
provider APIs, so a route out of the network exists.

**The replicated variant.** The store decides the form. An active-active pair
needs a refresh lease and a conditional compare-and-swap. A plain shared volume
gives neither, so it supports the active and passive form alone. Each instance
keeps its own state directory, because the seed in that directory joins the
host name and the listen address in the instance identity.

**The operator guide.** `docs/OPERATOR-GUIDE.md` replaces
`## Remote Starmap Catalogs` with `## Catalog Configuration`. The section holds
six parts:

- the catalog settings table
- the removed settings table
- the workspace and state directory layout
- the generation procedures
- the catalog routes
- the freshness alert rules

The settings table gives nineteen names. Each row holds a default, the valid
values, and the interactions of that name.

**The README.** `README.md` replaces the removed remote settings with a
`### Catalog topology` subsection. The subsection holds one mermaid diagram of
the central Starmap server topology and links
`docs/DEPLOYMENT-TOPOLOGIES.md`. The documentation list gains the same link.

**The environment example.** `.env.example` lists all nineteen canonical
`STARPORT_CATALOG_` names with their defaults. `STARPORT_CATALOG_STATE_DIR`
sits next to `STARPORT_CATALOG_WORKSPACE_PATH` and states that two instances
must never share one state directory.

**The link gate.** `scripts/verify-doc-links.sh` gains
`docs/DEPLOYMENT-TOPOLOGIES.md` in its file list.

## Verifier evidence

The campaign verifier ran from the plan worktree against this Starport tree.

```text
PASS CAT-V56 the Starport topology guide names the five topologies and the
replicated variant with a diagram each and its links resolve.
PASS CAT-V57 the Starport README names the central Starmap server topology and
links the guide.
PASS CAT-V58 the Starport operator guide and environment example document every
canonical catalog name and the example holds no removed name.
```

The same run reported `Summary: 59 passed, 9 failed, 0 unverified`. The nine
open conditions belong to the console task and to the Starmap task. They are
CAT-V50, CAT-V52, CAT-V53, CAT-V54, CAT-V55, CAT-V59, CAT-V63, CAT-V64, and
CAT-V68.

## Commands

| Command | Result |
| --- | --- |
| `bash scripts/verify-doc-links.sh` | `PASS documentation links` |
| `bash scripts/test-doc-link-verifier.sh` | `PASS documentation link verifier edge cases` |
| `bash scripts/verify-readme-quickstart.sh` | `PASS README quickstart and dynamic stable-release selection` |
| `bash scripts/verify-developer-experience.sh` | `Summary: 47 passed, 0 failed` |
| `technical-writing lint docs/DEPLOYMENT-TOPOLOGIES.md` | `PASS: 1 file(s), 0 diagnostic(s)` |
| `technical-writing lint README.md` | `PASS: 1 file(s), 0 diagnostic(s)` |
| `technical-writing lint docs/README.md` | `PASS: 1 file(s), 0 diagnostic(s)` |
| `technical-writing lint docs/OPERATOR-GUIDE.md` | `FAIL: 48 diagnostic(s)`, the same count as the base commit |
| `bash scripts/verify-catalog-distribution.sh` | CAT-V56, CAT-V57, and CAT-V58 pass |

The technical-writing helper reads the repository configuration at
`.agents/technical-writing.toml`, which sets `mode = "strict"`.

## Known gaps

**The operator guide lint baseline.** `docs/OPERATOR-GUIDE.md` reported 48
diagnostics on the base commit and reports 48 diagnostics now. Every remaining
diagnostic sits above line 695, outside the new catalog section. The new
section adds no diagnostic. A clean result needs a rewrite of the
authentication, identity, and credential sections, which this task does not
own.

**The alert rules use a JSON probe.** The gateway publishes no catalog metric
on the Prometheus scrape. The alert rules therefore read
`GET /api/v1/admin/catalog/status` and grade the JSON fields. A later task can
move the rules to a metric when one exists.

**The offline verification procedure.** The air-gapped section states that the
Starmap central server runbook holds the artifact verification procedure. The
Starmap command-line interface publishes no offline verification subcommand
today, so the section names no command.

**`STARPORT_CATALOG_SOURCE_MAX_AGE` reached no grade at review time.**
Starport passes the value to `starmap.WithSourceMaxAge`. At the review, Starmap
validated it and read it no further, so grading used the fixed
`DefaultFreshnessPolicy` thresholds of six hours and ten hours. Starmap task
CAT9.3 wired the value into the channel thresholds, and the documentation now
states the derived grade.

## Untouched by this task

This task changed no Go code, no test, and no console source. It changed the
prose files, the environment example, and the file list of the documentation
link gate.
