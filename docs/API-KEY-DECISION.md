# API Key Architecture Decision

## Summary

Starport will:
1. Use [github.com/agentstation/uuidkey](https://github.com/agentstation/uuidkey) for API key generation
2. Recognize that all provider credentials are BYOK with different scopes (no separate DefaultKey model needed)

## Key Points

### What's Changing

1. **API Keys** (STARPORT_API_KEY):
   - Moving from simple hash-based keys to UUIDKey format
   - Format: `STARPRT_38QARV01ET0G6Z2CJD9VA2ZZAR0XJJLSO7WBNWY3F_A1B2C3D8`
   - Benefits: Readable, checksum-validated, GitHub secret scanning compatible
   - Storage: Still stored by SHA256 hash for security

2. **Credential Simplification**:
   - Recognizing that "Default Keys" are just globally-scoped BYOK credentials
   - Gateway credentials use api_key_id = "global"
   - All credentials are BYOK with different scopes
   - Removes the need for a separate DefaultKey model

### What's NOT Changing

1. **Core BYOK Features**:
   - Continue using encrypted storage with AES-256-GCM
   - Still tied to API keys via api_key_id
   - No change to the 5% pricing model
   - Same fallback strategies (Gateway First, BYOK First, BYOK Only)
   - Same encryption and security mechanisms

## Architecture Clarification

```
┌─────────────────────────────────────────────────────┐
│                  API Keys (UUIDKey)                 │
│         STARPRT_xxxxx_xxxxx (User's Auth Key)       │
└──────────────────────────┬──────────────────────────┘
                           │ Links to
┌──────────────────────────┴──────────────────────────┐
│              All Credentials are BYOK               │
│                                                     │
│  User Scope:    api_key_id = "STARPRT_xxx"        │
│  Global Scope:  api_key_id = "global"              │
│                                                     │
│  Same model, same features, just different scopes!  │
└─────────────────────────────────────────────────────┘
```

## Rationale

1. **Security**: UUIDKey provides checksum validation and secure generation
2. **Usability**: Readable format helps debugging and prevents typos
3. **Compatibility**: Works with GitHub secret scanning
4. **Simplification**: Single credential model reduces code duplication
5. **Maintainability**: Unified approach is easier to test and maintain

## Implementation Plan

1. **Phase 1**: Add UUIDKey support alongside existing keys
2. **Phase 2**: Generate new keys for users, maintain dual support
3. **Phase 3**: Deprecate old format after migration period

## Summary of Changes

1. **API Keys**: Moving to UUIDKey format for better security
2. **Credentials**: Recognizing all credentials are BYOK with scopes (user or global)
3. **No RBAC Yet**: The full RBAC architecture is a future enhancement

This approach:
- Provides immediate security benefits with UUIDKey
- Simplifies the codebase by removing the DefaultKey model
- Maintains full backward compatibility
- Makes the mental model clearer: "It's all BYOK!"