# Unified Key Architecture for Starport

## Executive Summary

This document outlines the transformation of Starport's key system to use [uuidkey](https://github.com/agentstation/uuidkey) for API key generation while simplifying the credential architecture. The design unifies BYOK credentials and gateway default keys into a single model, maintaining full compatibility with [OpenRouter's BYOK specification](https://openrouter.ai/docs/use-cases/byok) while reducing code complexity.

## Table of Contents

1. [Current State Analysis](#current-state-analysis)
2. [Proposed Architecture](#proposed-architecture)
3. [OpenRouter BYOK Compatibility](#openrouter-byok-compatibility)
4. [Data Models](#data-models)
5. [KV Storage Schema](#kv-storage-schema)
6. [Implementation Phases](#implementation-phases)
7. [API Design](#api-design)
8. [Security Considerations](#security-considerations)
9. [Migration Strategy](#migration-strategy)
10. [Best Practices](#best-practices)

## Current State Analysis

### Current Architecture

1. **API Keys**: Basic hash-based authentication with scopes
2. **BYOK Credentials**: User-provided provider keys (encrypted, tied to API keys)
3. **Default Keys**: Gateway-wide provider keys (separate model, duplicates BYOK functionality)

### Key Improvements Needed

1. **Readable API Keys**: Replace hash-based keys with uuidkey format
2. **Unified Credentials**: Consolidate BYOK and Default keys into single model
3. **Enhanced Security**: Built-in checksum validation for API keys
4. **Better Tracking**: UUID v7 provides timestamp ordering
5. **GitHub Compatibility**: Keys compatible with GitHub secret scanning

## Proposed Architecture

### Core Changes

1. **API Keys use UUIDKey**: Format `STARPRT_38QARV01ET0G6Z2CJD9VA2ZZAR0XJJLSO7WBNWY3F_A1B2C3D8`
2. **Unified Credentials**: Merge BYOK and Default keys into single `ProviderCredential` model
   - User BYOK: Has `api_key_id` linking to user's API key
   - Gateway Default: Has empty `api_key_id` field
3. **Enhanced Permissions**: Add RBAC fields to API keys for future enterprise integration

### High-Level Design

```
┌─────────────────┐
│   API Request   │
└────────┬────────┘
         │
┌────────▼────────┐
│ Authentication  │ ◄── API Key (uuidkey format)
└────────┬────────┘
         │
┌────────▼────────┐
│ Access Control  │ ◄── Check scopes & permissions
└────────┬────────┘
         │
┌────────▼────────┐
│ Credential Lookup│ ◄── Find user/gateway credentials
└────────┬────────┘
         │
┌────────▼────────┐
│ Provider Router │ ◄── Select credential by priority
└────────┬────────┘
         │
┌────────▼────────┐
│  LLM Provider   │
└─────────────────┘
```

## OpenRouter BYOK Compatibility

### Maintained Features

1. **5% Pricing Model**: User-provided keys incur 5% of standard pricing
2. **Response Headers**:
   ```
   X-Key-Type: byok|gateway|default
   X-Provider-Used: openai|anthropic|...
   X-BYOK-Cost: 0.00001 (5% of standard cost)
   ```
3. **Fallback Behavior**: Keys can be configured as primary or fallback
4. **Provider Formats**: Support for OpenAI, Azure, AWS Bedrock key formats
5. **Model Routing**: Maintain `provider/model` ID format

## Data Models

### APIKey Model (Enhanced with UUIDKey)

```go
// Primary authentication key using uuidkey format
type APIKey struct {
    // Core fields
    ID          string    `json:"id"`          // uuidkey: STARPRT_38QARV01ET0G6Z2CJD9VA2ZZAR_A1B2C3D8
    Hash        string    `json:"-"`           // SHA256 for storage lookup
    Name        string    `json:"name"`        // Human-readable name
    
    // Permissions
    Scopes      []string  `json:"scopes"`      // Global permissions ["*"] or specific
    AllowedModels []string `json:"allowed_models"` // Model restrictions
    
    // RBAC fields (OSS has fields, Enterprise implements)
    OwnerID     string    `json:"owner_id,omitempty"`    // User/Team/Org ID
    OwnerType   string    `json:"owner_type,omitempty"`  // "user", "team", "org"
    
    // Configuration
    RateLimits  *RateLimitConfig `json:"rate_limits,omitempty"`
    ProviderPreferences *ProviderPreferences `json:"provider_preferences,omitempty"`
    
    // Metadata
    IsActive    bool      `json:"is_active"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
    ExpiresAt   *time.Time `json:"expires_at,omitempty"`
    LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
    UsageCount  int64     `json:"usage_count"`
}
```

### ProviderCredential Model (Unified)

```go
// Unified model for all provider credentials (user BYOK and gateway defaults)
type ProviderCredential struct {
    // Identity
    APIKeyID    string    `json:"api_key_id"`   // Empty string for gateway defaults
    Provider    string    `json:"provider"`     // "openai", "anthropic", etc.
    
    // Security
    EncryptedCredential []byte `json:"-"`        // AES-256-GCM encrypted
    
    // Configuration
    Config      map[string]interface{} `json:"config,omitempty"` // Provider-specific
    Priority    int        `json:"priority"`     // Lower = higher priority
    IsFallback  bool       `json:"is_fallback"`  // Use only as fallback
    RateLimits  *RateLimitConfig `json:"rate_limits,omitempty"` // Optional, mainly for defaults
    
    // Metadata
    CreatedAt   time.Time  `json:"created_at"`
    UpdatedAt   time.Time  `json:"updated_at"`
    LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
    UsageCount  int64      `json:"usage_count"`
}

// Examples:
// User BYOK:       {api_key_id: "STARPRT_xxx", provider: "openai", ...}
// Gateway Default: {api_key_id: "", provider: "openai", rate_limits: {...}, ...}
```

## KV Storage Schema

### Key Patterns

```
# API Keys (using hash for lookup)
apikey:{hash}                                    -> APIKey
apikey:id:{id}                                   -> {hash}
apikey:owner:{owner_type}:{owner_id}            -> Set[{id}]

# Provider Credentials (unified for user BYOK and gateway defaults)
credential:{api_key_id}:{provider}               -> ProviderCredential
credential:by-provider:{provider}:{api_key_id}   -> {credential_id}
# Note: Gateway defaults use empty api_key_id, e.g., credential::openai

# Usage Tracking (enhanced)
usage:apikey:{id}:{date}                         -> DailyUsage
usage:byok:{api_key_id}:{provider}:{date}        -> DailyUsage

# Rate Limiting (unchanged)
ratelimit:apikey:{id}:{window}:{bucket}          -> TokenBucket

# Audit Log (enhanced)
audit:apikey:{id}:{timestamp}                    -> AuditEntry
audit:byok:{api_key_id}:{provider}:{timestamp}   -> AuditEntry
```

### Access Resolution Algorithm

```go
func ResolveProviderCredential(ctx context.Context, apiKey *APIKey, provider string) (*ProviderCredential, error) {
    // 1. Get all available credentials (user and gateway)
    var creds []*ProviderCredential
    
    // User's BYOK credentials
    userCreds := getCredentials(apiKey.ID, provider)
    creds = append(creds, userCreds...)
    
    // Gateway default credential (api_key_id = "")
    if defaultCred := getCredentials("", provider); defaultCred != nil {
        creds = append(creds, defaultCred)
    }
    
    // 2. Filter active credentials
    activeCreds := filterActive(creds)
    
    // 3. Sort by priority and fallback status
    sort.Slice(activeCreds, func(i, j int) bool {
        if activeCreds[i].IsFallback != activeCreds[j].IsFallback {
            return !activeCreds[i].IsFallback
        }
        return activeCreds[i].Priority < activeCreds[j].Priority
    })
    
    // 4. Return first valid credential
    if len(activeCreds) > 0 {
        return activeCreds[0], nil
    }
    
    return nil, ErrNoCredentialFound
}
```

## Implementation Phases

### Phase 1: API Key Enhancement (Week 1)

1. **Add uuidkey dependency**:
   ```bash
   go get github.com/agentstation/uuidkey
   ```

2. **Update APIKey model**:
   - Add ID field with uuidkey format
   - Maintain Hash field for backward compatibility
   - Add key generation utilities

3. **Update authentication**:
   - Support both old hash format and new uuidkey format
   - Validate uuidkey checksum on authentication
   - Update key storage patterns

### Phase 2: Migration Tools (Week 2)

1. **Create migration utilities**:
   - Generate new uuidkey for existing API keys
   - Dual-write to support both formats
   - Update all references

2. **Update management APIs**:
   - Return new key format in responses
   - Support both formats in requests
   - Add deprecation notices

### Phase 3: Cleanup (Week 3)

1. **Remove old format support**:
   - Switch to uuidkey-only authentication
   - Clean up old hash-based lookups
   - Update documentation

## API Design

### API Key Management

```yaml
# Create an API key
POST /api/v1/keys
Authorization: Bearer {admin_key}
Content-Type: application/json

{
  "name": "Production API Key",
  "scopes": ["read", "write"],
  "allowed_models": ["openai/gpt-4", "anthropic/claude-3"],
  "rate_limits": {
    "requests_per_minute": 60,
    "tokens_per_minute": 90000
  }
}

Response:
{
  "id": "STARPRT_38QARV01ET0G6Z2CJD9VA2ZZAR0XJJLSO7WBNWY3F_A1B2C3D8",
  "key": "STARPRT_38QARV01ET0G6Z2CJD9VA2ZZAR0XJJLSO7WBNWY3F_A1B2C3D8", // Full key shown once
  "name": "Production API Key",
  "scopes": ["read", "write"],
  "created_at": "2024-01-15T10:00:00Z"
}
```

### Provider Credential Management

```yaml
# Add user BYOK credential
POST /api/v1/keys/{key_id}/credentials
Authorization: Bearer {api_key}
Content-Type: application/json

{
  "provider": "openai",
  "credential": {
    "api_key": "sk-...",
    "organization": "org-..."
  },
  "config": {
    "endpoint": "https://api.openai.com/v1"
  },
  "priority": 1,
  "is_fallback": false
}

Response: 204 No Content

# Add gateway default credential (admin only)
POST /api/v1/admin/credentials
Authorization: Bearer {admin_key}
Content-Type: application/json

{
  "provider": "openai",
  "credential": {
    "api_key": "sk-...",
    "organization": "org-..."
  },
  "priority": 100,  // Lower priority than user keys
  "rate_limits": {
    "requests_per_minute": 500,
    "tokens_per_minute": 100000
  }
}

Response: 204 No Content
```

## Security Considerations

### API Key Security

1. **UUIDKey Benefits**:
   - Built-in CRC32 checksum prevents typos
   - UUID v7 provides timestamp ordering
   - Compatible with GitHub secret scanning
   - Readable format aids debugging

2. **Storage Security**:
   - Keys stored by SHA256 hash
   - Original key never stored
   - Checksum validation on every request

### BYOK Security (Unchanged)

1. **Encryption**: AES-256-GCM for credentials
2. **Key Derivation**: Argon2id from master key + API key ID
3. **Isolation**: Per-API-key credential encryption
4. **Zero-Knowledge**: Gateway cannot decrypt without API key

## Migration Strategy

### Phase 1: Dual Support

```bash
# Deploy with dual-key support
starport migrate prepare --feature=uuidkey

# Both old and new keys work:
# Old: Authorization: Bearer sk_live_abcd1234...
# New: Authorization: Bearer STARPRT_38QARV01ET0G6Z2CJD9VA2ZZAR_A1B2C3D8
```

### Phase 2: Key Migration

```bash
# Generate new keys for existing users
starport migrate keys --generate-uuidkeys

# Notify users of new keys via email/dashboard
starport migrate notify --template=uuidkey-migration
```

### Phase 3: Deprecate Old Format

```bash
# Add deprecation warnings
starport migrate deprecate --old-format=hash

# After grace period, disable old format
starport migrate complete --disable-old-format
```

## Best Practices

### Key Management

1. **Naming Convention**: Use `STARPRT_` prefix for all API keys
2. **Key Rotation**: Rotate keys every 90 days
3. **Audit Logging**: Log all key usage with uuidkey ID
4. **Monitoring**: Alert on invalid checksum attempts

### Development

1. **Testing**: Use predictable keys in tests
2. **Documentation**: Show full key format in examples
3. **Error Messages**: Include checksum validation hints

## Conclusion

This architecture enhances Starport's system by:
1. **API Keys**: Adopting uuidkey for better security and usability
2. **Credentials**: Unifying BYOK and Default keys into a single model, reducing code complexity
3. **Compatibility**: Maintaining full OpenRouter BYOK compatibility

The phased approach ensures smooth migration with minimal disruption while simplifying the codebase.