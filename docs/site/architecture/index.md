---
title: Choose an architecture
area: architecture
order: 0
summary: Select a deployment target, a catalog source topology, and a storage recipe before you install.
---

This area is for an operator who plans a Starport deployment. Make three decisions in this order before you install or change a deployment.

## Three decisions

1. Select an architecture target. A target names the processes, the catalog authority, and the storage recipe.
2. Select a catalog source topology. The topology sets the network egress, the catalog freshness, and the GitHub request budget.
3. Select the storage backends. The storage recipe follows from the process count of the target.

Then read the [deployment recipe](recipes.md) of the target. Each recipe gives the durable owners, the runnable checks, and the recovery procedure.

## Process count is not organization size

A developer can run several gateway replicas. An enterprise can run one gateway process. Select the target from the process count and the network boundary, not from the size of the organization.

## Release limits

Some targets are complete in this release. Other targets are specification targets that wait for qualification. Each topic in this area states the status of each target.
