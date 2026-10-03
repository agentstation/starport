---
title: Manage configuration from the console
area: configure
order: 4
summary: Read and change catalog settings in Settings > Configuration, and learn the checks that each save runs.
---

The console shows the effective catalog settings in Settings > Configuration. An operator with the `admin` scope can check and save some settings there.

## Sections of the page

| Section | Settings |
| --- | --- |
| Catalog source | `catalog_source`, its URL, API key, repository, channel, signer workflow, token, refresh mode, poll interval, startup policy, and `catalog_generation_pin` |
| Inference access | `catalog_network_mode`, `catalog_acquisition_enabled`, the acquisition sources, the provider bindings, and the acquisition interval |
| Advanced details | All other settings, the paths, the storage, and the caches |

Each setting shows its value, its origin, and its authority. A sealed value shows only a presence marker.

## Management modes

`STARPORT_CONFIG_MANAGEMENT` selects the authority. The console shows one of these labels:

| Mode | Console label | Save target |
| --- | --- | --- |
| `local` | This process reads its configuration file | The local configuration file |
| `shared` | Every process reads one revision store | The next shared revision |
| `external` | A controller outside Starport owns the configuration | None. The console shows the difference only. |

Each edit control names its target before the save.

## Storage labels

The storage panel shows one row for each store. `Records` is the KV store. `Relational` is the SQL store. `File bytes` is the blob store. The lifetime is `process`, `local`, or `service`. The panel never shows a service address or a credential.

## Save a setting

**Audience:** an operator with a console session or a key with the `admin` scope.

**Before you start:**

- Make sure that the process environment does not set the value. An environment value wins over the file.
- Read the current revision in the page.

**Steps:**

1. Open Settings > Configuration.
2. Change the value.
3. Select the check. The console validates the edit and writes nothing.
4. For a source change, run the connection test.
5. Save the change.
6. Restart the gateway.

**Expected result:** The save returns a receipt with the status `saved`. After the restart, the receipt shows the new revision as `applied`.

**Verification:**

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_ADMIN_KEY" \
  http://127.0.0.1:8080/api/v1/admin/config/operations/<operation-id>
```

The receipt names the operation, the actor, the `saved` revision, and the `applied` revision.

**If it fails:** Read the refusal reason in the table below.

**Related settings:** `STARPORT_CONFIG_MANAGEMENT`, `STARPORT_CONFIG_FILE`.

## Save refusals

| Status | Reason | Action |
| --- | --- | --- |
| `403` | Cross-site origin or `foreign_deployment` | Use the console of this gateway |
| `409` | `stale_revision` | Reload the page and edit again |
| `409` | `operation_conflict` | Use a new operation ID |
| `409` | `busy` or `incomplete` | Follow the next step in the message |
| `422` | `migration_boundary`, `policy_change`, or `invalid_edit` | Fix the edit, or use the command line |
| `423` | `external_management` | Change the value in the external controller |
| `503` | `schema_behind` or `unavailable` | Start a gateway of this version, then save again |

An exact retry of a saved operation returns the first receipt.

## Local save checks

A local save rewrites one configuration file. The expected revision is the SHA-256 checksum of the file. A local save refuses in these cases:

- The process reads more than one configuration file.
- The configuration file path is not absolute.
- The process environment sets the same setting.
- The value contains a quote, a line break, or a trailing backslash.

The journal `.starport-config-operations.json` lets a retry finish an interrupted save. Back it up with the configuration file.

## Shared save checks

Under shared management, the expected revision is the head sequence number. A console save can change only the source API key and the source token. A change to the acquisition policy refuses with `policy_change`. Use `starport config apply` for that change. Refer to [Initialize and run a fleet](../operate-starport/fleet.md).

## Pin a catalog generation

`catalog_generation_pin` is the setting `STARPORT_CATALOG_GENERATION_PIN`. It selects one retained catalog generation and blocks catalog changes. Permission observation continues. An empty value clears the pin. The catalog panel does not set a pin. It points to this setting.

Refer to [Catalog update controls](../operate-starport/catalog-updates.md) before you pin a generation.
