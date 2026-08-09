# DX1 main branch proof

Date: 2026-08-09

## Fail-before state

- GitHub reported `master` as the default branch.
- The release workflow fetched and compared `origin/master`.
- The CI workflow accepted pull requests and pushes for `master`.

## Branch migration

The GitHub branch rename operation changed `master` to `main`. The operation
kept the existing branch protection. The repository now has these properties:

```json
{"default_branch":"main"}
{"name":"main","protected":true}
{"enforce_admins":true,"required_approvals":0,"required_checks":["Build","Security Scan","Release Snapshot","Action Pin Provenance"]}
```

The remote branch readback returned only `refs/heads/main` for the requested
`main` and `master` names. It did not return `refs/heads/master`.

## Repository changes

Work commit: `d63c268d68af5f88ac6528327fd8069b5c33a8c0`

- CI runs for `main` only.
- Release ancestry and exact-head checks use `origin/main`.
- The release contract test requires `origin/main`.
- Current contribution procedures use `main`.
- The historical architecture plan labels its old branch value as historical.

## Verification

Commands:

```bash
bash scripts/verify-release-workflow.sh
bash scripts/verify-action-pins.sh
bash scripts/verify-developer-experience.sh
git diff --check
```

Results:

```text
PASS release workflow contract
action pins: 15 reference(s) match their release tags
Summary: 4 passed, 35 failed
```

The developer-experience verifier must fail until later plan tasks implement
the other 35 conditions. The three DX1 branch conditions pass.

## Pull request gate

- Pull request: `https://github.com/agentstation/starport/pull/77`
- Merge commit: `05f4a791470a60d858b0620b2d3804bb1d02203e`
- Merge time: 2026-08-09 at 20:52:19 UTC

All 10 CI checks passed before merge:

- Action Pin Provenance
- Build
- Lint
- OpenRouter SDK Compatibility
- Release Contract
- Release Snapshot
- Security Scan
- Test on macOS
- Test on Ubuntu
- Test on Windows

The merge cleanup removed the remote and local `master` branches. Git history
can recover the local branch for an audit.
