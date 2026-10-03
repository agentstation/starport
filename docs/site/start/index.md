---
title: Start
area: start
order: 0
summary: Run a first Starport gateway, issue gateway API keys, and inspect the catalog.
---

This area is for a developer or an operator who runs Starport for the first time. The topics show two ways to run a gateway, the keys and roles that control access, and the commands that inspect the catalog.

## Choose a first gateway

Use a temporary development gateway to try Starport. It keeps all records in memory and removes its scratch files when it stops.

Use a persistent local gateway when you want to keep keys, usage records, and uploaded files. It writes a configuration file and a data directory on one machine.

Both gateways read the same catalog and serve the same routes. The difference is the lifetime of the data.

## Two kinds of credential

A gateway API key authenticates a client to Starport. A provider inference credential pays a provider. The two keys are not interchangeable. The topics in this area keep them separate in every example.
