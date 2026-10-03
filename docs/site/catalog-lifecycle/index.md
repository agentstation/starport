---
title: Catalog lifecycle
area: catalog-lifecycle
order: 0
summary: Learn how a catalog generation moves from a source to an accepted head, a routable model, and a discovery response.
---

This area explains how Starport gets its provider and model facts from Starmap and how those facts become routes. It is for operators and developers who must find why a model is in the catalog. It also shows why a model routes or does not route, and how fresh the facts are.

The lifecycle has four parts. A source supplies a catalog generation. Starport validates the candidate and accepts it as the head. Route planning selects the offerings that a request can reach. Discovery shows the permitted facts to a caller.

Two ideas stay separate in every topic. Catalog membership means that a model is in the accepted catalog generation. A callable provider offering means that the gateway can call a provider for that model with a usable provider inference credential.

Starport shows no removal targets. Starmap owns the rename and restore behavior of catalog records.
