---
title: Catalog source topologies
area: architecture
order: 2
summary: Select one of five catalog source topologies or the replicated central variant from four network questions.
---

A catalog source topology decides where each gateway gets its catalog. It sets the egress, the freshness age, and the GitHub request budget of the deployment.

## Decision questions

Answer four questions:

1. How many gateway instances leave one egress address?
2. Do the replicas reach GitHub?
3. Do the replicas reach the provider APIs?
4. Does any host inside the boundary reach the internet?

Read the table from the top. Use the first row that matches the deployment. If the answer to question 4 is no, use the air-gapped mirror. If only the answer to question 2 is no, use a central Starmap server.

## Topology table

| Topology | Replica egress | `SOURCE` | `ACQUISITION_ENABLED` | Freshness age |
| --- | --- | --- | --- | --- |
| Single Starport with direct GitHub | GitHub and providers | `public` | `true` | 6 hours |
| Starport fleet with direct GitHub | GitHub and providers | `public` | `true` | 6 hours |
| Central Starmap server with replica acquisition | Central server and providers | `starmap` | `true` | 6 hours |
| Restricted replica egress | Central server only | `starmap` | `false` | 6 hours |
| Air-gapped mirror | None | `file` | `false` | Transfer cadence |

Each setting name in the table has the prefix `STARPORT_CATALOG_`.

## Single Starport with direct GitHub

One gateway follows the public channel `catalog/v1`. The shipped defaults apply, so this topology needs no catalog setting. The gateway polls the source each hour. One gateway uses about two percent of the unauthenticated GitHub budget.

## Starport fleet with direct GitHub

Several gateways share one egress address and follow the public channel. Set `STARPORT_CATALOG_SOURCE_TOKEN` to one GitHub token for all replicas. The token raises the hourly limit from 60 requests for each address to 5,000 requests for each token.

Move to a central Starmap server at any of these points:

- More than 60 replicas behind one address without a token.
- More than about 5,000 replicas that share one token.
- More than 10,000 replicas.

## Central Starmap server with replica acquisition

One Starmap server follows GitHub and pushes each publication to the fleet. Set these values on each replica:

```dotenv
STARPORT_CATALOG_SOURCE=starmap
STARPORT_CATALOG_SOURCE_URL=<starmap-server-url>/api/v1
STARPORT_CATALOG_SOURCE_API_KEY=<starmap-server-api-key>
```

The source API key is a catalog credential. It never pays a provider. Each replica still observes the providers on its own schedule.

## Restricted replica egress

The central server reaches GitHub and the providers. Each replica reaches only the central server. Set `STARPORT_CATALOG_ACQUISITION_ENABLED=false` on every replica. This topology is not air-gapped, because the central server has a route to the internet.

## Air-gapped mirror

No host inside the boundary reaches the internet. A process outside the boundary reads the artifact and its verification bundle. An operator moves both files across the boundary on a schedule.

```dotenv
STARPORT_CATALOG_SOURCE=file
STARPORT_CATALOG_SOURCE_URL=/absolute/path/to/catalog-file
STARPORT_CATALOG_ACQUISITION_ENABLED=false
```

Set `STARPORT_CATALOG_SOURCE_MAX_AGE` to the transfer cadence. A late transfer changes the freshness grade to `warn` and then `critical`. The accepted head keeps every route at each grade.

## Replicated central Starmap servers

This variant applies to the three topologies with a central server. Two or more Starmap servers run behind one load balancer. Active-active operation needs a store with a refresh lease and a conditional write on the generation record.

A plain shared volume has neither property. With such a volume, run one active server and one standby. Start the standby only after the active server stops.

Each Starport keeps its own state directory. Never share `STARPORT_CATALOG_STATE_DIR` between two instances. Two instances that share it get one identity, and the lease then fences nothing.

## Failure behavior

A source failure leaves the accepted head in place in every topology. The default startup policy `prefer_source` starts on the embedded baseline. Then it adopts the source at the first successful read. The policy `require_source` stops startup when the first source read fails.

The [deployment topologies guide](../../DEPLOYMENT-TOPOLOGIES.md#decision-table) gives the request rates and the diagrams. Refer to [Catalog update controls](../operate-starport/catalog-updates.md) for the intervals.
