---
title: Run a persistent local gateway
area: start
order: 1
summary: Install Starport, initialize persistent state once, start the gateway, and open the console.
---

A persistent local gateway keeps its keys, records, and uploaded files on one machine. It runs as one process with Badger, SQLite, and local file storage.

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
- Make sure that no configuration file or identity store exists for this user. Initialization refuses to replace them.
- Set the provider inference credentials that you want the gateway to use, for example `OPENAI_API_KEY`.

**Steps:**

1. Create the configuration, the master key, and the first identity:

   ```bash
   starport init --name primary-admin
   ```

2. Save the gateway API key that the command prints. The command shows it one time only.
3. Start the gateway:

   ```bash
   starport serve
   ```

4. In a second terminal, open the console:

   ```bash
   starport ui
   ```

**Expected result:** The gateway listens on `http://127.0.0.1:7827`. The console opens in a browser with a session that this machine issued.

**Verification:**

```bash
curl --fail http://127.0.0.1:7827/health/live
curl --fail http://127.0.0.1:7827/health/ready
```

Both commands exit with status 0. `/health/live` answers when the process responds. `/health/ready` answers when the gateway can accept requests. Neither route needs a credential.

**If it fails:**

- If `init` reports existing state, keep that state. Run `starport config paths` to find it, and use `starport serve` with it.
- If `/health/ready` returns `503 not_ready`, the gateway has no authorization or catalog prerequisite yet. Read the startup log, then run `starport doctor`.
- If the console does not open, run `starport ui --no-open` and open the printed link.

**Related settings:** `STARPORT_SECURITY_MASTER_KEY`, `STARPORT_SERVER_PORT`, `STARPORT_STORAGE_BADGER_PATH`.

## What initialization writes

`starport init` writes the platform `config.env` file with mode `0600`. The file holds a generated master key. Starport uses the master key to encrypt stored provider credentials. A master key that you supply must contain at least 32 bytes.

The command also creates the first gateway identity in the Badger directory. Starport stores only the SHA-256 hash of each gateway API key.

Initialization does not select a provider. It does not copy or store provider inference credentials. Those credentials stay in the process environment or in their secret references.

Run `starport config paths` to see each location. Refer to [Files and paths](../configure/paths.md) for the defaults on each platform.

## Keep the master key

Back up the master key with the data directory. Without the key, Starport cannot decrypt the stored provider credentials. Refer to [Back up and restore](../storage/backup-and-restore.md) before you copy a data directory.

## Production form of initialization

A production deployment keeps its configuration in the environment or in a secret manager. Set the storage, master key, and provider values first. Then run:

```bash
starport init --configured-storage --name primary-admin
```

This form writes no local configuration file. It opens the configured store and refuses a store that already contains an identity. The [operator guide](../../OPERATOR-GUIDE.md#initialize-configured-storage) describes the retry behavior.

## Next steps

- Issue more keys. Refer to [Keys and roles](keys-and-roles.md).
- Inspect the models. Refer to [Inspect the catalog](first-catalog-inspection.md).
- Compare this gateway with the other targets. Refer to [Architecture targets](../architecture/targets.md).
