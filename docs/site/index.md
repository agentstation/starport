---
title: Starport documentation
area: home
order: 0
summary: Find the area that answers your question and send a first request to a local gateway.
---

Starport is a self-hosted LLM inference gateway in one binary. It serves the OpenAI-compatible API at `/v1` and the OpenRouter-compatible API at `/api/v1`, and Starmap supplies its provider and model facts.

## Areas of this site

Each area answers one kind of question. Start at the area that matches your task.

| Area | Use it to |
| --- | --- |
| [Start](start/index.md) | Run a first gateway, issue keys, and inspect the catalog. |
| [Choose an architecture](architecture/index.md) | Select a deployment target, a catalog topology, and storage. |
| [Configure](configure/index.md) | Set values, secret references, and paths. |
| [Catalog lifecycle](catalog-lifecycle/index.md) | Learn how a catalog generation becomes routable. |
| [Operate Starmap](operate-starmap/index.md) | Run a central Starmap server or an air-gapped mirror. |
| [Operate Starport](operate-starport/index.md) | Manage authentication, identity, fleets, and upgrades. |
| [Storage](storage/index.md) | Select backends, back up, restore, and tune caches. |
| [API compatibility](api-compatibility/index.md) | Connect SDKs and read errors and limits. |
| [Troubleshoot](troubleshoot/index.md) | Recover a gateway without a console session. |

## Version facts in the page header

The page header shows the facts of the build that made this site. Compare them with your gateway before you follow a procedure.

- The Starport release identifies the binary that these pages describe.
- The Starmap version identifies the Starmap module that this release pins. That module reconciles catalog records into one canonical catalog.

Run `starport version` to see the release of an installed binary. If the release differs from the header, read the pages for your release.

## Fastest path to a first request

This path uses a temporary development gateway. It keeps no data after the process stops.

1. Install Starport. Refer to [Run a persistent local gateway](start/local-persistent.md#install-starport).
2. Set a provider inference credential, for example `OPENAI_API_KEY`.
3. Run `starport dev` and keep the terminal open.
4. Copy the gateway API key that the command prints.
5. In a second terminal, send a request:

```bash
export STARPORT_API_KEY="<printed-gateway-api-key>"
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"Hello"}]}' \
  http://127.0.0.1:7827/api/v1/chat/completions
```

A model in the catalog is not always a callable offering. The request succeeds only when the gateway can call a provider with a usable credential. Refer to [Inspect the catalog](start/first-catalog-inspection.md) for the difference.

For a gateway that keeps its data, read [Run a persistent local gateway](start/local-persistent.md). The [operator guide](../OPERATOR-GUIDE.md#local-development) holds the full reference.
