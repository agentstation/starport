---
title: Imports and the air-gapped mirror
area: operate-starmap
order: 2
summary: Supply catalog facts from a local workspace or from a catalog file that an operator moves into an air-gapped network.
---

A gateway can get catalog facts without a request to GitHub. This topic covers local imports from a workspace and the air-gapped mirror that uses the `file` source.

## Local imports

A workspace holds catalog files that an operator writes and reviews. Starmap reads the workspace as a local acquisition input.

```text
<workspace>/
  providers.yaml
  providers/<provider-id>/models/<model>.yaml
  providers/<provider-id>/logo.svg
  authors/<author>/models/<slug>.yaml
```

| Setting | Effect |
| --- | --- |
| `STARPORT_CATALOG_WORKSPACE_PATH` | The workspace directory. It is never the state directory. |
| `STARPORT_CATALOG_ACQUISITION_SOURCES` | `local_catalog` permits the workspace input. |
| `STARPORT_CATALOG_NETWORK_MODE` | `offline` stops catalog network requests and keeps local imports. |

A workspace can be on a shared volume. `STARPORT_CATALOG_STATE_DIR` must stay on local disk, one directory for each process. Startup refuses a state directory that is equal to the workspace path.

## The air-gapped mirror

In an air-gapped network, no host inside the boundary reaches GitHub or a provider. A process outside the boundary gets a release and its verification bundle. An operator verifies the release and moves the catalog file across the boundary.

The `file` source reads one canonical catalog payload from local disk. The file can have at most 64 MiB. Starmap decodes and validates the payload when the file changes. The checksum of the payload becomes the generation ID. The file modification time sets the catalog age and the channel age.

The `file` source does not verify a publisher signature. Verify the release outside Starmap before the transfer, for example with `gh attestation verify`. A file that does not decode leaves the accepted head unchanged.

## Freshness of a mirror

Set `STARPORT_CATALOG_SOURCE_MAX_AGE` to the transfer cadence. A late transfer then shows `warn` and later `critical` in `freshness.catalog`. The accepted head keeps each route at either grade. Pair the freshness alert with the transfer schedule.

## Set up an air-gapped mirror

### Audience

An operator who runs Starport in a network with no route to the internet.

### Before you start

- Get a host outside the boundary with the GitHub CLI.
- Agree on a transfer cadence and a transfer method.
- Choose an absolute path for the catalog file on each gateway host.

### Steps

1. Outside the boundary, download the catalog release and its verification bundle.
2. Verify the release with `gh attestation verify`.
3. Move the catalog payload file across the boundary.
4. Copy the file to the chosen path on each gateway host.
5. Set the catalog settings on each gateway.
6. Restart each gateway.

```dotenv
STARPORT_CATALOG_SOURCE=file
STARPORT_CATALOG_SOURCE_URL=/var/lib/starport/catalog/catalog.json
STARPORT_CATALOG_ACQUISITION_ENABLED=false
STARPORT_CATALOG_SOURCE_MAX_AGE=24h
```

### Expected result

Each gateway reports `runtime.source_kind` as `file`. No gateway sends a catalog request out of the network.

### Verification

```bash
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/catalog/status | jq '.runtime.source_kind, .freshness.catalog'
```

```text
"file"
"current"
```

### If it fails

A `file` source with no path stops the start. A file larger than 64 MiB or a file that does not decode leaves the accepted head unchanged. Read [Catalog failures](../troubleshoot/catalog-failures.md).

### Related settings

- `STARPORT_CATALOG_SOURCE_POLL_INTERVAL` sets how often the runtime reads the file again.
- `STARPORT_CATALOG_GENERATION_PIN` stops catalog changes on one retained generation.
- Refer to [Sources and the baseline](../catalog-lifecycle/sources-and-baseline.md).
