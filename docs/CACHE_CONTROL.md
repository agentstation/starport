# Prompt Cache Control

Starport accepts OpenRouter-compatible prompt cache controls on chat message
content. A cache control marks a point where a provider can reuse prompt
content in a later request.

## Capability Source

Starmap owns the prompt-cache capability for each provider offering. Starport
reads that capability from its immutable Starmap catalog snapshot. Starport
does not keep a separate provider or model support list.

The selected route determines request behavior:

- If Starmap marks the offering as prompt-cache capable, Starport sends the
  cache control to the provider.
- If support is false or unknown, Starport removes the cache control from that
  provider attempt. The rest of the request stays unchanged.

Use the model catalog to select a current offering. Provider capabilities can
change between catalog generations.

## Request Format

Add `cache_control` to a text content part. Starport supports the `ephemeral`
type:

```json
{
  "model": "anthropic/claude-3-5-sonnet",
  "messages": [
    {
      "role": "system",
      "content": [
        {
          "type": "text",
          "text": "You are a helpful assistant with extensive knowledge about...",
          "cache_control": {
            "type": "ephemeral"
          }
        }
      ]
    },
    {"role": "user", "content": "Summarize the reference material."}
  ]
}
```

Starport returns a validation error for any other cache control type.

## Routing and Fallback

Starport evaluates prompt-cache support for every provider attempt. A fallback
route can therefore receive a different request form from the first route. The
gateway keeps the cache control only for routes that declare support in the
same Starmap catalog generation used for routing.

This behavior lets one request use fallback without sending unsupported
provider fields.

## Response Headers

For a non-streaming prompt-cache request, Starport can return these cost
headers when Starmap supplies cache token prices for the selected offering:

- `X-Cache-Write-Cost`
- `X-Cache-Read-Cost`
- `X-Cache-Total-Cost`

These values use the price data from the same Starmap offering. A missing
header means that Starport could not calculate that value from the selected
offering and response.

`X-Cache` and `X-Cache-Age` describe Starport response-cache state. They do not
describe provider prompt-cache state.

## Related Contracts

The OpenRouter protocol contract is in
[`internal/httpapi/openrouter/contract_test.go`](../internal/httpapi/openrouter/contract_test.go).
The route-specific behavior is in
[`internal/proxy/proxy_routing_test.go`](../internal/proxy/proxy_routing_test.go).
