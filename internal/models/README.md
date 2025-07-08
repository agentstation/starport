# Models Package

This package defines the core data models for the Starport gateway.

## Overview

The models package provides:
- Core data structures (APIKey, Preset, BYOKCredential, TokenBucket)
- Model validation methods
- Storage key generation and parsing
- Encryption/decryption utilities for sensitive data
- Serialization helpers

## Models

### APIKey
Represents an API key for authenticating with the gateway:
- Supports scopes for permission control
- Model restrictions for access control
- Expiration support
- Metadata for custom attributes
- Rate limit configuration

### Preset
Represents a reusable configuration template:
- Version control for config changes
- JSON-based configuration storage
- Template support for common use cases

### BYOKCredential
Represents encrypted credentials for bring-your-own-key:
- AES-256-GCM encryption
- Argon2 key derivation
- Provider-specific credential storage
- Zero-knowledge design

### TokenBucket
Implements the token bucket algorithm for rate limiting:
- Configurable capacity and refill rate
- Automatic token refill based on elapsed time
- Thread-safe token consumption

## Encryption

The package includes a complete encryption system:
- AES-256-GCM for data encryption
- Argon2id for key derivation
- Random salt and nonce generation
- Base64 encoding for storage

## Storage Keys

Consistent key patterns for KV store:
- `apikey:{hash}` - API key data
- `preset:{name}` - Preset configurations
- `credential:{api_key_id}:{provider}` - BYOK credentials
- `ratelimit:{key}:{window}` - Rate limit state
- `filter:{name}` - Filter rules

## Testing

The package includes comprehensive tests with 91.9% coverage:
- Model validation tests
- Encryption/decryption tests
- Storage key parsing tests
- Token bucket algorithm tests