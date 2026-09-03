# Starport v1 Operator Guide

Last updated: 2026-08-11

This guide covers a single Starport process and a Valkey-backed multi-node
deployment. Starport starts when storage, Starmap, and the initial gateway
identity are usable. Provider credential state does not control gateway
readiness.

The security review answers live in the
[security posture document](SECURITY-POSTURE.md).

## Requirements

- Go 1.26.5 for a source build.
- A writable Badger path, or a reachable Valkey service.

Local initialization generates the credential-encryption master key. A
production deployment must supply a master key with at least 32 bytes through
its environment or secret manager. Do not reuse that value as a gateway API
key. Starport stores only the SHA-256 hash of each gateway key.

## Local Development

Set any conventional provider credentials that you want Starport to discover.
Then start an isolated development gateway:

```bash
export OPENAI_API_KEY="replace-with-provider-inference-key"
starport dev
```

The command binds to `127.0.0.1`, uses in-memory storage, and creates no
configuration file. It prints one temporary gateway API key and one console
launch link, and opens the console in a browser:

```text
Starport development gateway
URL: http://127.0.0.1:8080
Authentication: required
Gateway API key (shown once): replace-with-generated-gateway-key
Console (one-time launch link): http://127.0.0.1:8080/launch?lt=replace-with-ticket
```

Add `--no-open` to print the link without opening a browser. Add `--no-auth`
to serve without a gateway API key; the banner then reports
`Authentication: disabled` and prints no key.

Keep the development process open. In a second terminal, set the printed key
and check the gateway:

```bash
export STARPORT_API_KEY="replace-with-generated-gateway-key"
curl --fail http://127.0.0.1:8080/health/ready
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  http://127.0.0.1:8080/api/v1/models
```

The model response contains the active Starmap catalog view. It does not prove
that a provider accepts its resolved credential. The first inference attempt
provides that evidence.

## Authentication Mode

Starport requires a gateway API key on every inference and management route by
default. An operator can turn that requirement off for a deployment where it
buys nothing: a workstation, a private container, a test rig.

Four places can state the mode. The first that states it wins:

| Source | How | Survives a restart |
| --- | --- | --- |
| `flag` | `--no-auth` on `starport serve` or `starport dev` | no |
| `config` | `STARPORT_SECURITY_AUTH_MODE=disabled` | yes |
| `console` | the switch under Settings | yes |
| `default` | nothing stated | `required` |

A flag or a configuration value fixes the mode for the process. The console
switch is refused while either is set, and the refusal names the value to
change, so an operator is never told only that they may not.

```bash
curl --fail-with-body http://127.0.0.1:8080/api/v1/auth/mode
```

That route needs no credential in either mode. It reports the mode, which of
the four sources stated it, whether this caller may change it, and why not
when they may not.

### The exposure tripwire

Starport refuses to start with authentication disabled on an address the
network can reach. Loopback is exempt: a caller who is already on the machine
gains nothing from a key. To run open on a reachable address, state it:

```bash
starport serve --no-auth --allow-remote-no-auth
```

A stored `disabled` mode is re-checked against the address this process binds.
A data directory carried from a laptop to a public address does not carry an
open gateway with it — the gateway falls back to `required` and warns, naming
the bind host.

The console switch enforces the same rule from the other side: it can be used
only from the machine running the gateway. An open gateway can always be closed
from that machine, and cannot be opened further from anywhere else.

### The local admin token

Issuing a gateway API key is itself an admin act, so a freshly installed
gateway would have no first move. Starport breaks that circle with a token the
machine gives itself. It is not a gateway API key:

| | Gateway API key | Local admin token |
| --- | --- | --- |
| Belongs to | an account | nobody |
| Proves | who you are | that you are on this machine |
| Lives in | encrypted storage | one file, mode 0600 |
| Prefix | `STARPORT_` | `starport_local_` |
| Issued by | an admin act | the machine, on first start |
| Revoked by | deleting the key | rotating the file |

```bash
starport auth status   # generation, age, and the exposure answer
starport auth token    # print this machine's token
starport auth url      # a one-time console launch link
starport ui            # mint that link and open it
```

`starport ui` reads the token file rather than calling the gateway, so it
produces a launch link whether the gateway is up, down, wedged, or refusing
the operator. That is the case an operator reaches for it in.

### Opening the console

A console session is one thing: a signed, HttpOnly cookie the gateway issues
and the browser cannot read. What differs is the grant that mints it. Three are
registered, and the console session route accepts them by name:

| Grant | Presents | Answers | Ships |
| --- | --- | --- | --- |
| `ticket` | a one-time launch ticket in the URL | where you are | yes |
| `local-token` | the local admin token, pasted | where you are | yes |
| `identity` | an identity provider assertion | who you are | inert until configured |

The first two are machine-local by construction. A launch ticket is minted from
the token file by `starport ui` or printed at start, and the paste path compares
the same token in constant time. Neither names a person, which is why neither
borrows the vocabulary of identity. A browser that reaches the console with no
session lands on a first-contact page that states whether the address it was
served on is loopback, takes the token, and prints the two commands that avoid
the paste:

```bash
starport auth token --copy   # the token, on this machine's clipboard
starport auth url --open     # a launch link, opened here
```

The third grant ships inert: with nothing configured it refuses every request
with `ErrIdentityProviderNotConfigured`, and the routes answer that this
deployment configured no identity provider. An operator fills the slot with
the `STARPORT_IDENTITY_OAUTH_*` settings below. It is also the only grant
allowed to describe itself in the vocabulary of identity; no machine-local
surface uses those words, and `scripts/verify-console-session-grants.sh`
enforces that.

For a deployment where the operator is not at the machine, the console takes a
gateway API key instead. That is a different credential with different
consequences: it authenticates a caller and is metered against an account, where
the token above is the operator of the machine.

### Identity providers

An enterprise deployment lets people authenticate through an identity
provider the operator registers with that provider. There are two
acquisition paths — per-provider OAuth applications, and enterprise SSO
through WorkOS — and either or both may be configured:

```bash
# Where a provider sends the browser back, shared by every path. Defaults
# to the gateway's own bind address, which is right for local use; a
# deployment behind a proxy or a domain sets the address the browser
# actually reaches.
STARPORT_IDENTITY_CALLBACK_BASE_URL=https://gateway.example.com

# OAuth applications, one per provider.
STARPORT_IDENTITY_OAUTH_GOOGLE_CLIENT_ID=…
STARPORT_IDENTITY_OAUTH_GOOGLE_CLIENT_SECRET=…

STARPORT_IDENTITY_OAUTH_GITHUB_CLIENT_ID=…
STARPORT_IDENTITY_OAUTH_GITHUB_CLIENT_SECRET=…

# Enterprise SSO through WorkOS. The key and client come from the WorkOS
# dashboard; the organization (or a connection) names which enterprise
# directory people arrive from, and at least one of the two is required.
STARPORT_IDENTITY_WORKOS_API_KEY=…
STARPORT_IDENTITY_WORKOS_CLIENT_ID=…
STARPORT_IDENTITY_WORKOS_ORGANIZATION=org_…
#STARPORT_IDENTITY_WORKOS_CONNECTION=conn_…
```

Register the callback address with the provider as
`<base>/console/identity/<provider>/callback` — for WorkOS, add
`<base>/console/identity/workos/callback` as a redirect URI in its
dashboard. A configured provider appears
on the first-contact page as a choice; choosing it runs the provider's
consent flow and comes back with the same session cookie every other grant
mints, recording the provider-issued subject it was minted for. The person's
record — subject, email, display name — is upserted in the identity store on
each pass, so a returning person is the same user.

The identity records live in the relational store — the embedded SQLite by
default, or the configured Postgres or MySQL. A half-configured path (an ID
without its secret, a WorkOS key without its client, or WorkOS with no
organization or connection) is refused at start rather than silently
skipped. A WorkOS arrival resolves to the same user shape an OAuth arrival
does; the paths differ only in who vouches.

The identity routes stay mounted on every deployment.
`GET /console/identity/providers` answers with the configured list — empty
when there is none — and the other identity routes answer 503 naming these
settings, so a reader learns the deployment has no identity provider rather
than guessing at an absent feature.

### Rotation

```bash
starport auth rotate
```

Rotation replaces the secret and increases the generation. Every key derived
from the token changes with it, so every outstanding launch link and every
live console session stops verifying at once. There is no session list to walk
and nothing to clear.

Rotate before you expose a gateway on a reachable address. The token printed
at first start has been in a terminal, and a terminal is scrollback, a tmux
buffer, a screen share, and a CI log. `starport auth status` reports whether
this machine's token has ever been rotated.

An unreadable token file is refused on every read path rather than replaced,
because minting over it would discard a secret the operator may still hold.
`starport auth rotate` is the deliberate exception: it repairs the file.

## Initialize Persistent State

For a persistent single-process installation, run local initialization once:

```bash
starport init --name primary-admin
```

Initialization writes the platform `config.env` file with mode `0600`. It
also creates one named identity in the platform Badger directory. The command
prints the new gateway API key once. Save it in a password manager or secret
manager. The command refuses to replace existing configuration or identity
storage. If it cannot write the gateway key, it removes the new state so that
you can retry.

Provider inference credentials stay in the process environment or their
configured secret sources. Initialization does not select, copy, or persist
them. Start the gateway after you set the provider credentials:

```bash
starport serve
```

The default listener is `http://127.0.0.1:8080`. The configuration package
selects the platform configuration directory. The Badger path is
`data/badger` under that directory.

Check process health:

```bash
curl --fail http://localhost:8080/health/live
curl --fail http://localhost:8080/health/ready
```

### Initialize configured storage

Production keeps configuration in environment variables or a secret manager.
Set the storage, master-key, and provider values before the first start. Then
create the first named identity directly:

```bash
starport init --configured-storage --name primary-admin
```

This form does not write a local configuration file. It opens the configured
Badger or Valkey service and refuses any identity repository that already
contains an identity. It prints the new gateway API key once. Save the key in
a secret manager before you start the gateway.

If credential output fails, the command atomically releases the initial
identity and its setup claim. You can then run the command again.

Always check the command exit status. If a remote write has an uncertain
result, the command prints the candidate key before it reports the storage
error. Keep that key while you inspect the repository state.

## Inspect Configuration and Startup State

Show each managed path:

```bash
starport config paths
```

Show the effective configuration without secret values:

```bash
starport config show
```

Validate the same effective configuration that `starport serve` loads:

```bash
starport config validate
```

Run passive startup checks:

```bash
starport doctor
```

Passive checks load configuration and Starmap facts. They also compile the
configured adapter and catalog intersection. They do not open configured
storage or send a provider inference request. A selected cloud identity or
direct secret reference can use its authentication network during credential
resolution.

Add `--probe` to open Badger or Valkey in read-only mode. This probe verifies
the current catalog generation and gateway identity state. Diagnosis never
writes storage and never returns a configured secret.

After an unclean Badger shutdown, the probe can require writable recovery. It
marks the storage and identity checks as skipped. Start and stop
`starport serve` cleanly, and then rerun the probe.

Badger does not support read-only mode on Windows. On Windows, the probe marks
the storage and identity checks as skipped. Use `starport serve` to verify the
normal writable startup.

### Read the Running Gateway

A running gateway states what it is and how it runs at one admin route:

```bash
curl -H "Authorization: Bearer $STARPORT_ADMIN_KEY" \
  http://127.0.0.1:8080/api/v1/admin/info
```

The route needs the `admin` scope. Every value comes from the linker, the
clock, or the loaded configuration. The gateway never guesses a fact it
cannot know. Such a field reads `unavailable`.

| Field | Meaning |
| --- | --- |
| `version`, `commit`, `build_time` | The release build stamp. A binary from a plain `go build` reads `dev` in all three. |
| `started_at`, `uptime` | When this process started, and how long it has answered, to the second. |
| `go_version`, `os`, `arch` | The toolchain and platform of the binary. |
| `storage.type`, `storage.relational` | The key-value store and the relational store the process opened. |
| `files.backend` | Where stored file bytes land, or `none` when the deployment stores no files. |
| `telemetry.metrics` | Who can scrape `/metrics`: `on`, `admin`, or `off`. |
| `telemetry.traces` | The collector host, or `null` when traces stay off. The path and any credential never appear. |
| `telemetry.usage_export` | The export `kind` (`off`, `file`, or `http`) and the records the sink `dropped`. |
| `guardrails.checks` | The guardrail names configuration turned on. |
| `guardrails.pii_mode` | What a PII finding does: `redact` or `refuse`. |
| `guardrails.moderation_model` | The catalog model the moderation route calls, or empty. |
| `retention.*_seconds` | The audit, file, and job asset retention windows. Zero means no expiry. |
| `webhooks` | The webhook summary this guide describes under Webhooks. |

The route never returns a configured secret, a provider credential, or a
receiver URL with its query string.

Each command accepts `--json` for stable machine-readable output. A failed
validation or diagnostic check returns a nonzero exit status.

## Client Base URLs

Use these substitutions in existing clients:

| Client contract | Base URL | API key |
| --- | --- | --- |
| OpenAI | `http://localhost:8080/v1` | A Starport gateway key |
| OpenRouter | `http://localhost:8080/api/v1` | A Starport gateway key |

Example OpenRouter-style request:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"openrouter/auto","messages":[{"role":"user","content":"Hello"}]}' \
  http://localhost:8080/api/v1/chat/completions
```

Each route reads its own scope:

- Chat needs `chat:write` or wildcard scope.
- Model discovery needs `models:read`.
- Embeddings need `embeddings:write`, `chat:write`, or wildcard scope.
- Reranking needs `rerank:write`. The scope stands alone, because a rerank
  request reads the caller's own documents.
- Image generation and image edits need `images:write`.
- Speech, transcription, and translation need `audio:write`.
- Video jobs need `videos:write` on each of their five routes.

A deployment with authentication disabled holds all of these, so the media
routes answer an unauthenticated caller exactly as the chat route does.

## Agent Surface

The CLI answers catalog questions offline from the embedded catalog:

```bash
starport models search gpt-4o --json
starport models show openai/gpt-4o-mini --json
```

`models search` matches every query word against model IDs, names, and
authors. It answers a compact summary: ID, name, context length, and token
prices. `models show` answers the full catalog projection for one exact model
ID. The projection carries prices, capabilities, modalities, and every
routable provider offering. Both commands read no configuration and no
network.

The binary also carries an agent skill that teaches a coding agent to
install, start, query, and diagnose this gateway:

```bash
starport agent setup
```

The command installs the embedded `SKILL.md` into a shared skills root. An
explicit `--dir` wins, then `$AGENTS_HOME/skills`, then `~/.agents/skills`.
Add `--print` to write the skill to standard output instead. Run the command
again after a CLI upgrade, so the installed skill tracks the installed
commands.

## Provider Configuration

Starmap owns each provider's exact ID, credential fields, ordered conventional
environment names, authentication profiles, endpoint templates, and service
metadata. Starport evaluates every catalog provider against that contract. It
checks conventional names first. It then checks a derived
`STARPORT_<PROVIDER>_<FIELD>` name. For example, it checks `OPENAI_API_KEY`
before `STARPORT_OPENAI_API_KEY`.

Starport selects the first nonempty value in that order. If the selected value
does not satisfy the catalog field contract, resolution fails. Starport does
not continue to a later name.

`starport init` creates gateway security and identity state. It never selects
or writes provider inference credential material. Runtime credential
resolution uses the catalog contract.

Starmap catalog acquisition uses an independent credential plane. Starport
never copies an acquisition credential into an inference request.

### Automatic discovery and refresh

During startup, Starport resolves each provider from its ordered
catalog-declared inference profiles. Deployment-owned material can come from a
static environment value, an explicit secret reference, or a catalog-declared
default cloud credential chain. An authentication-free profile needs no
material. A missing or failed provider credential does not block other
providers, account BYOK, or gateway readiness.

The provider reconciler repeats this work every minute by default. Set
`STARPORT_CREDENTIAL_SOURCES_RECONCILE_INTERVAL` to another nonnegative
duration. Set it to `0s` to disable interval reconciliation. The
`STARPORT_CREDENTIAL_SOURCES_RECONCILE_TIMEOUT` value bounds one source
operation.

An administrator can inspect the secret-free state or trigger the same shared
reconciliation work:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  http://127.0.0.1:8080/api/v1/admin/providers

curl --fail-with-body \
  -X POST \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  http://127.0.0.1:8080/api/v1/admin/providers/refresh
```

The status response separates compiled adapter support, operator credential
state, and offering availability. It contains stable reason codes and no
credential material. A manual refresh returns revision numbers, whether the
runtime changed, configured provider IDs, and a failure count. Concurrent
manual and scheduled refreshes share one operation. Request cancellation
cancels that caller's wait and any source work that it owns.

Starport does not send a billable provider request during discovery. A
resolved credential becomes `ready` before a provider accepts it. The
first inference result can change credential state to authentication,
permission, quota, or billing failure. Offering failures remain separate.

A running Starport process does not receive environment changes from another
process. Restart Starport after you change an environment value. File and
remote secret sources can return new material during scheduled or manual
refreshes. The resolver keeps cache hits off the network and uses the source
lifecycle for renewal and expiry.

### Direct secret sources

For each catalog field, Starport derives a product name such as
`STARPORT_OPENAI_API_KEY`. Add `_REFERENCE` to select a direct secret source:

```bash
export STARPORT_OPENAI_API_KEY_REFERENCE='aws-secrets-manager:starport/openai#api-key'
```

This table defines the supported reference resources and source
authentication:

| Backend | Resource | Source authentication |
| --- | --- | --- |
| `gcp-secret-manager` | `projects/PROJECT/secrets/SECRET` or `projects/PROJECT/locations/LOCATION/secrets/SECRET` | Google Application Default Credentials |
| `azure-key-vault` | `https://VAULT_HOST/secrets/SECRET` | `DefaultAzureCredential` |
| `aws-secrets-manager` | A secret name or ARN | AWS default credential chain |
| `vault` | `MOUNT/PATH` for a KV v2 secret | Vault client environment |
| `openbao` | `MOUNT/PATH` for a KV v2 secret | OpenBao client environment |

The full grammar is
`backend:resource?version=VERSION#field`. The version is optional. A
`#field` suffix selects one exact top-level JSON string from Google, Azure, or
AWS. Without a field, these sources preserve the complete scalar payload.
Vault and OpenBao select one exact string field. Without `#field`, their KV v2
record must contain exactly one string value.

For Google, `VERSION` is a version number or alias. For Azure, it is the secret
version. For AWS, it is `VersionId`. Vault and OpenBao require a positive KV v2
version number.

Quote a reference that contains `#field` in a shell or environment file.

An explicit reference precedes conventional and product environment values.
It fails closed by default. To use ambient discovery only when the direct
source reports `not_configured`, set the derived fallback name to `true`:

```bash
export STARPORT_OPENAI_API_KEY_REFERENCE_FALLBACK_AMBIENT=true
```

Denied access, invalid material, source unavailability, timeout, and
cancellation never fall back. A reference contains resource identity only. It
must not contain credentials for the secret store. Starport uses the store's
default identity chain or client environment for that authentication.

Use `env:NAME` when an operator-chosen environment variable must override the
catalog's conventional ambient discovery. For example, this value makes
`TEAM_OPENAI_API_KEY` authoritative even when `OPENAI_API_KEY` is also set:

```bash
export TEAM_OPENAI_API_KEY="replace-with-provider-inference-key"
export STARPORT_OPENAI_API_KEY_REFERENCE='env:TEAM_OPENAI_API_KEY'
```

The selected explicit value must satisfy the catalog field contract. Starport
does not continue to an ambient value after an invalid explicit value. Set
`STARPORT_OPENAI_API_KEY_REFERENCE_FALLBACK_AMBIENT=true` only if an absent
`TEAM_OPENAI_API_KEY` can fall back to conventional discovery.

Use `file:/absolute/path` for a mounted or projected secret. The file must be a
nonempty regular file no larger than 1 MiB. Starport preserves every byte and
does not trim a trailing newline. The resolver detects these replacement
patterns without depending only on modification time:

- An in-place rewrite.
- An atomic file replacement or rename.
- A symbolic-link target swap.
- A Kubernetes projected-volume `..data` symbolic-link swap.
- Mounted-content replacement by a CSI driver.
- A secret-agent rerender that replaces the file.

Use a new material version for a deliberate rotation. Concurrent resolutions
share one refresh operation. A cache hit makes no file or network request.
Environment values that a wrapper injects belong to that child process. Restart
the process to receive a changed value unless the wrapper owns a supervised
restart policy.

### Secret-manager command wrappers

These wrappers can inject the catalog-declared conventional names, such as
`OPENAI_API_KEY`, without a Starport-specific integration. Authenticate and
select the wrapper project or environment first. Then use one of these verified
forms:

```bash
# Doppler
doppler run -- starport serve

# 1Password: .env.starport contains NAME=op://vault/item/field references
op run --env-file="./.env.starport" -- starport serve

# Infisical
infisical run -- starport serve
```

See the official command references for
[Doppler](https://docs.doppler.com/docs/cli),
[1Password](https://www.1password.dev/cli/reference/commands/run), and
[Infisical](https://infisical.com/docs/cli/commands/run). Apply each product's
least-privilege workload authentication and production lifecycle guidance.

Vault uses its standard client environment, such as `VAULT_ADDR`,
`VAULT_TOKEN`, and `VAULT_NAMESPACE`. OpenBao uses `BAO_ADDR`, `BAO_TOKEN`, and
`BAO_NAMESPACE`.

Starport resolves a direct reference before inference uses the material. It
caches the result in memory and refreshes it through the credential lifecycle.
A cache hit does not contact the secret store. By default, the next resolution
after five minutes refreshes direct-source material. Set
`STARPORT_CREDENTIAL_SOURCES_REMOTE_REFRESH_INTERVAL` to a different positive
duration. Starport never logs or serializes the returned material.

Vertex AI needs a project ID, one location, and Google Application Default
Credentials:

```bash
export GOOGLE_CLOUD_PROJECT="replace-with-project-id"
export GOOGLE_CLOUD_LOCATION="us-central1"
```

Azure OpenAI needs a resource base URL. Select Azure
`DefaultAzureCredential` with this value:

```bash
export AZURE_OPENAI_ENDPOINT="https://replace-with-resource.openai.azure.com"
```

Set `AZURE_OPENAI_API_KEY` to select the catalog's static API-key profile.
Without that key, Starport uses the catalog's `azure-default` profile. Starport
refreshes default bearer tokens before expiry. Request cancellation also
cancels credential acquisition.

Ollama uses the catalog default `OLLAMA_BASE_URL` value when the environment
does not set it. You do not need a provider-specific CLI flag.

Starmap owns the model catalog. Only offerings from the active Starmap
generation and configured adapters are routable. A Starmap acquisition failure
does not add a static model list.

### The three provider credential sources

A request can be paid for out of three places. Two of them are the operator's
money and one is the account's. Only the last is BYOK.

| Source | Owner | Where it lives | Applied by |
| --- | --- | --- | --- |
| `environment` | the operator | this process's environment | starting the process |
| `gateway` | the operator | encrypted storage, scope `*` | `PUT /api/v1/providers/{provider}/credentials`, or the provider screen |
| `byok` | an account | encrypted storage, scope `account:<id>` | `PUT /api/v1/accounts/{account_id}/byok/{provider}`, or the account screen |

A **gateway credential** is deployment-wide: every account a strategy permits
can spend it. It is read-only from no route — an operator applies and rotates
it over HTTP without restarting — and it is not BYOK. An **environment
credential** cannot be changed over HTTP at all, because it lives in the
process the operator started.

`GET /api/v1/providers/status` reports the environment plane only. The console
shows all three on the provider screen: what the environment holds, what the
operator applied, and which plane actually paid over the last hour.

### Choosing between them

An account's `provider_credential_strategy` decides the order. Set it on the
account, or on one gateway API key's metadata to narrow the account's:

- `operator_first`: environment, then gateway, then the account's own BYOK.
- `byok_first`: the account's own BYOK, then environment, then gateway.
- `byok_only`: the account's own BYOK alone.

The default is `operator_first`. `byok_only` is how an operator withholds the
deployment's money from an account: it reads no operator plane and its external
missing-credential error is the same whether an operator credential exists or
not. A key may narrow its account's strategy and is refused if it would widen
it, so stamping a key cannot buy back a plane the account was denied.

Starport advances to the next source only when material is not configured or
when the provider reports an authentication, permission, quota, billing, or
rate-limit failure. Timeout, cancellation, and internal failures are terminal.
Each advance uses the existing total attempt budget. It does not create a
provider-health failure or a hidden retry budget, and an account's own BYOK
failure never moves shared provider state.

Every usage record names the source that paid, as `credential_source`. That is
how an operator sees an account drawing on the deployment's credential rather
than its own.

`STARPORT_CATALOG_ACQUISITION_ENABLED` and
`STARPORT_CATALOG_ACQUISITION_INTERVAL` control Starmap provider observation.
They update catalog facts. They do not control inference credential
reconciliation.
Use `STARPORT_CATALOG_WORKSPACE_PATH` for reviewed account facts, such as
Azure deployment names and local Ollama model mappings. Those facts enter a
durable Starmap generation before Starport makes the adapter routable.

## Catalog Configuration

Starport reads one connected Starmap runtime. A deployment names one source
kind. The runtime holds a candidate, Starport validates that candidate against
its own routes, and the accepted head advances under a lease epoch. The
[deployment topologies guide](DEPLOYMENT-TOPOLOGIES.md) maps the five
topologies onto the settings below.

The five source kinds are `public`, `github`, `starmap`, `file`, and
`embedded`. A private source never falls back to the public channel. The
selected kind reaches Starmap exactly as the operator named it, and startup
fails rather than reads another source.

Catalog-acquisition credentials stay separate from inference credentials.
`STARPORT_CATALOG_SOURCE_API_KEY` speaks the Starmap protocol, and
`STARPORT_CATALOG_SOURCE_TOKEN` reads a GitHub release. Neither one pays a
provider. Configuration inspection redacts both values and the source URL.

### The catalog settings

| Name | Default | Valid values | Interactions |
| --- | --- | --- | --- |
| `STARPORT_CATALOG_SOURCE` | `public` | `public`, `github`, `starmap`, `file`, `embedded` | `starmap` and `file` each need a source URL. Another value fails startup. |
| `STARPORT_CATALOG_SOURCE_URL` | empty | Safe source endpoint, or the file identity for the `file` kind | Required for `starmap` and for `file`. A `github` source treats it as an optional API base override, and the `public` and `embedded` kinds ignore it. A non-loopback endpoint uses HTTPS with a valid certificate chain, and it holds the versioned path, normally `/api/v1`. |
| `STARPORT_CATALOG_SOURCE_API_KEY` | empty | Starmap protocol credential | Starport sends it as `X-API-Key` to a `starmap` source. It is not a provider inference credential. |
| `STARPORT_CATALOG_SOURCE_REPOSITORY` | `agentstation/starmap` | GitHub repository | Read by the `public` and `github` kinds. |
| `STARPORT_CATALOG_SOURCE_CHANNEL` | `catalog-latest` | Attested channel name | Read by the `public` and `github` kinds. |
| `STARPORT_CATALOG_SOURCE_SIGNER_WORKFLOW` | empty | Expected GitHub workflow identity | An empty value selects the publisher preset. |
| `STARPORT_CATALOG_SOURCE_TOKEN` | empty | GitHub API token | Raises the hourly ceiling from 60 for each egress address to 5,000 for each token. It also reads a private repository. |
| `STARPORT_CATALOG_SOURCE_POLL_INTERVAL` | `1h` | Nonnegative duration | Each polling hop adds one interval to the freshness age. A push hop adds none. |
| `STARPORT_CATALOG_SOURCE_STARTUP_POLICY` | `prefer_source` | `prefer_source`, `require_source` | `prefer_source` starts on the embedded baseline and adopts the source at the first successful read. `require_source` reads the source once at open and fails startup when that read fails. |
| `STARPORT_CATALOG_SOURCE_MAX_AGE` | `6h` | Nonnegative duration | The served-catalog age at which this instance counts its catalog as stale. A negative value fails startup. The runtime grades the channel `warn` above this age and `critical` above five thirds of it. The default gives `6h` warn and `10h` critical. |
| `STARPORT_CATALOG_SOURCE_MAX_HOPS` | `8` | Positive integer | Bounds the publication chain of a `starmap` source. Zero fails startup. |
| `STARPORT_CATALOG_ACQUISITION_ENABLED` | `true` | `true`, `false` | A false value stops every automatic observation, and only an admin refresh then moves the catalog. |
| `STARPORT_CATALOG_ACQUISITION_INTERVAL` | `4h` | Nonnegative duration | `0s` means one observation at startup and no repeat. It has no effect while acquisition is off. |
| `STARPORT_CATALOG_WORKSPACE_PATH` | empty | Directory path | Holds the catalog files an operator supplies. It is never the state directory. |
| `STARPORT_CATALOG_STATE_DIR` | empty | Directory path | Names the process-local runtime state root. An empty value resolves to the user state directory. Startup refuses a value equal to the workspace path. |
| `STARPORT_CATALOG_STARTUP_SPREAD` | `15m` | Nonnegative duration | Spreads the first source read of a fleet, so many instances that start together do not ask at one moment. |
| `STARPORT_CATALOG_TRANSFER_IDLE_TIMEOUT` | `2m` | Positive duration | Ends a transfer that stops making progress. It does not bound a stream subscription. |
| `STARPORT_CATALOG_TRANSFER_MAX_DURATION` | `60m` | Positive duration | Bounds one complete body transfer. Zero fails startup, because a transfer with no bound never ends. |
| `STARPORT_CATALOG_REFRESH_TIMEOUT` | `0s` | Nonnegative duration | An added cap on one refresh run. `0s` adds no cap, and the two transfer bounds alone end a run that does not progress. |

### The removed catalog settings

The local-or-remote catalog choice is gone. One connected runtime reads one
source, so the names that selected a remote publication and a separate local
refresh schedule carry no meaning. A removed name has no runtime alias.
Startup fails and names the replacement, because a silent skip would leave an
operator believing a setting still applies.

| Removed name | Replacement | Reason |
| --- | --- | --- |
| `STARPORT_CATALOG_REFRESH_ON_START` | `STARPORT_CATALOG_ACQUISITION_ENABLED` | The connected runtime always reads its source at startup. |
| `STARPORT_CATALOG_REFRESH_INTERVAL` | `STARPORT_CATALOG_ACQUISITION_INTERVAL` | Provider observation owns its own schedule. |
| `STARPORT_CATALOG_REMOTE_URL` | `STARPORT_CATALOG_SOURCE_URL` | One source setting replaces the local and remote choice. |
| `STARPORT_CATALOG_REMOTE_API_KEY` | `STARPORT_CATALOG_SOURCE_API_KEY` | One source setting replaces the local and remote choice. |
| `STARPORT_CATALOG_REMOTE_ACTIVATION_INTERVAL` | `STARPORT_CATALOG_SOURCE_POLL_INTERVAL` | The source reports a new publication instead of a poll loop. |

### The workspace and the state directory

The workspace holds catalog files an operator writes and reviews. Starmap
reads that directory as a local source. The layout is:

```text
<workspace>/
  providers.yaml
  providers/<provider-id>/models/<model>.yaml
  providers/<provider-id>/logo.svg
  authors/<author>/models/<slug>.yaml
```

`providers.yaml` names each provider. Each provider model file carries the
exact provider model ID. Each author model file carries the canonical model
identity, and a provider model file links to it with `model: author/slug`.

The state directory is a different idea. It holds the state the connected
runtime retains: the layer store, the instance identity seed, and the source
discovery record. Two processes that share a seed derive one instance identity,
and the runtime lease then fences nothing. A workspace can sit on a volume a
fleet shares, so the state directory is never the workspace path and never a
shared volume. Keep it on local disk, one directory for each process.

```bash
export STARPORT_CATALOG_WORKSPACE_PATH=/srv/starport/catalog
export STARPORT_CATALOG_STATE_DIR=/var/lib/starport/catalog-state
```

An empty `STARPORT_CATALOG_STATE_DIR` resolves to `starport/catalog` under the
user state root. That root is `XDG_STATE_HOME`, or `~/.local/state` when the
variable is empty.

### Generation procedures

Starport keeps two pointers over shared immutable generation records. The
candidate is the newest generation the runtime holds. The accepted head is the
newest generation that passed the complete Starport route, connector, and
credential transaction. A refused candidate leaves the accepted head in place,
so the routes, the connectors, and the response-cache identity do not change.

Read the accepted head and the validation state:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_ADMIN_KEY" \
  http://127.0.0.1:8080/api/v1/admin/catalog/status
```

`provenance.effective` names the accepted head this gateway routes on.
`route_validation.state` reads `none`, `pending`, `accepted`, or `refused`.
`route_validation.candidate` names the newest candidate, and
`route_validation.rejected` names the refusal.

Start a refresh. The route answers `202` with the run that carries the work,
and a second caller joins the run in flight:

```bash
curl --fail-with-body -X POST \
  -H "Authorization: Bearer $STARPORT_ADMIN_KEY" \
  http://127.0.0.1:8080/api/v1/admin/catalog/refresh
```

Read the run through the `Location` header, and cancel a run that no longer
serves the deployment:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_ADMIN_KEY" \
  http://127.0.0.1:8080/api/v1/admin/catalog/refreshes/<run_id>

curl --fail-with-body -X DELETE \
  -H "Authorization: Bearer $STARPORT_ADMIN_KEY" \
  http://127.0.0.1:8080/api/v1/admin/catalog/refreshes/<run_id>
```

To pin a generation, set `STARPORT_CATALOG_SOURCE=embedded` and restart. The
gateway then adopts no new publication. To roll back to a known generation,
set `STARPORT_CATALOG_SOURCE=file`. Point `STARPORT_CATALOG_SOURCE_URL` at the
saved payload, and restart. The accepted head is the restart bootstrap in
every case, so a gateway that cannot reach its source still routes. Both
`starport doctor` and `starport doctor --probe` read the durable accepted
generation with no source request.

### The catalog routes

| Route | Scope | Meaning |
| --- | --- | --- |
| `GET /api/v1/catalog` | `models:read` | The allowlisted reader summary. |
| `GET /api/v1/catalog/changes` | `models:read` | What the last accepted generation changed. |
| `GET /api/v1/admin/catalog/status` | `admin` | The complete operator view. |
| `POST /api/v1/admin/catalog/refresh` | `admin` | Start one refresh run. |
| `GET /api/v1/admin/catalog/refreshes/{run_id}` | `admin` | Read one run. |
| `DELETE /api/v1/admin/catalog/refreshes/{run_id}` | `admin` | Cancel one run. |

The safe summary is an allowlist and not a redaction. It carries the generation
identity, the age, the usable verdict, and the freshness grade. It also carries
the source kind, the fallback verdict, the provider and model counts, and the
next update time.
It carries no source address, no source identity, no publication chain, no
lease, no run identifier, and no failure reason. Those values reach the admin
status route alone. A gateway with no catalog answers the safe route with a
sanitized `503`.

### Freshness alert rules

The server evaluates freshness and serves the verdict. An alert reads that
verdict. It does not compute an age from a timestamp, because the server owns
the grade. The closed grade set is `current`, `warn`, `critical`, and
`unknown`. Freshness measures the propagated channel time through every hop,
and local acquisition never resets it.

| Alert | Condition | Severity | Meaning |
| --- | --- | --- | --- |
| Catalog stale | `freshness.catalog` is `warn` for 30 minutes | warning | The source is late or a hop is slow. |
| Catalog critical | `freshness.catalog` is `critical` | page | The served facts are old enough to misprice a request. |
| Channel stale | `freshness.channel` is `warn` or worse for 30 minutes | warning | The upstream publication chain is late. |
| Source check stale | `freshness.source_check` is `critical` | warning | This instance did not reach its source. |
| Fallback active | `runtime.fallback` is true 15 minutes after startup | warning | The gateway serves the embedded baseline. |
| Candidate refused | `route_validation.state` is `refused` | warning | A candidate failed Starport route validation. |
| Freshness unknown | `freshness.catalog` is `unknown` for 30 minutes | warning | The runtime reported no grade. |

Poll `GET /api/v1/admin/catalog/status` with an admin gateway key and read
`freshness.catalog`, `freshness.channel`, `freshness.source_check`,
`runtime.fallback`, and `route_validation.state`. The gateway serves no
catalog metric on the Prometheus scrape, so a JSON probe owns these rules.

Pair each rule with the operator action. A `critical` grade with
`runtime.fallback` false means the accepted head still routes and the source
is late. A `critical` grade with `runtime.fallback` true means the gateway
serves the embedded baseline, and the deployment needs its source back.

## Storage Modes

Badger is the default for one process:

```text
STARPORT_STORAGE_MODE=badger
STARPORT_STORAGE_BADGER_PATH=/absolute/path/to/starport/data/badger
```

Stop Starport before copying a Badger directory for backup or restore. Keep
the directory on persistent storage.

Use Valkey for shared state across nodes:

```text
STARPORT_STORAGE_MODE=valkey
STARPORT_STORAGE_VALKEY_URL=valkey://valkey.example:6379
```

Use `rediss://` for a TLS Valkey endpoint. Apply the Valkey service's normal
backup, access-control, and failover procedures.

## File Storage

Starport stores a document once and lets a chat request name it. The record
goes to the configured `KVStore`. The bytes go to a separate backend.

### The routes and their scopes

| Route | Scope | Meaning |
| --- | --- | --- |
| `POST /v1/files` | `files:write` | store one file |
| `GET /v1/files` | `files:read` | list the account's files |
| `GET /v1/files/{file_id}` | `files:read` | read one file record |
| `DELETE /v1/files/{file_id}` | `files:write` | delete one file |
| `GET /v1/files/{file_id}/content` | `files:read` | read the stored bytes |

Every route scopes its answer to the calling credential. There is no route that
lists across accounts, so an operator reads another account's files with that
account's credential.

An upload is `multipart/form-data` with a `file` part and a `purpose` part.
Starport accepts two purposes, `user_data` and `vision`. It refuses any other
value and names the set it accepts in the refusal.

### Retention and size

```text
STARPORT_FILES_MAX_UPLOAD_BYTES=536870912
STARPORT_FILES_RETENTION=720h
STARPORT_FILES_SWEEP_INTERVAL=1h
```

The upload bound defaults to 512 MiB. The retention window defaults to 30 days,
and a sweep deletes expired files every hour.

An upload can ask for a shorter window with the `expires_after[anchor]` and
`expires_after[seconds]` form fields. Starport refuses a request under one hour
and a request longer than the deployment window. A caller therefore shortens
retention and never extends it.

`STARPORT_SERVER_MAX_REQUEST_SIZE` still bounds the HTTP body. Raise it with
the upload bound, or the server refuses a large upload before the file service
reads it.

An account also carries a stored-byte bound. Set it as `stored_bytes` on the
account limits or on a gateway API key, and the tighter of the two applies. The
gateway answers a full account with HTTP 413 and tells the caller to delete a
file to make room. A stored-byte bound is a level and not a rate: an upload
raises it and a delete lowers it, and no interval resets it.

### Choosing a backend

The default writes to the platform data directory:

```text
STARPORT_FILES_BACKEND=filesystem
# STARPORT_FILES_PATH=/absolute/path/to/starport/data/files
```

Keep that directory on persistent storage and include it in the same backup
procedure as the Badger directory.

Use an S3-compatible bucket for multiple nodes:

```text
STARPORT_FILES_BACKEND=objectstore
STARPORT_FILES_OBJECT_STORE_BUCKET=starport-files
STARPORT_FILES_OBJECT_STORE_REGION=us-east-1
STARPORT_FILES_OBJECT_STORE_ACCESS_KEY_ID=...
STARPORT_FILES_OBJECT_STORE_SECRET_ACCESS_KEY=...
# STARPORT_FILES_OBJECT_STORE_ENDPOINT=https://account.r2.cloudflarestorage.com
# STARPORT_FILES_OBJECT_STORE_PREFIX=production/
```

The object store reaches AWS S3, Cloudflare R2, MinIO, and Backblaze B2
buckets. Set the endpoint for a bucket that AWS does not host. Set the prefix
to share one bucket between two deployments.

An incomplete object-store selection refuses startup. Starport does not fall
back to the filesystem, because a second node would then answer for bytes the
first node holds alone.

`GET /api/v1/admin/info` reports the selected backend as `files.backend`, and
the console settings page shows the same value. A deployment with no file
storage reports `not configured`.

## Reranking

A rerank request scores a list of documents against one query and returns them
in relevance order. It generates no message, so a rerank model answers no chat
turn and appears in no chat model picker.

### The routes and the scope

```text
POST /v1/rerank
POST /api/v1/rerank
```

Both need `rerank:write`. The scope stands alone. A rerank request reads the
documents the caller sent rather than a stored one, so neither `chat:write` nor
`files:read` covers it.

The two paths plan one route and reach one provider. They differ at the wire.
The OpenAI path takes `return_documents` and echoes the ranked text only when
the caller asks. The OpenRouter schema echoes the text on every result and has
no flag.

### The request and the answer

A request names a model, a query, and the documents. `top_n` bounds how many
results come back. A result carries the index of the document in the caller's
list, its relevance score, and the text when the protocol states it.

The gateway refuses a request whose document count is over the bound the chosen
offering publishes. The refusal names the model and the count, which are the two
things the caller can change.

### Where the price comes from

Starmap owns the `rerank` operation, the offerings, and the price. Two billing
bases exist, and an offering states which one it uses.

- A `search-unit` basis bills one query against a bounded document count.
  Cohere meters this and publishes no token price at all.
- A `token` basis bills the tokens the provider read. Voyage meters this.

The basis decides which price the gateway reads. An offering that publishes no
price in the unit it bills loses its rerank operation before planning, so no
turn reaches it. A zero cost would let an account rerank without limit.

The usage record carries `search_units` beside the token counts. The spend
budget refuses a turn on a search-unit offering before the provider call. The
estimate uses the lowest search unit price in the generation, because the
planner chooses the offering afterward. A token-billed offering states no floor
before it reads, so it refuses nothing early.

## Document Parsing

A chat request attaches a document and names the `file-parser` plugin. Starport
turns the document into text before the chat model sees it, so a model that
reads no files still answers about one.

```json
{
  "model": "openai/gpt-4.1",
  "plugins": [{ "id": "file-parser", "pdf": { "engine": "native" } }],
  "messages": [
    {
      "role": "user",
      "content": [
        { "type": "text", "text": "What is the amount due?" },
        {
          "type": "file",
          "file": { "filename": "invoice.pdf", "file_id": "file_abc123" }
        }
      ]
    }
  ]
}
```

A part carries its bytes as `file_data` or names a stored file as `file_id`.
The file store that `## File Storage` above configures owns the stored one.

### The two engines

| Engine | What it does | What it costs |
| --- | --- | --- |
| `native` | reads the text layer inside this process | nothing |
| `recognition` | sends the page to a model that serves `documents-recognition` | one page price per page |

A request that names no engine gets `native`. A request that names no plugin
leaves the document to the chat model. That asks a different question, and it
is not the same as asking for the native engine.

The gateway refuses an engine outside those two names with HTTP 400, and the
refusal carries both names. It refuses a plugin identifier outside
`file-parser` the same way. OpenRouter publishes two more engine names that
reach vendors this catalog does not serve. Accepting one of them and reading
the page some other way would report work this deployment never did.

The native engine reaches no network address. It refuses a document over 200
pages, and it refuses an extraction that runs past 15 seconds. A document with
no usable text layer reads as scanned, and a caller that wanted its contents
asks again with `recognition`.

### Where the page price comes from

Starmap owns the `documents-recognition` operation and the price of one page.
An offering that serves the operation publishes `page_input` beside its token
prices, and `/api/v1/models` reports both. Starport publishes no page price of
its own and holds no table of recognition vendors.

An offering that serves recognition and publishes no page price does not
project at all. A page that reaches such an offering records
`cost_unavailable_reason`, which says the gateway lost its catalog rather than
that the page was free.

The spend budget refuses a document before the provider sees it. The gateway
prices the pages against the lowest page price in the generation, because the
planner chooses the offering afterward. A bound built on a higher price would
refuse work the account could pay for.

### What a reader sees

The usage record names the engine and the pages the turn attached. It also
names the pages a model recognized, the pages this process read, the
milliseconds the reads took, and the recognized share of the cost. The share is
also inside `cost`, because the spend budget meters that field alone.

The console reads those fields at `/documents`. The page names the engine, the
count, the time, and the cost of each document read. It also lists the
recognition models this catalog reaches, with the price of one page at each.

### The extraction cache

One document reads once for each account, engine, and catalog generation inside
a one-hour window. A turn that reused every attachment reports
`extraction_cached`, which separates a page an earlier turn paid for from a page
no provider ever charged for.

The entries hold text rather than bytes. They live in the key-value store under
the `extraction:` prefix, so a deployment on Valkey shares them across its
processes. A cache this gateway cannot reach costs a second read and fails no
turn.

## Video Jobs

A video takes minutes, so a submission answers with a job identifier rather
than with a video. The caller comes back to that identifier until the job
reaches a terminal state, and then reads the bytes this gateway stored for it.

Starport polls the provider itself. A caller never learns the provider's own
job identifier, so a deployment can move a model between providers without
breaking a caller that is mid-poll.

### The routes and their scopes

| Route | Scope | Meaning |
| --- | --- | --- |
| `POST /v1/videos` | `videos:write` | submit one job |
| `GET /v1/videos` | `videos:write` | list the account's jobs |
| `GET /v1/videos/{video_id}` | `videos:write` | read one job |
| `GET /v1/videos/{video_id}/content` | `videos:write` | read the stored bytes |
| `POST /v1/videos/{video_id}/cancel` | `videos:write` | cancel one job |

The OpenRouter family serves the same five paths under `/api/v1/videos`. A
caller polls a job through the family it submitted through.

One scope covers the whole surface. The account that submits a job is the only
account that can read it. A separate read scope would therefore name a
capability no other caller can hold.

### The job states

A job holds one of five states:

| State | Meaning |
| --- | --- |
| `queued` | the provider accepted the submission and has not started |
| `running` | the provider is working |
| `completed` | the video is ready, and this gateway may still hold the bytes |
| `failed` | the provider refused or gave up, and `error.message` says why |
| `cancelled` | a caller stopped the job before it finished |

The last three are terminal. A terminal job never returns to `running`, so a
caller that reads one of them can stop polling.

A completed job carries `expires_at` while this gateway still holds its bytes.
The field goes once the retention window closes. That tells a caller the work
finished and the video went, without spending a request to find out.

### Retention and the polling budget

```text
STARPORT_JOBS_ASSET_RETENTION=24h
STARPORT_JOBS_MAX_ASSET_BYTES=268435456
STARPORT_JOBS_SWEEP_INTERVAL=1h
```

The retention window defaults to 24 hours, measured from the moment this
gateway stored the asset. It is short beside the 30 days a file gets. A
generated video is an answer a caller collects rather than a document it keeps.
Both provider families publish their own links with windows measured in hours.
A caller that comes back past the window reads HTTP 410 and the window length
in the refusal.

One stored asset defaults to a 256 MiB bound. Without it a provider's decision
about how large its own answer is would size this deployment's storage. A sweep
reclaims expired bytes every hour. The sweep is a floor on how long expired
bytes survive on disk. It is not a floor on how long an asset reads: an expired
asset stops reading the moment it expires.

Starport polls a job for one hour. Past that it fails the job and states the
budget in the message. A provider that has not answered in an hour is not going
to. The wait between polls starts at two seconds and doubles to a 30-second
ceiling. The provider request count therefore grows with the logarithm of the
wait.

Video bytes go to the same backend that `## File Storage` above configures. A
deployment that sets `STARPORT_FILES_BACKEND=objectstore` serves a video from
any node. One that leaves the filesystem default serves it from the node that
stored it.

### Bounding what an account holds open

Set `outstanding_jobs` on the account limits or on a gateway API key, and the
tighter of the two applies. It defaults to eight. The gateway answers an
account at its bound with HTTP 429 and a `rate_limit_error`, and the caller
waits for one of its own jobs to finish.

An outstanding job bound is a level and not a rate. A submission raises it, a
terminal state lowers it, and no interval resets it. It bounds concurrent
provider work rather than request volume. It therefore composes with the
request and token rates rather than replaces them.

## Guardrails

A guardrail check reads canonical text and answers allow, redact, or refuse.
Checks run in the configured order over the request before planning and over
the answer before the caller reads it. Guardrails stay off until configuration
names a check, and an unconfigured deployment pays nothing on the request
path.

A configured check that cannot evaluate refuses rather than waves the text
through unread. A refusal answers HTTP 400 with the check name, and the usage
record carries the verdict. On a stream the gateway holds answer text in a
bounded window and inspects each window before release.

```bash
export STARPORT_GUARDRAILS_CHECKS="pii,moderation"
```

An unknown check name refuses to start.

### The pii check

The `pii` check detects personal identifiers with no model call: email
addresses, phone numbers, card numbers under Luhn, and dashed US SSNs. The
`redact` mode rewrites each identifier to a bracketed label such as
`[redacted-card]`. The `refuse` mode stops the exchange, and the reason names
the categories rather than the values.

```bash
export STARPORT_GUARDRAILS_CHECKS="pii"
export STARPORT_GUARDRAILS_PII_MODE="redact"
```

The mode defaults to `redact`.

### The moderation check

The `moderation` check classifies text with a catalog moderation model and
refuses when any category scores at or above its threshold. The call rides
the account's own routing. Credential selection, usage capture, and limits
treat it as the account's own moderation request. The classification draws
its own usage record beside the turn that asked for it.

```bash
export STARPORT_GUARDRAILS_CHECKS="moderation"
export STARPORT_GUARDRAILS_MODERATION_MODEL="openai/omni-moderation-latest"
export STARPORT_GUARDRAILS_MODERATION_THRESHOLD="0.5"
export STARPORT_GUARDRAILS_MODERATION_THRESHOLDS="violence=0.8,self-harm=0.2"
```

The default threshold is `0.5`. A category named in the override list reads
its own threshold, and every other category reads the default. A pipeline
that names the moderation check without a model refuses to start.

## Semantic Cache

The exact response cache answers only an exact repeat of a request. The
semantic cache adds a similarity index beside it, so a close paraphrase
of a cached prompt can answer from the cache. The layer never stores a
response of its own. It holds vectors that point at exact cache entries,
and the index drops a vector whose entry expires.

A similarity match stays inside one similarity scope. The scope pins the
account, the catalog generation, the model, the sampling parameters, the
tools, and the routing policy. Only the prompt text may differ. A paraphrase
therefore never crosses a boundary that the exact cache keeps apart.

The layer is off by default and needs two opt-ins:

```bash
export STARPORT_SEMANTIC_CACHE_ENABLED="true"
export STARPORT_SEMANTIC_CACHE_MODEL="openai/text-embedding-3-small"
```

The deployment flag alone answers nothing. Each request also opts in with
the `X-Semantic-Cache: true` header, and only a request the exact cache
would store is eligible. The embedding call rides the account's own routing
through the gateway's embeddings path. It draws its own usage record beside
the turn that asked for it. Enabling the layer without an embedding model
refuses to start.

Two optional bounds tune the index:

```bash
export STARPORT_SEMANTIC_CACHE_THRESHOLD="0.95"
export STARPORT_SEMANTIC_CACHE_MAX_ENTRIES="128"
```

The threshold is the minimum cosine similarity that answers, and it must sit
in `(0, 1]`. The entry bound caps the vectors one similarity scope holds,
and the oldest vector leaves first. The values above are the defaults.

A similarity answer reports `X-Cache: HIT` with `X-Cache-Age`, the same
vocabulary an exact hit uses. It adds `X-Cache-Similarity` with the cosine
score, so a caller can tell the two apart. An embedding or index failure
never blocks the request. The gateway logs the failure and pays the
provider it would have paid anyway.

## Preset Revisions

Every preset save is an immutable revision with an incrementing number.
The first save is revision 1, and each update or rollback adds one. An
edit therefore never destroys the configuration a running client depends
on. The revision number is also the optimistic-concurrency token an
update names, so the two meanings never diverge.

A request selects the latest revision with `@preset/name`, in the model
field or the OpenRouter `preset` body field. A request pins one stored
revision with `@preset/name@N`. A pin to a revision that does not exist
fails like an unknown preset.

The history routes sit beside the preset routes:

```bash
curl "$STARPORT_URL/api/v1/presets/fast/history" \
  -H "Authorization: Bearer $STARPORT_API_KEY"

curl -X POST "$STARPORT_URL/api/v1/presets/fast/rollback" \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"to_revision": 1, "revision": 3}'
```

The history answers stored revisions newest first, and any authenticated
key reads it. A rollback needs the `presets:write` scope. It saves a new
head revision that copies what `to_revision` stored. The `revision` field
names the head the caller read, so a concurrent save answers `409` instead
of a silent overwrite.

Deleting a preset drops its history. The console renders the history
behind each preset row, with a restore action per old revision.

## Container Start

The Compose file starts Starport with Valkey. Its optional `.env` file passes
catalog-declared provider values into the Starport container without a provider
roster in the Compose file. Copy the example, set the master key, and set the
provider values that this deployment needs. This example uses OpenAI:

```bash
cp .env.example .env
# Edit .env. Set STARPORT_SECURITY_MASTER_KEY and OPENAI_API_KEY.
docker compose up --build -d valkey
docker compose run --rm starport init --configured-storage --name primary-admin
docker compose up -d starport
```

Save the gateway key from the initialization output. Do not run the
initialization command again for the same Valkey data set.

## Limits and Shutdown

The main HTTP controls are:

- `STARPORT_SERVER_REQUEST_TIMEOUT`
- `STARPORT_SERVER_MAX_REQUEST_SIZE`
- `STARPORT_SERVER_MAX_HEADER_BYTES`
- `STARPORT_SERVER_SHUTDOWN_TIMEOUT`

`STARPORT_SERVER_MAX_REQUEST_SIZE` defaults to 33554432 bytes, which is 32
MiB. A caller attaches an image, an audio file, or a document as base64 inside
the JSON body, and base64 grows a payload by a third. The default therefore
holds one file of about 25 MB, which is the largest single file the provider
APIs commonly accept. Raise it if your callers send larger files.
The gateway holds the body in memory while it reads it.

The gateway answers a body above the limit with HTTP 413. The message states
the limit in bytes. It also states the received size when the caller sends a
`Content-Length` header. The gateway refuses a caller that states no length
while it reads the body, so that message states the limit alone.

`SIGINT` and `SIGTERM` start graceful shutdown. Starport drains HTTP first.
It then closes background work, cache, providers, and storage in reverse
construction order.

## Prometheus Metrics

Starport serves a Prometheus scrape at `GET /metrics`. The scrape is on by
default and needs no credentials, the same way the health checks do. Set
`STARPORT_TELEMETRY_METRICS` to change that:

- `on` (default): serve the scrape without credentials.
- `admin`: serve the scrape only to a gateway key with the `admin` scope.
- `off`: remove the route.

Point a Prometheus scrape job at the gateway:

```yaml
scrape_configs:
  - job_name: starport
    static_configs:
      - targets: ["127.0.0.1:8080"]
```

For `admin` mode, add the gateway key as a bearer credential:

```yaml
    authorization:
      type: Bearer
      credentials: <admin gateway key>
```

The metric names carry the `starport_` prefix. The gateway counts requests,
tokens, cost, latency, time to first token, gateway overhead, cache hits,
provider failures, and budget refusals. Labels name the protocol, operation,
provider, model, and outcome. Labels never carry an account, a key, or any
other caller identity. The scrape stays safe to share with a metrics system,
and its label cardinality stays bounded.

A budget refusal writes no usage record. The refusal counter
`starport_budget_refusals_total` therefore counts refusals at the budget
check itself, labeled by scope (`account` or `key`) and dimension (`spend`
or `tokens`).

## Distributed Traces

Starport exports OpenTelemetry traces over OTLP HTTP. Tracing is off until
you set the standard OpenTelemetry endpoint variable:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT="http://127.0.0.1:4318"
```

The more specific `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` wins when you set
both. These two names are the cross-vendor OpenTelemetry contract, so they
carry no `STARPORT_` prefix. With neither set, the tracer is a no-op and
the gateway dials nothing.

One chat request produces four spans:

- `starport.request`: the full HTTP request.
- `starport.route_plan`: the deterministic route computation.
- `starport.attempt`: one execution attempt, numbered from 1.
- `starport.provider_call`: the upstream provider call inside the attempt.

The provider-call span names the provider and the model. The request span
carries the gateway overhead and the time to first token as
`starport.overhead_ms` and `starport.ttft_ms`. Inbound W3C `traceparent`
headers continue the caller's trace, so a gateway span joins the trace your
client already started.

Span attributes never carry an account, a key, or prompt content. The same
privacy rule that bounds the metric labels bounds the spans.

## Usage Export

Usage records feed the activity API and die with retention. An analytics
system that wants its own copy has two paths: a streaming sink and an
export endpoint.

Set `STARPORT_TELEMETRY_USAGE_EXPORT` to stream each finalized record out:

- An `http://` or `https://` value posts NDJSON batches to that URL.
- Any other value is a file path that NDJSON lines append to.
- Empty (the default) exports nothing.

The sink buffers and flushes every five seconds and at shutdown. It never
blocks a request: when the target stays unreachable or the buffer fills,
records drop and `starport_usage_export_dropped_total` counts them on the
scrape. A failed post retries three times before its batch drops. The
durable store keeps every record either way. A drop only leaves a gap in
the streamed copy.

`GET /api/v1/activity/export` streams the stored records for the
authenticated key under the `activity:read` scope. It takes the same
filters the activity listing takes (`model`, `provider`, `status`,
`request_id`, `guardrail`, `since`, `until`) and serves NDJSON by default or
CSV with `format=csv`:

```bash
curl -H "Authorization: Bearer $STARPORT_API_KEY" \
  "http://127.0.0.1:8080/api/v1/activity/export?format=csv" > activity.csv
```

`GET /api/v1/admin/activity/export` streams the records of every key under
the admin scope. A `key_id` filter narrows it to one key. The `guardrail`
filter on both routes takes `refuse` or `redact` and keeps only the turns a
guardrail closed with that verdict. Each row carries the cache fields
(`cache_status`, `cache_semantic`, `cache_similarity`) and the guardrail
fields (`guardrail_verdict`, `guardrail_check`) after the cost columns. The
console usage page downloads the same file under its active filters with the
Export NDJSON and Export CSV controls.

## Audit Log

Every admin mutation leaves one durable record: who asked, what it touched,
and whether the store accepted it. The trail covers gateway API keys,
accounts, account templates, teams, memberships, grants, shared credentials,
BYOK, presets, and the authentication mode. A record never holds a credential
value.

Each record names its actor with one prefixed string:

- `key:<name>` is a gateway API key, by name when it has one and by ID
  otherwise.
- `console:<grant>` is a machine-local console session: `ticket` or
  `local-token`.
- `user:<subject>` is an identity-grant console session.
- `anonymous` is a caller without an authenticated identity.

The outcome is `ok` when the store accepted the mutation and `error` when the
store refused it. A request refused before the store, such as a validation
failure, records nothing.

Each record carries the `request_id` of the gateway request that made the
mutation. Pass it to the activity listing as `request_id` to reach the usage
row for the same request. A write without a request context leaves the field
empty.

Read the trail with `GET /api/v1/admin/audit` under the admin scope. The
listing serves the newest records first and takes `action`, `actor`, `since`,
`until`, `limit`, and `cursor` filters:

```bash
curl -H "Authorization: Bearer $STARPORT_API_KEY" \
  "http://127.0.0.1:8080/api/v1/admin/audit?action=key.create&limit=50"
```

The console renders the same trail on its Audit Log page.

Records prune on write past the retention window.
`STARPORT_AUDIT_RETENTION` sets the window, and the default `9600h` keeps 400
days. A failed audit write lands in the log and never changes the caller's
response, because the mutation already happened.

## Webhooks

Starport pushes gateway events to HTTP endpoints you name. Webhooks stay
off until configuration names a receiver.

Set two environment variables:

- `STARPORT_EVENTS_WEBHOOK_URLS` holds the receiver URLs, comma separated.
- `STARPORT_EVENTS_WEBHOOK_SECRET` holds the signing secret.

The gateway emits seven event types:

- `budget.exhausted` when a budget refusal writes a 402 answer.
- `job.completed`, `job.failed`, and `job.cancelled` when an asynchronous
  job reaches its terminal state, once per job.
- `provider.health.changed` when a provider's status page moves to a new
  indicator.
- `key.created` and `key.deleted` when an admin mutation lands.

Every delivery is one JSON envelope:

```json
{
  "id": "evt-sample",
  "type": "budget.exhausted",
  "time": "2026-08-30T00:00:00Z",
  "data": {"scope": "account"}
}
```

The `data` map carries identifiers, scopes, and states only. A payload
never holds a provider credential, a gateway key token, or prompt or
response content.

Each POST carries an `X-Starport-Signature` header: `sha256=` followed by
the hex HMAC-SHA256 of the raw request body under your secret. Verify it
before you trust a delivery. With the secret `whsec_demo_secret` and the
body

```json
{"id":"evt-sample","type":"budget.exhausted","time":"2026-08-30T00:00:00Z","data":{"scope":"account"}}
```

the header value is
`sha256=45f5f4544c3390994afa396a4fe0415d0c9574a32a3e1973bd9342b3e02a5b1d`.
Compute the same HMAC over the received bytes and compare with a
constant-time equality check.

Delivery is asynchronous and never blocks a request. Each event gets three
attempts per endpoint with doubling backoff, and a queue of at most 1000
undelivered events. An event that spends every attempt, or arrives at a
full queue, drops and counts on the scrape as
`starport_webhook_dead_letters_total`. At shutdown the dispatcher delivers
what is already queued before it stops.

Read the same state without a scrape:

```bash
curl -H "Authorization: Bearer $STARPORT_ADMIN_KEY" \
  http://127.0.0.1:8080/api/v1/admin/webhooks
```

The answer names each receiver with its credential and query string
removed. It lists the event types the gateway emits. It states the
undelivered queue depth against its capacity, and the dead letter count
since the process started:

```json
{
  "configured": true,
  "endpoints": ["https://hooks.example.com/starport"],
  "events": ["budget.exhausted", "job.completed", "job.failed", "job.cancelled", "provider.health.changed", "key.created", "key.deleted"],
  "queue": {"depth": 0, "capacity": 1000},
  "dead_letters": 0
}
```

A deployment with no receiver answers `configured: false` with an empty
endpoint list and the same event list. The console Settings page reads
this route.

## Failure Diagnosis

Run `starport doctor --probe` before you start the server. The output names
each failed check and keeps all secret values redacted.

- `provider credential master key is required`: set
  `STARPORT_SECURITY_MASTER_KEY`.
- `gateway identity is required; run "starport init"`: create the first named
  identity in local or configured storage.
- Provider credential state is `not_configured`: set one of the conventional,
  derived, cloud, or secret-reference values that its Starmap profile declares.
- HTTP 401: the bearer value does not match an active stored identity.
- HTTP 403: the identity lacks the required scope or owns a different key.
- No route candidate: the model, provider policy, account policy, capability,
  context limit, or current offering availability rejected every route.
- SDK check is `UNVERIFIED`: install the named optional official SDK before
  treating that client as tested.

Starport emits structured JSON logs by default. Each request includes a
request ID. Do not log raw gateway keys, provider keys, or master keys.

Starport trusts only the direct TCP peer for client-IP logs. It ignores
`X-Forwarded-For` and `X-Real-IP`. A future trusted-proxy configuration must
name the proxy header and trust boundary explicitly.

## Release Gate

Run these checks before you promote a production image:

```bash
bash scripts/verify-v1-architecture.sh
go test ./...
go test -race ./internal/inference ./internal/catalog ./internal/routing ./internal/execution ./internal/availability ./internal/response/cache ./internal/app ./internal/server
go vet ./...
make lint
make build
docker build .
bash scripts/smoke-openrouter-sdks.sh
```

The verifier must report `Summary: 12 passed, 0 failed`. Required raw HTTP
smoke checks must pass. Optional SDK checks can be green or `UNVERIFIED`. Do
not report an unverified SDK as compatible.
