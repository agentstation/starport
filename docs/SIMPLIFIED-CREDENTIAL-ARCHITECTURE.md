# Simplified Credential Architecture

## Current Problem

We have two separate models for essentially the same thing:
- `BYOKCredential` - User-provided provider keys (tied to API keys)
- `DefaultKey` - Gateway-wide provider keys

This creates unnecessary duplication and complexity.

## Proposed Solution

Use a single `ProviderCredential` model for all provider keys:

```go
// ProviderCredential represents any provider API key (user or gateway)
type ProviderCredential struct {
    // Identity
    ID          string    `json:"id"`          // Unique ID for this credential
    Provider    string    `json:"provider"`    // "openai", "anthropic", etc.
    
    // Ownership (empty = gateway default)
    APIKeyID    string    `json:"api_key_id,omitempty"`  // Empty for gateway defaults
    
    // Security
    EncryptedCredential string `json:"encrypted_credential"`
    
    // Configuration
    Config      map[string]interface{} `json:"config,omitempty"`
    Priority    int                    `json:"priority"`
    IsFallback  bool                   `json:"is_fallback"`
    RateLimit   *RateLimitConfig       `json:"rate_limit,omitempty"`
    
    // Metadata
    Name        string     `json:"name,omitempty"`        // Optional friendly name
    IsActive    bool       `json:"is_active"`
    CreatedAt   time.Time  `json:"created_at"`
    UpdatedAt   time.Time  `json:"updated_at"`
    LastUsed    *time.Time `json:"last_used,omitempty"`
    UsageCount  int64      `json:"usage_count"`
}
```

## How It Works

### User BYOK Credentials
```go
{
    "id": "cred_abc123",
    "provider": "openai",
    "api_key_id": "STARPRT_xxxxx",  // Tied to user's API key
    "encrypted_credential": "...",
    "priority": 1,
    "is_fallback": false
}
```

### Gateway Default Keys
```go
{
    "id": "cred_default_openai",
    "provider": "openai",
    "api_key_id": "",  // Empty = gateway default
    "encrypted_credential": "...",
    "priority": 100,   // Lower priority than user keys
    "rate_limit": {
        "requests_per_minute": 500,
        "tokens_per_minute": 100000
    }
}
```

## Storage Schema

```
# All provider credentials (user and gateway)
credential:{id} -> ProviderCredential

# Index by provider and API key (empty api_key_id = default)
credential:by-provider:{provider}:{api_key_id} -> Set[{id}]

# Quick lookup for defaults
credential:defaults:{provider} -> {id}
```

## Benefits

1. **Single Model**: One credential type to maintain
2. **Flexible**: Same model handles user and gateway keys
3. **Simpler Code**: One set of CRUD operations
4. **Easier Testing**: Single model to test
5. **Future Proof**: Can add team/org credentials later

## Migration Path

1. Update `BYOKCredential` to include `RateLimit` field
2. Migrate existing `DefaultKey` entries to `BYOKCredential` with empty `api_key_id`
3. Update credential lookup logic to handle both cases
4. Remove `DefaultKey` model entirely

## Key Resolution Logic

```go
func ResolveCredential(apiKeyID, provider string) (*ProviderCredential, error) {
    // 1. Try user's BYOK credentials first
    userCreds := getCredentials(provider, apiKeyID)
    
    // 2. Sort by priority and fallback status
    activeCreds := filterActive(userCreds)
    if len(activeCreds) > 0 {
        return selectByPriority(activeCreds), nil
    }
    
    // 3. Fall back to gateway defaults
    defaultCred := getCredentials(provider, "") // empty api_key_id
    if defaultCred != nil {
        return defaultCred, nil
    }
    
    return nil, ErrNoCredentialFound
}
```

This simplification makes the codebase cleaner and more maintainable while preserving all functionality.