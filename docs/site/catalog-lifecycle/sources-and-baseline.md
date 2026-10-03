---
title: Sources and the baseline
area: catalog-lifecycle
order: 1
summary: Select a catalog source, understand the embedded baseline and the publication channel, and read the freshness grades.
---

Each Starport process reads one connected Starmap runtime, and that runtime reads one selected source. This topic explains the source kinds, the embedded baseline, the publication channel, and freshness.

## Source kinds

`STARPORT_CATALOG_SOURCE` selects the source. The default is `public`. A private source never falls back to the public channel.

| Source | Reads | Needs |
| --- | --- | --- |
| `public` | Signed releases of the public Starmap repository | Network access to GitHub |
| `github` | Signed releases of a GitHub repository that you name | `STARPORT_CATALOG_SOURCE_REPOSITORY`, and `STARPORT_CATALOG_SOURCE_TOKEN` for a private repository |
| `starmap` | A central Starmap server | `STARPORT_CATALOG_SOURCE_URL` and `STARPORT_CATALOG_SOURCE_API_KEY` |
| `file` | One catalog file on local disk | An absolute `STARPORT_CATALOG_SOURCE_URL` |
| `embedded` | The baseline in the binary | Nothing |

The source API key and the source token are catalog credentials. They do not pay a provider. Configuration inspection redacts both values and the source URL.

## The embedded baseline

Each Starport binary contains an embedded catalog baseline. The startup policy decides when the gateway uses it.

| `STARPORT_CATALOG_SOURCE_STARTUP_POLICY` | Start behavior |
| --- | --- |
| `prefer_source` | Start on the baseline. Use the source after the first successful read. This is the default. |
| `require_source` | Read the source once at start. Stop the start if that read fails. |
| `require_authority` | Keep diagnostics. Refuse new inference until the configured authority permits it. |
| `prefer_local` | Start from the retained catalog or the baseline. |

While the gateway serves the baseline, `runtime.fallback` is `true` in the admin catalog status. The accepted head is the restart bootstrap. A gateway that cannot reach its source still routes on its last accepted head.

## The publication channel

The `public` and `github` sources read one attested channel. `STARPORT_CATALOG_SOURCE_CHANNEL` names it. Read the current default with `starport config effective --json`.

`STARPORT_CATALOG_SOURCE_POLL_INTERVAL` sets the check period. The default is `1h`. `STARPORT_CATALOG_SOURCE_MAX_HOPS` limits the publication chain of a `starmap` source. The default is `8`.

## Freshness

Freshness grades three ages. The grades are `current`, `warn`, `critical`, and `unknown`.

| Field | Age that it grades |
| --- | --- |
| `freshness.catalog` | The served catalog generation |
| `freshness.channel` | The origin publication, through each hop |
| `freshness.source_check` | The last check of the source by this instance |

`STARPORT_CATALOG_SOURCE_MAX_AGE` sets the stale age. The default is `6h`. Each polling hop adds one poll interval to the channel age. A local provider observation does not reset the channel age.

## Select a source

### Audience

An operator who selects where a gateway gets its catalog.

### Before you start

- Decide on a topology. Refer to [Catalog source topologies](../architecture/topologies.md).
- For a `starmap` source, get the server URL and a source API key.

### Steps

1. Set `STARPORT_CATALOG_SOURCE` to the source kind.
2. Set the URL, repository, or credential that the table names.
3. Set `STARPORT_CATALOG_SOURCE_STARTUP_POLICY` if `prefer_source` is not correct.
4. Run `starport config validate --json`.
5. Restart the gateway.

### Expected result

The admin catalog status shows the selected kind in `runtime.source_kind`. After the first read, `runtime.fallback` is `false`.

### Verification

```bash
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/catalog/status | jq '.runtime.source_kind, .runtime.fallback'
```

```text
"starmap"
false
```

### If it fails

An unknown source kind stops the start. A `starmap` or `file` source with no URL stops the start. For a stale source, read [Catalog failures](../troubleshoot/catalog-failures.md).

### Related settings

- `STARPORT_CATALOG_STARTUP_SPREAD` spreads the first source read of a fleet. The default is `15m`.
- `STARPORT_CATALOG_TRANSFER_IDLE_TIMEOUT` ends a transfer that stops. The default is `2m`.
- `STARPORT_CATALOG_TRANSFER_MAX_DURATION` limits one transfer. The default is `1h`.
