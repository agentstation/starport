---
title: Run a persistent local gateway
area: start
order: 2
summary: Install Starport, start the development gateway, which initializes persistent state on the first run, and restart it with the same key.
---

`starport dev` runs a persistent local gateway. The gateway keeps its keys, records, and uploaded files on one machine, under the platform storage root. It runs as one process with Badger, SQLite, and local file storage. `starport serve` runs the same gateway from the same root on a fixed port.

## Install Starport

Install the released Homebrew cask on macOS or Linux:

```bash
brew trust --cask agentstation/tap/starport
brew install --cask agentstation/tap/starport
starport --version
```

Release archives for macOS, Linux, and Windows are on the release page. The [repository README](../../README.md#install) lists the supported platforms and the source build steps.

## Initialize and start the gateway

**Audience:** a developer or an operator who runs one gateway process on one machine.

**Before you start:**

- Install Starport.
- Set the provider inference credentials that you want the gateway to use, for example `OPENAI_API_KEY`.

**Steps:**

1. Start the gateway:

   ```bash
   starport dev
   ```

   On an empty local root, the first run creates the configuration, the master key, and the first identity, `local-admin`. Then it starts the gateway and opens the console in a browser.

2. Save the gateway API key that the first run prints. The command shows it one time only.
3. Keep the terminal open while you use the gateway.

**Expected result:** On a default macOS installation, the first run prints this output:

```text
Initialized Starport.
Configuration: /Users/<user>/Library/Application Support/starport/config/config.env
Data: /Users/<user>/Library/Application Support/starport/data
Gateway API key (shown once): <generated-gateway-api-key>
Starport development gateway
URL: http://127.0.0.1:7827
Authentication: required
Console (one-time launch link): http://127.0.0.1:7827/launch?lt=<ticket>
```

The gateway listens on `http://127.0.0.1:7827`. When another process holds port 7827, `dev` binds a fallback port. Then the line `Port 7827 is busy. Bound port 7373.` comes before the URL line. Use the printed URL in the commands on this page. A port from `STARPORT_SERVER_PORT` or the configuration file turns off the fallback.

The console opens in a browser with a session that this machine issued. The launch link is not a key. Add `--no-open` to print the link and keep the browser closed. `starport ui` opens a new link at any time.

**Verification:**

```bash
curl --fail http://127.0.0.1:7827/health/live
curl --fail http://127.0.0.1:7827/health/ready
```

Both commands exit with status 0. `/health/live` answers when the process responds. `/health/ready` answers when the gateway can accept requests. Neither route needs a credential.

**If it fails:**

- If `dev` or `serve` reports that the setup state is incomplete, an earlier first run stopped before it completed. Inspect the named configuration file, then run `starport auth bootstrap --recover`. The [operator guide](../../OPERATOR-GUIDE.md#initialize-persistent-state) describes the recovery.
- If `dev` names a setting that it refuses, the configuration moves management or storage away from the platform roots. Remove the setting, or run `starport serve` for that configuration.
- If `/health/ready` returns `503 not_ready`, the gateway has no authorization or catalog prerequisite yet. Read the startup log, then run `starport doctor`.
- If the console does not open, run `starport ui --no-open` and open the printed link.

**Related settings:** `STARPORT_HOME`, `STARPORT_SECURITY_MASTER_KEY`, `STARPORT_SERVER_PORT`.

## Restart the gateway

Press Ctrl+C to stop the gateway. Then start it again:

```bash
starport dev
```

The next run reads the same configuration file, master key, and stores. It prints no gateway API key. The key from the first run still works.

`starport serve` uses the same root, configuration, and keys. It binds only its configured port, and it opens no browser. When another process holds that port, serve stops with an error. Use it for a gateway that clients reach at one fixed port:

```bash
starport serve
```

To open the console of a running `serve` process, run this command in a second terminal:

```bash
starport ui
```

## What initialization writes

The first run of `starport dev` or `starport serve` writes the platform `config.env` file with mode `0600`. The file holds a generated master key. Starport uses the master key to encrypt stored provider credentials. A master key that you supply must contain at least 32 bytes.

The first run also creates the first gateway identity in the Badger directory. Starport stores only the SHA-256 hash of each gateway API key.

Initialization does not select a provider. It does not copy or store provider inference credentials. Those credentials stay in the process environment or in their secret references.

Run `starport config paths` to see each location. Refer to [Files and paths](../configure/paths.md) for the defaults on each platform.

## Keep the master key

Back up the master key with the data directory. Without the key, Starport cannot decrypt the stored provider credentials. Refer to [Back up and restore](../storage/backup-and-restore.md) before you copy a data directory.

## Production form of initialization

Configured storage does not initialize itself. A production deployment keeps its configuration in the environment or in a secret manager. Set the storage, master key, and provider values first. Then create the first identity:

```bash
starport auth bootstrap --name primary-admin
```

This command writes no local configuration file. It opens the configured store and refuses a store that already contains an identity. The [operator guide](../../OPERATOR-GUIDE.md#initialize-configured-storage) describes the retry behavior.

## Next steps

- Issue more keys. Refer to [Keys and roles](keys-and-roles.md).
- Run a gateway that keeps nothing. Refer to [Run a scratch development gateway](temporary-development.md).
- Compare this gateway with the other targets. Refer to [Architecture targets](../architecture/targets.md).
