# Starport Security Posture

Last updated: 2026-08-30

This document answers the questions a security review asks. It covers
credential storage, encryption at rest, authentication, the audit
record, and what leaves the process. Each claim names the test or the
verification gate that proves it. The gates live in `scripts/` and run
in CI.

This document describes the deployed posture. Vulnerability disclosure
lives in [SECURITY.md](../SECURITY.md). The operational procedures live
in the [operator guide](OPERATOR-GUIDE.md).

## Credential model

Starport separates three credential ideas. A gateway API key
authenticates a caller. A provider credential pays a provider. A local
admin token admits the operator's own browser. The
`verify-auth-onboarding.sh` gate holds the separation across 26
conditions, including AON-V08: no controller derives a credential scope
from an API key ID.

### Gateway API keys

- Starport mints a key once with the `STARPORT` prefix and 42
  characters of Crockford base32 entropy from `crypto/rand`
  (`internal/apikey/issuer.go`).
- Starport stores only the SHA-256 hash of the secret. The plaintext
  appears once, in the response that answers the mint request.
  Evidence: `TestIssuerStoresOnlyCredentialHash` in `internal/apikey`.
- Authentication re-hashes the presented bearer and looks the hash up.
  Query-string credentials are not accepted. Evidence:
  `TestAuthMiddleware_RequireAPIKey` and `TestExtractAPIKey` in
  `internal/server`.

### Provider credentials

- Operator and account credentials rest under AES-256-GCM. The
  per-record key derives from the master key with Argon2id over a
  random 32-byte salt (`internal/credentials/encryption.go`).
  Evidence: `TestEncryptionService_EncryptDecrypt`,
  `TestArgon2Parameters`, and `TestEncryptionTampering`.
- The master key comes from `STARPORT_SECURITY_MASTER_KEY`. Startup
  refuses an empty key, and Argon2id stretches a short key.
  First-run setup writes the key to a mode `0600` config file in a
  mode `0700` directory (`internal/setup`).
- No API response returns a stored provider secret. Evidence:
  `TestACredentialResponseNeverCarriesItsSecret` in
  `internal/server/controllers` and `TestKeyLeakage` in
  `internal/providers/keyring`.
- A credential can instead reference an external secret manager.
  Supported: HashiCorp Vault, OpenBao, AWS Secrets Manager, Azure Key
  Vault, and GCP Secret Manager. Starport then holds the reference,
  not the material. Evidence: the `internal/credentials` secret-source tests,
  including `TestDirectSecretSourceErrorsDoNotExposeReferences`.

### Local admin token

- The token uses the `starport_local_` prefix, so it cannot be mistaken
  for a gateway key. It rests in one file at mode `0600`. Evidence:
  `TestTokenIsNotMistakableForAGatewayKey` and
  `TestTokenFileIsOwnerOnly` in `internal/localauth`.
- Comparison is constant time through `subtle.ConstantTimeCompare`.
  Starport refuses an unrotated token from off the machine. Evidence:
  `TestAuthorizesMatchesExactly` and
  `TestAnUnrotatedTokenIsRefusedFromOffMachine`.

## Encryption at rest

| Data | Store | At rest |
| --- | --- | --- |
| Provider credentials | KV store (`credentials:v1:`) | AES-256-GCM, Argon2id-derived key |
| Gateway API keys | KV store (`identity:v1:`) | SHA-256 hash only |
| Local admin token | one owner-only file | plaintext at mode `0600` |
| Accounts, users, teams, grants, audit log | relational store | plain rows that never hold a credential value |

The relational store defaults to embedded SQLite, and one contract
suite holds the Postgres and MySQL peers. Evidence:
`TestDialectMigrationSetsAgree` in `internal/sqlstore` and the
`verify-credential-sharing.sh` gate.

## Authentication modes

- Starport requires authentication by default.
  `STARPORT_SECURITY_AUTH_MODE` and the console can disable it, and
  the server reads the policy per request.
  Evidence: `TestAuthModeDefaultsToRequired` and
  `TestPolicyIsReadPerRequest`.
- The exposure tripwire refuses disabled authentication on a
  non-loopback bind unless the operator sets
  `STARPORT_SECURITY_ALLOW_REMOTE_NO_AUTH=true`. Evidence:
  `TestAuthenticationExposureTripwire` in `internal/config`.
- Disabled authentication meters an anonymous key whose scopes never
  include `admin`. Evidence: `TestAnonymousScopesExcludeAdmin` and
  `TestDisabledAuthenticationKeepsTheAdminPlaneClosed`.
- Admin is a scope on a gateway key, not a separate credential.
  The `RequireAdmin` middleware guards the admin plane, and account
  access needs the caller's own account or `admin`.
- Console sessions are HMAC-SHA256 signed cookies with a 12 hour TTL,
  `HttpOnly`, and `SameSite=Lax`. Rotating the local token invalidates
  every outstanding session and launch ticket at once. Evidence:
  `TestRotatingTheTokenInvalidatesEverySession` and
  `TestSessionCookiesKeepTheSecretAwayFromScripts`.
- The `verify-console-session-grants.sh` gate holds the session seam
  across 16 conditions, including the constant-time compare and the
  exposure rule.

### SSO and identity

- Google and GitHub OAuth sign-in run through goth. Enterprise SSO runs
  through WorkOS. Both are off until configured under
  `STARPORT_IDENTITY_`, and startup refuses a half-configured
  provider. Evidence: `TestOAuthSignInEndToEnd`, `TestWorkOSSSOEndToEnd`,
  and `TestAnUnconfiguredGatewayRefusesIdentitySignIn`.
- An identity session reaches only the accounts its grants name.
  Evidence: `TestReachableAccountsFoldGrants` in `internal/identity`.

## Audit surface

- Every admin mutation that reaches the store writes one
  actor-attributed record: time, actor, action, subject, and outcome.
  A record never holds a credential value. Evidence:
  `TestMutationHandlersWriteTheAuditTrail` and
  `TestAuditActorNamesEachCaller` in `internal/server/controllers`.
- The trail rests in the relational `audit_log` table and prunes past
  `STARPORT_AUDIT_RETENTION`, default 400 days. Evidence:
  `TestRepositoryPrunesPastTheRetentionWindow` in `internal/audit`.
- `GET /api/v1/admin/audit` serves the trail under the `admin` scope,
  and the console renders it at `/audit`.

## Data flows

Nothing leaves the process unless the operator configures a
destination. Starport sends no telemetry and phones no home. The
Starmap catalog supplies every provider endpoint, and the
`verify-starmap-ownership.sh` gate refuses a hardcoded provider URL.

The outbound surfaces are:

1. Provider inference APIs, addressed by the catalog and paid with the
   resolved credential.
2. Webhooks, which stay off until the operator sets
   `STARPORT_EVENTS_WEBHOOK_URLS` and
   `STARPORT_EVENTS_WEBHOOK_SECRET`. HMAC-SHA256 signs each delivery
   under `X-Starport-Signature`. A payload carries
   identifiers and states only, never key material or prompt content.
   Evidence: `TestDispatcherDeliversSignedEvents` and
   `TestKeyLifecycleEmitsNamedEvents`.
3. Provider status-page polling, only for providers whose catalog
   record declares a health URL.
4. A remote Starmap catalog, usage export sink, and OTLP trace
   endpoint, each empty by default.
5. Identity providers and external secret managers, only when
   configured.

Inbound, `GET /metrics` serves Prometheus text whose labels stop at
provider, model, protocol, operation, and outcome. No label carries a
caller identity. `STARPORT_TELEMETRY_METRICS` can restrict the route to
`admin` or turn it off.

`starport config` and `starport doctor` redact every field tagged
secret. Evidence: `TestRedactedNeverReturnsSecrets` and
`TestConfigurationSecretFieldsDeclareRedaction` in `internal/config`.

## Budgets and rate limits

- Spend and token budgets bind to an account or a key over fixed UTC
  windows. The tighter holder refuses first with HTTP 402.
  Evidence: `TestSpendBudgetExhaustionReturns402` and
  `TestRequestRulesKeepBothMetersAndOrderAccountFirst`.
- A budget storage error fails open: availability wins over
  enforcement, and the refusal never invents a balance. Evidence:
  `TestBudgetStorageErrorFailsOpen`.
- Global rate limiting is on by default at 60 requests per minute per
  key (`STARPORT_RATE_LIMITING_DEFAULT_REQUESTS_PER_MINUTE`).

## Deployment hardening checklist

- Keep the default bind of `127.0.0.1` until the gateway needs remote
  callers, then front it with TLS.
- Set `STARPORT_SECURITY_MASTER_KEY` to 32 or more random bytes and
  store it in a secret manager, not in shell history.
- Enable TLS with `STARPORT_SECURITY_ENABLE_TLS` and the certificate
  and key paths, or terminate TLS at a trusted proxy.
- Run `starport auth rotate` before exposing the console on a
  reachable address. Rotation invalidates every session and ticket.
- Leave authentication required. If a lab needs `--no-auth`, keep the
  bind on loopback so the tripwire holds.
- Keep CORS off (`STARPORT_SECURITY_ENABLE_CORS=false`) unless a
  browser origin needs the API, then name the origins.
- Bound request bodies and timeouts with the `STARPORT_SERVER_*`
  limits. The defaults cap bodies at 10 MiB.
- Restrict `/metrics` with `STARPORT_TELEMETRY_METRICS=admin` when the
  scrape network is not trusted.
- Set `STARPORT_AUDIT_RETENTION` to the window your compliance regime
  requires, and export usage if you need longer evidence.
- Keep GitHub Actions supply-chain pins intact. The
  `verify-action-pins.sh` gate refuses an unpinned action.

## Verification gates

| Gate | Holds |
| --- | --- |
| `verify-auth-onboarding.sh` | credential-idea separation, required-by-default auth, the exposure tripwire |
| `verify-console-session-grants.sh` | one session seam, constant-time token compare, the exposure rule |
| `verify-credential-sharing.sh` | the relational contract, BYOK policy, grant-scoped shared credentials |
| `verify-enterprise-readiness.sh` | the audit trail, signed webhooks, and this document's link from the README |
| `verify-doc-links.sh` | every link in this document resolves |
| `verify-action-pins.sh` | pinned GitHub Actions |
| `verify-dependency-direction.sh` | package dependency direction around the credential seams |

Run any gate with `bash scripts/<gate>.sh` from the repository root.
