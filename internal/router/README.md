# Model Routing Package

The `routing` package implements OpenRouter-compatible model routing for Starport, providing intelligent fallback chains, automatic model selection, and provider routing preferences.

## Features

### 1. Model Fallback Chains
Support for the `models` array parameter that specifies a fallback chain:
```json
{
  "models": ["openai/gpt-4", "anthropic/claude-3-sonnet", "groq/llama-3.1-8b"],
  "messages": [{"role": "user", "content": "Hello"}]
}
```

The router will try each model in order until one succeeds.

### 2. Automatic Model Selection
The special model ID `openrouter/auto` automatically selects the best model based on:
- Request characteristics (vision, functions, context length)
- User preferences (quality, speed)
- Provider availability
- Cost optimization

### 3. Fallback Triggers
The router automatically falls back to the next model when:
- **Rate Limit (429)**: Provider rate limit exceeded
- **Model Unavailable (404)**: Model not found or offline
- **Context Exceeded (400)**: Input too long for model
- **Provider Error (5xx)**: Server errors
- **Content Moderation**: Content policy violations
- **Timeout**: Request timeout

### 4. Provider Routing Preferences
Control which providers are used:
```json
{
  "provider_preferences": {
    "order": ["openai", "anthropic"],      // Try in this order
    "only": ["openai", "anthropic"],       // Only use these
    "ignore": ["azure"],                    // Never use these
    "allow_fallbacks": true                 // Allow other providers
  }
}
```

### 5. Circuit Breaker
Providers that fail repeatedly are temporarily disabled:
- 3 consecutive failures open the circuit
- Circuit stays open for 30 seconds
- Automatic recovery when provider is healthy

### 6. Response Metadata
All responses include the `model_used` field:
```json
{
  "id": "chatcmpl-123",
  "model": "openai/gpt-4",
  "model_used": "openai/gpt-4",  // Which model actually handled the request
  "choices": [...]
}
```

## Architecture

### Core Components

1. **ModelRouter Interface** (`model_router.go`)
   - Main routing logic
   - Fallback chain execution
   - Provider health tracking

2. **ModelSelector** (`model_selector.go`)
   - Auto-model selection logic
   - Model capability database
   - Request analysis

3. **Fallback Logic**
   - Error classification
   - Retry with exponential backoff
   - Circuit breaker per provider

### Integration

The routing package integrates with:
- `connectors` package for LLM providers
- `server` package for HTTP handling
- Provider registry for connector lookup

## Usage

### Basic Usage
```go
// Create router
router := routing.NewRouter(registry)

// Create routing request
req := &routing.RoutingRequest{
    ChatRequest: &connectors.ChatRequest{
        Models: []string{"openai/gpt-4", "anthropic/claude-3"},
        Messages: messages,
    },
}

// Route with fallback
resp, err := router.RouteWithFallback(ctx, req)
if err != nil {
    // All models failed
}

fmt.Printf("Used model: %s\n", resp.ModelUsed)
```

### Auto Model Selection
```go
req := &routing.RoutingRequest{
    ChatRequest: &connectors.ChatRequest{
        Model: "openrouter/auto",
        Messages: messages,
    },
}
```

### Provider Preferences
```go
req.ProviderPreferences = &routing.ProviderPreferences{
    Order: []string{"anthropic", "openai"},
    Ignore: []string{"azure"},
}
```

## Model Capabilities

The package maintains a database of model capabilities:
- Context length
- Max output tokens
- Vision support
- Function calling support
- Streaming support
- Cost per million tokens
- Latency class (fast/medium/slow)
- Quality tier (economy/standard/premium)

## Testing

The package includes comprehensive tests for:
- Fallback scenarios
- Model selection logic
- Provider preferences
- Circuit breaker behavior
- Error classification

Run tests:
```bash
go test ./internal/routing/...
```