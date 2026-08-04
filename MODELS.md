# Starport Model Catalog

Starport does not maintain a second static model list. Starmap is the only
source of model IDs, provider offerings, capabilities, context limits, and
prices.

At startup, Starport loads one immutable Starmap generation. Routing and
response-cache identity use that same generation. A connector can report live
availability, but it cannot add or change catalog facts.

## Discover Models

Use the API that matches the client contract:

```text
GET /v1/models
GET /api/v1/models
GET /api/v1/models/{model}/endpoints
```

The OpenAI route returns the OpenAI model-list shape. The OpenRouter route
returns the richer OpenRouter model metadata shape.

## Model IDs

Requests use the exact `provider/model` ID from the active catalog generation.
Starport does not normalize aliases. Current adapter IDs are:

- `openai`
- `anthropic`
- `google-ai-studio`
- `google-vertex`
- `groq`
- `mistral`
- `azure-openai`
- `ollama`

`openrouter/auto` lets the route planner consider all currently routable
offerings. In a model array, explicit model IDs remain ahead of the automatic
fallback set.

## Catalog Updates

Update model and price data in Starmap, not in Starport connector or routing
code. Starport derives a new routable snapshot from the new immutable catalog
generation and current offering availability.
