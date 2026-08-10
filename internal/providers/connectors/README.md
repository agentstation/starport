# Inference adapters

This package owns provider wire protocols and inference authentication. It does
not own provider membership, model discovery, model IDs, service URLs, prices,
capabilities, or reliability sources. Those facts come from one immutable
Starmap catalog generation.

The production adapter registry declares the inference operations, endpoint
protocols, and credential contract that compiled Starport code can execute. A
provider becomes active only when all three inputs exist:

1. A Starmap provider offering supports the operation.
2. A compiled adapter supports the offering protocol.
3. The operator selected a valid inference credential and supplied all runtime
   endpoint bindings.

Requests receive an exact provider model ID and a bound Starmap endpoint. An
adapter must send both without model-family inference, endpoint fallback, model
discovery, or hidden retries. The central executor owns retries and fallback.

Starmap catalog-acquisition credentials are a separate credential plane. They
must not enter an inference adapter.

Vertex AI and Azure OpenAI can use a static Starport secret or a renewable
source from `internal/providerauth`. The operator must select default cloud
credentials. Ambient cloud credentials do not activate an adapter.
