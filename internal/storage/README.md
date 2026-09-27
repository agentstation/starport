# Storage Package

This package provides the storage abstraction layer for Starport, supporting multiple backend implementations with a unified interface.

## Overview

The storage package defines a `KVStore` interface that abstracts key-value storage operations. It supports:

- Basic CRUD operations (Get, Set, Delete, Exists)
- TTL (Time-To-Live) support for temporary data
- Atomic operations (Increment, Decrement, CompareAndSwap)
- Batch operations for efficiency
- Atomic conditional mutation sets through `CompareAndSwapBatch`
- Scanning/listing capabilities

## Architecture

```
storage/
├── interface.go      # KVStore interface and configuration types
├── open.go          # Open function for creating storage instances
├── serialization.go  # Helpers for data serialization
├── mock.go          # In-memory mock implementation for testing
└── README.md        # This file
```

## Storage Backends

### Badger (Default)
- Embedded key-value store
- Zero external dependencies
- Excellent performance (<1ms latency)
- Perfect for single-node deployments

### Valkey
- Redis-compatible distributed store
- Required for multi-node deployments
- Supports shared state across instances
- Higher latency (<5ms) but better scalability

## Usage

```go
// Create storage instance
config := storage.Config{
    Type: "badger",
    Badger: storage.BadgerConfig{
        Path: "./data/badger",
    },
}

store, err := storage.Open(config)
if err != nil {
    return err
}
defer store.Close()

// Basic operations
ctx := context.Background()
err = store.Set(ctx, "key", []byte("value"))
value, err := store.Get(ctx, "key")

// TTL operations
err = store.SetWithTTL(ctx, "temp-key", []byte("temp-value"), 5*time.Minute)

// Atomic operations
count, err := store.Increment(ctx, "counter", 1)

// Apply all conditional mutations or none. A nil expectation requires absence.
err = store.CompareAndSwapBatch(ctx, []storage.CompareAndSwapMutation{
    {Key: "key1", ExpectedValue: nil, NewValue: []byte("value1")},
    {Key: "key2", ExpectedValue: nil, NewValue: []byte("value2")},
})
```

## Testing

`MockStore` implements the full `KVStore` interface in memory. Use it for unit
tests without external dependencies.

```go
// Create mock store for testing
store := storage.NewMockStore()
defer store.Close()

// Use exactly like a real store
err := store.Set(ctx, "test-key", []byte("test-value"))
```

## Key Patterns

The storage layer uses consistent key patterns for different data types:

- API Keys: `apikey:{hash}`
- Presets: `presets:v1:name:{base64url(name)}`
- BYOK Credentials: `credential:{api_key_id}:{provider}`
- Filters: `filter:{name}`
- Rate Limits: `ratelimit:{key}:{window}`

## Performance Considerations

1. **Batch Operations**: Use batch operations when working with multiple keys to reduce round trips
2. **TTL Usage**: Use TTL for temporary data to avoid manual cleanup
3. **Conditional writes**: Use `CompareAndSwapBatch` when writes must succeed together. A conflict changes no keys.
4. **Key Design**: Use hierarchical key patterns for efficient scanning

## Error Handling

The package defines specific error types:

- `ErrNotFound`: Key does not exist
- `ErrConflict`: Write conflict (e.g., CAS mismatch)
- `ErrInvalidKey`: Invalid key format
- `ErrStorageClosed`: An operation used a closed storage instance
- `ErrTimeout`: Operation timed out

Always check for `ErrNotFound` when a key might not exist:

```go
value, err := store.Get(ctx, "maybe-missing")
if errors.Is(err, storage.ErrNotFound) {
    // Handle missing key
}
```

## Deployment identity

Production composition opens storage through `Config.RuntimeStorage()`.
That projection supplies the canonical `STARPORT_DEPLOYMENT_ID` from the resolved product paths.
`OpenValkey` refuses an absent or invalid identity before it opens a connection.

Durable keys and notification channels use `{starport:v1:<base64url-deployment-id>:}kv:` before the logical repository key or channel.
The identity uses unpadded UTF-8 encoding. One deployment occupies one hash slot, including multi-key conditional mutations.
This layout does not qualify Cluster support. Cache-only service keys retain their separate cache prefix and service configuration.

Repositories use logical keys. The adapter adds the physical prefix and removes it from scan results and notification callbacks.
Bounded reads, native ownership checks, and fresh-initialization claims use the same physical prefix.
Fresh initialization still requires an empty dedicated KV database and SQL schema.
A namespace alone cannot approve populated state or a replacement backend.

Existing unprefixed records require an explicit migration before use.
Ordinary startup never copies them or falls back to them.
Badger retains its local layout inside private product storage.
