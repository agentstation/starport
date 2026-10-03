---
title: Run a central Starmap server
area: operate-starmap
order: 1
summary: Run one Starmap server that follows a catalog source and serves the catalog to each Starport replica.
---

A central Starmap server reads the catalog source once and serves each Starport replica. The fleet then sends no catalog request to GitHub. This topic covers the server settings, the source, authentication, and publication to the replicas.

## Server configuration

`starmap serve` starts the server. The default address is `localhost:8080`. `--host` and `--port` override `STARMAP_SERVER_HOST` and `STARMAP_SERVER_PORT`.

| Setting | Purpose |
| --- | --- |
| `STARMAP_STATE_DIR` | Runtime state, retained generations, and the instance seed. Put it on a persistent volume. |
| `STARMAP_CATALOG_WORKSPACE_PATH` | Catalog files that an operator maintains. It is not the state directory. |
| `--rate-limit` | Requests each minute from one address. The default is `100`. `0` turns off the limit. |
| `--sse-heartbeat-interval` | The stream heartbeat. Keep the default, because each replica expects it. |

One server on a plain persistent volume is a single-writer design. Two active servers need a store that supplies the refresh lease and a conditional write on the generation record. Do not run two active servers on one shared filesystem volume.

## Source updates

The server reads its own catalog source and can run its own provider acquisition.

| Setting | Effect on the server |
| --- | --- |
| `STARMAP_CATALOG_SOURCE` | The source kind of the server. The values are the same as in Starport. |
| `STARMAP_CATALOG_SOURCE_POLL_INTERVAL` | The check period of the source |
| `STARMAP_CATALOG_ACQUISITION_INTERVAL` | The period of provider observations |
| `STARMAP_CATALOG_ACQUISITION_ENABLED` | `false` stops provider requests from the server |
| `STARMAP_CATALOG_SOURCE_TOKEN` | A GitHub token that raises the rate limit of the channel |

## GitHub disabled

To stop GitHub requests from the server, set `STARMAP_CATALOG_SOURCE=embedded` or `STARMAP_CATALOG_SOURCE=file`. Neither kind contacts a host. To stop every catalog request, also set `STARMAP_CATALOG_ACQUISITION_ENABLED=false`.

## Authentication

Keep three credential groups in separate secrets.

| Credential | Name | Purpose |
| --- | --- | --- |
| Server API key | `API_KEY` on the server | The value that a replica sends |
| Source token | `STARMAP_CATALOG_SOURCE_TOKEN` | Reads the GitHub channel |
| Provider credentials | The provider names, for example `OPENAI_API_KEY` | Acquisition inputs of the server |

`--auth` makes the server require the server API key on each protected route. The health and readiness routes need no credential. A provider credential never acts as a server API key. Starport sends `STARPORT_CATALOG_SOURCE_API_KEY` in the `X-API-Key` header.

## Publication to replicas

Each replica holds one stream connection to the server. The server sends each new publication on that stream, so a push hop adds no poll interval to the freshness age. A stream that fails three times in a row falls back to a poll. A `401` or a `403` stops the subscription until the credential changes.

| Route | Reports |
| --- | --- |
| `GET /api/v1/health` | Process liveness only |
| `GET /api/v1/ready` | Catalog readiness and the connected runtime fields |
| `GET /api/v1/catalog/source-chain` | The hop list from the server to the origin |

Alert on `channel_freshness`, `source_check_freshness`, and `fallback` in the readiness response.

## Connect replicas to a central server

### Audience

An operator who runs a Starport fleet and one Starmap server.

### Before you start

- Install the Starmap binary on the server host.
- Create a persistent volume for `STARMAP_STATE_DIR`.
- Put the server API key in a secret store.

### Steps

1. Set `API_KEY` and `STARMAP_STATE_DIR` on the server.
2. Run `starmap serve --auth --host 0.0.0.0 --port 8080`.
3. On each replica, set `STARPORT_CATALOG_SOURCE=starmap`.
4. Set `STARPORT_CATALOG_SOURCE_URL` to the versioned base URL of the server.
5. Set `STARPORT_CATALOG_SOURCE_API_KEY` from the secret store.
6. Restart each replica.

```dotenv
STARPORT_CATALOG_SOURCE=starmap
STARPORT_CATALOG_SOURCE_URL=https://<starmap-host>/api/v1
STARPORT_CATALOG_SOURCE_API_KEY=<server-api-key>
```

### Expected result

Each replica reports `runtime.source_kind` as `starmap` and `runtime.fallback` as `false`.

### Verification

```bash
curl -fsS https://<starmap-host>/api/v1/ready
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/catalog/status | jq '.runtime.source_kind'
```

### If it fails

A non-loopback URL must use HTTPS. A `401` from the server means that the replica key does not match `API_KEY`. Read [Catalog failures](../troubleshoot/catalog-failures.md).

### Related settings

- `STARPORT_CATALOG_SOURCE_MAX_HOPS` limits the publication chain. The default is `8`.
- `STARPORT_CATALOG_ACQUISITION_ENABLED=false` keeps a replica off the provider APIs.
- Refer to [Catalog source topologies](../architecture/topologies.md).
