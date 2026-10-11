---
title: Run a temporary development gateway
area: start
order: 2
summary: Start a gateway that keeps no data, and learn what it loses at shutdown and which settings it refuses.
---

`starport dev` starts a gateway that keeps all records in memory. Use it to try Starport, to test a client, or to inspect the console. This mode is architecture target T7.

## Start a development gateway

**Audience:** a developer who wants a gateway for one session on a workstation.

**Before you start:**

- Install Starport. Refer to [Install Starport](local-persistent.md#install-starport).
- Set one provider inference credential, for example `OPENAI_API_KEY`.

**Steps:**

1. Remove persistent storage selectors from this shell:

   ```bash
   unset STARPORT_CATALOG_STATE_DIR STARPORT_FILES_BACKEND
   ```

2. Start the gateway:

   ```bash
   starport dev
   ```

3. Copy the gateway API key that the command prints.
4. Keep the terminal open while you use the gateway.

**Expected result:** The command prints a summary similar to this example:

```text
Starport development gateway
URL: http://127.0.0.1:7827
Authentication: required
Gateway API key (shown once): <generated-gateway-api-key>
Console (one-time launch link): http://127.0.0.1:7827/launch?lt=<ticket>
```

The console opens in a browser. The launch link is not a key. The gateway accepts the link one time and gives the browser a session. Add `--no-open` to print the link and keep the browser closed.

**Verification:**

```bash
curl --fail http://127.0.0.1:7827/health/ready
```

The command exits with status 0.

**If it fails:** Read the error. If the error names storage settings, remove those settings from the shell, or use a persistent gateway. Refer to [Settings that development mode refuses](#settings-that-development-mode-refuses).

**Related settings:** `--no-auth`, `--no-open`, `STARPORT_SERVER_PORT`.

## What the gateway loses at shutdown

A development gateway uses in-memory Badger and in-memory SQLite. It reads the process environment but not `config.env`. It writes no configuration file.

When the process stops, Starport removes these items:

- The gateway API keys, accounts, and console sessions.
- The usage records, request activity, and budgets.
- The uploaded files, batches, and presets.
- The catalog baseline and the runtime catalog state in the session scratch directories.

Provider inference credentials stay in the process environment. Starport does not store or delete them.

## Settings that development mode refuses

Development mode checks the environment before it opens storage. If the environment holds a persistent storage selector, startup stops. These settings cause the refusal:

- KV selectors: `STARPORT_STORAGE_MODE`, `STARPORT_STORAGE_BADGER_PATH`, and the `STARPORT_STORAGE_VALKEY_` settings.
- SQL selectors: `STARPORT_STORAGE_SQL_MODE`, the SQLite path, `STARPORT_STORAGE_SQL_POSTGRES_URL`, and `STARPORT_STORAGE_SQL_MYSQL_DSN`.
- File selectors: `STARPORT_FILES_BACKEND`, the files path, and the `STARPORT_FILES_OBJECT_STORE_` settings.
- Cache selectors: `STARPORT_CACHE_BACKEND`, `STARPORT_CACHE_URL`, `STARPORT_CACHE_ALLOW_INSECURE`, and `STARPORT_CACHE_CA_FILE`.
- Catalog selector: `STARPORT_CATALOG_STATE_DIR`.

The error names each setting that it found. It tells you to remove them or to use `starport init` and `starport serve`.

## Why persistent selectors fail

A persistent selector points at storage that must outlive the process. A development gateway requires isolated storage. It binds its catalog state and uploaded files to a session scratch directory that it owns. A persistent selector conflicts with that isolation. Starport stops before it opens any storage, so a development session never reads or writes persistent state.

## Scratch directory recovery

A development gateway that stops abnormally can leave scratch directories. The next development run removes an abandoned directory after it verifies ownership. It keeps the directories of live sessions. It also keeps changed or unknown directories for a manual decision. The [operator guide](../../OPERATOR-GUIDE.md#development-scratch-recovery) gives the recovery procedure.

## Move to a persistent gateway

A development gateway has no upgrade path to persistent state. To keep data, initialize a persistent gateway and issue new keys. Refer to [Run a persistent local gateway](local-persistent.md).
