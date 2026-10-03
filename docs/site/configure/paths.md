---
title: Files and paths
area: configure
order: 3
summary: Find the default configuration, data, state, and cache directories on each platform, and inspect each managed file.
---

Starport keeps its files in four root directories. Each platform has its own defaults, and each root has an override.

## Default roots

| Root | Linux | macOS | Windows |
| --- | --- | --- | --- |
| Configuration | `~/.config/starport` | `~/Library/Application Support/starport/config` | `%AppData%\starport` |
| Data | `~/.local/share/starport` | `~/Library/Application Support/starport/data` | `%LocalAppData%\starport\data` |
| State | `~/.local/state/starport` | `~/Library/Application Support/starport/state` | `%LocalAppData%\starport\state` |
| Cache | `~/.cache/starport` | `~/Library/Caches/starport` | `%LocalAppData%\starport\cache` |

On Linux, Starport uses `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, and `XDG_CACHE_HOME` when the environment defines them. Each XDG value must be an absolute path. Other operating systems need explicit roots.

## Root overrides

| Setting | Effect |
| --- | --- |
| `STARPORT_HOME` | Puts the four roots under one directory: `config`, `data`, `state`, and `cache` |
| `STARPORT_CONFIG_DIR` | Sets the configuration root |
| `STARPORT_DATA_DIR` | Sets the data root |
| `STARPORT_STATE_ROOT` | Sets the state root |
| `STARPORT_CACHE_DIR` | Sets the cache root |

A specific root setting wins over `STARPORT_HOME`.

## Files under the roots

| Item | Location |
| --- | --- |
| Configuration file | `<config>/config.env` |
| Badger directory | `<data>/badger` |
| SQLite file | `<data>/sqlite/starport.db` |
| Uploaded files | `<data>/files` |
| Local admin token | `<data>/local-admin-token.json` |
| Embedded baseline export | `<data>/catalog/baseline` |
| Catalog runtime | `<state>/catalog/runtime/<instance>` |
| Source caches | `<cache>/models.dev` and `<cache>/sources/models.dev-git` |

A local console save writes the journal `.starport-config-operations.json` next to the configuration file. SQLite also writes `-wal`, `-shm`, and `-journal` files next to its database file.

## Identity settings

`STARPORT_DEPLOYMENT_ID` defaults to `local`. `STARPORT_INSTANCE_ID` defaults to `default`. The instance ID selects the catalog runtime directory. Give each process its own instance ID and state directory.

## Inspect the paths

**Audience:** an operator who must find or back up a file.

**Before you start:** Use the same environment and configuration file as the service.

**Steps:**

1. Print each managed path:

   ```bash
   starport config paths
   ```

2. Print the file roles, the selected backends, and the access rules:

   ```bash
   starport config paths --files
   ```

3. Add bounded file system metadata:

   ```bash
   starport config paths --inspect --json
   ```

**Expected result:** The first command prints one labeled line for each location. On a default macOS installation, the output starts like this example:

```text
Configuration directory: /Users/<user>/Library/Application Support/starport/config
Configuration file: /Users/<user>/Library/Application Support/starport/config/config.env
Data directory: /Users/<user>/Library/Application Support/starport/data
```

**Verification:** The output ends with `Deployment: local` and `Instance: default` for a default installation.

**If it fails:** A relative path stops the command. Set an absolute path or `STARPORT_RELATIVE_PATH_BASE=config`.

**Related settings:** `STARPORT_HOME`, `STARPORT_CONFIG_FILE`, `STARPORT_CATALOG_STATE_DIR`, `STARPORT_CONFIG_ACCESS`.

## What the inspection does

`starport config paths` reads the configuration. It creates no directory and opens no database. The `--inspect` flag reads file metadata only. It reads no file contents, takes no lock, and changes no file. The default scan limit is 10,000 entries. Set `--max-entries` to change it.

The `--files` report marks a shared store as `kv`, `sql`, or `blobs` and shows no connection credentials. Accepted catalog generations use the selected KV backend.

## Locations of earlier releases

Earlier releases kept data in other directories. Before an upgrade, run this command:

```bash
starport config paths --legacy --json
```

The report lists earlier paths that the new defaults leave behind. Startup refuses such a conflict and keeps the old files. A change of a path setting does not copy files. Refer to [Upgrade and shut down](../operate-starport/upgrades.md).

## The catalog workspace

`STARPORT_CATALOG_WORKSPACE_PATH` names a directory of catalog files that an operator reviews. It must differ from the catalog state directory. Never share one `STARPORT_CATALOG_STATE_DIR` between two instances. Refer to the [operator guide](../../OPERATOR-GUIDE.md#the-workspace-and-the-state-directory).
