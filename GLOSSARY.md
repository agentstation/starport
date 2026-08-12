# Starport glossary

| Term | Definition | Avoid | Status | Evidence |
|---|---|---|---|---|
| Starport | The LLM inference gateway in this repository. | | approved | `README.md` |
| Starmap | The catalog system that owns provider and model facts for Starport. | | approved | `docs/ARCHITECTURE.md` |
| gateway API key | A Starport credential that authenticates one client identity. | | approved | `internal/identity/model.go` |
| provider inference credential | A secret that Starport uses for an inference request to one provider. | | approved | `internal/providers/connectors/README.md` |
| catalog generation | One immutable Starmap catalog version that binds provider and model facts. | | approved | `internal/catalog/control_plane.go` |
| adapter registry | The Starport registry that owns compiled provider adapter behavior. | | approved | `internal/providers/connectors/adapter_registry.go` |
| route plan | A deterministic ordered set of provider offering attempts for one request. | | approved | `internal/routing/types.go` |
| attempt budget | The total attempt and elapsed-time limit for one inference request. | | approved | `internal/execution/executor.go` |
| provider authentication | The Starport process that applies inference credential material to a provider request. | | approved | `internal/providers/auth/request.go` |
| cloud credential chain | The Starport process that gets renewable inference credential material from a cloud identity. | | approved | `internal/credentials/cloudchain/cloud.go` |
| Homebrew cask | A Homebrew package that installs a released Starport binary. | | approved | `https://docs.brew.sh/Cask-Cookbook` |
| OpenAI-compatible API | The Starport HTTP contract under `/v1`. | | approved | `docs/ARCHITECTURE.md` |
| OpenRouter-compatible API | The Starport HTTP contract under `/api/v1`. | | approved | `docs/ARCHITECTURE.md` |
| BYOK | Bring your own key, which stores a tenant provider inference credential. | | approved | `internal/providers/byok/provider_keys.go` |
| credential material | One resolved set of named credential fields with an opaque version and optional lifecycle metadata. | | approved | `internal/credentials/material.go` |
| credential source | A Starport or Starmap concept that resolves credential material from one configured source. | | approved | `internal/credentials/source.go` |
| secret reference | Operator configuration that identifies one external credential without containing credential or backend authentication values. | | approved | `internal/credentials/reference.go` |
| provider reconciler | The Starport concept that resolves inference credentials for all catalog providers and publishes one complete runtime generation. | | approved | `internal/providers/reconciler.go` |
| provider state | A secret-free Starport projection that keeps adapter, operator credential, and offering state separate. | | approved | `internal/providers/state/store.go` |
| runtime generation | One immutable Starport runtime value that binds catalog state, configuration, source handles, connectors, operations, and availability. | | approved | `internal/registry/generation.go` |
| Badger | The embedded storage backend for one Starport process. | | approved | `docs/ARCHITECTURE.md` |
| Valkey | The shared storage backend for a multi-process Starport deployment. | | approved | `docs/ARCHITECTURE.md` |
