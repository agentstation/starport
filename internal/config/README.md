# Configuration package

The configuration package owns source precedence, platform paths, decoding,
and validation. Loading reads process state but does not change it.

## Source precedence

Starport resolves each value in this order:

1. A `STARPORT_` process environment variable.
2. The first environment file that defines the value.
3. A built-in default.

The standard loader reads one platform file named `config.env`. It uses these
locations:

| Platform | File |
|---|---|
| Linux and other Unix systems | `$XDG_CONFIG_HOME/starport/config.env`, or `$HOME/.config/starport/config.env` when `XDG_CONFIG_HOME` is empty |
| macOS | `$HOME/Library/Application Support/starport/config.env` |
| Windows | `%AppData%\starport\config.env` |

`os.UserConfigDir` supplies the platform root. Tests can inject a different
root and environment map without changing global process state.

## Managed paths

The platform configuration directory owns these defaults:

| Concept | Relative path |
|---|---|
| Environment file | `config.env` |
| Badger data | `data/badger` |
| Rate-limit rules | `rate_limits.yaml` |

Starport resolves a configured relative path from the platform configuration
directory. An absolute path remains unchanged. This rule makes file behavior
independent of the directory that starts the process.

## Secure local defaults

The HTTP server listens on `127.0.0.1:8080`. CORS and rate-limit hot reload are
off until an operator enables them. A supplied credential master key must
contain at least 32 bytes.

`starport init --provider openai` and `starport init --provider ollama` create
the standard local file with mode `0600`. Initialization creates the credential
master key and the first named identity. It does not replace an existing file
or identity store.

The container image explicitly listens on `0.0.0.0` because publishing a
container port is an operator action. It also stores Badger data under
`/var/lib/starport/data/badger`. Its writable configuration root is
`/var/lib/starport/config`.

## Environment variables

All external fields use the `STARPORT_` prefix. For example:

```bash
STARPORT_SERVER_PORT=8080
STARPORT_STORAGE_MODE=badger
STARPORT_LOGGING_LEVEL=info
```

Use [the configuration reference](../../.env.example) for the complete field
list. Starmap acquisition credentials stay separate from Starport inference
credentials.

## Inspection

Use these commands to inspect the resolved configuration:

```bash
starport config paths
starport config show
starport config validate
```

`config show` uses the configuration schema to replace each secret and URL
with `<redacted>`. Loader errors report only the failed loading stage. These
commands never show configured values in an error and never change process or
file state. With `--json`, validation writes `valid: false` and a safe loading
stage before it returns a nonzero status.

`starport doctor` uses the same loader. Passive diagnosis does not open
storage. `starport doctor --probe` opens configured storage through a
write-blocking adapter and checks the stored catalog and identity state. If
Badger needs writable recovery, the probe skips storage inspection and gives
recovery instructions. It also skips this inspection on platforms where
Badger does not support read-only mode.

## Rate-limit reload

To enable rule reload, set both values:

```bash
STARPORT_RATE_LIMITING_ENABLE_HOT_RELOAD=true
STARPORT_RATE_LIMITING_CONFIG_PATH=/absolute/path/to/rate_limits.yaml
```

Starport requires the rules file after an operator enables reload. The hot
reloader watches its directory and also checks the file at the configured
interval.
