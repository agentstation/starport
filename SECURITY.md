# Security Policy

## Supported Versions

The latest Starport v1 release receives security fixes. Development snapshots
and older releases do not receive separate security support.

## Report a Vulnerability

Email security reports to `security@agentstation.ai`. Do not open a public
issue for a suspected vulnerability.

Include the affected version or commit, the deployment mode, reproduction
steps, the security effect, and any proposed remediation. Remove provider
keys, gateway keys, master keys, account data, and other secrets from every
report and attachment.

We will confirm receipt, assess the report, and coordinate remediation and
disclosure with the reporter. Do not disclose an unresolved report publicly
before that coordination is complete.

If a report, log, trace, or reproduction included a secret, revoke and replace
it immediately. Starport cannot recover or validate an exposed secret.
