---
title: Catalog updates
area: operate-starport
order: 4
summary: Control when a gateway reads its catalog source and observes providers, start a refresh, pin a generation, and alert on freshness.
---

A gateway changes its catalog in two ways. It reads new publications from its source, and it observes providers directly. Each way has its own controls. A candidate generation routes only after Starport accepts it. Refer to [Observations and authority](../catalog-lifecycle/observations-and-authority.md) for the acceptance rules.

## Update controls

| Setting | Default | Effect |
| --- | --- | --- |
| `STARPORT_CATALOG_SOURCE_REFRESH_MODE` | `automatic` | `manual` permits only explicit source reads. |
| `STARPORT_CATALOG_SOURCE_POLL_INTERVAL` | `1h` | The period of the source check |
| `STARPORT_CATALOG_ACQUISITION_ENABLED` | `true` | `false` stops automatic provider observations. |
| `STARPORT_CATALOG_ACQUISITION_INTERVAL` | `4h` | The period of provider observations. `0s` makes one observation at start. |
| `STARPORT_CATALOG_STARTUP_SPREAD` | `15m` | Spreads the first source read across a fleet |
| `STARPORT_CATALOG_REFRESH_TIMEOUT` | `0s` | An added limit on one refresh run. `0s` adds no limit. |
| `STARPORT_CATALOG_SOURCE_MAX_AGE` | `6h` | The oldest publication that this instance accepts |
| `STARPORT_CATALOG_GENERATION_PIN` | Not set | Holds the catalog on one retained generation |

`starport config effective` shows each value with its authority and origin. The command prints the shared Starmap names, for example `STARMAP_CATALOG_SOURCE_POLL_INTERVAL`. Starport reads the `STARPORT_CATALOG_` names first. For a deployment-scope setting, it also reads the `STARMAP_CATALOG_` name as a fallback.

The acquisition switch and the source refresh mode are independent. With `ACQUISITION_ENABLED=false`, the gateway still reads new source publications. To stop automatic changes from the source too, set `SOURCE_REFRESH_MODE=manual`.

Startup refuses a removed setting and names its replacement. For example, `STARPORT_CATALOG_REFRESH_INTERVAL` became `STARPORT_CATALOG_ACQUISITION_INTERVAL`.

## Update policy selector

The update policy decides when the catalog of a gateway can change. It is independent from the catalog source topology. First select the topology in [Catalog source topologies](../architecture/topologies.md). Then select one policy from this table.

| Policy | Settings | Catalog changes |
| --- | --- | --- |
| Automatic | The defaults | Source reads each hour and provider observations each 4 hours |
| Source only | `STARPORT_CATALOG_ACQUISITION_ENABLED=false` | Source reads only. The gateway makes no provider observation. |
| Manual | `STARPORT_CATALOG_SOURCE_REFRESH_MODE=manual` and `STARPORT_CATALOG_ACQUISITION_ENABLED=false` | Only an explicit refresh run |
| Pinned | `STARPORT_CATALOG_GENERATION_PIN=<generation-id>` | None until the pin changes |
| Offline | `STARPORT_CATALOG_NETWORK_MODE=offline` | No change from a catalog network request |

Each architecture target has a usual policy:

| Target | Usual policy |
| --- | --- |
| [T1](../architecture/recipes.md#t1-standalone-starmap) | Starmap controls its own updates. Refer to [Run a central Starmap server](../operate-starmap/central-server.md). |
| [T2](../architecture/recipes.md#t2-persistent-local-starport) | Automatic |
| [T3](../architecture/recipes.md#t3-one-production-server) | Automatic. Use the pin for a controlled change window. |
| [T4](../architecture/recipes.md#t4-replicated-starport) | Automatic. The replica that holds the refresh lease runs the source reads and the observations. |
| [T5](../architecture/recipes.md#t5-internal-starmap-server) | Automatic, or source only when the server owns the provider observations |
| [T6](../architecture/recipes.md#t6-restricted-or-air-gapped-installation) | Source only with the `file` source |
| [T7](../architecture/recipes.md#t7-ephemeral-development) | The development defaults |

## The generation pin

`STARPORT_CATALOG_GENERATION_PIN` names one retained generation. While the setting has a value, acceptance refuses each candidate that differs from the pinned generation. The pin stays until the configuration changes. The console does not pin a generation. It points to the pin field in Settings.

## The refresh run

`POST /api/v1/admin/catalog/refresh` starts one run and returns `202` with a `Location` header. A second request while a run is active joins that run, and the response shows `"joined": true`. The run states are `accepted`, `running`, `succeeded`, `failed`, and `canceled`. `DELETE /api/v1/admin/catalog/refreshes/{run_id}` cancels a run.

## Freshness alerts

`GET /api/v1/admin/catalog/status` grades three ages. Each grade is `current`, `warn`, `critical`, or `unknown`. The gateway computes the grade, so an alert reads the grade and does not compute an age. The Prometheus scrape has no catalog metric, so use a JSON probe.

| Alert | Condition | Action |
| --- | --- | --- |
| Catalog stale | `freshness.catalog` is `warn` for 30 minutes | Find the late source or the slow hop. |
| Catalog critical | `freshness.catalog` is `critical` | Page an operator. |
| Channel stale | `freshness.channel` is `warn` or worse for 30 minutes | Examine the upstream publication chain. |
| Source check stale | `freshness.source_check` is `critical` | Examine the network path to the source. |
| Fallback active | `runtime.fallback` is `true` 15 minutes after start | Restore the source. |
| Candidate rejected | `route_validation.state` is `rejected` | Read [Catalog failures](../troubleshoot/catalog-failures.md). |

A `critical` grade with `runtime.fallback` as `false` means that the accepted head still routes. With `runtime.fallback` as `true`, the gateway serves the embedded baseline.

## Refresh the catalog and read the run

### Audience

An operator who wants a new catalog now and does not want to wait for the next interval.

### Before you start

- Get a gateway key with the `admin` scope in `$STARPORT_ADMIN_KEY`.
- Make sure that `STARPORT_CATALOG_GENERATION_PIN` has no value.

### Steps

1. Start a refresh run.
2. Copy the `id` from the response.
3. Read the run until its state is `succeeded`, `failed`, or `canceled`.

```bash
curl -sS -X POST -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/catalog/refresh | jq '.id, .state, .joined'
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/catalog/refreshes/<run-id> | jq '.state, .changed, .generation_id'
```

### Expected result

The run state is `succeeded`. `changed` is `true` when the run produced a new generation.

### Verification

```bash
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/catalog/status | jq '.freshness.catalog, .route_validation.state'
```

### If it fails

A `failed` run has a `reason` field. The accepted head continues to route. Read [Catalog failures](../troubleshoot/catalog-failures.md).

### Related settings

- `STARPORT_CATALOG_NETWORK_MODE=offline` stops catalog network requests.
- Refer to [Sources and the baseline](../catalog-lifecycle/sources-and-baseline.md).
