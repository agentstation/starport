# CSG9 — the gate runs, and the guide describes the grants

## Problem

Two things were still missing after CSG8. The verifier existed but no workflow
ran it, and a gate no workflow runs cannot report a regression — it reports
whatever the last person to run it locally remembers. And the operator guide
still described the launch ticket as the only way into the console, so an
operator who typed the console URL by hand met a page the documentation had
never mentioned.

## Change

| File | Change |
| --- | --- |
| `.github/workflows/ci.yml` | `verify-console-session-grants.sh` added to the verification step, beside `verify-auth-onboarding.sh` |
| `AGENTS.md` (`CLAUDE.md`) | the script added to the pre-PR evidence list, and a paragraph stating what it guards and that it is terminal at 16 conditions |
| `docs/OPERATOR-GUIDE.md` | new `### Opening the console` section: the grant table, what the two shipping grants prove, the first-contact page, the two commands that avoid the paste, the inert identity grant, and the gateway API key as the off-machine alternative |
| `README.md` | the quickstart now names the paste path beside the launch link |

The evidence list and the CI step are kept in the same order so a reader can
diff them by eye.

## Why the guide says "the vocabulary of identity" rather than the words

The first draft of the new section wrote the reserved phrase out — *the words
`sign in` are reserved for it* — and CSG-V16 immediately went red on the
operator guide. That is the gate working: the guide is a scanned surface, and a
condition that let a sentence off because it was *about* the rule would be a
condition with a hole in it.

The section now names the grant instead of the phrase and points at the script
for the specifics. Fixing it also closed a real gap: the pattern matched
`sign in` and `signed in` but not `signing in`, so the alternation now carries
`signing` as well. `signingKey` and `signing key` stay clear of it, because the
pattern requires `in` immediately after the separator.

## Fail-before

| # | Mutation | Result |
| --- | --- | --- |
| 1 | the operator guide says *the words `sign in` are reserved* | CSG-V16 FAIL |
| 2 | a sentence saying *a reader who is signing in* is appended to the guide | CSG-V16 FAIL |

Control 1 is the draft that actually happened, kept here because it is the
evidence that the guide is inside the scanned surface. Control 2 proves the
widened alternation, and was red only after `signing` was added.

## Checks

```
bash scripts/verify-console-session-grants.sh   Summary: 16 passed, 0 failed
bash scripts/verify-auth-onboarding.sh          Summary: 26 passed, 0 failed
bash scripts/verify-readme-quickstart.sh        PASS
bash scripts/verify-doc-links.sh                PASS
```

The full pre-PR evidence list ran before the pull request; its results are in
the CSG9 execution-log row.
