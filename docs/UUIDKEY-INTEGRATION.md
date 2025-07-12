# UUIDKey Integration Plan

## Overview

The Starport project should use the `uuidkey` package from github.com/agentstation/uuidkey for generating secure API keys. Currently, the implementation uses placeholder key generation that needs to be replaced.

## Current Issues

1. **Admin Handler** (`internal/server/handlers/admin.go`):
   - Uses placeholder key generation with TODOs
   - `generateAPIKeyID()` uses timestamp-based IDs
   - `generateRandomString()` uses insecure random generation
   - No proper hash storage for key validation

2. **ChatUI Handler** (`internal/chatui/handler.go`):
   - Implements its own key generation
   - Should call the admin API endpoint instead

3. **Authentication Middleware** (`internal/server/middleware.go`):
   - Currently fixed to properly validate keys by hash lookup
   - Works correctly once keys are generated properly

## Implementation Steps

### 1. Add UUIDKey Dependency

```bash
go get github.com/agentstation/uuidkey
```

### 2. Update Admin Handler

Replace the placeholder implementations in `admin.go`:

```go
import (
    "github.com/agentstation/uuidkey"
    "github.com/google/uuid"
)

// In CreateKey handler:
func (h *AdminHandler) CreateKey(w http.ResponseWriter, r *http.Request) {
    // ... existing validation ...

    // Generate UUID for the key
    keyUUID := uuid.New().String()
    
    // Create API key using uuidkey with STARPORT prefix
    apiKeyObj := uuidkey.NewAPIKey("STARPORT", keyUUID)
    
    // The actual key value (only shown once)
    keyValue := apiKeyObj.String()
    
    // Hash for storage
    hash := sha256.Sum256([]byte(keyValue))
    hashStr := hex.EncodeToString(hash[:])
    
    // Create API key model
    apiKey := &models.APIKey{
        ID:        apiKeyObj.Prefix + "_" + apiKeyObj.UUID, // e.g., STARPORT_38QARV01ET0G6Z2CJD9VA2ZZAR
        Name:      req.Name,
        Hash:      hashStr,
        Scopes:    req.Scopes,
        Active:    true,
        CreatedAt: time.Now(),
    }
    
    // Store both the key data and hash mapping
    // ... existing storage logic ...
    
    // Also store hash -> ID mapping
    if err := h.store.Set(ctx, storage.APIKeyHashKey(hashStr), []byte(apiKey.ID)); err != nil {
        // ... error handling ...
    }
}
```

### 3. Update ChatUI to Use Admin API

The ChatUI should make an internal API call to `/api/v1/admin/keys` instead of generating its own keys. This requires:

1. Either exposing the admin endpoint without auth for internal calls
2. Or creating a shared key generation service that both can use
3. Or providing ChatUI with an internal auth token

### 4. Remove Obsolete Functions

Remove these from `admin.go`:
- `generateAPIKeyID()`
- `generateRandomString()`

## Key Format

With uuidkey, API keys will have the format:
```
STARPORT_38QARV01ET0G6Z2CJD9VA2ZZAR0XJJLSO7WBNWY3F_A1B2C3D8
```

Components:
- Prefix: `STARPORT`
- Encoded UUID: `38QARV01ET0G6Z2CJD9VA2ZZAR0XJJLSO7WBNWY3F`
- Entropy: `A1B2C3D8`
- CRC32 checksum is embedded for validation

## Benefits

1. **Security**: Cryptographically secure key generation
2. **Compatibility**: Works with GitHub Secret Scanning
3. **Validation**: Built-in CRC32 checksum
4. **Readability**: Base32-Crockford encoding is human-friendly
5. **Standardization**: Consistent key format across the application

## Testing

After implementation:
1. Generate a key via admin API
2. Verify the key format matches uuidkey spec
3. Test authentication with the generated key
4. Verify hash lookups work correctly
5. Test ChatUI key generation flows through admin API