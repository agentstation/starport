---
title: Catalog failures
area: troubleshoot
order: 2
summary: Find why a model in the catalog does not route, and correct a missing credential, a missing authority, a stale source, or a rejected generation.
---

A catalog failure stops a model from routing while the gateway process runs. This topic connects each symptom to a field in the admin catalog status or the provider status.

## Status routes for diagnosis

Both routes need a gateway API key with the `admin` scope. A console session from the gateway host can also read the catalog status.

```bash
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/catalog/status
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/providers
```

| Field | Route | Meaning |
| --- | --- | --- |
| `runtime.usable` | catalog status | The runtime serves a catalog now. |
| `runtime.fallback` | catalog status | The runtime serves the embedded baseline. |
| `freshness.catalog` | catalog status | `current`, `warn`, `critical`, or `unknown`. |
| `route_validation.state` | catalog status | `unknown`, `pending`, `accepted`, or `rejected`. |
| `providers[].operator_credential.state` | provider status | `ready`, `not_configured`, `denied`, `invalid`, `unavailable`, or `refreshing`. |

## No usable provider credential

A model can be in the catalog while no provider can carry a request for it. Catalog membership comes from the catalog generation. A callable offering also needs a provider inference credential that the gateway can use.

**Symptom.** `starport models search <name>` finds the model. A request to the model fails with a provider credential error.

**Check.** In the provider status, find the provider of the model. The value `not_configured` with the reason `credential_not_configured` means that the gateway has no credential for that provider.

**Correction.** Set a credential that the Starmap profile of the provider declares, or use a secret reference. Then run `POST /api/v1/admin/providers/refresh` or restart the gateway.

## No accepted authority

The startup policy `require_authority` binds catalog permission to one Starmap authority and one policy.

**Symptom.** Requests fail with `Catalog permission is unavailable.` Diagnostic routes still answer.

**Check.** Confirm that `STARPORT_CATALOG_SOURCE` is `starmap`. Confirm that `STARPORT_CATALOG_SOURCE_AUTHORITY_ID` and `STARPORT_CATALOG_SOURCE_POLICY_ID` match the upstream authority. Confirm that `STARPORT_CATALOG_ACQUISITION_ENABLED` is `false`.

**Correction.** Restore the connection to the authority. If the native permission clock is on, qualify the host time service. Refer to [Run a central Starmap server](../operate-starmap/central-server.md).

## Stale source

**Symptom.** `freshness.catalog` is `warn` or `critical`, or `freshness.source_check` is `critical`.

**Check.** If `runtime.fallback` is `false`, the accepted head still routes and the source is late. If `runtime.fallback` is `true`, the gateway serves the embedded baseline.

**Correction.** Restore the source. Then start a refresh with `POST /api/v1/admin/catalog/refresh`. Read the run at the `Location` header that the route returns.

## Rejected generation

A candidate must pass the route, connector, and credential checks of Starport before it becomes the accepted head. A rejected candidate leaves the accepted head unchanged.

**Symptom.** `route_validation.state` is `rejected`.

**Check.** Read `route_validation.rejected.generation`, `route_validation.rejected.reason`, and `route_validation.rejected.at`.

**Correction.** Correct the cause, for example a missing adapter or a missing price. Then wait for the next publication or start a refresh.

## Diagnose a catalog failure

### Audience

An operator with an admin gateway API key or shell access to the gateway host.

### Before you start

- Get an admin gateway API key in `$STARPORT_ADMIN_KEY`.
- Confirm that `GET /health/live` returns `200`.
- Install `jq` for the verification command.

### Steps

1. Read the admin catalog status.
2. Compare `runtime`, `freshness`, and `route_validation` with the sections above.
3. Read the provider status for the provider of the failed model.
4. Correct the cause that the status names.
5. Read the admin catalog status again.

### Expected result

`runtime.usable` is `true`. `route_validation.state` is `accepted`. The provider credential state is `ready`.

### Verification

```bash
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/catalog/status | jq '.route_validation.state'
```

```text
"accepted"
```

### If it fails

Collect the catalog status, the provider status, and the log lines with the `request_id` of a failed request. Then read [Recover without a console session](recovery.md).

### Related settings

- `STARPORT_CATALOG_SOURCE_STARTUP_POLICY` selects `prefer_source`, `require_source`, `require_authority`, or `prefer_local`.
- `STARPORT_CATALOG_SOURCE_MAX_AGE` sets the age at which the catalog becomes stale. The default is `6h`.
- Refer to [Catalog update controls](../operate-starport/catalog-updates.md).
