# Connectors Package

This package contains the LLM provider connector interfaces and implementations for Starport.

## Structure

- `interface.go` - Core connector interface and factory function
- `types.go` - Request/response types for LLM operations
- `errors.go` - Error types and helpers
- `mock.go` - Mock implementation for testing
- Provider-specific implementations:
  - `openai.go` - OpenAI connector
  - `anthropic.go` - Anthropic/Claude connector
  - `google_aistudio.go` - Google AI Studio (Gemini) connector
  - `google_vertex.go` - Google Vertex AI connector
  - `groq.go` - Groq connector
  - `mistral.go` - Mistral AI connector
  - `azure.go` - Azure OpenAI connector
  - `ollama.go` - Ollama connector for local models

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

### Ollama Example

```go
// Ollama connector (no API key needed)
config := connectors.ProviderConfig{
    BaseURL: "http://localhost:11434",
    Timeout: 30 * time.Second,
    Enabled: true, // Must be explicitly enabled
}

connector, err := connectors.NewConnector("ollama", config)
if err != nil {
    log.Fatal(err)
}

// List available models
models, err := connector.Models(ctx)
for _, model := range models.Data {
    fmt.Printf("Available: %s\n", model.ID)
}

// Use a local model
req := &connectors.ChatRequest{
    Model: "ollama/llama3.2",
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