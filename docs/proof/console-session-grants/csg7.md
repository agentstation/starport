# CSG7 — The first-contact page

## Problem

A browser with no usable credential reached one of two things, and neither one
answered the question it had.

Before CSG5 it reached `ConnectCard`: a card inside the console shell, sitting
under navigation to pages it could not read, asking for a gateway API key and
mentioning `starport ui` in prose. A reader learned they were locked out from a
wall of failing panels rather than from a page that told them.

CSG5 replaced the destination with a page outside the shell, which fixed the
wall of failures but left the page thin. It asked for the local admin token and
said nothing about what the token is, what this address can reach, or what to do
if the browser is not on the machine running the gateway. `ConnectCard` was
still mounted on nine routes for the case the new page did not cover.

## Change

`console/src/components/auth/` now owns first contact as a concept rather than
as a route body.

| File | Owns |
| --- | --- |
| `destination.ts` | `AUTH_PATH` and the `next` allowlist, moved out of the routes layer so components can import them |
| `trust.ts` | `trustScope(hostname, secure)` — what the page can honestly say about the address it was served on |
| `CommandBlock.tsx` | A command the reader runs elsewhere, with a copy control that renders only where a clipboard exists |
| `FirstContact.tsx` | The page |

`routes/auth.tsx` is now the route shell: search validation, the bounce guard,
and the component. `routes/__root.tsx` imports `AUTH_PATH` from the components
module and gained the rejection redirect described below.
`components/overview/ConnectCard.tsx` is deleted along with all eighteen
references across nine route files.

### What the page says

The heading is **Open this console**, not *Sign in*. That phrase is reserved
for the identity grant (CSG8), and spending it here on a machine-local token
would be the second time this campaign asked a reader to sign in to something
that does not know who they are.

The copy carries the thesis instead: *this gateway does not know who you are and
does not need to. The token below is a claim about where you are.* Under it, a
derived trust readout, the token field, the two commands that produce a token on
the machine running the gateway, and — behind a disclosure — the gateway API key
path for a browser that is somewhere else.

### Why the trust readout is derived

A fixed "Local-only · 127.0.0.1" is a claim the page cannot check, and it would
be wrong in the one case where being wrong costs something: a gateway bound to a
routable address, where the reader is about to paste another machine's admin
token over a hop they do not control.

`trustScope` reads the location and reports one of three things:

| Address | Label |
| --- | --- |
| `localhost`, `127.0.0.1`, `::1`, `[::1]` | `Local-only · <host>` |
| anything else, over HTTPS | `Network · <host>` |
| anything else, in the clear | `Network · <host> · not encrypted` |

A hostname that merely resolves to loopback is not on the list. This page cannot
resolve anything, and a readout that guessed would be asserting more than it
knows — in the direction that matters, `starport.local` reported as local is the
exact mistake.

### Why the gateway API key stays, behind a disclosure

Deleting it would break the remote case: a browser that is not on the machine
running the gateway cannot print the local admin token, so it has no other way
in. Keeping it in the same field group would undo AON's separation, which is the
point the console has been making since the five-term vocabulary shipped.

So it is present, subordinate, and labelled as the different credential it is: a
gateway API key authenticates a caller and is metered against a tenant; the
local admin token is the operator of the machine.

### Why a rejected credential needed new handling

Deleting `ConnectCard` removed the console's only handling of a credential that
stops working mid-visit, and the root `beforeLoad` guard cannot replace it. The
guard tests `hasCredential()`, which a stale session still satisfies — it is
present, it is just no good — and it runs before loading, which is before the
request that learns the news. It does not run again until the reader navigates.

Two changes close it:

- `RootLayout` subscribes to `useGatewayAccessRejected()` and renders
  `<Navigate to={AUTH_PATH} search={{ next: location.href }} replace />` when it
  flips. A render-pass redirect is wrong in `beforeLoad`'s position and right in
  this one: the 401 it reacts to has already happened, so there is no burst of
  requests left to prevent.
- `routes/auth.tsx` bounces only when `hasCredential() && !isCredentialRejected()`.
  Bouncing on presence alone would send a reader with a dead session straight
  back to the page that redirected them here, and round again.

The page then shows why, rather than presenting a bare form to somebody who
believed they were already in.

## Verifier conditions

```
PASS CSG-V14 the page states its trust scope in the page
PASS CSG-V15 the gateway key card no longer stands in for first contact
Summary: 15 passed, 1 failed
```

The remaining failure is `CSG-V16`, which belongs to CSG8.

## Tests

| Test | Holds |
| --- | --- |
| `trust.test.ts` — a loopback address is the only thing reported as local | All four loopback spellings, and only those |
| `trust.test.ts` — a routable address says so, and names the address | The label carries the real host |
| `trust.test.ts` — a name that is not literally loopback is not treated as loopback | `starport.local`, `127.0.0.1.example.com` |
| `trust.test.ts` — an unencrypted network address is called out separately | The third label exists and is distinct |
| `destination.test.ts` — only a path on this gateway is honoured | Moved beside its owner; unchanged from CSG5 |
| `firstContact.test.tsx` — the token never reaches browser storage, and the gateway API key does | Both halves, in one test |
| `firstContact.test.tsx` — the trust readout names the address this page was served on | Rendered from `location`, not from copy |
| `firstContact.test.tsx` — a session refused mid-visit returns the reader to the first-contact page | The rejection redirect, end to end through a real 401 |

The storage test asserts both halves deliberately. The write is what makes the
no-write assertion meaningful: a storage stub that recorded nothing at all would
pass the first half for the wrong reason.

The five CSG5 route tests are unchanged apart from the heading they look for.

## Fail-before

| Control | Result |
| --- | --- |
| `FirstContact` writes the pasted token to `localStorage` after `openSession` | `× the token never reaches browser storage…` — `expected [ 'starport.localToken', …(1) ] to deeply equal [ 'starport.apiKey' ]` |
| `trustScope` decides loopback by `hostname.includes("local")` | `× a loopback address is the only thing reported as local`; `× a name that is not literally loopback is not treated as loopback` |
| `RootLayout` drops the rejection `<Navigate>` | `× a session refused mid-visit…` — `expected '/models' to be '/auth'` |

Each control produced a clean test failure rather than a build failure, and each
file was restored from a backup immediately after.

One assertion was dropped rather than made to pass: an early version checked
that the pasted token did not appear anywhere in the rendered HTML. It failed,
correctly — React serialises a controlled password field's `value` attribute.
That is the field holding what the reader typed, not a leak, and asserting
otherwise would have been asserting something untrue.

## Checks

```
npx tsc --noEmit                       clean
npx vitest run                         19 files, 107 tests, all passed
npm run build                          built in 2.13s
scripts/verify-console-session-grants  15 passed, 1 failed (CSG-V16 is CSG8)
```
