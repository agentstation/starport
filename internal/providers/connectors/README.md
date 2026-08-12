# Inference adapters

This package owns provider wire protocols and inference authentication. It does
not own provider membership, model discovery, model IDs, service URLs, prices,
capabilities, or reliability sources. Those facts come from one immutable
Starmap catalog generation.

The production registries declare the endpoint protocols and authentication
primitives that compiled Starport code can execute. A provider adapter becomes
active when all three inputs exist:

1. A Starmap provider offering supports the operation.
2. A compiled adapter supports the offering protocol.
3. A compiled authentication primitive supports at least one ordered Starmap
   inference profile.

Operator credentials are not an adapter activation condition. A request can
select operator material, tenant BYOK material, or a catalog-default no-auth
profile. Starport then binds the selected material to the retained Starmap
endpoint template. Operator endpoint overrides never apply to tenant material.

Requests give adapters an exact provider model ID and a request-bound Starmap
endpoint. An adapter must send both without model-family inference, endpoint
fallback, model discovery, or hidden retries. The central executor owns retries
and fallback.

Starmap catalog-acquisition credentials are a separate credential plane. They
must not enter an inference adapter.

Vertex AI and Azure OpenAI can use a static Starport secret or a renewable
source from `internal/credentials/cloudchain`. Credential discovery and refresh
occur outside the inference hot path. `internal/providers/auth` applies the
resolved material to each request. Ambient cloud credentials do not determine
adapter activation.
