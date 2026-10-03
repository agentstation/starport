---
title: Identity providers and account selection
area: operate-starport
order: 2
summary: Let people open the console through Google, GitHub, or WorkOS SSO, and select the account that an identity session uses.
---

An identity provider lets a person open a console session with an external sign-in. This topic tells you how to configure the providers, register the redirect address, and select an account.

## Identity paths

Starport has two identity paths. You can configure one path or both paths.

| Path | Provider name | Settings |
| --- | --- | --- |
| OAuth application | `google` | `STARPORT_IDENTITY_OAUTH_GOOGLE_CLIENT_ID`, `STARPORT_IDENTITY_OAUTH_GOOGLE_CLIENT_SECRET` |
| OAuth application | `github` | `STARPORT_IDENTITY_OAUTH_GITHUB_CLIENT_ID`, `STARPORT_IDENTITY_OAUTH_GITHUB_CLIENT_SECRET` |
| Enterprise SSO | `workos` | `STARPORT_IDENTITY_WORKOS_API_KEY`, `STARPORT_IDENTITY_WORKOS_CLIENT_ID`, and an organization or a connection |

For WorkOS, set `STARPORT_IDENTITY_WORKOS_ORGANIZATION`, `STARPORT_IDENTITY_WORKOS_CONNECTION`, or both. One of the two is necessary.

Starport keeps the identity records in the SQL store. Each sign-in updates the subject, email, and display name of the person. A person who signs in again gets the same user record.

## Redirect address

The redirect address is the callback that the provider sends the browser to. Its form is `<base>/console/identity/<provider>/callback`.

- `STARPORT_IDENTITY_CALLBACK_BASE_URL` sets `<base>`.
- If you do not set it, `<base>` is `http://<STARPORT_SERVER_HOST>:<STARPORT_SERVER_PORT>`.
- Behind a proxy or a domain, set the address that the browser reaches.

For WorkOS, add `<base>/console/identity/workos/callback` as a redirect address in the WorkOS dashboard.

## Configure an identity provider

### Audience

An operator who lets people open the console with an external sign-in.

### Before you start

- Register an OAuth application with Google or GitHub, or get a WorkOS API key and client.
- Know the address that the browser uses to reach the gateway.
- Keep the client secret and the API key in a secret store. Refer to [Secret references](../configure/secret-references.md).

### Steps

1. Register `<base>/console/identity/<provider>/callback` with the provider.
2. Set `STARPORT_IDENTITY_CALLBACK_BASE_URL` to `<base>`.
3. Set the client ID and the client secret of each OAuth application.
4. For WorkOS, set the API key, the client ID, and the organization or connection.
5. Restart the gateway.

```dotenv
STARPORT_IDENTITY_CALLBACK_BASE_URL=https://<gateway-host>
STARPORT_IDENTITY_OAUTH_GITHUB_CLIENT_ID=<github-client-id>
STARPORT_IDENTITY_OAUTH_GITHUB_CLIENT_SECRET=<secret-reference>
```

### Expected result

The console first-contact page shows each configured provider as a choice. A completed sign-in opens a console session with the same cookie as the other session grants.

### Verification

```bash
curl -sS <gateway-url>/console/identity/providers
```

The response lists each configured provider. With no provider, the list is empty.

### If it fails

- An incomplete path stops the gateway at start. The gateway refuses a client ID with no secret.
- The gateway also refuses a WorkOS key with no client, or WorkOS with no organization or connection.
- If you configure no provider, the start and callback routes return `503` and name the settings.
- If the provider refuses the callback, compare the registered address with `<base>/console/identity/<provider>/callback`.

### Related settings

- `STARPORT_SERVER_HOST` and `STARPORT_SERVER_PORT` set the default callback base.
- `STARPORT_SECURITY_ENABLE_CORS` and `STARPORT_SECURITY_ALLOWED_ORIGINS` control cross-origin clients.

## Identity account selection

An identity session can use only the accounts that grants give to its user or its teams. An identity session gets account scopes. It does not get deployment administration.

| Grants | Request | Result |
| --- | --- | --- |
| One account | No header | Starport selects that account. |
| Two or more accounts | No header | `409 account_selection_required` |
| Two or more accounts | `X-Starport-Account-ID: <account-id>` | Starport uses the selected account. |
| Any | An account with no grant | `403 permission_error` |
| Any | Two `X-Starport-Account-ID` headers | `403 permission_error` |

No grant gives access to the default account of the deployment by implication. If the authorization policy is not available, the request gets `503` with `Retry-After: 1`.

### Cross-origin clients

For a client on a different origin, set `STARPORT_SECURITY_ENABLE_CORS=true`. Name the console origin in `STARPORT_SECURITY_ALLOWED_ORIGINS`. The default allowed headers include `X-Starport-Account-ID`.

## Related topics

- [Authentication](authentication.md) explains the local admin token and the other session grants.
- [Keys and roles](../start/keys-and-roles.md) explains gateway API keys and scopes.
