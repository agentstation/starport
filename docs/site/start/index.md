---
title: Start
area: start
order: 0
summary: Run a first Starport gateway, issue gateway API keys, and inspect the catalog.
---

This area is for a developer or an operator who runs Starport for the first time. The topics follow one path: inspect the catalog, run a gateway, issue keys, and prepare a team deployment.

## The first path

1. Inspect the embedded catalog without a credential. Refer to [Inspect the catalog](first-catalog-inspection.md).
2. Run `starport dev`. The gateway keeps its keys, usage records, and uploaded files on one machine between runs. Refer to [Run a persistent local gateway](local-persistent.md).
3. Issue gateway API keys for each client. Refer to [Keys and roles](keys-and-roles.md).
4. For a team, create the first key in configured storage with `starport auth bootstrap --name <name>`. Refer to [Production form of initialization](local-persistent.md#production-form-of-initialization).

## Run a gateway that keeps nothing

`starport dev --ephemeral` runs a scratch gateway. It keeps all records in memory and removes its scratch files when it stops. Refer to [Run a scratch development gateway](temporary-development.md).

Both gateways read the same catalog and serve the same routes. The difference is the lifetime of the data.

## Two kinds of credential

A gateway API key authenticates a client to Starport. A provider inference credential pays a provider. The two keys are not interchangeable. The topics in this area keep them separate in every example.
