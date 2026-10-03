---
title: Upgrade and shut down
area: operate-starport
order: 6
summary: Set request limits, stop a gateway safely, run the release gate, upgrade with steps that you can verify, and publish the documentation site.
---

This topic covers the request limits and graceful shutdown. It also gives the release gate for a source build and an upgrade procedure that you can verify. The last section tells how to publish and roll back the public documentation site.

## Request limits

| Setting | Default | Effect |
| --- | --- | --- |
| `STARPORT_SERVER_REQUEST_TIMEOUT` | `60s` | The time limit for one request |
| `STARPORT_SERVER_MAX_REQUEST_SIZE` | `33554432` | The largest body in bytes, which is 32 MiB |
| `STARPORT_SERVER_MAX_HEADER_BYTES` | `1048576` | The largest header block in bytes |
| `STARPORT_SERVER_SHUTDOWN_TIMEOUT` | `30s` | The time that shutdown waits for work to stop |

A caller sends a file as base64 in the JSON body, and base64 adds a third to the size. The default body limit holds one file of about 25 MB. A body above the limit gets HTTP `413`. The message states the limit in bytes. The gateway keeps the body in memory while it reads it.

## Graceful shutdown

`SIGINT` and `SIGTERM` start a graceful shutdown. Starport stops HTTP first. It then closes background work, the cache, the providers, and storage, in the reverse of the start order. Give the process manager a stop timeout longer than `STARPORT_SERVER_SHUTDOWN_TIMEOUT`.

## The release gate

Run these checks before you promote an image that you build from source.

```bash
bash scripts/verify-v1-architecture.sh
go test ./...
go vet ./...
make lint
make build
docker build .
bash scripts/smoke-openrouter-sdks.sh
```

The architecture check must report `Summary: 12 passed, 0 failed`. The raw HTTP smoke checks must pass. An optional SDK check can be `UNVERIFIED`. Do not report an unverified SDK as compatible. The full command list, with the race tests, is in [the operator guide](../../OPERATOR-GUIDE.md#release-gate).

## Upgrade limits

The upgrade procedure below is for a single gateway with local storage. Do not upgrade an existing shared deployment without a coordinated recovery plan. Refer to [the recovery document](../../RECOVERY.md) and [Initialize and run a fleet](fleet.md).

A new version can change a default path. Startup refuses a conflict with an earlier default path and keeps the old files. Startup does not move the files.

## Upgrade a single gateway

### Audience

An operator who upgrades one gateway that uses local storage.

### Before you start

- Get the release notes for the new version.
- Get a gateway key with the `admin` scope in `$STARPORT_ADMIN_KEY`.
- Get a location for an offline backup that only operators can read.

### Steps

1. Record the current version with `starport version --json`.
2. Inspect earlier default locations with the new binary.
3. Stop the gateway.
4. Take an offline backup of the configuration, data, and state directories.
5. Install the new binary.
6. Run the startup checks.
7. Start the gateway.

```bash
starport config paths --legacy --json
starport config validate
starport doctor --probe --json
```

For a Homebrew installation, install the new binary with these commands.

```bash
brew update
brew upgrade --cask agentstation/tap/starport
```

### Expected result

`config paths --legacy` lists no abandoned path. The doctor output shows `"ok": true`. The gateway starts and serves requests.

### Verification

```bash
starport version --json
curl -fsS <gateway-url>/health/ready | jq '.status, .version'
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/catalog/status | jq '.route_validation.state'
```

The version is the new version. The catalog status shows an accepted generation.

### If it fails

If `--legacy` lists a path, set explicit paths to keep the earlier locations. A change of selector does not copy files. Stop the gateway and install the earlier binary. Restore the offline backup if the new binary changed state. Read [Recover without a console session](../troubleshoot/recovery.md).

### Related settings

- `STARPORT_SERVER_SHUTDOWN_TIMEOUT` sets the drain time.
- Refer to [Back up and restore](../storage/backup-and-restore.md).

## Publish the documentation site

Each release attaches the documentation site as `starport-docs-<tag>.tar.gz`. The `checksums.txt` file of the release lists its digest. The gateway serves the same build at `/docs/`.

The public host is not yet enabled. GitHub Pages stays off for the repository. The `Docs Pages` workflow in `.github/workflows/docs-pages.yaml` is ready, and only a maintainer can start it.

### Version paths

| Path | Content |
| --- | --- |
| `/<tag>/` | The documentation archive of that release, unchanged |
| `/latest/` | A copy of the release that the `latest` input names |
| `/` | A redirect to `/latest/` with a list of the versions |
| `/manifest.json` | Each version with its archive digest and its embedded manifest |

Each version directory keeps its own `manifest.json`. That file names the release, the Starmap module version, and the content revision. It also lists the SHA-256 digest of each file. The root manifest entry for a version equals that file. To compare the public site with a gateway, compare the entry with `/docs/manifest.json` on the gateway.

A deployment replaces the whole site. Each run downloads every release that has a documentation archive and builds the site again. The workflow checks each archive against `checksums.txt` and each file against its manifest. A failed check stops the run before the deployment. A prerelease joins the site only when `tag` or `latest` names it.

### Before the first run

- Set the GitHub Pages source of the repository to GitHub Actions.
- Create the `github-pages` environment with required reviewers.

### Publish a release

1. Start the workflow with the release tag in `tag`.
2. Leave `latest` empty to serve the same release at `/latest/`.
3. Approve the deployment in the `github-pages` environment.

```bash
gh workflow run docs-pages.yaml -f tag=v1.4.0
```

### Roll back

Start the workflow again with an earlier release in `latest`. Keep the newest release in `tag`. The run keeps every version path and moves `/latest/` to the earlier release.

```bash
gh workflow run docs-pages.yaml -f tag=v1.4.0 -f latest=v1.3.0
```

A failed run leaves the last deployment in place. The deploy step runs only after the assembly and the artifact upload succeed.
