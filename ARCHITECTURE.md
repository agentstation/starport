# Starport Architecture

**Summary**  
Starport is a high-performance, open-source LLM gateway built in Go that provides unified access to multiple model providers with sub-millisecond latency overhead. The OSS version requires **zero external dependencies** - using embedded Badger KV store by default for all data (API keys, rate limits, presets, BYOK credentials). For multi-node deployments, it supports Valkey as a distributed KV store. PostgreSQL is used **only in the enterprise version** for relational data like users, organizations, and audit logs. The gateway includes advanced routing strategies, comprehensive caching, rate limiting, BYOK support, and preset management. The core is fully open source (GNU AGPLv3), while enterprise features (SSO, advanced filtering, analytics) are available through a separate commercial package via a plugin architecture.

## 1. Quick Start

```bash
# Single command to run Starport with embedded storage
docker run -p 8080:8080 ghcr.io/agentstation/starport:latest

# Or download and run the binary
wget https://github.com/agentstation/starport/releases/latest/starport
chmod +x starport
./starport  # Runs server with embedded Badger DB by default

# CLI commands in the same binary
./starport keys create --name production-key
./starport keys list
./starport config validate
./starport version
```

## 2. Core Architecture Overview

```mermaid
graph TD
  subgraph "Open Source Core"
    Client[API Clients] --> Gateway[API Gateway]
    Gateway --> Auth[Auth Manager]
    Auth --> Storage{Storage Mode}
    Storage -->|Single Node| Badger[(Badger KV)]
    Storage -->|Multi Node| Valkey[(Valkey)]
    Gateway --> Connector[Model Connectors]
    Connector --> OpenAI[OpenAI]
    Connector --> Anthropic[Anthropic]
    Connector --> GoogleAIStudio[Google AI Studio - Gemini]
    Connector --> VertexAI[Google Vertex AI - All Models]
    Connector --> Groq[Groq]
    Connector --> Mistral[Mistral AI]
    Connector --> Azure[Azure OpenAI]
    Connector --> Custom[Custom Providers]
    
    RESTAPI[REST Management API] --> Auth
    RESTAPI --> Storage
    CLI[CLI Tool] --> RESTAPI
    
    Badger -.->|"Stores"| OSData[API Keys, Presets, BYOK, Filters, Rate Limits]
    Valkey -.->|"Stores"| OSData
  end
  
  subgraph "Enterprise Package (Separate)"
    style Enterprise fill:#f9f,stroke:#333,stroke-width:2px,stroke-dasharray: 5 5
    EntAuth[Enterprise Auth] --> WorkOS[WorkOS SSO]
    EntAuth --> PostgreSQL[(PostgreSQL)]
    EntUI[React Admin UI] --> EntAuth
    EntFilter[Advanced Filters] --> MLModels[ML Classifiers]
    EntNotify[Multi-Channel Alerts] --> Channels[Slack/PagerDuty]
    EntAnalytics[Analytics Engine] --> PostgreSQL
    
    PostgreSQL -.->|"Stores"| EntData[Users, Orgs, RBAC, Audit Logs]
  end
  
  Gateway -.->|Plugin Interface| EntAuth
  Gateway -.->|Plugin Interface| EntFilter
  Gateway -.->|Plugin Interface| EntNotify
  Gateway -.->|Requires| Valkey
```

## 3. Repository Structure

```
# Open Source Repository
github.com/agentstation/starport/          # Main OSS repository
├── cmd/
│   └── starport/                          # Single binary (server + CLI)
├── internal/                              # Core implementation (OSS only)
├── pkg/                                   # Public packages
│   └── enterprise/                        # Enterprise interface definitions
└── docs/                                  # OSS documentation

# Enterprise Package (Private)
github.com/agentstation/starport-enterprise/  # Commercial package
├── auth/                                  # SSO, RBAC implementation
├── ui/                                    # React admin interface
├── filters/                               # Advanced ML filters
├── analytics/                             # Usage analytics
└── notify/                                # Multi-channel notifications

# Supporting Repositories
github.com/agentstation/starport-helm/     # Kubernetes deployment
github.com/agentstation/starport-connectors/  # Additional providers
```

## 4. Build Strategy

```go
// pkg/enterprise/interface.go (in OSS repo)
package enterprise

type Plugin interface {
    Name() string
    Initialize(config map[string]interface{}) error
}

type AuthPlugin interface {
    Plugin
    Authenticate(token string) (*User, error)
    Authorize(user *User, resource string, action string) bool
}

type FilterPlugin interface {
    Plugin
    FilterRequest(req *Request) (*Request, error)
    FilterResponse(resp *Response) (*Response, error)
}
```

```bash
# OSS Build (no enterprise features)
go build -o starport ./cmd/starport

# Enterprise Build (requires enterprise package)
go build -tags enterprise \
  -o starport-enterprise \
  ./cmd/starport
```

## 5. Technical Stack

### 5.1 Core Technologies

**Language & Runtime**
- **Go 1.22+** - For high performance and excellent concurrency
- **Single binary deployment** - Both server and CLI in one executable

**Web Framework & HTTP**
- **Chi router** - Lightweight, fast HTTP router with middleware support
- **Standard library net/http** - For maximum performance and compatibility
- **HTTP/2 support** - Built-in with Go's standard library

**CLI Framework**
- **urfave/cli** - Simple, fast CLI framework for the command interface

**Configuration Management**
- **sethvargo/go-envconfig** - Type-safe environment configuration with validation
- **Multiple .env file support** - local.env overrides .env for development
- **Hot reload** for specific settings (rate limits, routing rules) via file watching

**Logging & Observability**
- **Zerolog** - Zero-allocation structured logging for performance
- **OpenTelemetry** - For distributed tracing and metrics export

**Storage**
- **Badger v4** - Embedded KV store (default, zero dependencies)
- **Valkey** - Redis-compatible distributed KV store (for multi-node)
- **Standard library encoding/json** - For data serialization

**Security**
- **github.com/agentstation/uuidkey** - For Starport API key generation (UUID v7 based)
- **Argon2id** - For key derivation when encrypting BYOK credentials
- **AES-256-GCM** - For BYOK credential encryption
- **crypto/rand** - For additional secure random needs

**Testing**
- **Standard library testing** - For unit tests
- **Testify** - For assertions and mocks
- **httptest** - For API endpoint testing

### 5.2 Dependencies Philosophy

The core OSS version maintains minimal dependencies:
- Embedded storage (Badger) allows zero external dependencies
- All chosen libraries are well-maintained with strong track records
- Performance is prioritized over features
- Standard library preferred where possible

## 6. Application Architecture

### 6.1 Clean Architecture Pattern

Starport follows a clean architecture pattern with clear separation of concerns:

```go
// cmd/starport/main.go - Minimal entry point
func main() {
    start()
}

// cmd/starport/start.go - Signal handling and lifecycle
func start() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
    
    if err := run(ctx); err != nil {
        log.Fatalf("Fatal error: %v", err)
    }
}

// cmd/starport/run.go - Application setup
func run(ctx context.Context) error {
    app, err := app.New(
        app.WithConfig(loadConfig()),
        app.WithLogger(setupLogger()),
    )
    if err != nil {
        return fmt.Errorf("failed to create app: %w", err)
    }
    
    return app.Run(ctx)
}
```

### 6.2 Options Pattern

The application uses functional options for flexible configuration:

```go
type Option func(*App) error

func WithConfig(cfg *Config) Option {
    return func(a *App) error {
        a.config = cfg
        return nil
    }
}

func WithLogger(logger *zerolog.Logger) Option {
    return func(a *App) error {
        a.logger = logger
        return nil
    }
}
```

### 6.3 Graceful Shutdown

Comprehensive shutdown handling with cleanup aggregation:

```go
func (app *App) Shutdown(ctx context.Context) error {
    var shutdownErr error
    
    // Shutdown HTTP server first
    if err := app.server.Shutdown(ctx); err != nil {
        shutdownErr = fmt.Errorf("server shutdown error: %w", err)
    }
    
    // Always attempt cleanup
    if cleanupErr := app.cleanup(); cleanupErr != nil {
        if shutdownErr != nil {
            shutdownErr = fmt.Errorf("%v; cleanup error: %w", shutdownErr, cleanupErr)
        } else {
            shutdownErr = fmt.Errorf("cleanup error: %w", cleanupErr)
        }
    }
    
    return shutdownErr
}
```

### 6.4 Connector Initialization

The application automatically initializes LLM provider connectors based on configuration:

```go
// Connectors are initialized from environment configuration
// Only providers with configured base URLs are initialized
if providersConfig.OpenAI.BaseURL != "" {
    cfg := convertToConnectorConfig(providersConfig.OpenAI, "OPENAI_API_KEY")
    connector, err := connectors.NewOpenAIConnector(cfg)
    if err == nil {
        registry.Register("openai", connector)
    }
}

// API keys are loaded from environment variables:
// - OPENAI_API_KEY
// - ANTHROPIC_API_KEY  
// - GOOGLE_API_KEY
// - GROQ_API_KEY
// - MISTRAL_API_KEY
// - AZURE_OPENAI_API_KEY

// Falls back to mock connector if no providers configured
```

## 7. CLI Architecture

### 7.1 Single Binary Design

Starport uses a single binary that can operate in multiple modes:

```go
// cmd/starport/main.go
func main() {
    app := &cli.App{
        Name: "starport",
        Usage: "High-performance LLM gateway",
        Commands: []*cli.Command{
            {
                Name:    "serve",
                Aliases: []string{"server"},
                Usage:   "Run the gateway server",
                Action:  runServer,
                Flags:   serverFlags,
            },
            {
                Name:  "keys",
                Usage: "Manage API keys",
                Subcommands: []*cli.Command{
                    {Name: "create", Action: createKey},
                    {Name: "list", Action: listKeys},
                    {Name: "delete", Action: deleteKey},
                    {Name: "rotate", Action: rotateKey},
                },
            },
            {
                Name:  "config",
                Usage: "Configuration management",
                Subcommands: []*cli.Command{
                    {Name: "validate", Action: validateConfig},
                    {Name: "generate", Action: generateConfig},
                },
            },
        },
        Action: runServer, // Default action when no command specified
    }
    app.Run(os.Args)
}
```

### 6.2 CLI Commands

**Server Mode** (default):
- `starport` or `starport serve` - Run the gateway server
- `starport serve --config prod.yaml` - Run with specific config

**Key Management**:
- `starport keys create --name <name> --scopes <scopes>`
- `starport keys list [--format json]`
- `starport keys delete <key-id>`
- `starport keys rotate <key-id>`

**Configuration**:
- `starport config validate [--file config.yaml]`
- `starport config generate --mode [memory|embedded|distributed]`

**Utilities**:
- `starport version` - Show version info
- `starport health --url <gateway-url>` - Health check
- `starport migrate` - Run database migrations

## 8. Advanced Routing Architecture

### 8.1 OpenRouter-Compatible Model Naming

**Model ID Format**: All models use the `provider/model` format for full OpenRouter compatibility:
```
openai/gpt-4
anthropic/claude-3-opus-20240229
google-aistudio/gemini-1.5-pro
google-vertexai/gemini-1.5-pro
google-vertexai/claude-3-opus@20240229
groq/llama-3.1-70b-versatile
mistral/mistral-large-latest
azure/gpt-4
```

**Current Features**:
- Google AI Studio and Vertex AI are properly separated:
  - `google-aistudio`: Gemini models only (with dynamic fetching)
  - `google-vertexai`: All Vertex AI models (Gemini, PaLM, Codey, Claude via Model Garden)
- Dynamic model fetching for Anthropic, Google AI Studio, and Groq
- 1-hour cache TTL for model responses
- Fallback to static lists on API errors

### 8.2 Model Routing (OpenRouter Compatible)

```go
// Support for OpenRouter's model selection and fallback
type ChatRequest struct {
    Model   string   `json:"model"`           // Primary model (e.g., "openai/gpt-4")
    Models  []string `json:"models,omitempty"` // Fallback models in order
    // ... other fields
}

// Auto-routing support
const AutoRouterModel = "openrouter/auto"  // Dynamically selects best model
```

**Fallback Triggers**:
- Rate limit exceeded
- Model unavailable
- Context length exceeded
- Content moderation flags
- Provider errors (5xx)

### 8.3 Provider Routing Configuration

```yaml
# OpenRouter-compatible routing parameters
routing:
  # Provider preferences (in request or config)
  provider_preferences:
    order: ["openai", "anthropic", "google"]     # Try providers in this order
    only: ["openai", "anthropic"]               # Only use these providers
    ignore: ["azure"]                            # Never use these providers
    allow_fallbacks: true                        # Allow fallback to other providers
  
  # Routing strategies
  strategies:
    - type: latency_based
      config:
        sample_size: 100
        update_interval: 30s
    - type: cost_optimized
      config:
        max_latency_multiplier: 2.0
    - type: auto_router  # OpenRouter auto model
      config:
        classifier: "meta-llama/llama-3.1-8b"
```

### 8.4 Router Implementation

```go
type ModelRouter interface {
    // OpenRouter compatible methods
    SelectModel(ctx context.Context, req *Request) (string, connectors.Connector, error)
    RouteWithFallback(ctx context.Context, req *Request) (*Response, error)
}

type ProviderPreferences struct {
    Order          []string `json:"order,omitempty"`
    Only           []string `json:"only,omitempty"`
    Ignore         []string `json:"ignore,omitempty"`
    AllowFallbacks bool     `json:"allow_fallbacks,omitempty"`
}

// All routing features are integrated into a single router
type Router struct {
    registry             connectors.Registry
    providerHealth       map[string]*ProviderHealth
    latencyTracker       LatencyTracker
    costCalculator       CostCalculator
    stickySessionManager StickySessionManager
    config               Config
}
```

### 8.5 Routing Features

**Core Features Implemented**:
- **Provider Preferences**: Support for `order`, `only`, `ignore` parameters
- **Health Tracking**: Circuit breaker pattern (3 failures = 30s circuit open)
- **Latency-Based Routing**: EMA tracking with configurable alpha (0.2) and window (5)
- **Cost Optimization**: Model pricing data with 2x max cost multiplier
- **Sticky Sessions**: Conversation continuity with 30-minute TTL
- **Model Fallback**: Automatic retry with exponential backoff

**Fallback Triggers**:
- Rate limit exceeded (429)
- Model unavailable (404)
- Context length exceeded (400 + "context_length_exceeded")
- Content moderation (400 + "content_policy_violation")
- Provider errors (5xx)
- Timeout errors

**Configuration**:
```go
type Config struct {
    // Latency tracking
    LatencyAlpha      float64       // EMA smoothing (default: 0.2)
    LatencyWindowSize int           // Warmup samples (default: 5)
    
    // Cost optimization
    EnableCostOptimization bool    // Default: true
    MaxCostMultiplier      float64 // Default: 2.0
    
    // Latency constraints
    MaxLatencyMultiplier float64   // Default: 2.0
    
    // Sticky sessions
    EnableStickySessions bool      // Default: true
    SessionTTL           time.Duration // Default: 30m
    
    // Circuit breaker
    CircuitBreakerThreshold int    // Default: 3 failures
    CircuitBreakerDuration  time.Duration // Default: 30s
}
```
- **A/B testing** with traffic splitting

## 9. Rate Limiting Architecture

### 9.1 Token Bucket Design

Starport uses a hierarchical token bucket implementation optimized for LLM workloads:

```go
// Token bucket hierarchy
type RateLimitConfig struct {
    // Global gateway protection
    Global GlobalLimits
    
    // Per API key limits
    PerKey map[string]KeyLimits
    
    // Per model limits (unless BYOK)
    PerModel map[string]ModelLimits
}

type TokenBucket struct {
    Capacity    int64         // Max tokens in bucket
    RefillRate  int64         // Tokens per second
    BurstMultiplier float64   // Allow burst = capacity * multiplier
}
```

### 9.2 Chi Middleware Integration

```go
// Rate limit middleware with early rejection
func RateLimitMiddleware(store storage.KVStore) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Extract API key
            apiKey := extractAPIKey(r)
            
            // Check limits in order: global → key → model
            if !checkGlobalLimit(store, r) {
                writeRateLimitResponse(w, "global limit exceeded")
                return
            }
            
            if !checkKeyLimit(store, apiKey, r) {
                writeRateLimitResponse(w, "key limit exceeded")
                return
            }
            
            // Model limits checked after parsing request
            next.ServeHTTP(w, r)
        })
    }
}
```

### 9.3 Rate Limit Tiers

**1. Global Limits** (Gateway Protection)
- Purpose: Prevent gateway overload
- Scope: All requests regardless of key
- Config: High limits (10K+ RPS)

**2. API Key Limits** (Customer Quotas)
- Request-based: X requests per minute/hour
- Token-based: Y tokens per minute/hour
- Burst allowance: 2x normal rate
- Headers: X-RateLimit-Limit, X-RateLimit-Remaining

**3. Model Limits** (Provider Protection)
- Applied only to gateway-proxied requests
- BYOK requests bypass these limits
- Different limits per model tier

### 9.4 Storage Implementation

**Badger (Single Node)**
```go
// Atomic token bucket operations
func (b *BadgerStore) ConsumeTokens(key string, tokens int64) (allowed bool, remaining int64) {
    err := b.db.Update(func(txn *badger.Txn) error {
        // Get current bucket state
        item, err := txn.Get([]byte(key))
        // ... refill calculation ...
        // Atomic consume operation
        return txn.Set([]byte(key), encodedBucket)
    })
    return allowed, remaining
}
```

**Valkey (Distributed)**
```lua
-- Lua script for atomic token consumption
local key = KEYS[1]
local requested = tonumber(ARGV[1])
local now = tonumber(ARGV[2])
local capacity = tonumber(ARGV[3])
local refill_rate = tonumber(ARGV[4])

-- Get current bucket
local bucket = redis.call('HMGET', key, 'tokens', 'last_refill')
-- ... refill and consume logic ...
return {allowed, remaining}
```

### 9.5 Rate Limit Headers

Following industry standards (GitHub/Stripe style):
```
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 999
X-RateLimit-Reset: 1672531200
X-RateLimit-Retry-After: 60
```

### 9.6 Configuration

```yaml
rate_limits:
  # Global protection
  global:
    requests_per_second: 10000
    burst_multiplier: 2.0
    
  # Default key limits
  default_key_limits:
    requests:
      - { window: "1m", limit: 60 }
      - { window: "1h", limit: 1000 }
    tokens:
      - { window: "1m", limit: 100000 }
      - { window: "1h", limit: 1000000 }
    
  # Model-specific limits
  model_limits:
    "gpt-4":
      requests_per_minute: 20
      tokens_per_minute: 40000
    "claude-3-opus":
      requests_per_minute: 30
      tokens_per_minute: 100000
```

## 10. Feature Breakdown

### 10.1 Open Source Features

**High-Performance Gateway Core**
- OpenAI-compatible REST API with <1ms overhead
- Concurrent request handling with Go routines
- Streaming response support with minimal buffering
- Connection pooling for upstream providers
- Circuit breaker pattern for resilience

**Advanced Routing & Load Balancing**
- Static routing by model name
- Dynamic latency-based routing
- Cost-aware routing policies
- Automatic failover with exponential backoff
- Request deduplication for identical concurrent calls
- Health checks with moving average tracking

**Caching Layer**
- Response caching for identical requests
- In-memory auth token cache with TTL
- Valkey-backed distributed cache
- Cache warming and invalidation strategies

**Authentication & Keys**
- Starport API key generation using github.com/agentstation/uuidkey (UUID v7 based)
- Direct key comparison (no hashing needed with uuidkey)
- JWT token support for stateless auth
- Key scopes and model permissions
- Rate limit configuration per key

**BYOK (Bring Your Own Key)**
- Encrypted credential storage (AES-256-GCM)
- Key derivation using Argon2id for encryption keys
- Multi-provider support with validation
- Automatic key rotation
- HashiCorp Vault integration option
- Zero-knowledge key passing mode

**Preset Management**
- JSON/YAML configuration templates
- Model-specific optimizations
- Version control with migrations
- Import/export functionality
- Template variables and inheritance

**Advanced Rate Limiting**
- Token bucket algorithm (primary) with configurable burst
- Request-based and token-based limits
- Multi-tier hierarchy: global → per-key → per-model
- Chi middleware integration for early rejection
- Storage backends:
  - Badger: Local token buckets with atomic operations
  - Valkey: Distributed token buckets with Lua scripts
- Hot-reloadable limits without restart
- BYOK requests bypass provider limits
- Rate limit headers (X-RateLimit-*) for transparency

**Content Filtering & Moderation**
- Pre-request prompt validation
- Post-response content filtering
- Regex-based content rules
- PII detection and redaction
- Configurable filter chains

**Management API**
- RESTful endpoints with OpenAPI 3.0
- GraphQL API option
- Bulk operations support
- Async job processing
- Webhook event system

**Integrated CLI**
- Built into main binary (no separate tool)
- Full management capabilities via subcommands
- Interactive mode with shell completion
- Configuration profiles support
- Batch operations for bulk management
- Import/export with validation

**Comprehensive Observability**
- Prometheus metrics with custom labels
- OpenTelemetry tracing support
- Structured logging with correlation IDs
- Real-time metric aggregation
- Latency histograms and percentiles

### 10.2 Enterprise Features (Separate Package)

**Single Sign-On (SSO)**
- WorkOS integration with SAML/OIDC
- Active Directory sync
- MFA enforcement
- Session management

**User & Organization Management**
- Multi-tenancy with data isolation
- Hierarchical RBAC with custom roles
- Team quotas and budgets
- API key inheritance policies

**React Admin UI**
- Embedded web interface with SSO
- Real-time dashboards and metrics
- Model playground for testing
- Visual policy editor
- Bulk operations interface

**Advanced ML-Based Filtering**
- NSFW detection using multiple classifiers
- Custom content moderation models
- Streaming content filtering
- Compliance filters (GDPR, HIPAA, PCI)
- DLP integration for sensitive data
- Guardrails framework integration

**Enterprise Notifications**
- Multi-channel alerts (Email, Slack, PagerDuty, Teams)
- Intelligent alert routing and escalation
- Anomaly detection with ML
- Custom webhook templates
- Incident management integration

**Advanced Analytics**
- ClickHouse-powered analytics
- Real-time cost tracking and forecasting
- Model performance comparison
- Usage pattern analysis
- Custom report builder
- Data export to BI tools

**Compliance & Audit**
- Append-only audit logs
- SIEM integration
- Compliance report generation
- Automated retention policies
- Encrypted log storage
- Chain of custody tracking

## 11. Data Models

### 11.1 Open Source Data Model (KV Store)

OSS uses a key-value store (Badger or Valkey) with the following key patterns:

```go
// API Keys
"apikey:{hash}" -> {
    "id": "uuid",
    "name": "string",
    "hash": "string",
    "scopes": ["read", "write"],
    "allowed_models": ["openai/gpt-4", "anthropic/claude-3-opus"],
    "provider_preferences": {
        "order": ["openai", "anthropic"],
        "allow_fallbacks": true
    },
    "rate_limit_config": {...},
    "metadata": {...},
    "active": true,
    "created_at": "timestamp",
    "expires_at": "timestamp"
}

// Presets
"preset:{name}" -> {
    "id": "uuid",
    "name": "string",
    "description": "string",
    "config": {...},
    "version": 1,
    "created_at": "timestamp"
}

// BYOK Credentials (encrypted)
"credential:{api_key_id}:{provider}" -> {
    "provider": "openai",
    "encrypted_credential": "base64_encrypted_data",
    "config": {
        "endpoint": "https://api.openai.com/v1",
        "api_version": "2024-02-01",
        "deployment_id": "gpt-4-prod"
    },
    "is_fallback": true,
    "priority": 1,
    "created_at": "timestamp",
    "last_used": "timestamp",
    "usage_count": 12345
}

// Filter Rules
"filter:{name}" -> {
    "id": "uuid",
    "name": "string",
    "type": "request|response",
    "rules": {...},
    "active": true,
    "created_at": "timestamp"
}

// Rate Limit Counters (with TTL)
"ratelimit:{key}:{window}" -> {
    "tokens": 42,
    "last_refill": "timestamp"
}
```

### 11.2 Enterprise PostgreSQL Schema

Enterprise features use PostgreSQL for relational data (NOT for API keys or OSS features):

```sql
-- Organizations and Multi-tenancy
CREATE TABLE organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT UNIQUE NOT NULL,
    workos_id TEXT UNIQUE,
    settings JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- User Management (SSO)
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id),
    email TEXT UNIQUE NOT NULL,
    workos_user_id TEXT UNIQUE,
    role TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- RBAC Permissions
CREATE TABLE roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    org_id UUID REFERENCES organizations(id),
    permissions JSONB NOT NULL,
    UNIQUE(org_id, name)
);

CREATE TABLE user_roles (
    user_id UUID REFERENCES users(id),
    role_id UUID REFERENCES roles(id),
    PRIMARY KEY (user_id, role_id)
);

-- Audit Logging for Compliance
CREATE TABLE audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    timestamp TIMESTAMPTZ DEFAULT NOW(),
    org_id UUID REFERENCES organizations(id),
    user_id UUID REFERENCES users(id),
    action TEXT NOT NULL,
    resource_type TEXT,
    resource_id UUID,
    metadata JSONB,
    ip_address INET
);

-- Usage Analytics and Reporting
CREATE TABLE usage_analytics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    timestamp TIMESTAMPTZ DEFAULT NOW(),
    org_id UUID REFERENCES organizations(id),
    -- Note: api_key_id is stored as TEXT since keys are in KV store
    api_key_id TEXT NOT NULL,
    model TEXT,
    tokens_used INT,
    cost DECIMAL(10,6),
    latency_ms INT
);

-- SSO Sessions
CREATE TABLE sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id),
    token_hash BYTEA UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
```

## 12. API Endpoints

### 12.1 Open Source Management API

```yaml
# Key Management
GET    /api/v1/keys                    # List all keys
POST   /api/v1/keys                    # Create new key
GET    /api/v1/keys/{id}              # Get key details
PUT    /api/v1/keys/{id}              # Update key
DELETE /api/v1/keys/{id}              # Delete key
POST   /api/v1/keys/{id}/rotate       # Rotate key

# Preset Management  
GET    /api/v1/presets                 # List presets
POST   /api/v1/presets                 # Create preset
GET    /api/v1/presets/{id}           # Get preset
PUT    /api/v1/presets/{id}           # Update preset
DELETE /api/v1/presets/{id}           # Delete preset

# BYOK Management
POST   /api/v1/keys/{id}/credentials   # Add provider credential
DELETE /api/v1/keys/{id}/credentials/{provider}  # Remove credential

# Filter Management
GET    /api/v1/filters                 # List filters
POST   /api/v1/filters                 # Create filter
PUT    /api/v1/filters/{id}           # Update filter
DELETE /api/v1/filters/{id}           # Delete filter

# Metrics & Health
GET    /api/v1/metrics                 # Basic usage metrics
GET    /api/v1/health                  # Health check
GET    /api/v1/metrics/prometheus      # Prometheus format
```

### 12.2 LLM Proxy Endpoints (Full OpenRouter Compatibility) ✅

```yaml
# Chat Completions (OpenAI & OpenRouter compatible) ✅
POST   /v1/chat/completions            # OpenAI-style endpoint
POST   /api/v1/chat/completions        # OpenRouter-style endpoint
  # Supports: model (string), streaming, all OpenAI parameters
  # TODO: models (array), provider preferences (P1-S3-3.4)

# Completions (Legacy) 🔴
POST   /v1/completions                 # OpenAI-style endpoint
POST   /api/v1/completions             # OpenRouter-style endpoint

# Embeddings ✅
POST   /v1/embeddings                  # OpenAI-style endpoint
POST   /api/v1/embeddings              # OpenRouter-style endpoint

# Models ✅
GET    /v1/models                      # OpenAI-compatible: basic model info only
GET    /api/v1/models                  # OpenRouter-compatible: full metadata
  # /v1/models returns: id, object, created, owned_by
  # /api/v1/models returns: pricing, context_length, architecture, supported_parameters

GET    /v1/models/{model}              # Get specific model details 🔴
GET    /api/v1/models/{model}/endpoints # List providers for model ✅
  # Returns providers that offer the specified model with pricing

# Providers (OpenRouter Specific) ✅
GET    /api/v1/providers               # List all providers
  # Returns: name, slug, logging_policy, privacy_url, is_moderated, etc.

# Generation Stats (OpenRouter compatible)
GET    /api/v1/generation/{id}         # Get generation details
  # Returns: id, model, usage, created_at, provider used

# Authentication & Limits (OpenRouter compatible)
GET    /api/v1/auth/key                # Check API key info
  # Returns: usage, limit, is_free_tier, rate_limit_remaining
```

### 12.3 OpenRouter-Specific Features

```yaml
# Model Response Format (OpenRouter Compatible)
{
  "data": [
    {
      "id": "openai/gpt-4",
      "name": "OpenAI: GPT-4",
      "created": 1686935002,
      "description": "GPT-4 is OpenAI's latest model...",
      "pricing": {
        "prompt": "0.00003",
        "completion": "0.00006",
        "image": "0",
        "request": "0"
      },
      "context_length": 8192,
      "architecture": {
        "input_modalities": ["text"],
        "output_modalities": ["text"],
        "tokenizer": "cl100k_base"
      },
      "top_provider": {
        "is_moderated": false,
        "max_completion_tokens": 4096
      },
      "supported_parameters": [
        "temperature", "top_p", "frequency_penalty",
        "presence_penalty", "tools", "tool_choice"
      ]
    }
  ]
}

# Provider Response Format
{
  "data": [
    {
      "name": "OpenAI",
      "slug": "openai",
      "logging_policy": "will_not_log",
      "privacy_policy_url": "https://openai.com/privacy",
      "is_moderated": false,
      "status_page_url": "https://status.openai.com"
    }
  ]
}
```

## 13. Configuration

### 13.1 Open Source Configuration

```yaml
# config.yaml (OSS)
server:
  port: 8080
  host: 0.0.0.0

# Storage configuration
storage:
  mode: badger  # Options: badger (default), valkey
  
  # Badger settings (default)
  badger:
    path: ./data/starport
    sync_writes: false
    compression: snappy
  
  # Valkey settings (for multi-node deployments)
  valkey:
    url: valkey://localhost:6379
    max_connections: 50
    cluster_mode: false
    password: ""  # Optional

providers:
  openai:
    base_url: https://api.openai.com
    timeout: 30s
    # API key from environment: OPENAI_API_KEY
  anthropic:
    base_url: https://api.anthropic.com
    timeout: 30s
    # API key from environment: ANTHROPIC_API_KEY
  google_aistudio:
    base_url: https://generativelanguage.googleapis.com/v1beta
    timeout: 30s
    # API key from environment: GOOGLE_AISTUDIO_API_KEY
  google_vertexai:
    # Base URL is auto-generated from project_id and location
    timeout: 30s
    # Access token from environment: GOOGLE_VERTEXAI_TOKEN
    extra:
      project_id: "your-project-id"
      location: "us-central1"
  groq:
    base_url: https://api.groq.com/openai/v1
    timeout: 30s
    # API key from environment: GROQ_API_KEY
  mistral:
    base_url: https://api.mistral.ai/v1
    timeout: 30s
    # API key from environment: MISTRAL_API_KEY
  azure:
    # Replace YOUR-RESOURCE-NAME with your Azure OpenAI resource name
    base_url: https://YOUR-RESOURCE-NAME.openai.azure.com
    timeout: 30s
    # API key from environment: AZURE_OPENAI_API_KEY

rate_limiting:
  default_burst: 10
  default_sustained: 100
  window: 1m

security:
  master_key_env: STARPORT_MASTER_KEY
  tls_cert: /path/to/cert.pem
  tls_key: /path/to/key.pem
```

### 13.2 Enterprise Configuration (Additional)

```yaml
# enterprise.yaml (Additional config for enterprise build)
# Note: Enterprise REQUIRES Valkey for distributed state
storage:
  mode: valkey  # Required for enterprise
  valkey:
    url: valkey://valkey-cluster:6379
    cluster_mode: true

# PostgreSQL for enterprise features only
database:
  url: postgresql://user:pass@postgres:5432/starport_enterprise
  max_connections: 25
  # Stores: users, organizations, RBAC, audit logs
  # Does NOT store: API keys, rate limits, or OSS data

workos:
  api_key: ${WORKOS_API_KEY}
  client_id: ${WORKOS_CLIENT_ID}

analytics:
  retention_days: 90
  aggregation_interval: 5m

notifications:
  smtp:
    host: smtp.example.com
    port: 587
  slack:
    webhook_url: ${SLACK_WEBHOOK_URL}
  pagerduty:
    api_key: ${PAGERDUTY_API_KEY}

ui:
  enabled: true
  base_path: /admin
```

## 14. Deployment

### 14.1 Docker Images

```dockerfile
# Open Source Image
FROM golang:1.22 AS builder
WORKDIR /app
COPY . .
RUN go build -o starport ./cmd/starport

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/starport /usr/local/bin/
ENTRYPOINT ["starport"]

# Enterprise Image (separate Dockerfile)
FROM golang:1.22 AS builder
WORKDIR /app
COPY . .
RUN go get github.com/agentstation/starport-enterprise
RUN go build -tags enterprise -o starport-enterprise ./cmd/starport

FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/starport-enterprise /usr/local/bin/
COPY --from=builder /app/web/build /app/web/build
ENTRYPOINT ["starport-enterprise"]
```

### 14.2 Deployment Options

#### Single Binary Deployment
```bash
# Simplest deployment - uses Badger by default
./starport

# Or with Docker
docker run -p 8080:8080 -v ./data:/data \
  ghcr.io/agentstation/starport:latest
```

#### Kubernetes Deployments
```yaml
# Single-node deployment (Badger storage)
apiVersion: apps/v1
kind: Deployment
metadata:
  name: starport
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: starport
        image: ghcr.io/agentstation/starport:latest
        env:
        - name: STORAGE_MODE
          value: "badger"  # Default
        volumeMounts:
        - name: data
          mountPath: /data
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: starport-data

---
# Multi-node OSS deployment (Valkey storage)
apiVersion: apps/v1
kind: Deployment
metadata:
  name: starport-multinode
spec:
  replicas: 3
  template:
    spec:
      containers:
      - name: starport
        image: ghcr.io/agentstation/starport:latest
        env:
        - name: STORAGE_MODE
          value: "valkey"
        - name: VALKEY_URL
          valueFrom:
            secretKeyRef:
              name: starport-secrets
              key: valkey-url

---
# Enterprise deployment (Valkey + PostgreSQL)
apiVersion: apps/v1
kind: Deployment
metadata:
  name: starport-enterprise
spec:
  replicas: 3
  template:
    spec:
      containers:
      - name: starport
        image: ghcr.io/agentstation/starport-enterprise:latest
        env:
        - name: STORAGE_MODE
          value: "valkey"  # Required for enterprise
        - name: VALKEY_URL
          valueFrom:
            secretKeyRef:
              name: starport-secrets
              key: valkey-url
        - name: DATABASE_URL  # PostgreSQL for enterprise features
          valueFrom:
            secretKeyRef:
              name: starport-secrets
              key: database-url
```

## 15. Security Model

### 15.1 Open Source Security
- API key authentication
- TLS enforcement
- Request signing
- Input validation
- Rate limiting protection
- Encrypted credential storage

### 15.2 Enterprise Security (Additional)
- SSO/SAML authentication
- Multi-factor authentication
- IP allowlisting
- Audit logging
- Compliance certifications
- Advanced threat detection

## 16. Storage Architecture

### 16.1 OSS Storage Options

Starport uses a unified KV store for all data (API keys, presets, BYOK credentials, filters, and rate limits):

#### Badger Mode (Default)
- **Use Case**: Single-node deployments, edge locations
- **Performance**: <1ms latency for all operations
- **Benefits**: 
  - Zero external dependencies
  - Single binary deployment
  - Persistent storage with crash recovery
  - Supports 10K+ RPS per instance
  - Built-in TTL for rate limiting
- **Configuration**: Default mode, no setup required

#### Valkey Mode
- **Use Case**: Multi-node deployments requiring shared state
- **Performance**: <5ms latency
- **Benefits**:
  - Horizontal scaling across nodes
  - High availability with replication
  - Shared rate limits across instances
  - 50K+ RPS with cluster
  - Pub/Sub for cache invalidation
  - Automatic failover with Sentinel
- **Requirements**: External Valkey instance
- **Features**:
  - Full Redis protocol compatibility
  - Pipeline support for batch operations
  - Transaction support (MULTI/EXEC)
  - Lua scripting for atomic operations
  - Pattern-based pub/sub subscriptions
- **Configuration**:
  ```yaml
  storage:
    type: valkey
    valkey:
      url: redis://localhost:6379
      password: ""
      db: 0
      max_retries: 3
      cluster_mode: false
  ```

### 14.2 Enterprise Storage Requirements

Enterprise deployments require BOTH:

#### Valkey (Required)
- Distributed state for all nodes
- Same data as OSS (keys, rate limits, etc.)
- Ensures consistency across instances

#### PostgreSQL (Required)
- **Used ONLY for enterprise features**:
  - User accounts and authentication
  - Organizations and teams
  - RBAC permissions
  - Audit logs and compliance
  - Usage analytics and reporting
  - SSO sessions and tokens
- **NOT used for**: API keys, rate limits, or any OSS features

### 16.3 Storage Interface

```go
type KVStore interface {
    // Basic operations
    Get(ctx context.Context, key string) ([]byte, error)
    Set(ctx context.Context, key string, value []byte) error
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
    
    // TTL operations
    SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error
    GetTTL(ctx context.Context, key string) (time.Duration, error)
    ExpireAt(ctx context.Context, key string, expireAt time.Time) error
    
    // Atomic operations
    Increment(ctx context.Context, key string, delta int64) (int64, error)
    Decrement(ctx context.Context, key string, delta int64) (int64, error)
    CompareAndSwap(ctx context.Context, key string, old, new []byte) error
    
    // Batch operations
    BatchGet(ctx context.Context, keys []string) (map[string][]byte, error)
    BatchSet(ctx context.Context, items map[string][]byte) error
    BatchDelete(ctx context.Context, keys []string) error
    BatchSetWithTTL(ctx context.Context, items map[string][]byte, ttl time.Duration) error
    
    // Transaction support
    BeginTransaction(ctx context.Context) (Transaction, error)
    
    // Scan operations
    Scan(ctx context.Context, pattern string, limit int) ([]string, error)
    ScanWithPrefix(ctx context.Context, prefix string, limit int) ([]string, error)
    
    // Health and lifecycle
    Ping(ctx context.Context) error
    Close() error
}

// PubSub support for cache invalidation
type PubSubProvider interface {
    GetPubSub() PubSubClient
}

type PubSubClient interface {
    Subscribe(pattern string, handler func(channel, message string)) error
    Publish(ctx context.Context, channel string, message string) error
    Close() error
}

// Storage factory using Open pattern (Go convention)
func Open(config Config) (KVStore, error) {
    switch config.Type {
    case "valkey":
        return OpenValkey(config.Valkey)
    default: // badger is default
        return OpenBadger(config.Badger)
    }
}

// Unified storage for all OSS data
type Storage struct {
    kv KVStore
}

func (s *Storage) CreateAPIKey(ctx context.Context, key *APIKey) error {
    data, _ := json.Marshal(key)
    return s.kv.Set(ctx, "apikey:"+key.Hash, data)
}

func (s *Storage) GetAPIKey(ctx context.Context, hash string) (*APIKey, error) {
    data, err := s.kv.Get(ctx, "apikey:"+hash)
    if err != nil {
        return nil, err
    }
    var key APIKey
    json.Unmarshal(data, &key)
    return &key, nil
}
```

### 16.4 Valkey Implementation Details

The Valkey storage backend provides enterprise-grade distributed storage:

#### Connection Management
```go
type ValkeyConfig struct {
    URL           string        `env:"URL,default=redis://localhost:6379"`
    MaxRetries    int           `env:"MAX_RETRIES,default=3"`
    MinIdleConns  int           `env:"MIN_IDLE_CONNS,default=10"`
    MaxConnAge    time.Duration `env:"MAX_CONN_AGE,default=0"`
    PoolTimeout   time.Duration `env:"POOL_TIMEOUT,default=4s"`
    ReadTimeout   time.Duration `env:"READ_TIMEOUT,default=3s"`
    WriteTimeout  time.Duration `env:"WRITE_TIMEOUT,default=3s"`
    Password      string        `env:"PASSWORD"`
    DB            int           `env:"DB,default=0"`
    ClusterMode   bool          `env:"CLUSTER_MODE,default=false"`
}
```

#### Key Features

1. **Atomic Operations**
   - Uses Lua scripts for compare-and-swap
   - Native INCR/DECR for counters
   - Pipeline support for batch operations

2. **Transaction Support**
   - MULTI/EXEC for atomic updates
   - Optimistic locking with WATCH
   - Rollback capability

3. **Pub/Sub Integration**
   - Pattern-based subscriptions
   - Automatic reconnection
   - Used for cache invalidation

4. **Performance Optimizations**
   - Connection pooling with configurable limits
   - Auto-pipelining for concurrent operations
   - Lazy connection establishment

#### Example Usage
```go
// Initialize Valkey storage
store, err := storage.Open(storage.Config{
    Type: "valkey",
    Valkey: storage.ValkeyConfig{
        URL: "redis://localhost:6379",
        ClusterMode: false,
    },
})

// Use pub/sub for cache invalidation
if provider, ok := store.(storage.PubSubProvider); ok {
    pubsub := provider.GetPubSub()
    pubsub.Subscribe("cache:invalidate:*", func(channel, message string) {
        // Handle cache invalidation
    })
}
```

## 17. Performance Architecture

### 17.1 Optimization Strategies
- **Connection pooling** for upstream providers
- **Object pools** for request/response structures
- **Efficient JSON parsing** with streaming where possible
- **Goroutine pooling** to reduce allocation overhead
- **Caching** at multiple levels (local and distributed)

### 17.2 Caching Architecture

Starport uses a sophisticated data-type-specific caching strategy optimized for LLM gateway workloads:

#### Cache Strategy by Data Type

| Data Type | Single-Node (Badger) | Multi-Node (Valkey) | Invalidation |
|-----------|---------------------|---------------------|--------------|
| API Keys | Local + Badger | Local + Valkey + Pub/Sub | On update/disable |
| Rate Limits | Badger only | Valkey only | Not needed (TTL) |
| LLM Responses | Local + Badger | Valkey only | Not needed (immutable) |
| Model Metadata | Local only | Local + Pub/Sub | On model update |
| Presets | Local + Badger | Local + Valkey + Pub/Sub | On update/delete |

#### Implementation

```go
// CacheManager manages different cache strategies for different data types
type CacheManager struct {
    // API Keys: Local + Distributed with pub/sub invalidation
    apiKeys      *HybridCache
    
    // Rate Limits: Distributed only (needs consistency)
    rateLimits   *DistributedCache
    
    // LLM Responses: Distributed only for multi-node
    responses    Cache
    
    // Model Metadata: Local only with long TTL
    models       *LocalCache
    
    // Presets: Local + Distributed with pub/sub invalidation
    presets      *HybridCache
    
    storage      storage.KVStore
    pubsub       PubSubClient
}

// HybridCache implements two-layer caching with invalidation
type HybridCache struct {
    local        *ristretto.Cache    // Hot tier (microseconds)
    distributed  storage.KVStore     // Cold tier (milliseconds)
    pubsub       PubSubClient       // For invalidation
    localTTL     time.Duration      // Shorter TTL for local
}
```

#### Cache Coherence with Pub/Sub

When deployed with Valkey, Starport automatically enables pub/sub based cache invalidation:

```go
// API key disabled on Node A
func (cm *CacheManager) DisableAPIKey(ctx context.Context, hash string) error {
    // 1. Update in distributed store
    apiKey.Active = false
    cm.SetAPIKey(ctx, hash, apiKey)
    
    // 2. Delete from cache (triggers invalidation)
    cm.apiKeys.Delete(ctx, hash)
    // This publishes to "cache:inv:apikey:{hash}"
    
    // 3. All nodes receive invalidation and clear local cache
}

// Invalidation channels
const (
    ChannelAPIKeyInvalidate = "cache:inv:apikey:"
    ChannelPresetInvalidate = "cache:inv:preset:"
    ChannelModelInvalidate  = "cache:inv:model:"
)
```

#### Deployment Mode Detection

The cache manager automatically detects the deployment mode and adjusts strategies:

```go
func NewCacheManager(config Config, store storage.KVStore) (*CacheManager, error) {
    // Detect pub/sub capability
    if provider, ok := store.(PubSubProvider); ok {
        cm.pubsub = provider.GetPubSub()
        // Multi-node mode: Use distributed cache for responses
        cm.responses = NewDistributedCache(store, "response:")
    } else {
        // Single-node mode: Use hybrid cache for better performance
        cm.responses = NewHybridCache(config, store, nil)
    }
}
```

### 17.3 Performance Targets
- **Latency**: <1ms P99 overhead at 10K QPS
- **Throughput**: 50,000+ RPS per instance
- **Concurrent connections**: 100,000+ per instance
- **Memory usage**: <500MB at full load
- **CPU efficiency**: <1 core per 10K RPS

## 18. Error Handling Strategy

### 18.1 Error Categories

```go
// Standardized error codes across the system
const (
    // Client errors (4xx)
    ErrInvalidRequest    = "invalid_request"
    ErrUnauthorized      = "unauthorized"
    ErrRateLimited       = "rate_limit_exceeded"
    ErrQuotaExceeded     = "quota_exceeded"
    ErrModelNotFound     = "model_not_found"
    
    // Server errors (5xx)
    ErrProviderError     = "provider_error"
    ErrProviderTimeout   = "provider_timeout"
    ErrInternalError     = "internal_error"
    ErrStorageError      = "storage_error"
)

// Error response format (OpenAI compatible)
type ErrorResponse struct {
    Error struct {
        Code    string `json:"code"`
        Message string `json:"message"`
        Type    string `json:"type"`
        Param   string `json:"param,omitempty"`
    } `json:"error"`
}
```

### 18.2 Retry Strategy

```yaml
retry_policy:
  # Automatic retry for transient errors
  retryable_errors:
    - provider_timeout
    - rate_limit_exceeded (with backoff)
    - 503_service_unavailable
  
  # Exponential backoff
  initial_delay: 100ms
  max_delay: 10s
  max_attempts: 3
  backoff_multiplier: 2
```

### 18.3 Error Handling Flow

1. **Provider Errors**: Automatically retry with fallback
2. **Rate Limits**: Return immediately with retry-after header
3. **Storage Errors**: Degrade gracefully (e.g., skip logging)
4. **Auth Errors**: Return immediately, no retry

## 19. API Versioning Strategy

### 19.1 Versioning Approach

- **Path-based versioning**: `/v1/`, `/v2/` for major versions
- **Header-based feature flags**: `X-Starport-Features: streaming-v2`
- **Backward compatibility**: Minimum 6 months deprecation notice

### 19.2 Version Compatibility

```go
// Version negotiation
type APIVersion struct {
    Version          string
    DeprecatedDate   *time.Time
    SunsetDate       *time.Time
    Features         []string
}

var SupportedVersions = []APIVersion{
    {
        Version: "v1",
        Features: []string{"chat", "completions", "embeddings"},
    },
    // Future: v2 with breaking changes
}
```

### 19.3 Migration Support

- Dual-write period for data format changes
- Response transformation for older clients
- Clear migration guides with examples

## 20. BYOK (Bring Your Own Key) Architecture

### 20.1 BYOK Overview

BYOK allows users to use their own provider API keys directly through Starport, matching OpenRouter's functionality. This feature provides:

- **5% Pricing Model**: Users pay 5% of the standard rate when using their own keys
- **Higher Rate Limits**: Combines provider's native limits with Starport's capacity
- **Unified Analytics**: Track usage across all providers in one place
- **Default Keys**: Gateway can provide default keys for providers
- **Fallback Support**: Automatic fallback between BYOK and gateway keys

```go
// BYOK credential storage (encrypted)
"credential:{api_key_id}:{provider}" -> {
    "provider": "openai",
    "encrypted_credential": "base64_encrypted_data",
    "config": {
        "endpoint": "https://api.openai.com/v1",  // Custom endpoints supported
        "api_version": "2024-02-01",             // Azure-specific
        "deployment_id": "gpt-4-prod",           // Azure-specific
        "model_slug": "gpt-4"                    // Model mapping for Azure
    },
    "is_fallback": true,  // Use as fallback when rate limited
    "priority": 1,        // Order preference (lower = higher priority)
    "created_at": "timestamp",
    "last_used": "timestamp",
    "usage_count": 12345
}

// Default provider keys (gateway-wide)
"default_key:{provider}" -> {
    "provider": "openai",
    "encrypted_credential": "base64_encrypted_data",
    "config": {...},
    "rate_limit": {
        "requests_per_minute": 500,
        "tokens_per_minute": 100000
    },
    "created_at": "timestamp"
}
```

### 20.2 BYOK Request Flow

```go
// BYOK-aware request routing
func (h *ProxyHandler) routeRequest(ctx context.Context, req *ChatRequest) {
    // 1. Extract provider from model ID (e.g., "openai/gpt-4" -> "openai")
    provider := extractProvider(req.Model)
    
    // 2. Determine key selection strategy
    keyStrategy := h.determineKeyStrategy(ctx, req)
    
    switch keyStrategy {
    case UseOnlyBYOK:
        // Customer wants only their keys
        if cred := h.getBYOKCredential(ctx, req.APIKey, provider); cred != nil {
            return h.proxyWithBYOK(ctx, req, cred)
        }
        return errors.New("no BYOK credential found for provider")
        
    case PreferBYOK:
        // Try BYOK first, fallback to gateway
        if cred := h.getBYOKCredential(ctx, req.APIKey, provider); cred != nil {
            if resp, err := h.proxyWithBYOK(ctx, req, cred); err == nil {
                return resp, nil
            }
        }
        return h.proxyWithGateway(ctx, req)
        
    case PreferGateway:
        // Try gateway first, fallback to BYOK
        resp, err := h.proxyWithGateway(ctx, req)
        if err != nil && h.isRateLimitError(err) {
            if cred := h.getBYOKCredential(ctx, req.APIKey, provider); cred != nil {
                return h.proxyWithBYOK(ctx, req, cred)
            }
        }
        return resp, err
        
    case UseDefaultKey:
        // Use gateway's default key for provider
        return h.proxyWithDefaultKey(ctx, req, provider)
    }
}

// BYOK billing calculation
func (h *ProxyHandler) calculateBYOKCost(usage *Usage, provider string) float64 {
    standardCost := h.getStandardCost(usage, provider)
    return standardCost * 0.05  // 5% of standard rate
}
```

### 20.3 BYOK Security

- **Encryption**: AES-256-GCM for credential storage
- **Key Derivation**: Argon2id from master key + API key ID
- **Isolation**: Each API key's BYOK credentials are encrypted with unique key
- **No Logging**: BYOK requests bypass request/response logging
- **Audit Trail**: Track BYOK usage without exposing credentials

### 20.4 Supported BYOK Providers

```yaml
byok_providers:
  openai:
    required_fields: ["api_key"]
    optional_fields: ["organization"]
    validation:
      - check_api_key_format: "sk-*"
      - test_endpoint: "/v1/models"
    
  anthropic:
    required_fields: ["api_key"]
    optional_fields: ["version"]
    validation:
      - check_api_key_format: "sk-ant-*"
      - test_endpoint: "/v1/messages"
    
  azure:
    required_fields: ["api_key", "endpoint", "deployment_id"]
    optional_fields: ["api_version", "model_slug"]
    validation:
      - validate_endpoint_url
      - test_deployment_exists
    config_format: |
      {
        "endpoint": "https://your-resource.openai.azure.com",
        "api_key": "your-api-key",
        "deployment_id": "gpt-4-deployment",
        "model_slug": "gpt-4"
      }
    
  google_aistudio:
    required_fields: ["api_key"]
    optional_fields: []
    validation:
      - check_api_key_length: 39
    
  google_vertexai:
    required_fields: ["service_account_json"]
    optional_fields: ["project_id", "location"]
    validation:
      - parse_service_account
      - validate_permissions
    
  aws_bedrock:
    required_fields: ["access_key_id", "secret_access_key", "region"]
    optional_fields: ["session_token"]
    validation:
      - validate_aws_credentials
      - check_bedrock_access
    config_format: |
      {
        "access_key_id": "AKIAIOSFODNN7EXAMPLE",
        "secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
        "region": "us-east-1"
      }
      
  groq:
    required_fields: ["api_key"]
    optional_fields: []
    validation:
      - check_api_key_format: "gsk_*"
      
  mistral:
    required_fields: ["api_key"]
    optional_fields: []
    validation:
      - check_api_key_length: 32
```

### 20.5 BYOK Features & Benefits

**Key Management**:
- Encrypted storage with AES-256-GCM
- Per-API-key isolation of BYOK credentials
- Support for multiple keys per provider
- Priority-based key selection
- Automatic key validation on add

**Rate Limiting Benefits**:
- BYOK requests bypass gateway rate limits
- Use provider's native rate limits
- Combined capacity when using fallback
- Separate quota tracking for BYOK vs gateway

**Fallback Behavior**:
1. **Gateway First** (default):
   - Use gateway keys/credits initially
   - Fall back to BYOK on rate limit or failure
   - Transparent to the client
   
2. **BYOK First**:
   - Prefer customer's keys
   - Fall back to gateway if BYOK fails
   - Lower costs (5% rate)
   
3. **BYOK Only**:
   - Never use gateway keys
   - Fail if BYOK unavailable
   - Maximum cost savings

**Response Headers**:
```http
X-Provider-Used: openai
X-Key-Type: byok  # or "gateway", "default"
X-BYOK-Cost: 0.0015  # 5% of standard rate
X-Credits-Remaining: 95.50
```

### 20.6 BYOK API Endpoints

```yaml
# BYOK Credential Management
GET    /api/v1/keys/{key_id}/credentials          # List BYOK credentials
POST   /api/v1/keys/{key_id}/credentials          # Add BYOK credential
PUT    /api/v1/keys/{key_id}/credentials/{provider} # Update credential
DELETE /api/v1/keys/{key_id}/credentials/{provider} # Remove credential
POST   /api/v1/keys/{key_id}/credentials/{provider}/validate # Test credential

# Default Key Management (Admin only)
GET    /api/v1/admin/default-keys                 # List default keys
POST   /api/v1/admin/default-keys                 # Set default key
DELETE /api/v1/admin/default-keys/{provider}      # Remove default key

# BYOK Usage Analytics
GET    /api/v1/keys/{key_id}/usage/byok          # BYOK usage stats
GET    /api/v1/keys/{key_id}/usage/comparison    # BYOK vs gateway comparison
```

## 21. Operational Considerations

### 20.1 Graceful Shutdown

```go
// Shutdown sequence
func (s *Server) Shutdown(ctx context.Context) error {
    // 1. Stop accepting new requests
    // 2. Wait for in-flight requests (max 30s)
    // 3. Flush metrics and logs
    // 4. Close storage connections
    // 5. Final cleanup
}
```

### 20.2 Configuration Management

- **Hot reload** for rate limits and routing rules
- **No restart required** for adding provider keys
- **Validation** before applying changes
- **Rollback** on configuration errors

### 20.3 Health Checks

```yaml
# Health check endpoints
/health/live    # Is the process running?
/health/ready   # Are all dependencies available?
/health/startup # Is initialization complete?

# Detailed health response
{
  "status": "healthy",
  "checks": {
    "storage": "healthy",
    "providers": {
      "openai": "healthy",
      "anthropic": "degraded"
    }
  }
}
```

### 20.4 Operational Metrics

- Request rate by endpoint
- Latency percentiles (P50, P95, P99)
- Error rates by category
- Storage capacity and performance
- Provider health scores

## 21. Testing Strategy

### 21.1 Testing Levels

1. **Unit Tests**
   - Target: 90% coverage for business logic
   - Framework: Go standard testing + testify
   - Mocking: gomock for interfaces

2. **Integration Tests**
   - Badger with in-memory mode
   - Mock provider responses
   - Full request/response cycle

3. **Load Tests**
   - Tool: k6 or vegeta
   - Target: 10K RPS sustained
   - Measure: latency distribution, error rates

4. **Chaos Tests**
   - Network failures
   - Storage failures
   - Provider timeouts

### 21.2 Test Data Management

```go
// Test fixtures for consistent testing
type TestFixtures struct {
    APIKeys    []APIKey
    Presets    []Preset
    Requests   []TestRequest
    Responses  []TestResponse
}

// Provider mocks with realistic responses
type MockProvider struct {
    Latency    time.Duration
    ErrorRate  float64
    Responses  map[string]Response
}
```

## 22. Migration Strategies

### 22.1 Badger to Valkey Migration

```bash
# Zero-downtime migration process
1. Deploy with dual-write (Badger primary, Valkey secondary)
2. Verify data consistency
3. Switch reads to Valkey
4. Verify performance
5. Switch writes to Valkey
6. Decommission Badger
```

### 22.2 Data Export/Import

```go
// Backup format for portability
type BackupFormat struct {
    Version   string    `json:"version"`
    Timestamp time.Time `json:"timestamp"`
    Data      struct {
        APIKeys     []APIKey     `json:"api_keys"`
        Presets     []Preset     `json:"presets"`
        Filters     []Filter     `json:"filters"`
        Credentials []Credential `json:"credentials"`
    } `json:"data"`
}
```

## 23. Plugin Architecture

### 23.1 Plugin System Design
```go
// Focused plugin interface for specific use cases
type Plugin interface {
    Name() string
    Version() string
    Initialize(context.Context, map[string]interface{}) error
    Shutdown(context.Context) error
}

// Specific plugin types (not all features are pluggable)
type AuthPlugin interface {
    Plugin
    ValidateRequest(context.Context, *Request) error
}

type TransformPlugin interface {
    Plugin
    TransformRequest(context.Context, *Request) (*Request, error)
    TransformResponse(context.Context, *Response) (*Response, error)
}
```

### 23.2 Plugin Loading
- **Built-in plugins only** (no dynamic loading in OSS)
- **Enterprise plugins** via build tags
- **Configuration-based activation**
- **No external plugin loading** (security)

### 23.3 Core Plugin Points
- **Authentication**: Custom auth schemes
- **Request/Response Transform**: Modify payloads
- **Logging**: Custom log destinations
- **Metrics**: Additional metric collectors

## 24. API Documentation Strategy

### 24.1 OpenAPI Specification

```yaml
# openapi/starport.yaml
openapi: 3.1.0
info:
  title: Starport LLM Gateway API
  version: 1.0.0
  description: |
    High-performance LLM gateway with unified access to multiple providers.
    
    ## Authentication
    Use API keys in the Authorization header:
    ```
    Authorization: Bearer sk-...
    ```
    
    ## Rate Limiting
    Rate limits are returned in response headers:
    - `X-RateLimit-Limit`: Maximum requests per window
    - `X-RateLimit-Remaining`: Remaining requests
    - `X-RateLimit-Reset`: Unix timestamp of window reset
    
  contact:
    name: Starport Support
    email: support@starport.dev
  license:
    name: GNU AGPLv3
    url: https://www.gnu.org/licenses/agpl-3.0.html

servers:
  - url: https://api.starport.dev
    description: Production
  - url: http://localhost:8080
    description: Local development

tags:
  - name: LLM
    description: LLM completion endpoints (OpenAI compatible)
  - name: Management
    description: API key and configuration management
  - name: Health
    description: Health and status endpoints
```

### 24.2 API Documentation Generation

```go
// internal/api/docs.go
package api

import (
    "github.com/swaggo/swag"
    "github.com/go-chi/chi/v5"
)

// @title Starport LLM Gateway API
// @version 1.0.0
// @description High-performance LLM gateway
// @host api.starport.dev
// @BasePath /v1
// @securityDefinitions.apikey Bearer
// @in header
// @name Authorization

// Embedded OpenAPI spec
//go:embed openapi/starport.yaml
var OpenAPISpec string

// Serve OpenAPI documentation
func ServeAPIDocs(r chi.Router) {
    // Serve OpenAPI spec
    r.Get("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/yaml")
        w.Write([]byte(OpenAPISpec))
    })
    
    // Serve Swagger UI
    r.Get("/docs", swaggerUI)
    
    // Serve ReDoc
    r.Get("/redoc", redocUI)
}
```

### 24.3 Documentation Endpoints

```yaml
# API Documentation Endpoints
GET /openapi.yaml              # OpenAPI 3.1 specification
GET /docs                      # Swagger UI interface
GET /redoc                     # ReDoc interface
GET /api/v1/docs              # API documentation JSON

# SDKs and Examples
GET /sdks                      # SDK download page
GET /examples                  # Code examples
GET /postman.json             # Postman collection
GET /insomnia.json            # Insomnia collection
```

### 24.4 Developer Documentation Structure

```
docs/
├── api/
│   ├── openapi.yaml          # OpenAPI 3.1 specification
│   ├── authentication.md     # Authentication guide
│   ├── rate-limiting.md      # Rate limiting details
│   ├── errors.md             # Error codes and handling
│   └── webhooks.md           # Webhook documentation
├── guides/
│   ├── quickstart.md         # 5-minute quickstart
│   ├── migration/            # Migration guides
│   │   ├── from-openai.md    # Migrating from OpenAI
│   │   ├── from-anthropic.md # Migrating from Anthropic
│   │   └── from-azure.md     # Migrating from Azure
│   ├── deployment/           # Deployment guides
│   │   ├── docker.md         # Docker deployment
│   │   ├── kubernetes.md     # Kubernetes deployment
│   │   └── cloud/            # Cloud-specific guides
│   └── integrations/         # Integration guides
│       ├── langchain.md      # LangChain integration
│       ├── llama-index.md    # LlamaIndex integration
│       └── vercel-ai.md      # Vercel AI SDK
├── reference/
│   ├── configuration.md      # Configuration reference
│   ├── cli.md               # CLI reference
│   ├── models.md            # Supported models
│   └── plugins.md           # Plugin development
└── sdk/
    ├── python/              # Python SDK docs
    ├── javascript/          # JS/TS SDK docs
    ├── go/                  # Go SDK docs
    └── java/                # Java SDK docs
```

### 24.5 SDK Generation

```yaml
# .github/workflows/sdk-generation.yml
name: Generate SDKs
on:
  push:
    paths:
      - 'openapi/starport.yaml'

jobs:
  generate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Generate Python SDK
        uses: openapi-generators/openapi-generator-action@v1
        with:
          generator: python
          openapi-file: openapi/starport.yaml
          output-dir: sdks/python
          
      - name: Generate TypeScript SDK
        uses: openapi-generators/openapi-generator-action@v1
        with:
          generator: typescript-axios
          openapi-file: openapi/starport.yaml
          output-dir: sdks/typescript
          
      - name: Generate Go SDK
        uses: openapi-generators/openapi-generator-action@v1
        with:
          generator: go
          openapi-file: openapi/starport.yaml
          output-dir: sdks/go
```

### 24.6 Documentation Features

1. **Interactive API Explorer**
   - Try API calls directly from documentation
   - Auto-populated with user's API key
   - Real responses from gateway

2. **Code Examples**
   - Examples in 5+ languages
   - Copy-paste ready
   - Common use cases covered

3. **Versioning**
   - Version selector in docs
   - Changelog for each version
   - Migration guides between versions

4. **Search**
   - Full-text search across docs
   - AI-powered search suggestions
   - Quick navigation shortcuts

## 25. OpenRouter Compatibility

### 25.1 API Compatibility

Starport provides full OpenRouter API compatibility, making it a drop-in replacement:

```go
// Request format supports both OpenAI and OpenRouter styles
type ChatCompletionRequest struct {
    Model       string        `json:"model"`
    Messages    []Message     `json:"messages"`
    
    // OpenRouter-specific fields
    Provider    *Provider     `json:"provider,omitempty"`
    Route       string        `json:"route,omitempty"`
    
    // Standard parameters
    Temperature *float32      `json:"temperature,omitempty"`
    TopP        *float32      `json:"top_p,omitempty"`
    MaxTokens   *int          `json:"max_tokens,omitempty"`
    
    // Advanced sampling (OpenRouter compatible)
    MinP             *float32 `json:"min_p,omitempty"`
    TopA             *float32 `json:"top_a,omitempty"`
    RepetitionPenalty *float32 `json:"repetition_penalty,omitempty"`
}

// Provider routing configuration (OpenRouter compatible)
type Provider struct {
    Order            []string `json:"order,omitempty"`
    AllowFallbacks   *bool    `json:"allow_fallbacks,omitempty"`
    RequireParameters *bool    `json:"require_parameters,omitempty"`
    DataCollection   string   `json:"data_collection,omitempty"`
    Ignore           []string `json:"ignore,omitempty"`
    Allow            []string `json:"allow,omitempty"`
}
```

### 25.2 Custom Headers Support

```go
// OpenRouter-compatible headers
const (
    HeaderReferer = "HTTP-Referer"  // App identification
    HeaderTitle   = "X-Title"        // App title for rankings
)

// Request handling with custom headers
func (h *Handler) extractHeaders(r *http.Request) RequestContext {
    return RequestContext{
        Referer: r.Header.Get(HeaderReferer),
        Title:   r.Header.Get(HeaderTitle),
        // Standard headers
        APIKey:  extractBearerToken(r.Header.Get("Authorization")),
    }
}
```

### 25.3 Provider Routing Implementation

```yaml
# Configuration for OpenRouter-style provider routing
routing:
  # Default provider order (by cost)
  default_order:
    - groq              # Fastest inference
    - openai
    - anthropic
    - google-aistudio   # Gemini models
    - google-vertexai   # All Vertex AI models
    - mistral
    - together
    - azure
  
  # Provider capabilities
  providers:
    openai:
      supports_tools: true
      supports_vision: true
      max_tokens: 128000
      data_collection: allow
    anthropic:
      supports_tools: true
      supports_vision: true
      max_tokens: 200000
      data_collection: deny
    google-aistudio:
      supports_tools: true
      supports_vision: true
      max_tokens: 1048576  # 1M context window for Gemini 1.5
    google-vertexai:
      supports_tools: true
      supports_vision: true
      max_tokens: 1048576  # Includes Gemini, PaLM, Codey models
      data_collection: allow
    groq:
      supports_tools: false
      supports_vision: false
      max_tokens: 32768
      data_collection: deny
      inference_speed: ultra_fast  # Special capability
    mistral:
      supports_tools: true
      supports_vision: false
      max_tokens: 32768
      data_collection: allow
    together:
      supports_tools: false
      supports_vision: false
      max_tokens: 32768
      data_collection: deny
      
  # Fallback behavior
  fallback:
    enabled: true
    max_attempts: 3
    backoff_ms: 1000
```

### 25.4 Migration Guide from OpenRouter

```bash
# Before (OpenRouter)
curl https://openrouter.ai/api/v1/chat/completions \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -H "HTTP-Referer: $YOUR_SITE_URL" \
  -H "X-Title: $YOUR_SITE_NAME" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Hello"}],
    "provider": {
      "order": ["OpenAI", "Azure"],
      "allow_fallbacks": true
    }
  }'

# After (Starport)
curl https://your-starport.com/api/v1/chat/completions \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  -H "HTTP-Referer: $YOUR_SITE_URL" \
  -H "X-Title: $YOUR_SITE_NAME" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Hello"}],
    "provider": {
      "order": ["openai", "azure"],
      "allow_fallbacks": true
    }
  }'
```

### 25.5 SDK Compatibility

```javascript
// OpenRouter SDK usage (unchanged)
import OpenAI from 'openai';

const openai = new OpenAI({
  baseURL: 'https://your-starport.com/api/v1',
  apiKey: process.env.STARPORT_API_KEY,
  defaultHeaders: {
    'HTTP-Referer': process.env.YOUR_SITE_URL,
    'X-Title': process.env.YOUR_SITE_NAME,
  }
});

// Works exactly the same
const completion = await openai.chat.completions.create({
  model: 'gpt-4',
  messages: [{ role: 'user', content: 'Hello' }],
  provider: {
    order: ['openai', 'anthropic'],
    allow_fallbacks: true
  }
});
```

### 25.6 Feature Parity Checklist

- [x] `/api/v1/*` endpoint paths
- [x] Bearer token authentication
- [x] HTTP-Referer and X-Title headers
- [x] Provider routing with order preference
- [x] Fallback control
- [x] Parameter requirements filtering
- [x] Data collection policies
- [x] Provider ignore/allow lists
- [x] Generation stats endpoint
- [x] Auth key checking endpoint
- [x] Advanced sampling parameters (min_p, top_a, repetition_penalty)
- [x] Model endpoint listing
- [x] Streaming support for all models
- [x] Normalized response format

### 25.7 Enhanced Features Beyond OpenRouter

While maintaining full compatibility, Starport adds:

1. **Self-hosted option** - Run on your infrastructure
2. **Preset management** - Save and reuse configurations
3. **Advanced caching** - Reduce costs with intelligent caching
4. **BYOK support** - Use your own provider API keys
5. **Content filtering** - Built-in safety filters
6. **Metrics and observability** - Prometheus/Grafana integration
7. **Plugin architecture** - Extend functionality

## 26. Developer Experience

### 26.1 Getting Started Experience

```bash
# One-line install
curl -sSL https://starport.dev/install.sh | sh

# Or via package managers
brew install starport
apt install starport
yum install starport

# Quick start
starport init my-gateway
cd my-gateway
starport serve

# Your gateway is running at http://localhost:8080
# View docs at http://localhost:8080/docs
```

### 26.2 Developer Tools

```yaml
# starport.yaml - Configuration with IDE support
# Schema available at https://starport.dev/schema.json

server:
  port: 8080
  
providers:
  openai:
    # IntelliSense autocomplete for all options
    models:
      - gpt-4
      - gpt-3.5-turbo
    
# Real-time validation and error highlighting
```

### 26.3 CLI Developer Commands

```bash
# Development helpers
starport dev                  # Hot-reload development mode
starport test                 # Run test suite
starport benchmark            # Performance testing
starport validate             # Validate configuration
starport generate api-key     # Generate test API key
starport playground           # Launch interactive playground

# Debugging tools
starport debug request <id>   # Debug specific request
starport trace <api-key>      # Trace all requests for key
starport explain error <code> # Detailed error explanation
```

### 26.4 Development Plugins

```json
// .vscode/extensions.json
{
  "recommendations": [
    "starport.vscode-starport",      // Official VS Code extension
    "starport.openapi-preview",      // OpenAPI preview
    "starport.config-validator"      // Config validation
  ]
}

// Features:
// - Syntax highlighting for starport.yaml
// - IntelliSense for configuration
// - Integrated API testing
// - Performance profiling
```

### 26.5 Documentation Quality Standards

1. **Every API endpoint** must have:
   - Clear description
   - Request/response examples
   - Error scenarios
   - Rate limit information
   
2. **Every configuration option** must have:
   - Type information
   - Default value
   - Example usage
   - Related options
   
3. **Every error code** must have:
   - Explanation
   - Common causes
   - Resolution steps
   - Related documentation

---

## References

[1] Valkey Project - https://valkey.io  
[2] OpenAI API Specification  
[3] Token Bucket Algorithm  
[4] PostgreSQL Row-Level Security  
[5] Go Build Tags Documentation  
[6] WorkOS Documentation (Enterprise)  
[7] Prometheus Metrics Best Practices  
[8] OWASP API Security Guidelines