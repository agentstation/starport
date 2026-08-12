# POR6 Starport authentication ownership proof

POR6 starts from clean Starport `main` at
`9bbeccefadce3c2afac41f6dc60135fba8b71e74`.

The task moves request-scoped inference authentication to
`internal/providers/auth`. It moves cloud credential acquisition to
`internal/credentials/cloudchain`. The task will preserve credential lifecycle,
redaction, request selection, and catalog-driven provider behavior.
