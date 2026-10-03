---
title: Troubleshoot
area: troubleshoot
order: 0
summary: Find the cause of a gateway, catalog, or resource failure and recover with commands that need no console session.
---

This area is for an operator who must find why a gateway does not serve requests. It applies to a laptop gateway, a single server, and a fleet.

Each topic starts from a symptom that you can see. Examples are a health route that returns `503`, a model that the catalog lists but no request reaches, or a storage error in the logs. Each topic then gives the commands that find the cause and the steps that correct it.

The recovery topic needs no console session and no gateway API key. Use it first when the console does not open. The catalog and resource topics use the admin catalog status, which needs an admin gateway API key or a console session from the gateway host.

Do not delete a journal, a lock, or a recovery file to correct a failure. Starport and Starmap keep these files as recovery evidence.
