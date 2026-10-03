---
title: Membership and routability
area: catalog-lifecycle
order: 3
summary: Find why a model in the accepted catalog does not route, and read the counts that each route reports.
---

A model can be in the accepted catalog generation and still have no route. This topic explains how route planning selects the offerings that a request can reach.

## Membership compared with a route

Catalog membership means that the accepted generation contains the model definition and its provider offerings. A route means that the gateway has a compiled adapter for the provider and that the offering passes each planning check. A callable provider offering also needs a usable provider inference credential at the time of the request.

Route planning gives each offering in the generation one verdict. An offering that planning excludes carries the first exclusion that applies, in this order.

| Exclusion | Cause | Correction |
| --- | --- | --- |
| `adapter_not_ready` | This build has no adapter that can carry a request to the provider. | Use a build with an adapter for the provider. |
| `catalog_retired` | The offering lifecycle is retired. | Select another offering. |
| `catalog_unavailable` | The catalog declares the offering unavailable. | Wait for a new generation, or select another offering. |
| `offering_unavailable` | The provider status withholds the offering now. | Read the provider status. |
| `operation_unsupported` | The offering and the adapter share no operation with a usable endpoint. | Use a build that supports the operation. |
| `operation_unpriced` | Each shared operation has no catalog price, so the gateway cannot bill it. | Add a price in the Starmap catalog. |

## Adapter readiness and credential readiness

Adapter readiness and credential readiness are different facts. Adapter readiness means only that a compiled adapter for the provider exists. It does not read a credential. A provider with a ready adapter and no credential has routes. A request to one of those routes then fails with `Provider credentials are unavailable.`

The provider status at `GET /api/v1/admin/providers` shows both facts for each provider.

| Field | Values |
| --- | --- |
| `adapter.state` | `ready`, `unsupported_transport`, `unsupported_authentication`, `no_offerings` |
| `operator_credential.state` | `ready`, `not_configured`, `denied`, `invalid`, `unavailable`, `refreshing` |
| `offerings[].routing.state` | `unknown`, `routable`, `unroutable` |
| `offerings[].routing.reason` | The exclusion from the table above. The field is absent for `operation_unpriced`. |

## Counts

| Count | Route | Meaning |
| --- | --- | --- |
| `catalog.providers` | `GET /api/v1/admin/catalog/status` | Providers in the accepted generation |
| `catalog.models` | `GET /api/v1/admin/catalog/status` | Routable models in the accepted generation |
| `providers` | `GET /api/v1/catalog` | Permitted providers with accepted offerings |
| `models` | `GET /api/v1/catalog` | Permitted routable model definitions |

The output of `starport doctor` also gives the number of provider adapters and routable offerings in this build.

## No removal targets in Starport

Starport shows no removal targets. Starport has no removal store, route, command, or console screen for catalog records. Starmap owns the rename and restore behavior of catalog records. To remove a model from the routes of one deployment, change the account or key permissions, or change the Starmap catalog.

## Find why a model does not route

### Audience

An operator who finds a model in the catalog and cannot send a request to it.

### Before you start

- Get an admin gateway API key in `$STARPORT_ADMIN_KEY`.
- Get the provider ID and the provider model ID of the offering.
- Install `jq`.

### Steps

1. Read the provider status.
2. Find the provider, and read `adapter.state`.
3. Find the offering, and read `routing.state` and `routing.reason`.
4. Read `operator_credential.state` for the provider.
5. Apply the correction for the exclusion or the credential state.

### Expected result

`routing.state` is `routable`, and `operator_credential.state` is `ready`.

### Verification

```bash
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/providers \
  | jq '.providers[] | select(.provider_id == "<provider-id>") | .offerings[] | select(.provider_model_id == "<provider-model-id>") | .routing'
```

```json
{ "state": "routable" }
```

### If it fails

If `routing.state` is `unroutable` and `routing.reason` is absent, read the catalog price of the operation. If the state is `unknown`, the gateway has not published a route plan yet. Read [Catalog failures](../troubleshoot/catalog-failures.md).

### Related settings

- `STARPORT_CATALOG_ACQUISITION_ENABLED` controls provider observations.
- Refer to [Observations, authority, and acceptance](observations-and-authority.md).
