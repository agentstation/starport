# Starport deployment topologies

Starport reads one connected Starmap runtime. A deployment names one catalog
source kind, and that kind decides the egress, the freshness age, and the
request budget. This guide names five topologies and one replicated variant.

Every setting name below carries the `STARPORT_CATALOG_` prefix. The
[operator guide](OPERATOR-GUIDE.md#catalog-configuration) gives each name, its
default, its valid values, and its interactions. The
[configuration reference](../.env.example) lists every name with its default.

Starmap publishes to the `catalog/v1` channel every four hours at minute
17. A gateway asks its source for a newer publication every hour. The
end-to-end freshness objective is six hours. A polling hop adds one poll
interval to that age. A push hop adds no interval.

## Decision table

| Topology | Catalog egress | Inference egress | `SOURCE` | `ACQUISITION_ENABLED` | Freshness age |
| --- | --- | --- | --- | --- | --- |
| Single Starport with direct GitHub | GitHub and providers | providers | `public` | `true` | 6 hours |
| Starport fleet with direct GitHub | GitHub and providers | providers | `public` | `true` | 6 hours |
| Central Starmap server with replica acquisition | central server and providers | providers | `starmap` | `true` | 6 hours |
| Restricted replica egress | central server alone | providers | `starmap` | `false` | 6 hours |
| Air-gapped mirror | none | providers inside the boundary | `file` | `false` | transfer cadence |

Catalog egress carries the source reads and the provider observations.
Inference egress carries the client requests that the gateway sends to a
provider. A topology sets the catalog egress alone.

Read the table from the top and take the first row that matches the
deployment. Four questions select the row:

1. How many gateway instances leave one egress address?
2. Do the replicas reach GitHub?
3. Do the replicas reach the provider APIs?
4. Does any host inside the boundary reach the internet?

A deployment that answers no to question 4 uses the air-gapped mirror. A
deployment that answers no to question 2 alone uses a central Starmap server.

## Single Starport with direct GitHub

One gateway follows the public publication channel and observes the providers
on its own schedule.

```mermaid
flowchart LR
  GH[("GitHub catalog/v1")]
  SP[Starport gateway]
  PR[Provider APIs]
  GH -->|hourly conditional poll| SP
  SP -->|acquisition every 4 hours| PR
```

**Settings.** The shipped defaults hold, and this topology needs no catalog
setting. `SOURCE` stays `public`, `ACQUISITION_ENABLED` stays `true`, and
`SOURCE_POLL_INTERVAL` stays `1h`.

**Request budget.** The gateway sends about one conditional source request an
hour. GitHub allows 60 unauthenticated requests an hour for each egress
address, and a `304` answer counts against that ceiling. One gateway therefore
uses about two percent of the unauthenticated budget.

**Freshness age.** Starmap publishes every four hours. The gateway polls every
hour, so the age objective is six hours. The runtime grades the served
catalog `warn` above `SOURCE_MAX_AGE` and `critical` above five thirds of it.
The default of six hours gives `warn` above six hours and `critical` above
ten hours.

**Egress.** For the catalog, the gateway reaches GitHub and reaches each
provider API for acquisition. For inference, the gateway reaches each provider
API that serves a request.

**Failure behavior.** A source that does not answer leaves the accepted head in
place. The default `prefer_source` policy starts the gateway on the embedded
baseline and adopts the source at the first successful read. The
`require_source` policy reads the source once at open. It fails startup when
that read fails.

## Starport fleet with direct GitHub

Two or more gateways follow the same public channel behind one egress address.

```mermaid
flowchart LR
  GH[("GitHub catalog/v1")]
  subgraph NAT[One egress address]
    S1[Starport 1]
    S2[Starport 2]
    S3[Starport N]
  end
  PR[Provider APIs]
  GH -->|hourly conditional poll| NAT
  NAT -->|acquisition every 4 hours| PR
```

**Lease ownership.** In a shared fleet, one replica at a time holds the
refresh lease. Only that replica reads the source and observes the providers.
The other replicas send no GitHub request. They check the shared accepted head
every 30 seconds and follow it. A shared fleet therefore puts the load of one
poller on GitHub. Each separate deployment behind the same address adds one
poller.

**Settings.** Set `SOURCE_TOKEN` to one GitHub token on every replica. Any
replica can take the lease, so each replica needs the token. The token raises
the hourly ceiling from 60 requests for each address to 5,000 requests for each
token. Keep `STARTUP_SPREAD` at `15m`, because the spread holds many pollers
away from one moment.

**Request budget.** A direct consumer budgets from the GitHub rate-limit
headers. The four headers are `x-ratelimit-limit`, `x-ratelimit-used`,
`x-ratelimit-remaining`, and `x-ratelimit-reset`. The runtime records the
measured requests for each refresh cycle. The capacity in pollers is the
remaining budget minus a reserved headroom, divided by the measured requests
for each cycle. The status warns when `used` passes 80 percent of `limit`.

The rate on GitHub follows the poller count and the window:

| Pollers | 15-minute startup spread | 1-hour poll interval | 4-hour acquisition |
| --- | --- | --- | --- |
| 100 | 0.111 requests a second | 0.028 requests a second | 0.007 requests a second |
| 10,000 | 11.11 requests a second | 2.78 requests a second | 0.69 requests a second |
| 100,000 | 111.1 requests a second | 27.78 requests a second | 6.94 requests a second |

A published ceiling is not a safe threshold. Move to a central Starmap server
at any of these three points:

- Above 60 pollers behind one egress address with no token. Each poller
  needs one poll an hour, and the hourly ceiling is 60.
- Above about 5,000 pollers that share one token. The token ceiling is 5,000
  requests an hour.
- Above 10,000 pollers. The 15-minute spread then puts 11 requests a second
  against a secondary limit of 15 requests a second. No headroom remains.

**Freshness age.** The objective stays six hours, because the lease owner reads
the channel directly. The followers check the shared head every 30 seconds.

**Egress.** Every replica needs a route to GitHub and to the provider APIs for
the catalog, because any replica can take the lease. Every replica reaches the
provider APIs for inference.

**Failure behavior.** A rate-limit refusal leaves the shared accepted head in
place, and the next phase retries. The followers keep the same head. A lease
owner that stops loses the lease when the lease expires, and another replica
takes it.

## Central Starmap server with replica acquisition

One Starmap server follows GitHub and serves the fleet. Each replica reads the
server. In a shared fleet, the replica that holds the refresh lease runs the
provider acquisition. A separate deployment runs its own acquisition.

```mermaid
flowchart LR
  GH[("GitHub catalog/v1")]
  SM[Central Starmap server]
  subgraph FLEET[Starport fleet]
    S1[Starport 1]
    S2[Starport 2]
    S3[Starport N]
  end
  PR[Provider APIs]
  GH -->|hourly conditional poll| SM
  SM -->|server-sent events| FLEET
  FLEET -->|acquisition every 4 hours| PR
```

**Settings.** Set `SOURCE=starmap` and set `SOURCE_URL` to the versioned base
URL of the server. Set `SOURCE_API_KEY` when the server needs one. Keep
`ACQUISITION_ENABLED=true`, and keep `SOURCE_MAX_HOPS` at `8`.
`SOURCE_API_KEY` is a catalog-acquisition credential. It never pays a provider.

**Request budget.** One host reaches GitHub, so the fleet size no longer counts
against the GitHub budget. The central server holds one subscription for each
replica. Size the server on the connection count and on the burst after a new
publication. A fleet of 100,000 replicas draws about 350 Mbps from the server
inside the 15-minute spread window.

**Freshness age.** The server pushes each publication over server-sent events,
so a push hop adds no poll interval. The objective stays six hours. A hop that
falls back to polling adds one poll interval to the age.

**Egress.** The central server reaches GitHub. For the catalog, each replica
reaches the central server and the provider APIs. For inference, each replica
reaches the provider APIs.

**Failure behavior.** A stream that fails three times in a row falls back to a
poll of the upstream manifest. A server outage leaves each replica on its
accepted head, and the runtime status reports the fallback state. A `401` or a
`403` stops the subscription until the credential changes.

## Restricted replica egress

The central server keeps the catalog egress to GitHub and to the providers.
Each replica sends catalog requests to the central server alone.

```mermaid
flowchart LR
  GH[("GitHub catalog/v1")]
  PR[Provider APIs]
  SM[Central Starmap server]
  subgraph BOUND[Restricted network]
    S1[Starport 1]
    S2[Starport 2]
    S3[Starport N]
  end
  GH -->|hourly conditional poll| SM
  PR -->|acquisition every 4 hours| SM
  SM -->|server-sent events| BOUND
```

**Settings.** Set `SOURCE=starmap` and `SOURCE_URL` as above, and set
`ACQUISITION_ENABLED=false` on every replica. A false value stops every
automatic observation, so the replica opens no provider connection for the
catalog.

**Request budget.** The replicas send no GitHub request and no provider
observation. The central server owns the complete external budget.

**Freshness age.** The objective stays six hours, because the server-sent
events reach the replicas without a poll.

**Egress.** For the catalog, each replica reaches one address, which is the
central server. For inference, each replica still reaches the provider APIs.

**Failure behavior.** A replica that loses the server keeps its accepted head
and reconnects with a bounded retry. The replica observes no provider, so a
provider outage does not change its catalog facts.

This topology is not air-gapped. The central server still reaches GitHub and
still reaches the provider APIs, so a route out of the network exists. An
attacker who reaches the central server reaches the internet through it. A
deployment that permits no such route uses the air-gapped mirror below.

## Air-gapped mirror

No host inside the boundary reaches GitHub or a provider API on the internet.
An external process outside the boundary reads the artifact and its
verification bundle. An operator moves both files across the boundary on a
schedule.

```mermaid
flowchart LR
  GH[("GitHub catalog/v1")]
  EX[External puller]
  MV{{Manual transfer}}
  subgraph AIR[Air-gapped boundary]
    FS[(Catalog file)]
    S1[Starport 1]
    S2[Starport N]
  end
  GH --> EX
  EX -->|verify the bundle offline| MV
  MV --> FS
  FS --> S1
  FS --> S2
```

**Settings.** Set `SOURCE=file` and set `SOURCE_URL` to the path of the
transferred catalog file. Set `ACQUISITION_ENABLED=false` on every host. Leave
`SOURCE_MAX_AGE` at the transfer cadence, so the grade follows the schedule
that the operator controls.

**Request budget.** No host inside the boundary sends a catalog request. The
external puller alone counts against the GitHub budget.

**Freshness age.** The age follows the transfer cadence. A transfer later
than `SOURCE_MAX_AGE` reads `warn` on every replica, and one later than five
thirds of it reads `critical`. The accepted head keeps routing at either
grade. Pair the freshness
alert with the transfer schedule, so an operator reads a late transfer and not
a broken gateway.

**Egress.** No host inside the boundary reaches the internet. The runtime has
no OCI source, so the `file` source is the supported entry point. For
inference, each gateway reaches only the provider endpoints inside the
boundary.

**Failure behavior.** A missed transfer raises the catalog age. The freshness
grade moves from `current` to `warn` and then to `critical`, and the accepted
head keeps every route in place. A file that fails verification leaves the
accepted head where it was.

Verify the artifact and its bundle outside the boundary before the transfer.
The Starmap central server runbook holds the verification procedure. Starport
checks the payload checksum again when it reads the file.

## Replicated central Starmap servers

The replicated variant applies to the three central-server topologies above.
Two or more Starmap servers run active-active behind one load balancer only on
a lease-capable shared store.

```mermaid
flowchart LR
  GH[("GitHub catalog/v1")]
  subgraph CENTRAL[Central tier]
    A[Starmap server A]
    B[Starmap server B]
    ST[(Lease-capable store)]
  end
  LB[Load balancer]
  FLEET[Starport fleet]
  GH --> A
  GH --> B
  A <--> ST
  B <--> ST
  A --> LB
  B --> LB
  LB --> FLEET
```

**The store decides the form.** An active-active pair needs two properties from
its store: the refresh lease, and a conditional compare-and-swap on the
generation record. The lease has a 90-second lifetime and a 30-second renewal
interval, and it carries an epoch. The epoch fences a durable commit, so a
holder that lost the lease cannot advance the head. A holder that loses the
lease cancels its run inside one renewal interval, drops the results, reports
`lease_lost`, and retries at the next phase.

**A plain shared volume supports one writer.** A plain shared filesystem volume
gives neither the lease nor the conditional write. Such a volume therefore
supports the single-server design alone, in the active and passive form. In
that form one standby server starts only after the active server stops. Two
servers that write one volume at the same time corrupt the generation record.

**The fleet reads the balancer.** Each Starport subscribes through the load
balancer. A failover ends the stream, and the replica reconnects through the
balancer to the server that holds the lease. The accepted head of each replica
does not move during the failover.

Each Starport instance keeps its own state directory. The seed in that
directory joins the host name and the listen address in the instance identity.
Two replicas that share a state directory derive one identity, and the lease
then fences nothing. `STARPORT_CATALOG_STATE_DIR` is never a shared volume.

## Related documents

- [Operator guide](OPERATOR-GUIDE.md): the catalog configuration reference, the
  workspace layout, the generation procedures, and the alert rules.
- [Configuration reference](../.env.example): every environment name with its
  default.
- [Architecture](ARCHITECTURE.md): the concept boundaries between Starport and
  Starmap.
- [Model catalog contract](../MODELS.md): the Starmap ownership rules.
