---
title: API compatibility
area: api-compatibility
order: 0
summary: Connect OpenAI and OpenRouter clients to Starport, and learn the routes, SDK versions, errors, and limits of each API family.
---

This area is for a developer who connects a client or an SDK to a Starport gateway. Starport serves two API families on one listener.

## Two API families

| Family | Base URL | Error body |
| --- | --- | --- |
| OpenAI | `http://<gateway-host>:8080/v1` | OpenAI error object |
| OpenRouter | `http://<gateway-host>:8080/api/v1` | OpenRouter error object |

Each request needs a gateway API key in the `Authorization: Bearer` header. A provider key does not work as a gateway key.

## Catalog and callable models

A model can be in the Starmap catalog and still not be callable. The model routes list only the offerings that this gateway can route. Use a model ID from that list in a request.

## What this area covers

- The routes of each family and the scope that each route needs.
- SDK setup and the SDK versions that the repository tests.
- Error bodies, rate limits, budgets, request limits, and guardrails.
