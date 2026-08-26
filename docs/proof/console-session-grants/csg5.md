# CSG5 — Reaching first contact

## Problem

`console/src/routes/__root.tsx` wrapped every route in `<Shell>`. A reader with
no session got navigation to pages that all answer 401, and every panel behind
that navigation issued a request the gateway refused. The reader learned they
were signed out from a wall of failures rather than from a page that told them.

## Change

The root route guards in `beforeLoad` and redirects a credential-less browser to
`/auth`. `console/src/routes/auth.tsx` (new) renders outside the shell;
`RootLayout` wraps every other route in it as before.

`console/src/lib/api.ts` gained `openSession`, which posts to
`/console/session` — the route CSG4 added.

## Why `beforeLoad` and not a render-time redirect

The acceptance is *no authenticated fetch*, and that is a question about
ordering. A redirect issued from a render pass happens after the queries in that
pass have already gone out; the gateway answers a burst of 401s to a reader who
was on the way to being told how to sign in. `beforeLoad` runs before any
component mounts, so nothing fetches.

Putting it on the root rather than on each route means a route cannot be added
without it.

## Why `openSession` does not go through `request`

`request` attaches whatever credential this browser already holds. The whole
point of this call is that it holds none, and a console that sent a stored
gateway API key alongside would be presenting one credential to ask for another.
`openSession` is the one call in that file addressing a gateway path directly.

Nothing is stored here either. The session arrives as an HttpOnly cookie the
browser attaches on its own, so a token that opens a session never reaches
localStorage.

## Why `next` is restricted to a path on this origin

`next` arrives in a URL, so it is whatever the last link said. A console that
honoured a full URL would be a redirector: follow a link, sign in to your own
gateway, and land on somebody else's page having just proved you trust this one.
A leading `//` is rejected for the same reason — browsers read it as a host —
and `/auth` itself is rejected because it is a loop rather than a destination.
Every rejected form falls back to the overview rather than failing.

## Why the page is complete rather than a placeholder

CSG5 and CSG7 merge as separate pull requests. A placeholder would leave `main`
with a route that redirects readers to a page which cannot sign them in. CSG7
adds the copyable command blocks and the `Local-only · 127.0.0.1` trust readout;
what is here already works.

## The CI gap this uncovered

`pnpm -C console check` was `pnpm build && pnpm typecheck`. It never ran
`pnpm test`, so seventeen test files and a hundred assertions have never run in
CI — including, until this change, the router test below. `check` now ends in
`pnpm test`, and the workflow already calls `check`.

## Tests

`console/src/routes/firstContact.test.tsx`, seven cases. They mount the
*generated* route tree rather than a hand-built one: the acceptance is about
every route, and a hand-built tree would only hold it for the routes the test
remembered to add.

| Test | Holds |
| --- | --- |
| `a browser with no credential meets the sign-in page and asks the gateway for nothing` | The acceptance: first contact renders and `fetch` is never called. |
| `the sign-in page remembers the route that was asked for` | A deep link survives signing in. |
| `a browser holding a session gets the console, not the sign-in page` | Asserts the shell's own navigation landmark, so it cannot pass on an error boundary. |
| `the sign-in page bounces a reader who already has a session` | A stale bookmark does not show a form for a session already held. |
| `pasting a token posts it to the session route and sends no bearer key` | Token in the body, trimmed, with no `Authorization` header. |
| `a refused paste shows the gateway's own message and stays put` | The route's "which credential this field wants" message reaches the reader. |
| `only a path on this gateway is honoured as a destination` | The open-redirect guard, including `//`, `javascript:`, and the `/auth` loop. |

The signed-in cases stub `matchMedia`, which jsdom lacks. Without it the shell
throws and those tests would pass on an error boundary — proving nothing.

## Fail-before

The plan's named control is the first. All three restored immediately.

1. **The CSG4 tree** — `/auth` does not exist, and there is no guard.
2. **Remove the root guard:**

   ```
   FAIL > a browser with no credential meets the sign-in page and asks the gateway for nothing
   TestingLibraryElementError: Unable to find role="heading" and name `/sign in to this gateway/i`
   FAIL > the sign-in page remembers the route that was asked for
   ```

   Both time out after ~1s waiting for a page the shell has replaced.

3. **Move the guard into `RootLayout`'s render path** — the same two tests fail
   the same way. A redirect thrown during render never produces the page at all,
   which is the stronger form of the ordering argument above.
4. **Drop the same-origin check from `destination`:**

   ```
   AssertionError: expected 'https://example.com/steal' to be '/'
   ```

## Checks

| Check | Result |
| --- | --- |
| `bash scripts/verify-console-session-grants.sh` | `Summary: 12 passed, 4 failed` — matches the CSG5 target |
| `pnpm -C console check` (build, typecheck, test) | 17 files, 100 tests passed |
| `go test ./...` | all packages ok |
| `go vet ./...` | clean |
| `bash scripts/verify-auth-onboarding.sh` | 26 passed, 0 failed |
| `bash scripts/verify-developer-experience.sh` | 47 passed, 0 failed |
| `bash scripts/verify-package-layout.sh` | passed |
| `bash scripts/verify-dependency-direction.sh` | 6 passed, 0 failed |
| `bash scripts/verify-v1-architecture.sh` | 12 passed, 0 failed |
| `bash scripts/verify-doc-links.sh` | passed |
