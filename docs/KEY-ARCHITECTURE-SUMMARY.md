# Key Architecture Summary

## Overview

This document summarizes the key architecture decisions for Starport:

1. **API Keys**: Moving to UUIDKey format
2. **Provider Credentials**: Unifying provider keys and default keys into single model

## API Keys (Authentication)

### Current
- Simple hash-based keys
- Format: `sk_live_abcd1234...`
- Stored by hash in KV store

### New
- UUIDKey format with checksum
- Format: `STARPRT_38QARV01ET0G6Z2CJD9VA2ZZAR0XJJLSO7WBNWY3F_A1B2C3D8`
- Benefits:
  - Built-in checksum validation
  - GitHub secret scanning compatibility
  - Readable and debuggable
  - UUID v7 provides timestamp ordering

## Provider Credentials (LLM Keys)

### The Realization
All provider credentials are just provider keys with different scopes:
- **User Provider Keys**: Scoped to specific API key
- **Gateway Defaults**: Provider keys with global scope that admins set up

### Current (Duplicated Models)
```go
// User's provider keys
type ProviderKey struct {
    APIKeyID    string  // Links to user's API key
    Provider    string
    EncryptedCredential string
    // ... other fields
}

// Gateway default keys (separate model - unnecessary!)
type DefaultKey struct {
    Provider    string
    EncryptedCredential string
    RateLimit   *RateLimitConfig
    // ... other fields
}
```

### New (Just BYOK with Scopes)
```go
// All provider credentials are provider keys
type ProviderKey struct {
    APIKeyID    string  // Empty/"global" = gateway-wide scope
    Provider    string
    EncryptedCredential string
    RateLimit   *RateLimitConfig  // Same features for all
    // ... all existing fields
}
```

## Storage Patterns

### User BYOK Credentials
```
provider_key:STARPRT_xxx:openai -> ProviderKey{scope: "STARPRT_xxx", ...}
```

### Gateway BYOK Credentials (Global Scope)
```
provider_key:global:openai -> ProviderKey{scope: "global", ...}
```

All credentials are provider keys - the only difference is scope!

## Benefits

1. **Conceptual Clarity**: Gateway defaults are just globally-scoped provider keys
2. **Simpler Code**: One model, one set of logic
3. **Easier Testing**: Single code path for all credentials
4. **Better Maintainability**: No duplicate logic
5. **Same Features**: Rate limits, encryption, priority - all available
6. **Full Compatibility**: OpenRouter BYOK features preserved

## Migration

1. Add `RateLimit` field to `ProviderKey` (if missing)
2. Migrate `DefaultKey` entries to `ProviderKey` with `scope = "global"
3. Update lookup logic to include global scope
4. Remove `DefaultKey` model entirely

## No RBAC Yet

The full RBAC architecture with provider key permissions is a future enhancement. This change focuses on:
- Better API key security with UUIDKey
- Simplified credential management
- Foundation for future enhancements