---
title: Secret references and secret managers
area: configure
order: 2
summary: Resolve provider inference credentials from a secret manager, a file, or a named variable, or inject them with a command wrapper.
---

Starport reads provider inference credentials from the environment by default. A secret reference tells Starport to read one credential field from a secret manager, a file, or another environment variable.

## Conventional names

Each provider in the catalog declares conventional environment names, such as `OPENAI_API_KEY`. Starport finds a credential under that name with no extra setting.

For each credential field, Starport also derives a product name, such as `STARPORT_OPENAI_API_KEY`. Add the suffix `_REFERENCE` to that name to select a direct source.

## Reference grammar

```text
backend:resource?version=VERSION#field
```

The version and the field are optional. Put quotes around a reference that contains `#` in a shell or an environment file.

| Backend | Resource | Store authentication |
| --- | --- | --- |
| `gcp-secret-manager` | `projects/PROJECT/secrets/SECRET` | Google Application Default Credentials |
| `azure-key-vault` | `https://VAULT_HOST/secrets/SECRET` | `DefaultAzureCredential` |
| `aws-secrets-manager` | A secret name or ARN | AWS default credential chain |
| `vault` | `MOUNT/PATH` of a KV v2 secret | Vault client environment |
| `openbao` | `MOUNT/PATH` of a KV v2 secret | OpenBao client environment |
| `env` | The name of an environment variable | None |
| `file` | An absolute path | None |

A reference holds only the identity of the resource. Never put a credential for the secret store in a reference. Starport authenticates to each store with its default identity chain or its client settings.

## Examples

Read the key from AWS Secrets Manager:

```bash
export STARPORT_OPENAI_API_KEY_REFERENCE='aws-secrets-manager:starport/openai#api-key'
```

Use another environment variable instead of the conventional name:

```bash
export STARPORT_OPENAI_API_KEY_REFERENCE='env:TEAM_OPENAI_API_KEY'
```

Read a mounted secret file:

```bash
export STARPORT_OPENAI_API_KEY_REFERENCE='file:/run/secrets/openai-api-key'
```

## Field selection

For Google, Azure, and AWS, `#field` selects one top-level JSON string. Without a field, Starport keeps the full payload. For Vault and OpenBao, `#field` selects one string field. Without a field, the KV v2 record must contain exactly one string value.

## File references

The file must be a regular file that is not empty and is 1 MiB or smaller. Starport keeps every byte. It does not remove a trailing newline. Starport detects an in-place rewrite, an atomic replacement, and a symbolic link swap. It also detects a Kubernetes projected volume swap.

## Failure behavior

An explicit reference wins over the conventional name and the product name. By default, a failed reference does not fall back. To use the conventional name when the store reports `not_configured`, set the fallback flag:

```bash
export STARPORT_OPENAI_API_KEY_REFERENCE_FALLBACK_AMBIENT=true
```

Denied access, invalid material, an unavailable store, a timeout, and a cancellation never fall back. The fallback flag without a reference stops startup.

## Refresh

Starport resolves a reference before an inference request uses it. It keeps the result in memory. The next resolution after five minutes reads the store again. Set `STARPORT_CREDENTIAL_SOURCES_REMOTE_REFRESH_INTERVAL` to change the interval. Starport never logs or serializes the material.

## Command wrappers

A secret manager wrapper can set the conventional names for the child process. Authenticate the wrapper and select its project first. Then use one of these forms:

```bash
doppler run -- starport serve
op run --env-file="./.env.starport" -- starport serve
infisical run -- starport serve
```

For 1Password, `.env.starport` holds `NAME=op://vault/item/field` lines. A wrapper sets the values when the child process starts. Restart the process to read a changed value.

## Verify a reference

**Audience:** an operator who added a reference.

**Before you start:** Authenticate the shell to the secret store with the same identity as the service.

**Steps:**

1. Run the passive startup checks:

   ```bash
   starport doctor
   ```

2. Start the gateway and send one small inference request to a model of that provider.

**Expected result:** `starport doctor` exits with status 0. The inference request returns `200`.

**Verification:** The response body names the model, and the usage record of the request names the provider.

**If it fails:** Read the error name. Fix the store access or the reference. Do not add the fallback flag to hide an access failure.

**Related settings:** `STARPORT_CREDENTIAL_SOURCES_REMOTE_REFRESH_INTERVAL`, the `_REFERENCE_FALLBACK_AMBIENT` flags.

The [operator guide](../../OPERATOR-GUIDE.md#direct-secret-sources) lists the version rules for each backend.
