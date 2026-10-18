# Starport infra audit (2026-10-15, read-only)

Source checkout: /private/tmp/starport-launch at b310cad7 (`v1.3.0-39-gb310cad7`).
Workflow paths below are relative to that checkout.

## Owner-only actions required (with exact UI/CLI steps)

1. **Site deploy secrets (blocker for automated deploys).** Environment `starport-site`
   has 0 secrets and 0 variables. The Deploy job is NOT skipped when secrets are missing:
   it runs and fails. Evidence: manual dispatch run 38274256026 (2026-10-12) failed at
   "Deploy to Cloudflare" with wrangler "it's necessary to set a CLOUDFLARE_API_TOKEN".
   - Cloudflare dashboard > My Profile > API Tokens > Create Token > template "Edit Cloudflare
     Workers"; scope Account = AgentStation, Zone = agentstation.ai.
   - `gh secret set CLOUDFLARE_API_TOKEN --env starport-site -R agentstation/starport`
   - `gh secret set CLOUDFLARE_ACCOUNT_ID --env starport-site -R agentstation/starport`
2. **Environment reviewers.** `starport-site` has `protection_rules: []` and
   `deployment_branch_policy: null`, but site.yaml:14 says the maintainer creates it "with
   required reviewers". UI: Settings > Environments > starport-site > Required reviewers (add
   owner); Deployment branches and tags > Selected > add tag rule `v*` and branch `main`.
   Note: a deployment-tag policy blocks `workflow_dispatch` from a non-main ref only; the
   job ref is the dispatching ref (main), so `main` must be allowed.
3. **Redeploy the site after secrets exist.** The deployed site is not the v1.3.0 tag
   (see Raw evidence H4). The v1.3.0 Release run (2026-10-06) predates the `workflow_run`
   trigger (#458, merged 2026-10-14), so no Deploy ever ran for it. Choose one:
   - Deploy the tag: `gh workflow run site.yaml -R agentstation/starport -f ref=v1.3.0`.
     This REGRESSES the live splash (removes the #427 scroll journey, which is post-tag).
   - Recommended: cut v1.3.1 (or v1.4.0) from main; the tag push now chains
     Release -> Site Deploy automatically.
4. **macOS signing secrets (direct-download Gatekeeper failure).** release.yaml:197-201 and
   .goreleaser.yaml:115 need five secrets. Repo has only `MACOS_NOTARY_KEY`,
   `MACOS_NOTARY_KEY_ID` (+ `HOMEBREW_TAP_DEPLOY_KEY`); org shares only `CODECOV_TOKEN`,
   `HOMEBREW_TAP_TOKEN`. Missing: `MACOS_SIGN_P12`, `MACOS_SIGN_PASSWORD`,
   `MACOS_NOTARY_ISSUER_ID`. Result: v1.3.0 darwin binary is `Signature=adhoc`,
   `TeamIdentifier=not set`, `spctl` = rejected. A browser-downloaded archive will be blocked.
   `gh secret set MACOS_SIGN_P12 -R agentstation/starport < cert.p12.b64` (and the other two).
5. **Repo security settings (all disabled).** Settings > Code security:
   - Secret scanning: Enable; Push protection: Enable.
   - Dependabot security updates: Enable (alerts are already on: 204).
   - Private vulnerability reporting: Enable (currently `false`; SECURITY.md exists).
   - CodeQL default setup: `not-configured`. Enable "Default" (languages detected: actions,
     go, javascript-typescript, python). Or `gh api -X PATCH .../code-scanning/default-setup -f state=configured`.
6. **Repo metadata.** Topics are `[]`. Add, e.g.:
   `gh repo edit agentstation/starport --add-topic llm-gateway,openrouter,openai-compatible,ai-gateway,golang,self-hosted`.
   Consider `has_discussions` on if you want a support channel (currently false).
   `delete_branch_on_merge=false` (cosmetic; branch clutter visible to the public).
7. **Edge HTTPS.** `http://starport.agentstation.ai` returns `200` (no redirect) and no
   `Strict-Transport-Security` header on HTTPS. Cloudflare > agentstation.ai > SSL/TLS >
   Edge Certificates: enable "Always Use HTTPS"; enable HSTS (start max-age 6 months, no
   preload until verified across subdomains).
8. **Tag protection.** No rulesets (`[]`); legacy tag protection API 404 (retired). Anyone
   with write can push `v*` and start Release (gate requires tag == main head, which limits
   damage). Settings > Rules > Rulesets > New tag ruleset: target `v*`, restrict creation/
   deletion/update to admins, bypass = repo admin.
9. **Main CI is red.** 4 of the last 6 ci.yml runs on main failed (flakes: macOS native tests,
   windows-11-arm native tests, Application Recovery). The README CI badge (README.md:4)
   shows "failing" at launch. Not owner-only, but needs a decision before launch day.

## Fixable in-repo (file:line + proposed change)

- `.github/workflows/site.yaml:3-15` header: say a missing secret fails Deploy (not skip),
  and that a Release which predates #458 never auto-deploys. Optional: add a first step in
  `deploy` that fails fast with an explicit message when `CLOUDFLARE_API_TOKEN` is empty.
- `.github/workflows/site.yaml:106-110` `if`: correct as written. `workflow_run` uses the
  default-branch site.yaml; `workflow_run.event == 'push'` excludes Release recovery
  dispatches; `conclusion == 'success'`; checkout uses `workflow_run.head_sha` (= tag commit,
  because release.yaml:74-77 forces tag == main head). No defect found.
- `.github/workflows/site.yaml:118` with `workflow_dispatch ref=v1.3.0`: builds website/ as
  of v1.3.0, which lacks #427 and later site work (25 files, +7949/-148 in website/ since
  the tag). It is a rollback, not a refresh. Document this in the header.
- `.github/workflows/update-copyright-year.yml`: only run ever (2026-01-01) failed on the
  former `master` branch. Next fire 2027-01-01. Check that it still works on `main` or delete
  it (low priority; cause of failure not inspected).
- `website/`: `/sitemap.xml` returns 404. Add `src/app/sitemap.ts` (Next static export
  supports it) so search engines index /docs. `/llms.txt` 404 (optional; fits an AI tool).
- `go.mod:11` pins Starmap pseudo-version `v0.17.1-0.20261009150341-120a49889009`
  (32 commits ahead of v0.17.0). verify-v1-release.sh:57 accepts pseudo-versions, so the next
  release will ship and the site footer will print a pseudo-version as "Starmap <ver>"
  (website/scripts/release-facts.mjs:51-53). Recommend: tag Starmap v0.17.1 first and pin it.
- `.github/workflows/docs-pages.yaml`: dormant (dispatch only; Pages off, 404). Two docs
  hosts become possible if someone runs it. Recommend delete it, or state in its header that
  starport.agentstation.ai/docs is canonical. No conflict today.

## Verified OK

- Repo public, default `main`, license AGPL-3.0, homepage https://starport.agentstation.ai,
  description set, wiki off, issues on. Community profile 87 % (README, CoC, CONTRIBUTING,
  LICENSE, PR template, SECURITY.md, issue templates in .github/ISSUE_TEMPLATE).
- Branch protection on main: required checks Build, Security Scan, Release Snapshot, Action
  Pin Provenance (all exist in ci.yml:725,771,893,1009); strict; linear history; conversation
  resolution; enforce_admins; no force push/deletion; 0 required approvals with stale-review
  dismissal; signatures not required.
- Workflow token default `read`; Actions cannot approve PRs. Immutable releases enabled.
- Dependabot: github-actions, gomod, npm(/console) weekly, grouped. Alerts enabled.
  NOTE: no `website/` npm entry; website deps get no Dependabot updates (add one).
- Releases: v1.3.0 Latest, `immutable: true`, not draft; 12 assets: checksums, docs tarball,
  darwin_arm64, linux_arm64, linux_x86_64, windows_arm64, windows_x86_64 + SBOMs. These match
  README.md:63 supported targets.
- Attestations: `gh attestation verify` exit 0 for checksums.txt and
  `oci://ghcr.io/agentstation/starport:1.3.0`.
- GHCR: anonymous pull token works; manifests `1.3.0` and `latest` return 200 (package is
  public). Tags include 1.1.0..1.3.0 with and without `v`.
- Homebrew: agentstation/homebrew-tap public; Casks/starport.rb `version "1.3.0"`.
- Release chain: tag push `v*` -> Release Gate -> Assemble/Verify/Publish -> Homebrew publish
  -> verify-homebrew (ubuntu+macOS). Last 4 tag runs success (v1.1.0 had one failed then
  recovered).
- Hosting: A records 172.67.219.23 / 104.21.62.26 (Cloudflare proxied, no CNAME; Worker custom
  domain per website/wrangler.jsonc). TLS Google Trust Services WE1, SAN includes
  starport.agentstation.ai and *.starport.agentstation.ai, expires 2027-01-04 (auto-renew).
  `/` 200, `/docs` 200, `/docs/start` 200, `/docs/` 307 -> /docs, `/nonexistent` 404 (custom
  404 page), `/favicon.svg` 200, `/demo/first-use.gif` 200.
- No Web Analytics beacon in the served HTML (no `cloudflareinsights` match).
- Starmap: repo public; release v0.17.0 public (2026-10-06); runtime channel is branch
  `catalog/v1` (internal/config/catalog.go:46-49), last commit 2026-10-15T00:43Z.
- Org: verified domain, blog https://agentstation.ai, twitter agentstationai, 2 public
  members, `.github` repo with profile/README.md (2724 bytes). No separate site repo.
- `www.starport.agentstation.ai`, `docs.*`, `api.*`: no DNS records; no repo reference
  to them (only https://starport.agentstation.ai is referenced). Nothing to fix.
- `https://agentstation.ai` 301 -> https://www.agentstation.ai/.

## Raw evidence (trimmed)

R1 `gh api repos/agentstation/starport` -> visibility public, default_branch main,
   has_discussions false, has_wiki false, topics [], security_and_analysis: dependabot_security_updates
   disabled, secret_scanning disabled, push_protection disabled.
R2 `.../branches/main/protection` -> checks [Build, Security Scan, Release Snapshot, Action Pin
   Provenance], strict true, approvals 0, enforce_admins true, linear true; required_signatures false.
R3 `.../code-scanning/default-setup` -> state not-configured.
R4 `.../vulnerability-alerts` -> 204. `.../private-vulnerability-reporting` -> enabled false.
R5 `.../rulesets` -> []. `.../tags/protection` -> 404.
R6 `.../environments/starport-site/secrets --jq .total_count` -> 0; protection_rules [];
   repo secrets [HOMEBREW_TAP_DEPLOY_KEY, MACOS_NOTARY_KEY, MACOS_NOTARY_KEY_ID];
   org secrets visible [CODECOV_TOKEN, HOMEBREW_TAP_TOKEN].
R7 `.../pages` -> 404 (Pages off).
R8 `.../releases/tags/v1.3.0` -> immutable true, draft false, 12 assets (listed above).
R9 `gh api orgs/agentstation/packages?package_type=container` -> 403 (token lacks
   read:packages); used anonymous GHCR token instead -> manifests 200.
R10 Site runs by event: pull_request 34, push 15, workflow_dispatch 1 (38274256026, failure,
    Deploy step "Deploy to Cloudflare": CLOUDFLARE_API_TOKEN empty). workflow_run: 0.
R11 ci.yml main last 10: failures 38744546899 (Application Recovery), 38714342063 (Test macos-15),
    38699687968 (Test windows-11-arm), 38650754120, 38561204482; successes 38728610629, 38633794332.
R12 release.yaml runs: v1.3.0 success 2026-10-06; v1.2.1, v1.2.0, v1.1.0 success.
R13 codesign -dv starport (v1.3.0 darwin_arm64) -> flags adhoc,linker-signed; Signature=adhoc;
    TeamIdentifier=not set; `spctl --assess --type execute` -> rejected.
H1 dig +short starport.agentstation.ai -> 172.67.219.23, 104.21.62.26; CNAME empty.
H2 curl -sI https:// -> HTTP/2 200, server cloudflare, cf-cache-status HIT,
   cache-control "public, max-age=0, must-revalidate", no strict-transport-security.
H3 curl -sI http:// -> HTTP/1.1 200 OK (no redirect).
H4 Served HTML footer: "This build: release v1.3.0 · Starmap v0.17.0". Served HTML contains
   `#afterword`; `git grep -l afterword v1.3.0 -- website` -> 0 files; on main -> 4 files;
   introduced by 06542922 (#427, 2026-10-06, after tag). So the live site was a manual deploy
   from a post-tag main commit (before the go.mod Starmap bump), labelled v1.3.0.
H5 Paths: /docs 200, /docs/ 307, /docs/start 200, /nonexistent 404, /install.sh 404 (README
   has no install.sh; OK), /robots.txt 200 (Cloudflare managed content-signals only, no
   Sitemap line), /sitemap.xml 404, /llms.txt 404.
H6 openssl -> issuer Google Trust Services WE1, notAfter Jan 4 04:41:54 2027 GMT.
S1 go.mod:11 starmap v0.17.1-0.20261009150341-120a49889009; v1.3.0:go.mod -> v0.17.0;
   compare v0.17.0...120a4988 -> ahead_by 32.
O1 gh api orgs/agentstation -> blog https://agentstation.ai, is_verified true, public_repos 24;
   public_members 2; .github/profile/README.md 2724 bytes; homebrew-tap public.

## Not checked / uncertain

- Package settings page (visibility, linked repo) via API: 403, so visibility is inferred from
  anonymous pull only.
- Cloudflare zone settings (Always Use HTTPS, HSTS, Web Analytics) not read; inferred from
  HTTP responses only. Did not run wrangler.
- Cause of the 2026-01-01 copyright workflow failure not read.
- Whether Homebrew 6 strips quarantine for the unsigned cask on user machines: release.yaml
  verify-homebrew checks this on a runner only.
- Did not download Linux/Windows archives (only darwin_arm64, 35 MB).
