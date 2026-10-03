---
title: Authentication and the local admin token
area: operate-starport
order: 1
summary: Select the authentication mode, understand the exposure tripwire, and use and rotate the local admin token to open the console.
---

Starport requires a gateway API key on each inference and management route by default. This topic explains the authentication mode, the exposure tripwire, the local admin token, rotation, and how to open the console.

## Authentication modes

The mode is `required` or `disabled`. Four sources can state it. The first source that states the mode wins.

| Source | How | Survives a restart |
| --- | --- | --- |
| Flag | `--no-auth` on `starport serve` or `starport dev` | No |
| Configuration | `STARPORT_SECURITY_AUTH_MODE=disabled` | Yes |
| Console | The switch in Settings | Yes |
| Default | No statement | The mode is `required` |

A flag or a configuration value fixes the mode for the process. The console switch then refuses a change and names the value to change. `GET /api/v1/auth/mode` needs no credential. It reports the mode, its source, and whether this caller can change it.

## The exposure tripwire

Starport does not start with authentication disabled on an address that the network can reach. A loopback address is exempt. To run without keys on a reachable address, add `--allow-remote-no-auth` to `--no-auth`.

Starport checks a stored `disabled` mode against the bind address of each process. If the address is reachable, the gateway uses `required` and logs a warning. The console switch works only from the machine that runs the gateway.

## The local admin token

The machine makes a local admin token at the first start. The token proves that you are on the machine. It is not a gateway API key. `starport dev` never writes the token file.

A gateway on an address that the network can reach does not start with the first-boot token. That token went to a terminal, so it is safe only on loopback. Run `starport auth rotate`, and then start the gateway again.

| | Gateway API key | Local admin token |
| --- | --- | --- |
| Belongs to | An account | No account |
| Proves | The identity of the caller | Access to the machine |
| Stored in | Encrypted storage | One file with mode `0600` |
| Prefix | `STARPORT` | `starport_local_` |
| Revoked by | Deletion of the key | `starport auth rotate` |

| Command | Effect |
| --- | --- |
| `starport auth status` | Shows the generation, the age, and the exposure of the token |
| `starport auth token` | Prints the token. `--copy` puts it on the clipboard. |
| `starport auth url` | Prints a one-time launch link. `--open` opens it. |
| `starport ui` | Makes a launch link and opens the console |

`starport ui` reads the token file and does not call the gateway. It makes a link while the gateway is down.

## Opening the console

A console session is a signed HttpOnly cookie. Three grants can start a session.

| Grant | Presents |
| --- | --- |
| `ticket` | A one-time launch ticket in the URL |
| `local-token` | The local admin token, pasted on the first-contact page |
| `identity` | An identity provider sign-in. Refer to [Identity providers and account selection](identity.md). |

An operator who is not at the machine can use a gateway API key in the console. That key authenticates a caller, and Starport meters its use against an account.

## Rotate the local admin token

### Audience

An operator who binds a gateway to a reachable address, or who thinks that the token leaked.

### Before you start

- Get shell access to the gateway host as the account that runs the gateway.

### Steps

1. Run `starport auth status`.
2. Run `starport auth rotate`.
3. Run `starport ui` to open a new console session.

### Expected result

The generation increases. Each earlier launch link and each live console session stops working.

### Verification

```bash
starport auth status --json
```

The output shows a higher generation than before the rotation.

### If it fails

Starport refuses an unreadable token file on each read path and does not replace it. `starport auth rotate` repairs the file. For other failures, read [Recover without a console session](../troubleshoot/recovery.md).

### Related settings

- `STARPORT_SECURITY_AUTH_MODE` sets the authentication mode.
- `starport auth rotate --no-secret` reports the rotation and does not print the secret.
- Refer to [Keys and roles](../start/keys-and-roles.md).
