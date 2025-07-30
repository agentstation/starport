# Credential Architecture Clarification

## The Realization

All provider credentials are fundamentally the same thing - encrypted API keys for LLM providers. The only difference is their **scope**:

1. **User Provider Keys**: Scoped to a specific API key (user)
2. **Gateway Defaults**: Scoped globally (available to all users)

## Unified Mental Model

```
All Provider Credentials are Provider Keys
│
├── User Scope (scope = "STARPRT_xxx")
│   └── Only available to that specific API key holder
│
└── Global Scope (scope = "" or "global")
    └── Available to all API key holders as fallback
```

## Implementation

```go
// All provider credentials are provider keys
type ProviderKey struct {
    // Scope
    Scope       string    `json:"scope"`        // Empty/global = gateway-wide
    Provider    string    `json:"provider"`     
    
    // The actual credential (always encrypted)
    EncryptedCredential string `json:"encrypted_credential"`
    
    // Configuration (same for all)
    Config      map[string]interface{} `json:"config,omitempty"`
    Priority    int                    `json:"priority"`
    IsFallback  bool                   `json:"is_fallback"`
    RateLimit   *RateLimitConfig       `json:"rate_limit,omitempty"`
    
    // Metadata (same for all)
    Name        string     `json:"name,omitempty"`
    CreatedAt   time.Time  `json:"created_at"`
    UpdatedAt   time.Time  `json:"updated_at"`
    LastUsed    *time.Time `json:"last_used,omitempty"`
    UsageCount  int64      `json:"usage_count"`
}
```

## Admin Workflow

```bash
# Admin adds a gateway-wide OpenAI key (just a provider key with global scope)
POST /api/v1/admin/credentials
{
  "provider": "openai",
  "credential": {
    "api_key": "sk-gateway-key-xxx"
  },
  "name": "Gateway OpenAI Key",
  "priority": 100,  # Lower priority than user keys
  "rate_limit": {
    "requests_per_minute": 1000,
    "tokens_per_minute": 1000000
  }
}

# This creates a ProviderKey with scope = "" (global scope)
```

## Resolution Logic

```go
func ResolveCredential(apiKeyID, provider string) (*ProviderKey, error) {
    // 1. Get user-specific provider keys
    userCreds := getCredentials(apiKeyID, provider)
    
    // 2. Get globally-scoped provider keys (gateway defaults)
    globalCreds := getCredentials("", provider)  // or "global"
    
    // 3. Combine and sort by priority
    allCreds := append(userCreds, globalCreds...)
    sort.ByPriority(allCreds)
    
    // 4. Return highest priority active credential
    return selectBest(allCreds), nil
}
```

## Benefits of This Mental Model

1. **Conceptual Simplicity**: Only one type of credential - provider keys
2. **Code Simplicity**: No special "DefaultKey" type needed
3. **Flexibility**: Admins can set rate limits, priority, etc. on gateway keys
4. **Consistency**: Same encryption, validation, and management for all keys
5. **Future Proof**: Easy to add team/org scopes later

## Storage

```
# User provider keys
provider_key:{scope}:{provider} -> ProviderKey

# Gateway provider keys (global scope)
provider_key:global:{provider} -> ProviderKey
# or
provider_key::{provider} -> ProviderKey  # empty scope

# Index for quick lookups
credential:by-scope:user:{api_key_id} -> Set[credential_ids]
credential:by-scope:global -> Set[credential_ids]
```

## Migration Path

Since we're keeping the same `ProviderKey` model:

1. Add `RateLimit` field to `ProviderKey` (if not already there)
2. Migrate any existing `DefaultKey` entries to `ProviderKey` with empty/global `scope`
3. Update admin UI/API to manage global provider keys
4. Remove `DefaultKey` model entirely

## Summary

Gateway default keys aren't a special type - they're just provider keys that admins set up with global scope. This makes the entire system simpler and more consistent.