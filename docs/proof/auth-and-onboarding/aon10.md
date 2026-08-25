# AON10 Console separation

Date: 2026-08-25
Plan: [Auth and Onboarding](../../plans/auth-and-onboarding-plan.html)
Verifier: `scripts/verify-auth-onboarding.sh`, AON-V26

## What changed

Nine tasks moved the credential planes apart on the server. This task moves
them apart on the screen, which is where they were taught to be the same thing.

Before, a person learned the model from the console like this: the keys page
had a section that attached a provider credential to a gateway API key, and a
provider that was not working sent them to that page to fix it. Both are gone.

```text
/keys       gateway API keys: scopes, limits, the account each belongs to
/tenants    accounts: limits, credential strategy, keys held, BYOK
/providers  the two credentials an operator owns: environment, gateway
```

Each of the five words the campaign defines is now on exactly one screen, and
the screen it is on is the one whose owner can act on it.

## Whose credential is it

The provider screen and the account screen answer the same question — whose
provider account pays for this call — and they answer it for different owners.

| | Owner | Address | Screen |
| --- | --- | --- | --- |
| Environment credential | the operator's shell | the process environment | provider, read-only |
| Gateway credential | the operator | storage, scope `*` | provider, editable |
| BYOK | one account | storage, scope `tenant:<id>` | account |

The provider screen never says BYOK, and the account screen never edits the
deployment's credential. A screen that showed both answers at once is what
taught nobody the difference.

## Two panels, one field renderer

`GatewayCredentialPanel` and `ByokPanel` ask for the same thing — the
credential fields the catalog declares for that provider — and store it at
different addresses. They share `CredentialFields.tsx`, which owns the draft
type, the split between secret and configuration values, the request body, and
the rule that a submission with no secret value is refused.

That sharing is the point. Two hand-written forms over one catalog would drift:
one would learn a new field kind and the other would not, and the difference
would look like a provider that only works for one kind of owner.

Both are components rather than sections of a route. Importing a route module
to test it executes `createFileRoute`, which drags in the generated route tree
and makes a unit test depend on a build artifact. A panel that takes a
`providerId` or a `tenantId` as a prop needs neither.

## The environment panel reports one plane

`CredentialPanel` is now `EnvironmentCredentialPanel`, because that is all it
ever reported. `publishCredentialState` fills the runtime `operator_credential`
projection from `config.ProvidersConfig`, so the state it renders — ready,
not configured, invalid, unavailable — is the state of what this process read
from its own environment. A stored gateway credential never appears in it.

The old panel ended its fix-it copy with a link to `/keys`. It now ends with a
sentence pointing at the panel directly beneath it:

```text
Set GROQ_API_KEY or STARPORT_GROQ_API_KEY in the gateway environment,
or apply a gateway credential below.
```

That link is what AON-V26 forbids, and its absence is what the verifier asserts.

## The account screen

`/tenants` is the screen an operator governs from. An account owns its keys,
its spending ceiling, its rate limits, its credential strategy, and its BYOK.
The canonical `default` account is marked and cannot be deleted from the table.

The word on screen is "account" and the identifier on the wire is `tenant_id`.
The wire word is the one the gateway, the keys, the limits, and the routes
already use, so translating it in the console would cost a translation at every
seam and would leave the API and the UI describing different objects.

## Guarding the vocabulary

`console/src/lib/vocabulary.test.ts` reads the source of the keys screen and
every provider component and asserts the word BYOK appears in none of them,
and appears in `ByokPanel.tsx`.

It reads the source rather than a render on purpose. Absence is the claim, and
a render test proves only that the word was missing from whatever state the
fixture happened to draw — not from the loading state, the error state, or the
branch no fixture reaches.

Fail-before, on the AON9 head:

```console
$ git grep -c 'BYOK' 515966f -- console/src/routes/keys.tsx
515966f:console/src/routes/keys.tsx:11
```

And with a single `// BYOK` comment appended to the current `keys.tsx`:

```console
AssertionError: expected true to be false // Object.is equality
      Tests  1 failed (1)
```

## The open-gateway banner

When `GET /api/v1/auth/mode` reports `disabled`, every page carries a strip
that says so and offers the way back:

```text
Authentication is off. This gateway answers every request that reaches
it, with no API key.                                    [Require a key]
```

Its height is stated once, as `BANNER_HEIGHT`, and published to the routes as a
`--app-banner` custom property. The chat route is the one route that sizes a
column against the viewport; it reads the property rather than assuming the
whole height, so the strip pushes it instead of overflowing it.

## Deviation from step 5

Step 5 asked the provider screen to show which credential source served a
request, so an operator could see an account drawing on the deployment's
credential. The console cannot show it, because nothing records it:

```console
$ grep -rn 'CredentialSource' internal/usage internal/proxy internal/execution
$
```

`internal/router/credential_policy.go` decides the source as
`credentialSelection{material, source}`. `bindSelectedEndpoint` consumes the
material and the source is dropped on the floor. The runtime
`operator_credential` projection is not a substitute — as above, it reports the
environment plane only.

Showing a served source honestly means carrying `keyring.CredentialSource`
through the attempt into `usage.Record` and back out through the activity API.
That is a server-side change this task's acceptance criteria do not cover and
its console verification could not prove, so it became AON14, placed before the
documentation task so that task describes a finished screen.

## Evidence

```console
$ bash scripts/verify-auth-onboarding.sh
PASS AON-V26 no provider screen links to /keys for credentials
Summary: 26 passed, 0 failed

$ bash scripts/verify-console-modernization.sh
Summary: 21 passed, 0 failed

$ cd console && npx tsc --noEmit
$ npx vitest run
 Test Files  15 passed (15)
      Tests  85 passed (85)

$ npm run build
✓ built
```

Fail-before for AON-V26, on the AON9 head:

```console
$ git grep -n 'to="/keys"' 515966f -- console/src/components/providers
515966f:console/src/components/providers/ProviderDetail.tsx:156:            to="/keys"
```

New tests: `GatewayCredentialPanel.test.tsx` (4), `ByokPanel.test.tsx` (3),
`vocabulary.test.ts` (1). Rewritten: three `ProviderDetail.test.tsx` cases that
asserted the old `/keys` fix-it path now assert its absence.
