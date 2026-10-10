---
title: Configuration sources and precedence
area: configure
order: 1
summary: Learn which source supplies each setting, the order in which sources win, and how shared management changes that order.
---

Starport reads each setting from a fixed list of sources. The first source that defines a value wins.

## Order of precedence

For each setting, Starport uses the first source in this list that defines a value:

1. Command-line overrides. Only `--no-auth` and `--allow-remote-no-auth` act as overrides, and Starport applies them after it loads the other sources.
2. The process environment. Each Starport setting has the prefix `STARPORT_`.
3. The configuration file.
4. The built-in default.

`starport dev` reads the process environment and the defaults. It reads no configuration file.

## The configuration file

The `starport serve` command reads one primary file in the dotenv format. Starport selects the file in this order:

1. The path in `STARPORT_CONFIG_FILE`.
2. The `config.env` file in the platform configuration directory.

A missing default file is not an error. Starport then uses the environment and the defaults. A missing file at an explicit `STARPORT_CONFIG_FILE` path stops startup. The file must be 1 MiB or smaller.

```dotenv
STARPORT_SERVER_PORT=7827
STARPORT_STORAGE_MODE=badger
STARPORT_CATALOG_SOURCE=public
```

Refer to [Files and paths](paths.md) for the default directory on each platform.

## More than one environment file

The configuration loader accepts more than one environment file. In that case, the first file that defines a value wins. The shipped commands read one file only. A program that embeds Starport can add files.

When the process reads more than one file, a configuration save from the console refuses. Refer to [Manage configuration from the console](console-management.md#local-save-checks).

## Relative paths

A path value must be absolute by default. To anchor relative paths under the configuration directory, set this value:

```dotenv
STARPORT_RELATIVE_PATH_BASE=config
```

`config` is the only permitted value.

## Bootstrap variables

The loader reads these variables before it decodes the other settings. The [generated settings reference](../generated/settings.md) does not list them. It lists the other settings.

| Variable | Source | Effect |
| --- | --- | --- |
| `STARPORT_CONFIG_FILE` | Process environment | Selects the primary configuration file. A missing file at this path stops startup. |
| `STARPORT_CONFIG_DIR` | Process environment | Sets the configuration root. The configuration file cannot change this root. |
| `STARPORT_CONFIG_ACCESS` | Process environment | Sets the access rule for the primary file. The values are `owner-only` and `service-managed`. The default is `owner-only`. `service-managed` requires `STARPORT_CONFIG_FILE`. |
| `STARPORT_RELATIVE_PATH_BASE` | Process environment, then the configuration file | Permits relative paths. The value in the process environment applies to `STARPORT_CONFIG_FILE`. |
| `STARPORT_HOME` | Process environment, then the configuration file | Puts the four roots under one directory |
| `STARPORT_DATA_DIR` | Process environment, then the configuration file | Sets the data root |
| `STARPORT_STATE_ROOT` | Process environment, then the configuration file | Sets the state root |
| `STARPORT_CACHE_DIR` | Process environment, then the configuration file | Sets the cache root |
| `STARPORT_INSTANCE_ID` | Process environment, then the configuration file | Sets the instance ID. The default is `default`. |
| `STARPORT_DEPLOYMENT_ID` | Process environment, then the configuration file | Sets the deployment ID. The default is `local`. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | Process environment, then the configuration file | Sets the trace export endpoint |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Process environment, then the configuration file | Sets the trace export endpoint when `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is not set or is empty |

Refer to [Files and paths](paths.md) for the root and identity settings.

## Shared management

`STARPORT_CONFIG_MANAGEMENT` selects the authority for the catalog settings of the deployment. The values are `local`, `shared`, and `external`. The default is `shared` for the Valkey recipe and `local` for all other recipes.

Under shared management, one revision in the relational store replaces every catalog setting with deployment scope. A local value for such a setting then has no effect. `starport config effective` lists it with the reason `shared-authority`.

These values always use the normal order of precedence:

- Bootstrap values: the management mode, storage coordinates, deployment and instance identity, listeners, trust roots, and the master key.
- Node-scope catalog values, such as the workspace and state directories.

Refer to [Initialize and run a fleet](../operate-starport/fleet.md) for the shared revision commands.

## Aliases and special names

- The permission clock settings also accept the prefix `STARMAP_CATALOG_PERMISSION_CLOCK_`. In each source, the `STARPORT_` name wins over the alias.
- Trace export reads `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` first and `OTEL_EXPORTER_OTLP_ENDPOINT` second.
- Provider inference credentials use the conventional names of the catalog, such as `OPENAI_API_KEY`. Refer to [Secret references](secret-references.md).

## Authentication mode order

The authentication mode has its own order. Starport uses the first value in this list:

1. The `--no-auth` flag.
2. The configured value of `STARPORT_SECURITY_AUTH_MODE`.
3. The switch that the console saved.
4. The default value `required`.

Refer to [Authentication](../operate-starport/authentication.md) for the switch.

## Removed settings

A removed setting name stops startup. The error names the setting. Remove it from the environment and from the configuration file.

## Verify the result

**Audience:** an operator who changed a value.

**Before you start:** Make sure that the shell has the same environment as the service.

**Steps:**

1. Validate the configuration:

   ```bash
   starport config validate
   ```

2. Show each catalog setting with its origin:

   ```bash
   starport config effective --json
   ```

**Expected result:** The validate command exits with status 0. The effective report names the winning authority and the origin of each setting.

**Verification:** Find the changed setting in the report. Its origin names the source that you edited.

**If it fails:** If the origin names another source, that source has a higher precedence. Remove the value from that source.

**Related settings:** `STARPORT_CONFIG_FILE`, `STARPORT_CONFIG_MANAGEMENT`, `STARPORT_RELATIVE_PATH_BASE`.
