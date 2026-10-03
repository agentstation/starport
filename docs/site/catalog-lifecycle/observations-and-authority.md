---
title: Observations, authority, and acceptance
area: catalog-lifecycle
order: 2
summary: Understand how provider observations, an authority, and route validation decide which catalog generation becomes the accepted head.
---

A catalog generation becomes the accepted head only after Starport validates it. This topic explains observations, authority, acceptance, and the counts that the status routes report.

## Provider observations

An observation is a read of a provider model list by the Starmap runtime of this instance. Starmap reconciles the observations with the authored model definitions and makes a new candidate generation.

| Setting | Default | Effect |
| --- | --- | --- |
| `STARPORT_CATALOG_ACQUISITION_ENABLED` | `true` | `false` stops automatic observations. An explicit refresh still runs. |
| `STARPORT_CATALOG_ACQUISITION_INTERVAL` | `4h` | `0s` makes one observation at start and no repeat. |
| `STARPORT_CATALOG_ACQUISITION_SOURCES` | not set | Selects the permitted inputs: `providers`, `local_catalog`, `models_dev_http`, `models_dev_git`. When you do not set it, the host defaults apply. An explicit empty value turns off each acquisition input. |

An observation uses a catalog acquisition credential. Starport checks `STARPORT_CATALOG_<PROVIDER>_<FIELD>` first, then `STARMAP_<PROVIDER>_<FIELD>`, then `STARPORT_<PROVIDER>_<FIELD>`, then the names that the catalog declares. Account BYOK records do not supply acquisition credentials.

## Authority

The `require_authority` startup policy binds catalog permission to one Starmap authority and one policy.

- `STARPORT_CATALOG_SOURCE` must be `starmap`.
- `STARPORT_CATALOG_ACQUISITION_ENABLED` must be `false`.
- `STARPORT_CATALOG_SOURCE_AUTHORITY_ID` and `STARPORT_CATALOG_SOURCE_POLICY_ID` must match the upstream authority.

The embedded baseline can supply diagnostics at a cold start. It cannot give inference permission. A new request, a retry, a queued batch line, and a cache delivery each need current permission. A stream that started before a withdrawal can finish.

## Acceptance

Starport keeps two pointers to the generation records.

| Pointer | Meaning |
| --- | --- |
| Candidate | The newest generation that the runtime holds |
| Accepted head | The newest generation that passed the route, connector, and credential checks of Starport |

A rejected candidate leaves the accepted head unchanged. The routes, the connectors, and the response cache identity then stay the same. `route_validation.state` in the admin catalog status is `unknown`, `pending`, `accepted`, or `rejected`.

`STARPORT_CATALOG_GENERATION_PIN` selects one retained generation. When the pin has a value, acceptance refuses a candidate that differs from the pinned selection. An empty value clears the pin.

## Generation counts

| Count | Where | Meaning |
| --- | --- | --- |
| `catalog.providers` | Admin catalog status | Providers in the accepted generation |
| `catalog.models` | Admin catalog status | Routable models |
| `providers`, `models` | `GET /api/v1/catalog` | Permitted providers with accepted offerings, and permitted routable models |
| Runtime generations | Process | At most four current, retained, or prepared generations |

An update that reaches the limit of four runtime generations stops with the reason `runtime_generation_capacity`. Retained requests can finish. Retry the update after a retained request releases its generation.

## Read the acceptance state

### Audience

An operator who must know which generation a gateway routes on.

### Before you start

- Get an admin gateway API key in `$STARPORT_ADMIN_KEY`.
- Install `jq`.

### Steps

1. Read the admin catalog status.
2. Read `provenance.effective` for the accepted head.
3. Read `route_validation.candidate` for the newest candidate.
4. If `route_validation.state` is `rejected`, read `route_validation.rejected.reason`.

### Expected result

`route_validation.state` is `accepted`, and the candidate and the accepted head name the same generation.

### Verification

```bash
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/catalog/status | jq '.route_validation.state'
```

```text
"accepted"
```

### If it fails

Read [Catalog failures](../troubleshoot/catalog-failures.md#rejected-generation). Starport keeps the last accepted head while you correct the cause.

### Related settings

- `STARPORT_CATALOG_SOURCE_STARTUP_POLICY` selects the authority policy.
- `STARPORT_CATALOG_REFRESH_TIMEOUT` caps one refresh run. The default `0s` adds no cap.
- Refer to [Catalog update controls](../operate-starport/catalog-updates.md).
