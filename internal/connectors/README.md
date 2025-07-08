# Connectors Package

This package contains the LLM provider connector interfaces and implementations for Starport.

## Structure

- `interface.go` - Core connector interface and factory function
- `types.go` - Request/response types for LLM operations
- `errors.go` - Error types and helpers
- `mock.go` - Mock implementation for testing
- Provider-specific implementations will be added in P1-S3-3.2:
  - `openai.go` - OpenAI connector
  - `anthropic.go` - Anthropic/Claude connector
  - `gemini.go` - Google Gemini/Vertex AI connector
  - `groq.go` - Groq connector
  - `mistral.go` - Mistral AI connector
  - `azure.go` - Azure OpenAI connector

## Usage

```go
// Create a connector
config := connectors.ProviderConfig{
    BaseURL: "https://api.openai.com",
    APIKey:  os.Getenv("OPENAI_API_KEY"),
    Timeout: 30 * time.Second,
}

connector, err := connectors.NewConnector("openai", config)
if err != nil {
    log.Fatal(err)
}
defer connector.Close()

// Use the connector
req := &connectors.ChatRequest{
    Model: "gpt-4",
    Messages: []connectors.Message{
        {Role: "user", Content: "Hello!"},
    },
}

resp, err := connector.Chat(ctx, req)
```

## Design Principles

1. **Streaming First**: All connectors support streaming responses
2. **Context Aware**: Proper context propagation for cancellation
3. **Error Handling**: Consistent error types across providers
4. **Health Checks**: Built-in health monitoring
5. **OpenRouter Compatible**: Maintain compatibility with OpenRouter API
6. **Idiomatic Go**: Simple interfaces, no unnecessary abstraction