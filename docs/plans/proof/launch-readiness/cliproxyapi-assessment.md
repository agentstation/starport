# CLIProxyAPI assessment for Starport subscription credentials

Date: 2026-10-15. Inputs: CLIProxyAPI (CPA) shallow clone at commit `54946fa3`
(`~/src/github.com/router-for-me/CLIProxyAPI`), Starport at `b310cad7`
(`/private/tmp/starport-launch`), Starmap module
`v0.17.1-0.20261009150341-120a49889009` from the Go module cache.
`[U]` marks a claim that is uncertain or that comes from secondary web sources.

## 1. Executive answer

**Partially, and mostly as a reference, not as code.**

- License: no blocker. CPA is MIT (`LICENSE:1-4`). MIT code can go into an
  AGPL-3.0 work if the MIT notice stays with it.
- Code import: not practical. The provider auth flows and executors are in
  `internal/` packages. Go does not let another module import them (section 3).
- UI: not reusable. The management UI is a different repository with a
  different stack (section 4).
- Provider terms are the deciding factor. They split the providers into three
  groups:
  - **Anthropic (Claude Pro/Max) and Google (Gemini CLI/Antigravity): do not
    build.** Both providers state that consumer OAuth tokens in third-party
    tools break their terms, and both enforce this with account bans `[U]`. CPA
    makes these providers work by disguising itself as the official CLI: a TLS
    fingerprint copy, a copy of the official header order, replacement of the
    system prompt, and billing-header signing (section 2.3). If Starport ships
    this, Starport helps users evade the provider's controls. That does not fit
    Starport's enterprise and trust position.
  - **OpenAI (ChatGPT Plus/Pro): build natively, on the official path only.**
    OpenAI announced "Sign in with ChatGPT" on 2026-09-29. Secondary reports say
    that it is open to open-source projects and uses dynamic client
    registration `[U]`. Starport is AGPL open source, so it can qualify. CPA's
    way is different: it reuses the Codex CLI client ID and the `codex-tui`
    User-Agent. Do not copy that.
  - **xAI Grok, Devin, Kimi, Meta: unknown.** CPA uses each vendor's
    first-party CLI client ID. Check each vendor's terms before work starts
    `[U]`.
- The first PR is a provider-neutral "OAuth grant" credential source in
  Starport, tested against a fake token server. Section 7 describes it.

## 2. CLIProxyAPI facts (Part A)

### 2.1 Project shape
- Module `github.com/router-for-me/CLIProxyAPI/v8`, `go 1.26.0`
  (`go.mod:1-3`). Starport needs `go 1.27.2` (`starport go.mod:3`). A newer Go
  can build an older module, so this is no blocker.
- Major direct dependencies (`go.mod:5-42`): gin, pgx/v5, minio, go-git/v6
  (alpha), redis, pion/webrtc, `refraction-networking/utls`, bubbletea TUI,
  logrus, gjson/sjson, x/oauth2.
- The build version is `"dev"` and the release tooling sets it
  (`internal/buildinfo/buildinfo.go:8`). The clone is shallow, so I could not
  measure the release cadence. The project is at major version v8, and
  `docs/sdk-usage.md:10-20` still says `/v6`. Both facts point to fast,
  breaking changes `[U]`.
- The README has many links to relay resellers and residential-proxy vendors
  ("one-IP-per-account", "multi-account operations") (`README.md:47-130`). The
  user base includes resellers of subscription capacity. This is a reputation
  risk for any integration that Starport presents as a product feature.

### 2.2 Subscription auth flows

Each row gives the flow type, the client and endpoint values, and where CPA
stores the token.

**Claude Pro/Max**
- Flow: authorization code with PKCE S256 (`internal/auth/claude/anthropic_auth.go:323-341`).
- Client and endpoints: the Claude Code public client `9d1c250a-…`. It
  authorizes at `claude.ai/oauth/authorize` and exchanges the code at
  `platform.claude.com/v1/oauth/token`. The redirect is `localhost:54545`.
  The scope is `user:inference user:sessions:claude_code …` (`:24-35`).
- Storage: JSON with `access_token`, `refresh_token`, `id_token`, `expired`,
  `email`, `account_uuid`, `organization_uuid`, `claude_device_ids`, and
  `type:"claude"` (`internal/auth/claude/token.go:18-56`).

**Codex (ChatGPT)**
- Flow: authorization code with PKCE, plus a device flow
  (`internal/auth/codex/openai_auth.go:25-28`, `sdk/auth/codex_device.go:26-30`).
- Client and endpoints: the Codex CLI client `app_EMoamEEZ73f0CkXaXp7hrann`
  with `auth.openai.com/oauth/{authorize,token}` and redirect `localhost:1455`.
  The device flow uses `auth.openai.com/api/accounts/deviceauth/*`.
- Storage: JSON with `access_token`, `refresh_token`, `id_token`,
  `account_id`, `plan_type`, and `expired` (`internal/auth/codex/token.go:21-37`).

**Antigravity (Google)**
- Flow: Google OAuth with a loopback redirect on port 51121
  (`internal/auth/antigravity/constants.go:5-24`).
- Client: the Antigravity desktop client ID and **client secret are embedded
  in the source**. Scopes include `cloud-platform`, `cclog`, and
  `experimentsandconfigs`.
- Storage and upstream: the token is a Google OAuth token. The upstream is
  `cloudcode-pa.googleapis.com` (`internal/runtime/executor/antigravity_executor.go:34-36`).

**xAI Grok**
- Flow: OIDC discovery and a device code (`internal/auth/xai/xai.go:20,114`).
- Client and endpoint: the "public xAI Grok CLI" client `b1a00492-…`. The
  upstream is `cli-chat-proxy.grok.com` (`internal/auth/xai/types.go:13-19`).

**Meta Muse**
- Flow: a device flow with a fixed client `1031625952748946`. The flow then
  mints an API key at `api.meta.ai/muse-code/key` (`internal/auth/meta/meta.go:27-35,444`).

**Devin**
- Flow: PKCE through `app.devin.ai` and `server.codeium.com`
  (`internal/auth/devin/devin_auth.go:24-28`).

Storage for all providers:
- CPA writes one plaintext JSON file per account, mode `0600`, in `auth-dir`
  (default `~/.cli-proxy-api`) (`sdk/auth/filestore.go:135,150`,
  `config.example.yaml:861`). The file is not encrypted.
- Other backends are Postgres, an object store, and git (`internal/store/`).
- Refresh uses singleflight with backoff. For Claude, see
  `anthropic_auth.go:37-46,490-610`.

### 2.3 Wire translation and client disguise
- Translators in `internal/translator/{openai,claude,gemini,codex,antigravity,interactions}`
  map between the client formats and the format of each backend. They are
  exposed through `sdk/translator`.
- Codex requests go to `https://chatgpt.com/backend-api/codex`, which uses the
  Responses API (`codex_executor_execute.go:33`). CPA sets
  `User-Agent: codex-tui/0.154.0 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11` and
  `Originator: codex-tui`. It also drops the User-Agent of the downstream
  client "to reduce Cloudflare 1010 blocks"
  (`codex_executor_request.go:27-28,178-186,212`).
- Claude requests have a "cloaking" layer, `claude_executor_cloaking.go`:
  - `claudeCodeCLIIdentity = "You are Claude Code, Anthropic's official CLI for Claude."` (`:307`).
  - It adds a generated `x-anthropic-billing-header` block in front of the
    system prompt (`:150-158,417-427`).
  - It moves the system prompt of a third-party caller so that the prompt does
    not reach Anthropic in the system slot (`:461-467`).
  - It does "CCH" body signing (`claude_signing.go`, `claude_fingerprint_policy.go:28-41`).
  - It uses the default User-Agent `claude-cli/2.1.280 (external, cli)`
    (`helps/claude_device_profile.go:23`).
  - OAuth calls copy the Axios headers and header order
    (`anthropic_auth.go:215-226`, `utls_transport.go:32-53`).
  - A uTLS ClientHello copy hides the TLS fingerprint
    (`utls_transport.go:113-252`, `helps/utls_client.go`).
  - The config says that the default cloaks "only non-Claude-Code clients"
    (`config.example.yaml:392-395`).
- **Conclusion:** the Claude and Google paths deliberately evade detection.
  That is the ToS-sensitive core of CPA, and it is the part that Starport must
  not copy.
## 3. Can Starport import CPA? (Part A.2)

- `sdk/cliproxy` embeds the **whole proxy service**: gin server, watcher,
  refresh, and executors (`docs/sdk-usage.md:1-45`). Examples:
  `examples/custom-provider/main.go`, `examples/translator`, and
  `examples/plugin` (a C-ABI dynamic-library plugin system with Go, C, and
  Rust samples).
- The executors are in `internal/runtime/executor` and the service registers
  them itself (`sdk/cliproxy/service_executors.go:10,301`). Starport cannot
  call a single executor.
- The login flows are in `internal/auth/*`. `sdk/auth` wraps them with an
  `Authenticator{Provider, Login, RefreshLead}` interface
  (`sdk/auth/interfaces.go:25-29`). That wrapper needs CPA's `config.Config`
  (an alias type re-exported in `sdk/config/config.go:9-11`) and writes CPA's
  `coreauth.Auth` records (`sdk/cliproxy/auth/types.go:48-88`). It also opens
  browsers and runs loopback callback servers. All of that duplicates the
  Starport keyring.
- A Go dependency adds the CPA tree to Starport's dependency graph: pion
  WebRTC, minio, go-git alpha, redis, a TUI, utls, and logrus next to zerolog.
  Every change that CPA makes on a v8.x tag becomes a Starport upgrade task.
- Verdict:
  - Import: no.
  - Vendor: no. The useful code is the vendor-specific disguise, which we
    reject.
  - Reimplement: yes, for the OpenAI official flow only. Use CPA only as a
    reference for refresh backoff, singleflight, and device-flow polling
    (`anthropic_auth.go:37-120`, `sdk/auth/codex_device.go`).

## 4. Management UI (Part A.3)

- CPA does not include the UI. At run time, it downloads `management.html`
  from the GitHub release of `router-for-me/Cli-Proxy-API-Management-Center`
  and checks the SHA-256 digest. If that download fails, it uses a fallback at
  `cpamc.router-for.me` with **no digest check** (`internal/managementasset/updater.go:29-36,192-301`).
- CPA serves the file at `/management.html`. A management key protects
  `/v0|/v8/management` (`internal/api/server_middleware.go:36-48`,
  `docs/management-api-v8.md:8-13`).
- The management API that the UI calls is in `docs/management-api-v8.md:83-141`:
  - `/credentials` CRUD, status, fields, and refresh.
  - The OAuth login: `/oauth/auth-url?provider=` gives a URL and `state`, then
    the UI polls `/oauth/status?state=`, then posts `/oauth/callback`.
    Cancellation uses `/oauth/session`.
  - Plugin quota, usage, and logs.
- The UI repository is MIT. Its stack is React 19, Vite 8, Zustand, SCSS
  Modules, Motion, CodeMirror, i18next, and React Router 7. Bun builds it into
  one inlined HTML file. Features: config editor, auth files and OAuth, quotas
  for Claude, Antigravity, Codex, Kimi, and xAI, request logs, and i18n. These
  facts come from the GitHub page through WebFetch `[U]`. I did not clone the
  repository.
- Community dashboards (named only, not inspected): dongshuyan/CPA-Dashboard,
  itsmylife44/cliproxyapi-dashboard, and 0xAstroAlpha/cliProxyAPI-Dashboard.
- **Reuse verdict: none.** The Starport console uses shadcn 4, Base UI 1.9,
  Tailwind 4, TanStack Query 5, and Recharts 3 (`console/package.json`). The
  CPA UI uses SCSS Modules, Zustand, and a different design system, so a port
  is a rewrite. Reuse only the interaction pattern: start login, show the
  URL or device code, poll the status, show the result, then list accounts
  with their quota windows.

## 5. Starport seams (Part B)

These are the seams that a subscription credential touches. A subscription
credential has an access token, a rotating refresh token, an expiry, a login
flow, and a quota for each account.

### 5.1 Catalog contract (Starmap)
- `catalogs.ProviderAuthenticationPrimitive` defines only these values:
  `none`, `api-key`, `bearer-token`, `google-default`, `azure-default`, and
  `aws-default` (`starmap …/pkg/catalogs/provider_credentials.go:63-73`).
- Changes:
  - A new primitive, for example `oauth-grant`.
  - Profile `ProtocolOptions` with the issuer or token endpoint, the
    registration mode, the scopes, and the account header. The model for this
    is `ProtocolOptions.GoogleDefault`, which `starport internal/providers/auth/request.go:117` reads.
  - A new provider entry for the ChatGPT-plan backend, because its base URL
    and endpoints differ from `api.openai.com` `[U: exact backend for Sign in with ChatGPT]`.

### 5.2 Material model
- `credentials.Material` already has `ExpiresAt` and
  `Lease{Renewable, RefreshAfter}` (`internal/credentials/material.go:24-37`).
  It can hold an access token without change.
- The refresh token is new. It is **rotating, durable secret state**, and
  every refresh must write it back. Today, material is resolved and read-only:
  `ReferenceSource.Resolve` (`internal/credentials/source.go:62-65`) and
  `CloudChain.Resolve` (`internal/credentials/resolver.go:54-63`).
- Gap: a write-back contract to the encrypted repository, with a compare-and-swap
  version. `keyring` already retries updates for CAS conflicts
  (`provider_keys.go:27`, `maxCredentialUpdateAttempts`).

### 5.3 Lifecycle
- `keyring.NewProviderKeys(repository, masterKey, validator, limits…)`
  (`internal/providers/keyring/provider_keys.go:31-60`) owns the encrypted
  stored credentials. The managed-material cache (`managed_material.go:53-58`)
  applies `MaterialLimits`, which caps `Validity` at 5 minutes and sets a
  refresh interval (`internal/credentials/material_limits.go:9-38`).
- The precedent for renewable material is the cloud chain: a chain registered
  for each primitive (`internal/config/loader.go:242-244`) and the Google token
  minting (`internal/credentials/cloudchain/google.go:47-104`). The OAuth grant
  source must work for each stored credential. For a cloud chain, the
  credential is process-wide.

### 5.4 Request signing
- `providerauth.ProductionRegistry` maps a primitive to an applicator
  (`internal/providers/auth/request.go:60-69`). Placements set the headers
  (`:128-175`), and connectors call the registry through
  `connectors/authentication.go:13-18`.
- A new applicator adds the bearer token and, if the provider needs it, an
  account header. The value comes from a material field, not from code.
- Connectors send an honest `User-Agent: starport/1.0`
  (`connectors/anthropic.go:186`). Keep that.
### 5.5 Translation
- No Starport connector speaks the OpenAI Responses API. No connector file
  contains `/responses`.
- The ChatGPT plan backend in CPA uses Responses
  (`codex_executor_execute.go:33`). If the official path also uses it, a
  Responses connector is new work `[U]`.

### 5.6 Control plane
- Shared credentials: `/api/v1/providers/{provider}/credentials` CRUD and
  validate, admin scope (`internal/server/routes.go:198-213,504-509`). The
  handlers are in `internal/server/controllers/provider_credentials.go`.
- BYOK: `/api/v1/accounts/{account_id}/byok` (`routes.go:215-222,512`).
- New endpoints: login start, login status, and login cancel. Use a device
  flow so that a headless gateway works and needs no loopback port.

### 5.7 Console
- Provider detail: `console/src/routes/providers_.$providerId.tsx:8,14,183`.
- Components: `components/credentials/{ProviderCredentialCard,CredentialFields,SharedCredentialPanel,ByokPanel,CredentialApplyModal}.tsx`.
- Client: `console/src/lib/api.ts:1118-1200` (shared credentials) and `:1361`
  (provider usage for each account).
- New: a "Sign in with ChatGPT" action, a device-code panel with polling, the
  account identity, the expiry and refresh state, and a quota-window display.
  The quota display uses the existing Recharts and usage patterns.

### 5.8 Quota
- Starport has usage projections for each account (`lib/api.ts:1361`) and
  rate limits (`internal/ratelimit`).
- A subscription quota is a provider-reported window, for example a 5-hour or
  weekly window. Starport has no concept for this yet. Read it from response
  headers or a quota endpoint `[U]`.

### 5.9 Docs
- `GLOSSARY.md:8-23` has terms for provider inference credential, cloud
  credential chain, BYOK, and credential material. Add "subscription
  credential" and "OAuth grant source".
- `docs/OPERATOR-GUIDE.md` needs a section with the supported providers and an
  explicit statement of what is not supported, and why.

## 6. The three options

### Option 1: CPA as a sidecar upstream (config only)
- **How:** run CPA, then set `STARPORT_OPENAI_INFERENCE_BASE_URL=http://127.0.0.1:8317`
  (CPA default port, `config.example.yaml:30`) and `OPENAI_API_KEY=<a CPA
  client key>`. Starport accepts an `http` origin on loopback or a private
  network (`docs/OPERATOR-GUIDE.md:1190-1216`). The same works for Anthropic
  through `STARPORT_ANTHROPIC_INFERENCE_BASE_URL`.
- **Limits:**
  - The override works with environment credentials only. Gateway and BYOK
    credentials always go to the catalog origin (`OPERATOR-GUIDE.md:1218-1221`).
  - It replaces the origin for the whole provider in the deployment.
  - The model IDs must match Starport catalog IDs `[U]`.
  - Starport cannot see the subscription accounts, refresh, or quota.
- **Effort:** 0 code PRs. One docs PR if we document it.
- **Verdict:** it works today, without any change. **Do not document it as a
  supported feature.** Users would read it as an endorsement of the CPA
  disguise paths for Claude and Google. At most, add a neutral FAQ line: "any
  OpenAI-compatible relay works through the approved-origin setting".

### Option 2: Import CPA `sdk/` as a Go dependency
- **Effort:** 4-6 PRs to adapt, plus continuing upgrade work for each CPA v8.x
  release.
- **Verdict:** rejected.
  - The useful parts are `internal/` and cannot be imported (section 3).
  - The only real embed is a second gin proxy inside Starport.
  - It adds a large dependency tree.
  - It brings the disguise code into the AGPL binary that Starport ships.

### Option 3: Native implementation in the Starport credential seam (recommended, OpenAI first)
- **Scope:** a provider-neutral OAuth grant source, then one official
  provider: OpenAI "Sign in with ChatGPT".
- **Not in scope:** Claude and Google consumer OAuth, the uTLS fingerprint
  copy, and User-Agent or system-prompt disguise.
- **Effort:** 7-9 PR-sized tasks (section 7).

## 7. Recommended phased path

**Phase 0: verify (no code)**
- Read the official OpenAI docs for "Sign in with ChatGPT". Find these facts:
  - the registration flow (dynamic client registration),
  - the device-flow support,
  - the inference endpoint and its API shape (Responses or Chat Completions),
  - the account header,
  - the quota signals,
  - the terms for hosted, multi-user gateways compared with local personal
    use. A shared gateway that serves many users from one person's plan can
    be outside the terms even on the official path `[U]`.
- Record the result as a decision in `docs/`. The verdict on Claude and Google
  is "not supported", with the reasons.

**Phase 1, PR 1 (first concrete PR): the `oauthgrant` credential source in Starport**
- Add a concept package `internal/credentials/oauthgrant`. It contains:
  - The RFC 8628 device flow (start, then poll) and the RFC 6749
    refresh-token grant with refresh-token rotation.
  - Singleflight refresh for each credential, and backoff on `429`, `5xx`,
    and `Retry-After`.
  - A token-set type that resolves to renewable `credentials.Material`
    (`ExpiresAt`, `Lease.RefreshAfter`).
- No vendor endpoints are in code. The issuer, the client, and the scopes come
  from catalog profile options.
- Tests with an `httptest` fake authorization server cover these cases:
  - success,
  - `authorization_pending` and `slow_down`,
  - `expired_token`,
  - a rotated refresh token,
  - concurrent refresh, which must make one upstream call,
  - an `invalid_grant` that ends in a terminal "re-login required" state.
- Acceptance: `go test ./internal/credentials/oauthgrant/...` passes, the
  package has no `internal/providers` import, and `GLOSSARY.md` has the new
  term.

**Phase 1, more PRs**
- PR 2 (Starmap): add the `oauth-grant` primitive and its `ProtocolOptions`
  (issuer, registration, scopes, account-header field), then regenerate the
  catalog.
- PR 3: keyring write-back. Store the refresh token in the encrypted
  repository and update it with CAS on rotation. Then wire the source into
  `managedMaterials`.
- PR 4: add the `oauth-grant` applicator in `providers/auth/request.go`.
  Include placement tests.
- PR 5: control-plane login endpoints (start, status, cancel) for shared
  credentials and BYOK. The login state is short-lived and server-side.
  Include audit events.
- PR 6: console sign-in panel. Use shadcn and Base UI. It shows the device
  code, polls the status, shows the account, the expiry, and the re-login
  state, and has vitest tests.

**Phase 2: the OpenAI provider (only after Phase 0 confirms the terms)**
- PR 7: the Starmap provider entry for the ChatGPT plan.
- PR 8: a Responses API connector, if the backend needs one.
- PR 9: quota-window projection and the console chart.

## 8. Risk register

**License**
- Risk: **Low**. CPA and the Management Center are MIT. Both are compatible
  with AGPL-3.0. If we copy code, keep the MIT notice. Option 3 copies no
  code.

**Anthropic ToS**
- Risk: **High**.
- Anthropic updated its docs on 2026-02-19: Free, Pro, and Max OAuth tokens
  are for Claude Code and Claude.ai only. Use in other tools breaks the
  consumer terms. Enforcement started on 2026-01-09 `[U: press reports]`.
- CPA makes it work only through the disguise.

**Google ToS**
- Risk: **High**.
- Reports from February and March 2026 describe mass `403 ToS` suspensions of
  Antigravity and Gemini CLI accounts that went through third-party proxies,
  paid Ultra accounts included `[U: forum and press reports]`.
- CPA embeds a client secret that belongs to Google's Antigravity client.

**OpenAI ToS**
- Risk: **Medium, then Low** if we use the official path.
- Sign in with ChatGPT (2026-09-29) is the official path `[U]`.
- Reuse of the Codex CLI client ID and the `codex-tui` User-Agent, which CPA
  does, is not the official path.

**Account bans**
- Risk: **High** for Claude and Google, which ban the end user's account, not
  Starport's.
- Starport's support and reputation take the cost.

**Token security**
- Risk: **Medium**.
- A refresh token is a long-lived credential for a consumer account. Store it
  encrypted (the keyring already does), never log it, and do not resolve it
  for many tenants.
- CPA itself stores tokens as plaintext files and loads its UI from a fallback
  URL with no digest check (`managementasset/updater.go:294-301`). Do not run
  CPA next to production Starport.

**Maintenance velocity**
- Risk: **High** for Options 1 and 2.
- CPA is at major version v8, its docs are stale (v6), and the disguise must
  follow every official CLI release. The code pins versions such as Claude
  Code 2.1.220/2.1.280 and codex-tui 0.154.0.
- The risk is Low for Option 3 because the source uses only standard OAuth.

**Plugins and safe mode**
- Risk: **N/A** for Starport.
- CPA loads native dynamic-library plugins through a C ABI
  (`internal/pluginhost`, `examples/plugin/README.md`) and installs them from
  a plugin store (`/plugins/store/<id>/install`). Native code has full process
  trust.
- `internal/safemode` only blocks the proxy endpoints while `api-keys` still
  holds template values (`internal/api/server_middleware.go:36-90`).
- Do not copy this model.

**Multi-user use of one personal plan**
- Risk: **High** and `[U]`.
- A gateway that shares one subscriber's plan with many accounts is probably
  outside every consumer plan's terms, even on an official path.
- Limit subscription credentials to BYOK, which is per account, by default.

## 9. Not checked
- I did not open the Management Center repository or any community dashboard.
  The stack and license come from the GitHub page.
- I did not read the primary terms documents. The ToS claims come from web
  search summaries.
- I did not read the official OpenAI "Sign in with ChatGPT" documentation.
- I did not run any OAuth flow or any test in either repository. I did not
  build CPA.
- I did not check the xAI, Devin, Kimi, or Meta terms.
- I did not check whether the CPA model IDs match the Starport catalog IDs for
  Option 1.
- I did not inspect `internal/providers/byok` storage in depth.

Sources: [GIGAZINE on Anthropic ban](https://gigazine.net/gsc_news/en/20260220-anthropic-third-party-block),
[WinBuzzer](https://winbuzzer.com/2026/02/19/anthropic-bans-claude-subscription-oauth-in-third-party-apps-xcxwbn/),
[Google AI forum: mass 403 ToS bans](https://discuss.ai.google.dev/t/urgent-mass-403-tos-bans-on-gemini-api-antigravity-for-open-source-cli-users-paid-tier/124508/7),
[yage.ai on Sign in with ChatGPT](https://yage.ai/share/chatgpt-subscription-oauth-en-20261013.html),
[The New Stack](https://thenewstack.io/sign-in-with-chatgpt/),
[Management Center repository](https://github.com/router-for-me/Cli-Proxy-API-Management-Center).
