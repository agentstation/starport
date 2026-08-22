# Starport documentation

Use this index to find current product, operator, and developer information.

## Start and operate Starport

- [README](../README.md): install, initialize, and send a first request.
- [Operator guide](OPERATOR-GUIDE.md): configure storage, provider credentials,
  direct secret sources, remote catalogs, clients, diagnosis, and shutdown.
- [Configuration reference](../.env.example): list all supported environment
  fields and secure defaults.
- [Vertex AI configuration](VERTEX_AI_CONFIG.md): configure static or renewable
  Vertex AI inference authentication.
- [Model catalog contract](../MODELS.md): understand Starmap ownership and model
  identity rules.
- [Prompt cache control](CACHE_CONTROL.md): send route-aware prompt-cache
  controls.
- [Security policy](../SECURITY.md): report a vulnerability.

## Understand the system

- [Architecture](ARCHITECTURE.md): read the canonical version 1 design and
  concept boundaries.
- [Architecture control-plane history](ARCHITECTURE_CONTROL_PLANE.md): inspect
  the completed architecture-hardening record.
- [Task status](TASKS.md): inspect current repository work. A durable plan
  lives under `docs/plans/` while its campaign runs, and is deleted when the
  campaign closes.

Starport has no legacy provider aliases or storage compatibility readers.
Starmap owns provider and model facts. Starport owns inference and gateway
runtime behavior.

## Develop and contribute

- [Development guide](../DEVELOPMENT.md): set up tools, run tests, and check a
  release snapshot.
- [Contribution guide](CONTRIBUTING.md): prepare a focused pull request.
- [Community rules](CODE_OF_CONDUCT.md): follow the rules for project work.
