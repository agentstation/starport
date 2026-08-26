# CSG0 — Baseline

Baseline commit: `20dd9b1` on `main`.

## Fail-before

```
$ bash scripts/verify-console-session-grants.sh
FAIL CSG-V01 internal/localauth owns the grant seam
FAIL CSG-V02 the three grant kinds are named in one place
FAIL CSG-V03 a session records the grant that minted it
FAIL CSG-V04 ticket redemption goes through the registered grant
FAIL CSG-V05 the local admin token is a registered grant
FAIL CSG-V06 the paste path compares in constant time via the token
FAIL CSG-V07 the paste path obeys the token exposure rule
FAIL CSG-V08 the identity grant is registered
FAIL CSG-V09 the inert identity grant refusal is held by a contract test
FAIL CSG-V10 the console session route accepts the local token grant
FAIL CSG-V11 the route is registered
FAIL CSG-V12 the console has an auth route outside the shell
FAIL CSG-V13 starport auth can copy the token and open the console URL
FAIL CSG-V14 the page states its trust scope in the page
FAIL CSG-V15 the gateway key card no longer stands in for first contact
FAIL CSG-V16 sign-in copy belongs to the identity grant alone
Summary: 0 passed, 16 failed
exit=1
```

Two conditions are red because the baseline holds the behavior they forbid,
not because a file is missing:

- **CSG-V15** — `console/src/components/overview/ConnectCard.tsx` exists and
  is the first-contact surface.
- **CSG-V16** — `internal/cli/development.go:88` prints
  `Console (one-time sign-in link): %s`, spending the reserved words on a
  launch ticket.

## Baseline behavior

Against the live dev gateway at `127.0.0.1:8080`, built from `20dd9b1`:

| Observation | Result |
| --- | --- |
| `GET /health/ready` | `200` |
| `starport auth token` | prints `starport_local_…` |
| `POST /console/session` | `404` — no route accepts that token |
| `GET /` with no session cookie | serves the console, which renders `ConnectCard` asking for a `STARPORT_…` gateway API key |

The asymmetry this campaign closes: the CLI prints one credential, the console
asks for a different one, and nothing accepts the first.

`ConnectCard` renders from ten routes: `index`, `keys`, `tenants`, `models`,
`presets`, `usage`, `authors`, `authors_.$authorId`, `providers`, and its own
module.

## Source facts pinned at baseline

- `internal/localauth/gate.go` — `Redeem` is the only caller of
  `IssueSession`. No path accepts a pasted token.
- `internal/localauth/token.go` — `Token.Authorizes` already compares with
  `subtle.ConstantTimeCompare`. `AllowsExposure(bindHost, token)` already
  holds the loopback rule the paste path must reuse.
- `internal/localauth/session.go` — `Session` carries `IssuedAt` and
  `ExpiresAt` only. It records nothing about how it was minted.
- `console/src/routes/__root.tsx` — wraps every route in `<Shell>`.
- `internal/cli/auth.go` — `token`, `url`, `status`, `rotate`. No `--copy`,
  no `--open`.

## Checks

| Check | Result |
| --- | --- |
| `bash scripts/verify-console-session-grants.sh` | 0 passed, 16 failed, exit 1 (intended) |
| `bash scripts/verify-doc-links.sh` | PASS |
| `bash scripts/verify-auth-onboarding.sh` | 26 passed, 0 failed |

No Go or console source changed by this task.
