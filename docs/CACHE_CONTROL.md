# Cache Control Implementation

Starport now supports OpenRouter-compatible cache control for prompt caching, allowing you to reduce costs and improve performance for requests with repeated context.

## Overview

Cache control allows you to mark specific content parts in your messages as cacheable. When providers support this feature, they will cache the marked content and reuse it in subsequent requests, significantly reducing token costs.

## Supported Providers

The following providers support cache control:
- OpenAI (gpt-4o, gpt-4o-mini)
- Anthropic (claude-3-5-sonnet, claude-3-haiku)
- Groq
- DeepSeek
- Azure OpenAI

Providers that use implicit caching (not cache_control):
- Google AI Studio
- Google Vertex AI
- Mistral

## Usage

To use cache control, add a `cache_control` field to content parts in your messages:

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
    {
      "role": "user",
      "content": "What is the capital of France?"
    }
  ]
}
```

## Provider-Specific Limits

### Anthropic
- Maximum of 4 cache control breakpoints per request
- Cache write cost: 1.25x prompt cost
- Cache read cost: 0.1x prompt cost

### OpenAI
- No specific limit on breakpoints
- Cache write cost: 2.5x prompt cost  
- Cache read cost: 0.5x prompt cost

### DeepSeek
- Free caching (same as prompt cost)
- Cache read cost: 0.1x prompt cost

## Response Headers

When cache control is used, Starport adds the following headers to responses:

- `X-Cache`: Cache status (HIT/MISS)
- `X-Cache-Age`: Age of cached content in seconds
- `X-Cache-Write-Cost`: Cost of writing to cache
- `X-Cache-Read-Cost`: Cost of reading from cache
- `X-Cache-Total-Cost`: Total cache operation cost

## Automatic Provider Handling

If you send a request with cache control to a provider that doesn't support it (like Google providers), Starport will automatically:
1. Strip the cache_control fields from the request
2. Forward the cleaned request to the provider
3. Return the response normally (without cache headers)

This ensures compatibility across all providers while allowing you to use cache control where supported.

## Example Code

See `examples/cache_control_demo.go` for a complete example of using cache control with Starport.