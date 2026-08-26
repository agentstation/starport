# CSG3 — The identity grant, inert

## Problem

An enterprise deployment will need a person's identity, not a claim about
reach. If that path is a future refactor, the two machine-local grants grow
into it by accident and take the word *sign in* with them — which is the
confusion the whole campaign exists to remove.

## Change

`internal/localauth/grant_identity.go` (new) registers `identity`. It holds an
optional `IdentityProvider` and refuses with
`ErrIdentityProviderNotConfigured` when none is set. `NewGate` registers it
with no provider, and nothing in this repository supplies one.

`Session` gained a `Subject` field, signed into the cookie as `"s"` and
omitted entirely for the machine-local grants. The subject and the grant must
agree in both directions, enforced twice:

| Where | Rule |
| --- | --- |
| `IssueSession` | Refuses `GrantIdentity` outright — it has no subject to give one. |
| `issueIdentitySession` (unexported) | Refuses an empty subject. |
| `VerifySession` | Refuses a cookie where `(grant == identity) != (subject != "")`. |

No provider, no config key, and no HTTP route ship with this change.

## Why the error is not `ErrGrantUnknown`

They say different things to whoever reads them. `ErrGrantUnknown` says this
gateway has never heard of identity sign-in. `ErrIdentityProviderNotConfigured`
says the seam is here and this deployment has not filled it — which is an
operator's answer to give, and the reason the grant is registered rather than
absent. `TestTheShippedGatewayConfiguresNoIdentityProvider` asserts the
positive and explicitly asserts `NotErrorIs(err, ErrGrantUnknown)`, so the two
cannot quietly collapse into one.

## Why a stub provider, and why the subject field now

The plan called for an inert grant. An inert grant whose only tested state is
*refuses* is a seam nobody has checked can be filled — the shape of the thing
is asserted by a comment rather than by a test, and the first real provider
discovers what the contract actually was.

So the wired path is proved with a stub: a provider that returns a subject, a
session that carries it, and a signed round trip that still has it on the other
side. That is not shipping a provider — no config reads one, no route reaches
one — it is proving the slot has the shape the next change will need.

The `Subject` field is the part of that which had to land in `Session`. The
existing `Session` doc already anticipated it: *"An identity grant would be the
one that changes that."* This is that change.

## Why the pairing is an invariant rather than a convention

Once both ideas live in one signed cookie, nothing structural stops a
machine-local session from carrying a subject. That would let a grant which
only proves *reach* assert *identity* — precisely the conflation this campaign
exists to prevent, now expressible in a value the gateway trusts.

The exported `IssueSession` cannot mint an identity session at all, so the
pairing is structural at the entry point rather than a rule every future caller
has to remember. `VerifySession` re-checks it on the way back in, because a
cookie is a value that outlives the code that made it. The refusal does not
echo the subject: it identifies a person, and refusals get logged.

## What a provider must supply

Documented on `IdentityProvider`: one method, and a stable subject — the
identifier that will be the same person on the next sign-in. It reaches
`Session.Subject` and is signed into the cookie, so it must be an identifier a
deployment is willing to have in a browser and in a log: a provider's subject
claim rather than a name or an address.

A provider does not decide the session. Lifetime, cookie shape, and refusal
handling stay in this package for every grant, so an identity session cannot
quietly outlive a machine-local one.

The identity grant is deliberately *not* throttled and *not* host-checked. Both
of those controls exist in the local-token grant because it reads a secret this
gateway printed; a provider owns how many times a person may get their own
credential wrong, and from where.

## Tests

`internal/localauth/grant_identity_test.go`, five cases:

| Test | Holds |
| --- | --- |
| `TestTheShippedGatewayConfiguresNoIdentityProvider` | Registered, and refuses with the named error rather than `ErrGrantUnknown`. |
| `TestAProviderVouchedSessionCarriesItsSubject` | The filled seam works: the subject survives the signed round trip. |
| `TestAProviderThatNamesNobodyIsRefused` | An empty or whitespace subject is a broken provider, not an anonymous success. |
| `TestAProviderRefusalReachesTheCaller` | A provider's own error is wrapped, not translated into one of ours. |
| `TestTheSubjectAndTheGrantMustAgree` | Three correctly signed forgeries refused, plus the entry-point half. |

## Fail-before

Three controls. The first is the one the plan named.

1. **Look the grant up on the CSG2 tree** — `Gate.Grant(GrantIdentity)` returns
   `ErrGrantUnknown`; the file does not exist.
2. **Remove the identity registration from `NewGate`:**

   ```
   --- FAIL: TestTheShippedGatewayConfiguresNoIdentityProvider (0.00s)
       Error: Received unexpected error:
   ```

3. **Remove the pairing check from `VerifySession`:**

   ```
   --- FAIL: TestTheSubjectAndTheGrantMustAgree/a_ticket_that_claims_a_person
       Error: Expected error with "the console session is not a session record"
              in chain but got nil.
   --- FAIL: TestTheSubjectAndTheGrantMustAgree/a_pasted_token_that_claims_a_person
       Error: (same)
   ```

Both restored immediately after.

## Checks

| Check | Result |
| --- | --- |
| `bash scripts/verify-console-session-grants.sh` | `Summary: 9 passed, 7 failed` — matches the CSG3 target |
| `go test ./... ` | all packages ok |
| `go test ./internal/localauth/ -race -count=2` | ok |
| `go vet ./...` | clean |
| `make lint` | 0 issues |
| `bash scripts/verify-auth-onboarding.sh` | 26 passed, 0 failed |
| `bash scripts/verify-package-layout.sh` | passed |
| `bash scripts/verify-dependency-direction.sh` | 6 passed, 0 failed |
| `bash scripts/verify-v1-architecture.sh` | 12 passed, 0 failed |
