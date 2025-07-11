# Handler Organization Migration Guide

This guide explains how to migrate from the current handler structure to the new, improved organization.

## Overview of Changes

### Old Structure
```
internal/server/
├── server.go                      # Server + route registration
├── proxy_handler.go              # Main handler + ConnectorRegistry
├── proxy_handler_routing.go      # Routing-specific handlers
├── proxy_handler_cached.go       # Caching layer v1
├── proxy_handler_cached_v2.go    # Caching layer v2
├── health.go                     # Health endpoints
└── ...
```

### New Structure
```
internal/
├── proxy/                        # Business logic layer
│   ├── service.go               # Service interface
│   ├── service_impl.go          # Service implementation
│   ├── validator.go             # Request validation
│   ├── transformer.go           # Data transformations
│   └── errors.go                # Domain errors
│
├── registry/                     # Connector management
│   ├── registry.go              # Core registry
│   └── adapter.go               # Server adapter
│
└── server/
    ├── routes.go                # Centralized routing
    ├── handlers/                # Thin HTTP handlers
    │   ├── base.go             # Base handler
    │   ├── chat.go             # Chat endpoints
    │   ├── embeddings.go       # Embeddings endpoints
    │   ├── models.go           # Models endpoints
    │   ├── providers.go        # Providers endpoints
    │   └── health.go           # Health endpoints
    └── dto/                     # HTTP DTOs
        ├── requests.go          # Request parsing
        └── responses.go         # Response formatting
```

## Migration Steps

### 1. Update Dependencies

The new structure requires updating how components are initialized:

**Old:**
```go
registry := NewConnectorRegistry()
handler := NewProxyHandler(registry)
server := New(config, registry)
```

**New:**
```go
// Create registry
registry := registry.New()
adapter := registry.NewAdapter(registry)
adapter.InitializeFromConfig(ctx, config)

// Create router
router := routing.NewModelRouter(routingConfig)

// Create server with dependencies
server := NewRefactored(config, registry, router)
```

### 2. Update Route Registration

**Old:** Routes spread across handler files
```go
func (h *ProxyHandler) RegisterRoutes(r chi.Router) {
    r.Post("/v1/chat/completions", h.handleChatCompletions)
    // ... more routes
}
```

**New:** Centralized in routes.go
```go
func (s *Server) setupRoutes(mux *chi.Mux) {
    mux.Route("/v1", func(r chi.Router) {
        r.Post("/chat/completions", s.handlers.Chat.Create)
        // ... all routes in one place
    })
}
```

### 3. Update Handler Implementation

**Old:** Mixed concerns in handler
```go
func (h *ProxyHandler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
    // Parsing, validation, routing, business logic, response writing
}
```

**New:** Thin handler delegates to service
```go
func (h *ChatHandler) Create(w http.ResponseWriter, r *http.Request) {
    req, err := dto.ParseChatCompletionRequest(r)
    if err != nil {
        dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request")
        return
    }
    
    resp, err := h.service.ProcessChatCompletion(r.Context(), req)
    if err != nil {
        h.writeError(w, err)
        return
    }
    
    dto.WriteJSON(w, http.StatusOK, resp)
}
```

### 4. Extract Business Logic

Move business logic from handlers to the proxy service:

**Old:** In handler
```go
// In proxy_handler.go
func (h *ProxyHandler) routeRequest(req *ChatRequest) (*RoutingResult, error) {
    // Complex routing logic
}
```

**New:** In service
```go
// In proxy/service_impl.go
func (s *ServiceImpl) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
    // Validation
    if err := ValidateChatCompletionRequest(req); err != nil {
        return nil, err
    }
    
    // Routing
    result, err := s.router.Route(ctx, TransformChatRequest(req))
    // ... business logic
}
```

### 5. Update Tests

Tests become easier to write with the new structure:

**Old:** Testing HTTP handlers with mocks
```go
func TestHandleChatCompletions(t *testing.T) {
    // Complex setup with HTTP mocks
}
```

**New:** Test business logic separately
```go
// Test service logic without HTTP
func TestProcessChatCompletion(t *testing.T) {
    mockRegistry := &mockRegistry{}
    mockRouter := &mockRouter{}
    service := proxy.NewService(mockRegistry, mockRouter)
    
    resp, err := service.ProcessChatCompletion(ctx, req)
    // Assert business logic
}

// Test handlers separately (thin layer)
func TestChatHandlerCreate(t *testing.T) {
    mockService := &mockProxyService{}
    handler := handlers.NewChatHandler(mockService)
    
    // Test HTTP concerns only
}
```

## Benefits of New Structure

1. **Separation of Concerns**
   - HTTP handling separate from business logic
   - Easier to test each layer independently
   - Clear boundaries between packages

2. **Better Testability**
   - Business logic can be tested without HTTP
   - Handlers are simple to test
   - Mocking is straightforward

3. **Improved Maintainability**
   - All routes visible in one file
   - Consistent handler patterns
   - Easy to add new endpoints

4. **Scalability**
   - Easy to add new handlers
   - Business logic can be reused
   - Clear extension points

## Gradual Migration Strategy

1. **Phase 1**: Create new structure alongside old (✅ Complete)
2. **Phase 2**: Implement service layer with routing (✅ Complete)
3. **Phase 3**: Create thin handlers (✅ Complete)
4. **Phase 4**: Update server to use new handlers
5. **Phase 5**: Migrate remaining handlers (provider keys, admin)
6. **Phase 6**: Remove old handler files
7. **Phase 7**: Update all tests

## Next Steps

1. Update the main server initialization in `cmd/starport/`
2. Migrate provider keys handlers
3. Migrate admin handlers
4. Update integration tests
5. Remove old handler files once migration is complete