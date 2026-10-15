# LR6 critique 1: Starport launch readiness plan

Scope: `docs/plans/launch-readiness-plan.html` (699 lines) in the `/private/tmp/starport-launch` worktree, its two verifiers, the audit files, and open PRs #464, #465, #466, and #467. The checks were read-only. I ran `gh` GET calls against `agentstation/starport` and `curl` against `https://starport.agentstation.ai` on 2026-10-15.

Counts: 3 blocker, 19 should, 10 nice.

## Findings

### 1. Blocker. verify.sh check 4 fails permanently after LR13 step 2, so the LR15 "verify.sh 14 passed" acceptance cannot pass.

Evidence: `verify.sh:23-30` passes only when the lines under `## Unreleased` include a `- ` bullet. LR13 step 2 (plan L571) renames `## Unreleased` to `## v1.4.0` and adds an empty Unreleased heading. LR15 (plan L603) then requires 14 passed.

Proposed change, verify.sh check 4: "Pass when `## Unreleased` has a bullet. Also pass when `## v1.4.0` has a `### Breaking changes` heading with a bullet below it." Add to LR15: "Run verify.sh at the tag commit and on main. Record both outputs."

### 2. Blocker. On launch day the live site and README still show the v1.3.0 recording with `starport init` and "temporary gateway".

Evidence: `website/scripts/copy-assets.mjs:14-15` copies the first-use-v1.3.0 GIF and poster to `/demo/`. `journey.tsx:971` says "starts a temporary gateway". `docs/assets/first-use-v1.3.0/events.json:191` shows "starport init → starport serve". LR8 runs after the tag (plan L497). The site deploys only from a successful Release push run (`site.yaml` deploy `if`). The announce gate (checklist row 9, L635) does not include LR8. The PR #466 CHANGELOG line 165 also says that the recording "comes from the attested v1.3.0 archive".

Proposed change, LR8 and the checklist: "LR8 is an announce gate. After the LR8 pull request merges, tag v1.4.1 at the new main head. The Site workflow then deploys the new recording. Announce only after the v1.4.1 Site run succeeds." Add to LR8 step 2: "Change the CHANGELOG recording line to name the v1.4.0 archive." Move checklist row 8 above row 9, and add LR8 to the LR15 record list.

### 3. Blocker. The LR13 dependencies omit LR9, LR10, and LR14, but the tag ships or deploys their results.

Evidence: LR13 step 1 (L570) waits for LR1, LR2, LR3, and LR7 only. Step 4 needs the LR9 environment, or the Site deploy fails as run 38274256026 did. Step 5 needs LR10 for spctl. LR14 changes `LICENSE` and `Dockerfile:56`, which the tag freezes into the release archives and the image label.

Proposed change, LR13 step 1: "Wait for LR1, LR2, LR3, LR7, and LR14 to merge. Confirm that conditions 1, 2, and 9 pass. Confirm that main CI is green." If the owner defers LR10, add: "Without LR10, remove the spctl check from step 5. Keep the README quarantine note."

### 4. Should. The PR #466 README text "The macOS release binary is not yet notarized" becomes false when LR10 lands before the tag.

Evidence: `pr466.diff:212` adds the sentence and an `xattr` command. LR10 (L520-533) makes the v1.4.0 darwin binary notarized. No task owns the removal.

Proposed change, LR10: "Add step 6. An agent removes the not-notarized paragraph from README before the tag. If LR10 does not land, keep the paragraph."

### 5. Should. The Release fails after publish if the Developer ID certificate belongs to a team other than 244SS6JJ8Q.

Evidence: the `verify-homebrew` macOS job in `release.yaml` requires `TeamIdentifier=244SS6JJ8Q` when the binary has a Developer ID signature. This job runs after the immutable publish. A failed Release run also means that no Site deploy starts. LR10 step 1 (L525) does not name the team.

Proposed change, LR10 step 1: "Export the Developer ID Application certificate of team 244SS6JJ8Q as a .p12 file. Confirm that the App Store Connect key belongs to the same team."

### 6. Should. The plan has no tag-day recovery, rollback, or merge freeze.

Evidence: the Release Gate tests `"$TAG_SHA" = "$(git rev-parse origin/main)"`. A "re-run all jobs" fails after any later merge to main. Releases are immutable, so a bad v1.4.0 cannot be replaced. `site.yaml` deploys only after a Release run with event push and conclusion success.

Proposed change, add LR13 step 0 and a "Recovery" paragraph: "Freeze merges to main from the tag push until Release and Site succeed. If a job fails after the gate, use Re-run failed jobs. If the Site run does not start, run `gh workflow run site.yaml -R agentstation/starport -f ref=v1.4.0`. A bad release rolls forward to v1.4.1 on a new commit. A bad cask rolls back with a revert in agentstation/homebrew-tap."

### 7. Should. A second version tag on the v1.4.0 commit breaks the Site release facts and `git describe --exact-match`.

Evidence: the `site.yaml` "Set release facts" step requires exactly one version tag at HEAD. The release metadata step uses `git describe --tags --exact-match`. Finding 2 and finding 6 both add tags. Neither the plan nor the workflow stops a second tag on the same commit.

Proposed change, LR13: "Put each new version tag on its own commit. Never tag a commit that already has a version tag."

### 8. Should. The v1.4.0 GitHub release notes do not show the breaking changes.

Evidence: `.goreleaser.yaml:143-170` uses `changelog.use: github`, which lists commits and excludes docs, test, and chore commits. `CHANGELOG.md:3` confirms that the release carries the generated commit list. The breaking changes (port 7827, `auth bootstrap`, persistent `dev`) appear only in CHANGELOG.md.

Proposed change, LR2: "Add a release header that links `https://github.com/agentstation/starport/blob/{{ .Tag }}/CHANGELOG.md`. The header says that v1.4.0 has breaking changes and that the CHANGELOG lists them."

### 9. Should. Condition 13 passes on a site that still shows v1.3.0 facts and the old recording.

Evidence: `verify-launch-state.sh:40-42` only tests that `127.0.0.1:8080` and `Temporary by default` are absent. It does not test the footer release, port 7827, the sitemap, or the og image. Live today: footer "release v1.3.0", `/sitemap.xml` 404.

Proposed change, condition 13: "Require `release <code>v1.4.0` (or the final tag) in the footer, `7827` present, and `temporary gateway` absent. Require `/sitemap.xml` status 200 and an `og:image` meta tag." Add condition 17: "The agentstation/homebrew-tap cask version equals 1.4.0."

### 10. Should. Condition 16 reads a run that is still in progress.

Evidence: `verify-launch-state.sh:52` runs `gh run list -w ci.yml -b main -L 1 --json conclusion` with no status filter. An in-progress run has an empty conclusion, so the check fails right after any merge.

Proposed change, condition 16: "Add `--status completed` to the `gh run list` call."

### 11. Should. The LR8 owning paths name a file that pins nothing and omit the files that pin v1.3.0.

Evidence: `website/scripts/release-facts.mjs` names v1.3.0 only in a comment. The real pins are `website/scripts/copy-assets.mjs:14-15`, `website/src/lib/splash.ts:32`, `journey.tsx:971,976`, `docs/assets/starport-demo.json:5-8`, and `website/README.md:27`. The `site.yaml` path filters still name `docs/assets/first-use-v1.2.0/**`.

Proposed change, LR8 owning paths: "`docs/assets/first-use-v1.4.0/`, `README.md:12-16,36`, `website/scripts/copy-assets.mjs`, `website/src/lib/splash.ts`, `website/src/components/journey/journey.tsx`, `docs/assets/starport-demo.json`, `website/README.md`, and the `site.yaml` path filters."

### 12. Should. The LR8 acceptance `grep -c init` also counts words such as "initialized".

Evidence: plan L504. `internal/cli/credential.go:54,62` prints "Initialized Starport" text that a recording can capture.

Proposed change, LR8 verification: "`grep -c 'starport init' docs/assets/first-use-v1.4.0/events.json` equals 0."

### 13. Should. The LR16 acceptance fails because PR #466 links the plan from TASKS.md.

Evidence: `pr466.diff:312` adds "The [launch readiness plan](plans/launch-readiness-plan.html) is the only active plan." LR16 (L610) owns only the plan, the proof root, and `docs/plans/README.md`. `git grep launch-readiness` then still matches `docs/TASKS.md`.

Proposed change, LR16 owning paths: "Add `docs/TASKS.md`. Remove the plan link from it in the cleanup pull request."

### 14. Should. LR7 leaves the tag commit open, keeps a stale CHANGELOG line, and runs too few tests.

Evidence: plan L484 says "at or after 120a49889009". Starmap main is 67 commits past v0.17.0, and 120a4988 is 32 past. A later tag pulls in 35 unreviewed Starmap commits on launch week. `pr466.diff:171` says the dependency "moves from v0.17.0 to main commit `120a49889009`". Plan L486 tests only two packages.

Proposed change, LR7: "Tag Starmap v0.17.1 exactly at 120a49889009. The bump then changes only the version name. Change the CHANGELOG line to name v0.17.1. Run `go test ./...`."

### 15. Should. The PR #466 issue chooser links a private advisory form that does not work until LR11 runs.

Evidence: `pr466.diff:75` links `security/advisories/new`. Private vulnerability reporting is off today (verify-launch-state condition 6 fails). `SECURITY.md` names email only.

Proposed change, checklist and LR1: "Run LR11 step 2 before the LR1 pull request merges. Add the advisory form to SECURITY.md as the first report path."

### 16. Should. The owner checklist puts the long-lead items late.

Evidence: checklist rows 5 and 6 (L631-632) are LR7 and LR14. Each needs an owner action and then an agent pull request before the tag.

Proposed change, checklist order: "1 LR7 Starmap tag. 2 LR14 license decision. 3 LR11. 4 LR9. 5 LR10. 6 LR12. 7 LR13. 8 LR8. 9 announce." Add: "Start rows 1 and 2 first, because agent pull requests follow them."

### 17. Should. verify.sh check 5 misses plan jargon in published docs.

Evidence: `verify.sh:31` tests only `CSP22` in README and `D42` in CHANGELOG. `docs/site/architecture/recipes.md:168` still says "CSP13" on the published site. OPERATOR-GUIDE still has six CSP references.

Proposed change, check 5: "`! git grep -qE 'CSP[0-9]+' -- README.md CHANGELOG.md docs/site`." LR1 then removes the remaining matches.

### 18. Should. A published docs page says that the public host is not enabled.

Evidence: `docs/site/operate-starport/upgrades.md:105-145` says "The public host is not yet enabled" and gives docs-pages steps. The site publishes this page at `/docs`. This contradicts D4.

Proposed change, LR1 owning paths: "Add `docs/site/operate-starport/upgrades.md`. Name https://starport.agentstation.ai/docs as the documentation host. Remove the docs-pages steps."

### 19. Should. A user with a usage question has no channel.

Evidence: `has_discussions` is false. `.github/ISSUE_TEMPLATE/config.yml` sets `blank_issues_enabled: false`. The only templates are bug_report.md and feature_request.md. CONTRIBUTING tells users to open an issue.

Proposed change, LR1: "Add a question issue template. If the owner enables Discussions in LR11, link Discussions from the chooser instead."

### 20. Should. The LR14 acceptance contradicts its own step, and no verifier condition proves LR14.

Evidence: plan L587 says "restore the verbatim license appendix". Plan L590 accepts a diff that is "empty except the appendix fill-in". The checklist proof (L632) is "Labels agree", which no script checks.

Proposed change, LR14 acceptance: "`diff` of LICENSE against the upstream AGPL-3.0 text is empty. Dockerfile, SKILL.md, and README name the same SPDX identifier." Add a verify.sh condition for the identifier match.

### 21. Should. No condition proves that the public catalog channel is fresh.

Evidence: the Starmap public source reads `catalog/v1` through api.github.com every hour. The embedded fallback hides a stale channel from new users. The CAT campaign had scheduled generation failures on 2026-10-10 to 14.

Proposed change, verify-launch-state.sh: "Add a condition. `gh api repos/agentstation/starmap/commits/catalog/v1 --jq .commit.committer.date` is less than 26 hours old."

### 22. Should. LR13 step 6 has no command, and nothing gates its result.

Evidence: plan L575 says "Run `native-release.yaml` with tag v1.4.0". The input default is a fixed `v1.3.0` after PR #464 (`pr464.diff:47`). LR15 does not record the run.

Proposed change, LR13 step 6: "`gh workflow run native-release.yaml -R agentstation/starport -f tag=v1.4.0`." Add to LR15: "Record the native-release run. Its conclusion is success."

### 23. Nice. The LR13 step 5 brew command does not refresh the tap or name the full cask.

Evidence: plan L574 says `brew upgrade --cask starport` prints 1.4.0. `brew upgrade` prints no version.

Proposed change: "`brew update && brew upgrade --cask agentstation/tap/starport && starport --version` prints 1.4.0. Also test a fresh install on a second Mac."

### 24. Nice. The LR13 step 3 tag command depends on a local main that can be stale.

Evidence: plan L572 uses `<main head>`. The Release Gate compares with origin/main.

Proposed change: "`git fetch origin main && git tag -s v1.4.0 -m 'Starport v1.4.0' origin/main && git push origin v1.4.0`."

### 25. Nice. The LR11 tag ruleset names no bypass actor.

Evidence: plan L545 restricts create, update, and delete to admins, but a ruleset restricts everyone except its bypass list.

Proposed change: "Add the Repository admin role to the bypass list with mode Always."

### 26. Nice. The resume state and the baseline row are stale.

Evidence: plan L173 names PR #466 head 4a81e8ee. The head is now 045e1ec2. PR #467 is now 132673c8. Plan L176 says 0/16, but LR5 run 38744546899 now passes, so one condition holds.

Proposed change: "Update the PR heads. Set the baseline row to 1/16 and cite run 38744546899."

### 27. Nice. The LR3 owning paths do not match PR #465.

Evidence: plan L425 names `opengraph-image.tsx` or `public/og`. PR #465 adds `website/src/app/og/starport.png/route.tsx`, `robots.ts`, `sitemap.ts`, and `website/src/lib/social-card.ts`, and changes `layout.shared.tsx`.

Proposed change: "List the five paths that PR #465 adds or changes."

### 28. Nice. PRs #466 and #467 both edit `docs/plans/README.md`.

Evidence: both diffs change the index file. The second merge gets a conflict.

Proposed change, resume state: "Merge #467 first. Then rebase #466."

### 29. Nice. The LR10 owning path line numbers are wrong.

Evidence: plan L522 cites `.goreleaser.yaml:117-131`. The notarize block is at `.goreleaser.yaml:112-127`.

Proposed change: "`.goreleaser.yaml:112-127`."

### 30. Nice. Two verifier conditions are weaker than their names.

Evidence: condition 9 (`verify-launch-state.sh:29-30`) reads repository secrets only. Condition 1 uses jq `contains`, which matches substrings of names.

Proposed change: "Compare exact names with `index(...)` or `all(...)`. State in the condition 9 name that it reads repository secrets."

### 31. Nice. The site has no `/.well-known/security.txt`.

Evidence: `curl -s -o /dev/null -w '%{http_code}' https://starport.agentstation.ai/.well-known/security.txt` returned 404.

Proposed change, LR3: "Add `website/public/.well-known/security.txt` with the SECURITY.md contact and the advisory form URL."

### 32. Nice. The plan has no post-launch watch.

Evidence: the ledger ends at LR16 cleanup. No row owns the first-week issues, the catalog channel, or the site status.

Proposed change: "Add a post-launch row. For seven days, triage new issues each day and rerun verify-launch-state.sh."

## Confirmed correct

- LR5: run 38744546899, attempt 2, completed with success at b310cad7.
- F6: run 38274256026 is a Site workflow_dispatch that failed on 2026-10-12. The `starport-site` environment has 0 secrets, no protection rules, and no branch policy.
- F7: the repository secrets are only HOMEBREW_TAP_DEPLOY_KEY, MACOS_NOTARY_KEY, and MACOS_NOTARY_KEY_ID.
- F8: all security_and_analysis features are disabled. Topics are empty. There are 0 rulesets. Discussions are off. The description overclaims.
- D3: jackspirou and savkat both have admin access.
- v1.3.0 has 12 assets. Release run 37437368384 succeeded. The cask is 1.3.0 with a GitHub homepage. PR #458 merged at 2026-10-14T20:38Z.
- Live site: six `127.0.0.1:8080` matches, "Temporary by default", footer "release v1.3.0", no HSTS, http 200 with no redirect, `/sitemap.xml` 404, and `robots.txt` 200 with Cloudflare content signals only.
- Starmap release.yaml requires only that the tag is an ancestor of main, so a v0.17.1 tag at 120a4988 is valid.
- The Release Gate accepts `-rc.N` tags. `record-readme-demo.sh` accepts an rc tag.
- The homebrew-tap `.claude/settings.local.json` allows only `git add`, so N6 has no effect.
- verify.sh resolves the repository root correctly. The task-to-condition map covers all 14 conditions. LR11 covers seven conditions.
- The LR11 `gh api` nested `-f` syntax and the PVR and CodeQL endpoints are valid.
- The ghcr image name in `README.md:364-372` matches the release workflow.
