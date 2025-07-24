// Package providers manages LLM provider configurations and connectors.
// It provides a clean separation between provider metadata, models,
// and the actual connector implementations that handle API communication.
//
// Architecture:
//
// The package follows a clear separation of concerns:
//   - Provider: Metadata, configuration, and model management
//   - Model: Individual model capabilities and specifications
//   - Connector: API implementation interface (embedded in Provider)
//   - Registry: Thread-safe storage and retrieval of providers
//
// Key design decisions:
//   - Connectors are embedded in Providers for a clean API (provider.Chat())
//   - Models have type-specific capabilities (chat, embedding, etc.)
//   - No circular dependencies: Connectors don't reference Providers
//   - HTTP clients are injected into Connectors for testability
//
// Usage:
//
//	// 1. Create a provider with metadata
//	provider := &providers.Provider{
//	    ID:      "openai-prod",
//	    Name:    "OpenAI Production",
//	    Type:    "openai",
//	    BaseURL: "https://api.openai.com",
//	    APIKey:  os.Getenv("OPENAI_API_KEY"),
//	    Enabled: true,
//	}
//
//	// 2. Add models with their capabilities
//	provider.AddModel(&models.Model{
//	    ID:            "gpt-4",
//	    Architecture: &models.Architecture{
//	        InputModalities:  []string{"text"},
//	        OutputModalities: []string{"text"},
//	    },
//	    ContextLength: 128000,
//	})
//
//	// 3. Create an HTTP client
//	httpClient, _ := httpclient.New("openai", httpclient.DefaultProviderConfig("openai"))
//
//	// 4. Create and assign a connector
//	connector := openai.NewConnector(provider.BaseURL, provider.APIKey, httpClient)
//	provider.Connector = connector
//
//	// 5. Add to registry
//	registry := providers.NewRegistry()
//	registry.Add(provider)
//
//	// 6. Use the provider
//	p, _ := registry.Get("openai-prod")
//	resp, _ := p.Chat(ctx, "gpt-4", &providers.ChatRequest{
//	    Messages: []providers.Message{{Role: "user", Content: "Hello"}},
//	})
//
// The embedded Connector pattern allows for clean usage while maintaining
// testability through dependency injection of the HTTP client.
package providers