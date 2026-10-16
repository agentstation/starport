# LR6 critique pass 2 — 2026-10-16

## Findings

### 1. Condition 13 cannot pass after v1.4.0 (should)

**Claim:** LR13 acceptance and checklist row 7 say that conditions 13 and 14 pass after v1.4.0, but condition 13 must fail until LR8 ships v1.4.1.

**Evidence:**
- `website/src/components/journey/journey.tsx:971` in the LR1 tree has the alt text "...starts a temporary gateway...".
- The home page renders `<Journey>` at `src/app/(home)/page.tsx:73`.
- `verify-launch-state.sh` condition 13 fails on a case-insensitive "temporary gateway" match.
- The live site has 2 matches today.
- Plan L611 and L681 claim the pass.

**Remedy:**
- Set the LR13 acceptance and row 7 proof to "Condition 14 passes, and the step 6 curl checks pass."
- Move condition 13 to the LR8 acceptance and the row 8 proof.
- Alternatively, LR1 removes "temporary gateway" from `journey.tsx:971` before #466 merges.

### 2. The pass 1 recovery remedy fails after the immutable publish (should)

**Claim:** The "Re-run failed jobs" recovery (plan L608) cannot recover a failure after the immutable release is published. Examples are the Homebrew cask publish and `verify-homebrew`.

**Evidence:**
- A rerun runs goreleaser and the single-draft check (`select(.draft==true)` length 1) again, and that check fails on a published release.
- `release.yaml` has a "Recover Verified Draft" `workflow_dispatch` path. Its inputs are `tag` and `source_run_id`. It reuses the `release-dist` artifact, which has 7-day retention, and it sets PUBLISH_RELEASE=false for a published release.
- The Site deploy `if` (`site.yaml:112-116`) requires a Release run with event `push`, so a dispatched recovery does not deploy the site.

**Remedy:** Replace L608 with this procedure:
1. Within 7 days, run `gh workflow run release.yaml -R agentstation/starport -f tag=v1.4.0 -f source_run_id=<failed run id>`.
2. Then run `gh workflow run site.yaml -R agentstation/starport -f ref=v1.4.0`.
3. Keep the freeze until both runs succeed, because recovery requires tag == origin/main (`release.yaml:73-77`).

### 3. No freeze protects the v1.4.1 tag (should)

**Claim:** LR13 step 8 (L606) says to tag v1.4.1 "on that merge commit with the step 4 commands", but no freeze protects main between the LR8 merge and the tag.

**Evidence:**
- Step 4 tags `origin/main`.
- The gate requires tag SHA == origin/main (`release.yaml:73-77`).
- Any merge after LR8 puts v1.4.1 on a different commit, or makes the gate fail.
- No task adds a `## v1.4.1` section to CHANGELOG.
- LR8 changes the README recording line after v1.4.0 ships.
- The release header (`.goreleaser.yaml:171-189`) links CHANGELOG at the tag and says that it "lists the breaking changes and the upgrade notes of each release".

**Remedy:**
- Freeze main from the LR8 merge until the v1.4.1 Site run succeeds.
- In step 8, first confirm that `git rev-parse origin/main` equals the LR8 merge, then tag.
- LR8 adds `## v1.4.1` with "Documentation: first-request recording from the attested v1.4.0 archive."

### 4. Condition 9 conflicts with "LR10 optional" (should)

**Claim:** The owner cannot finish row 9 without asking a question. Row 9 (L683) gates the announcement on 16 of 16 conditions, but row 5 (L679) and the LR13 row (L377) make LR10 signing optional.

**Evidence:** Condition 9 (`signing_secrets`) passes only when all five `MACOS_*` secret names exist. If LR10 is skipped, condition 9 fails permanently.

**Remedy:** Make one decision and record it as a D-row:
- (a) LR10 is required before the announcement, so row 5 becomes "Required before row 9". Or:
- (b) Condition 9 passes when all five names exist, or when README contains the "not yet notarized" paragraph (README L86-91).

### 5. LR17 runs after the cleanup task, and the cleanup deletes its output (should)

**Claim:** LR17 (L655-666) comes after LR16 cleanup (L642-653). This breaks the plans-skill rule that cleanup is the last task.

**Evidence:**
- LR16 deletes the proof root, but LR17 writes `proof/launch-readiness/lr17-watch.md`.
- The LR16 acceptance (`git grep launch-readiness` is empty) cannot hold while LR17 runs.
- The goal block (L688-720) is met when "LR16 waits only on the final merge" and does not name LR17.
- The skill references (`references/execution.md:124-126,142-158`) say that cleanup is the final task.

**Remedy:**
- Move LR17 before LR16, make LR16 depend on LR17 being terminal, and add LR17 to the goal block.
- Alternatively, move the watch to a GitHub issue outside the plan and delete LR17.

### 6. Main CI flakes are not only on Windows (should)

**Claim:** F27 names only Windows flakes, but main CI also fails on ARM. This makes condition 16 and LR13 step 1 ("main CI green") unreliable.

**Evidence:**
- Run 38798627903 at aa36a7ec (the #464 merge) failed with these tests:
  - windows-2025: TestDevOpensTheBoundAddress, TestRecoverySelectionBindsEncryptionSelection, and a TestPanicRecovery panic log.
  - ubuntu-24.04-arm: TestDevelopmentCatalogAcceptsUnchangedDerivedPublication.
- 6 of the last 14 completed main runs failed.

**Remedy:**
- Add the ARM test and run 38798627903 to F27.
- LR13 step 1 adds: "On a failure that matches F27, re-run failed jobs once and record both run IDs. A second failure stops the launch."
- Open a flake issue that LR17 owns.

### 7. Row 1 (LR7, Starmap v0.17.1) has no command or precondition (should)

**Claim:** The owner cannot tag Starmap from row 1 without asking a question.

**Evidence:**
- Tagging 120a49889009 starts the Starmap Release workflow at that commit. The workflow runs an ancestor-of-main check, `make verify`, `make release-check`, and GPG-signed checksums.
- Starmap `CHANGELOG.md` `[Unreleased]` is empty at that commit, so v0.17.1 has no changelog section.
- The site footer will print "Starmap v0.17.1" (condition 15 regex).

**Remedy:** Row 1 gives this command:

`git -C ~/src/github.com/agentstation/starmap tag -s v0.17.1 120a49889009 -m 'Starmap v0.17.1' && git -C ~/src/github.com/agentstation/starmap push origin v0.17.1`

Row 1 also adds two steps:
- "Watch the Starmap Release run to success."
- "Accept the generated notes, or add a v0.17.1 CHANGELOG entry first."

### 8. Row 8 and LR8 step 1 "then `--review`" cannot run as written (should)

**Claim:** The review step (L514, L682) has no arguments and no prerequisites.

**Evidence:**
- The `scripts/record-readme-demo.sh` usage requires `--review DIR --reviewer NAME --pacing-verdict PASS|FAIL [--notes]`.
- The script needs macOS ARM64, python3 with Pillow, and a `gh` login.

**Remedy:** Row 8 and LR8 step 1 give the exact command `scripts/record-readme-demo.sh --review docs/assets/first-use-v1.4.0 --reviewer <owner> --pacing-verdict PASS`. They also list the prerequisites: macOS ARM64, `python3 -c 'import PIL'`, and `gh auth status`.

### 9. Nobody has proven that the only security report path delivers mail (should)

**Claim:** `security@agentstation.ai` is the only vulnerability report channel until LR11 step 7 enables private reporting, and nobody has tested delivery.

**Evidence:**
- `SECURITY.md` and `website/well-known/security.txt` name only this address.
- MX is `smtp.google.com`, and DMARC is `p=quarantine`.
- No plan task sends a test message.

**Remedy:** Add this step to row 3 and the LR11 acceptance: "From an outside address, send a test mail to security@agentstation.ai, and record the receipt time in the LR11 evidence."

### 10. Condition 7 passes when every CodeQL analysis fails (nice)

**Claim:** Condition 7 checks only that the CodeQL default setup `state` is `configured`. It passes with zero successful analyses, and the CodeQL Go extractor on Go 1.27.2 is not verified.

**Evidence:** Condition 7 in `verify-launch-state.sh` reads only `.state`.

**Remedy:** Fold this into condition 7: `gh api 'repos/agentstation/starport/code-scanning/analyses?per_page=1' --jq 'length'` must be ≥ 1, with a `created_at` after the setup was enabled.

### 11. Plan hygiene (should)

**Claim:** The ledger and the resume state are stale.

**Evidence:**
- **LR0 status (L364):** LR0 is `in_progress`, but its acceptance is met (#467 merged at 2af4a0ed). Its evidence names the branch `codex/launch-readiness`, which is now `-2`.
- **Resume state (L173-179):**
  - The Worktree row says b310cad7 with dirty files. The actual state is 583776f1 on 2af4a0ed.
  - The Verification row says verify.sh 0 passed. Main prints 6/14, the LR1 tree prints 14/14, and the launch-state script prints 1/16.
- **LR6 evidence:** It says pass 2 runs "after the four merges", but the resume state says "after #466".
- **Ordering:** F27 is listed before F20, and D6 is listed after D10.

**Remedy:**
- Set LR0 to `done` with the evidence "#467 merged 2af4a0ed".
- Update both resume rows with the current values.
- Make the LR6 wording consistent.
- Sort the findings and decisions tables.

### 12. Small public-content gaps (nice)

**Evidence and remedies:**
- **security.txt renewal:** `website/well-known/security.txt` has `Expires: 2027-10-15`, and no task owns its renewal. Remedy: add an LR17 or issue item, "Renew before 2027-09-15."
- **LICENSE comparison source:** The LR14 acceptance says "diff against upstream is empty" but gives no URL. The LICENSE differs from gnu.org `agpl-3.0.txt` only in http vs https (lines 4, 646, 661) and in the filled-in appendix (632-635). Remedy: name `https://www.gnu.org/licenses/agpl-3.0.txt`, and allow only the appendix and the URL scheme to differ.
- **Rollback example:** `docs/site/operate-starport/upgrades.md:128` shows `gh workflow run site.yaml -f ref=v1.3.0`, which is a deploy that D6 rejects. Remedy: LR8 changes the example to `ref=v1.4.0`.

## Verified claims

- **PR states:**
  - #464 is MERGED at aa36a7ec (head 84b74c97).
  - #465 is MERGED at dbd1fa1a (head f3286f8d).
  - #467 is MERGED at 2af4a0ed (head e85ca045).
  - #466 is OPEN at dfda8354.
  - The top of `git log origin/main` is 2af4a0ed, dbd1fa1a, aa36a7ec, b310cad7.
- **Verifier output:**
  - `verify.sh` on dfda8354: 14 passed.
  - `verify-launch-state.sh` now: 1 passed (condition 16), 15 failed.
- **Release gate:** The tag SHA must equal origin/main (`release.yaml:73-77`).
- **Site deploy rules:**
  - Deploy runs only when the triggering Release run has event `push` and conclusion success (`site.yaml:112-116`).
  - Exactly one version tag must be at HEAD (`site.yaml:153-161`).
  - The deploy job uses the `starport-site` environment.
- **`starport-site` environment:** It exists, with no protection rules and no branch policy.
- **Release history:** v1.3.0 has 12 assets. Release run 37437368384 succeeded on attempt 1 with all jobs green. Release gates for v1.0.3 to v1.3.0 succeeded on attempt 1, except one v1.1.0 failure.
- **Homebrew cask:** `Casks/starport.rb` is at version "1.3.0", with a quarantine-strip postflight. The macOS `verify-homebrew` job trusts the cask automatically. `brew trust --cask` is valid in Homebrew 7.0.8 (README L65-70).
- **Notarization:** Notarize runs only when all five `MACOS_*` variables are set (`.goreleaser.yaml:112-127`). The macOS job runs spctl and checks team 244SS6JJ8Q only for a Developer ID binary.
- **Native release:** The `native-release.yaml` input is named `tag`.
- **Changelog link:** The release header links CHANGELOG at `{{ .Tag }}` (`.goreleaser.yaml:171-189`).
- **Site URLs:** The sitemap, robots, and metadataBase/og/twitter URLs come from SITE_URL. The `wrangler.jsonc` custom domain is `starport.agentstation.ai`.
- **Repository content:**
  - The `question` issue label exists.
  - CODEOWNERS names @jackspirou and @savkat.
  - `config.yml` links SECURITY.md.
  - No CSP jargon remains (verify.sh condition 5 passes).
  - `upgrades.md:105-135` names the public host.
  - README badges (Release, CI, License) resolve to the correct targets.

## Not checked

- Cloudflare API token permissions for a `custom_domain` route deploy, and the Cloudflare dashboard settings (zone HSTS, Always Use HTTPS).
- Whether the Starmap Release workflow passes today at 120a49889009.
- Whether the CodeQL Go extractor supports Go 1.27.2.
- The site build output (`next build` did not run). Social card rendering was not checked in a real unfurler.
- Whether `security@agentstation.ai` delivers mail.
- The Windows `native-release.yaml` result for v1.4.0.
- macOS Gatekeeper behavior for an unsigned v1.4.0 on a clean machine.
