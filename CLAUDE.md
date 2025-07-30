# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

**Last Updated**: December 2024
**Codebase Version**: v0.1.0-alpha

## 🚀 Quick Start Agent Workflow

When you receive a task (e.g., P1-S1-1.2), follow these steps:

1. **Update TASKS.md immediately**:
   ```markdown
   | Backend | P1-S1-1.2 Project Structure | task/P1-S1-1.2-project-structure | 🟢 In Progress | 4:00 PM | - |
   ```

2. **Verify prerequisites are complete**:
   - Check TASKS.md shows dependency tasks as "✅ Complete"
   - Run any verification commands provided in your context
   - If prerequisites aren't met, update TASKS.md with blocker and stop

3. **Implement the task**:
   - Requirements are in TASKS.md (search for your task ID)
   - Technical decisions follow ARCHITECTURE.md
   - Create branch exactly as specified in your context

4. **When PR is ready**:
   - Update TASKS.md: move to "Completed Today" with PR number
   - Submit PR with format: `[P1-S1-1.2] Brief description`

**Remember**: TASKS.md is the ONLY place to track status.

## Project Context

**Project**: Starport - High-Performance LLM Gateway
**Phase**: Implementation Phase 1 Complete
**Status**: Core functionality complete, ready for Phase 2

### Quick Stats
- **Providers Supported**: 8 (OpenAI, Anthropic, Google AI Studio, Vertex AI, Groq, Mistral, Azure OpenAI, Ollama)
- **Test Coverage**: ~75% overall (varies by package)
- **API Compatibility**: OpenAI v1 + OpenRouter v1
- **Performance**: Sub-millisecond routing decisions

## Current Codebase Status

### Completed Components (Phase 1, Subphases 1.1-1.3 and partial 1.4)

**✅ Repository & Structure (P1-S1-1.1, P1-S1-1.2)**
- Go module initialized: `github.com/agentstation/starport`
- Clean architecture with `cmd/starport/` for single binary
- CLI framework integrated (urfave/cli v2)
- Basic command structure: `serve` (default), `version`

**✅ Development Environment (P1-S1-1.3)**
- Docker Compose configuration for local development
- GitHub Actions CI/CD pipeline with testing
- Pre-commit hooks for code quality
- Makefile with standard targets (build, test, lint, fmt)

**✅ HTTP Server Foundation (P1-S1-1.4)**
- Chi router with optimized middleware chain
- Graceful shutdown with context handling
- Health check endpoints (`/health/live`, `/health/ready`)
- Request ID generation and structured logging
- 93% test coverage on server package

**✅ Configuration System (P1-S1-1.5)**
- Type-safe configuration with go-envconfig
- Environment variable support with `STARPORT_` prefix
- `.env` file loading (local.env > .env precedence)
- Comprehensive validation for all config sections
- Hot reload for rate limit rules (YAML)
- Support for 8 LLM providers: OpenAI, Anthropic, Google AI Studio, Vertex AI, Groq, Mistral, Azure OpenAI, Ollama

**✅ Storage Interface (P1-S2-2.1)**
- Complete KVStore interface abstraction
- Support for TTL operations (rate limiting)
- Atomic operations (increment, CAS)
- Batch operations for efficiency
- Transaction support with isolation
- Mock implementation for testing
- Serialization helpers with type safety
- Good test coverage with mock implementation

**✅ Badger DB Integration (P1-S2-2.2)**
- Full KVStore interface implementation
- TTL support for rate limiting
- Backup/restore functionality
- Automatic compaction
- Thread-safe operations
- Comprehensive test coverage

**✅ Core Storage Models (P1-S2-2.3)**
- APIKey model with validation and permissions (moved to internal/apikeys package)
- Preset model with versioning
- ProviderKey with AES-256-GCM encryption (used for user keys)
- TokenBucket for rate limiting
- Encryption service with Argon2 key derivation
- Storage key helpers and parsers
- Well-tested with security focus

**✅ Model Connector Interface (P1-S3-3.1)**
- Connector interface with Chat, ChatStream, Embeddings, Models, and Health methods
- OpenAI-compatible request/response types
- Full streaming support with ChatStream interface
- Provider configuration with connection pooling and retry settings
- Mock connector implementation for testing
- Connector registry with factory pattern
- Comprehensive error types (APIError, StreamError)
- Comprehensive test coverage with mocks

**✅ LLM Provider Connectors (P1-S3-3.2)**
- 8 provider connectors implemented: OpenAI, Anthropic, Google AI Studio, Vertex AI, Groq, Mistral, Azure OpenAI, Ollama
- Full streaming support for all providers
- OpenRouter-compatible model IDs (provider/model format)
- Provider-specific error handling and retry logic
- Dynamic model discovery for Anthropic, Groq, Google providers, and Ollama
- Model response caching with 1-hour TTL
- ~80% test coverage

**✅ Proxy Endpoints (P1-S3-3.3)**
- OpenAI-compatible endpoints (/v1) and OpenRouter-compatible endpoints (/api/v1)
- Chat completions, embeddings, and models endpoints
- Full streaming support with SSE
- Request validation and transformation
- Connector initialization from configuration
- 85.4% test coverage

**✅ Model Routing (P1-S3-3.4)**
- OpenRouter-compatible model routing with fallback chains
- Support for models array parameter and auto model selection
- Provider preferences (order, only, ignore)
- Circuit breaker pattern for provider failures
- model_used field in responses
- Request metadata extraction for routing decisions

**✅ Provider Routing (P1-S3-3.5)**
- Advanced routing features: latency tracking, cost optimization, sticky sessions
- Provider health monitoring with circuit breaker
- Exponential moving average for latency tracking
- Cost-aware routing with provider pricing
- Fallback support with configurable retry attempts
- 76.2% test coverage

**✅ Provider Metadata (P1-S3-3.6)**
- /api/v1/providers endpoint with provider metadata
- Enhanced /api/v1/models with full metadata (pricing, context_length, architecture)
- /api/v1/models/{model}/endpoints to list providers for a model
- OpenRouter-compatible metadata structures
- Consolidated implementation (no duplicate "enhanced" methods)
- 85%+ test coverage

**✅ Dynamic Model Fetching & Google Provider Separation (P1-S3-3.7)**
- Dynamic model fetching for Anthropic, Gemini, and Groq connectors
- Split GeminiConnector into GoogleAIStudioConnector and VertexAIConnector
- Model response caching with 1-hour TTL
- Vertex AI models support (Gemini, PaLM, Codey, Claude via Model Garden)
- Backward compatibility maintained (legacy "gemini" maps to "google-aistudio")
- 85%+ test coverage

**✅ Enhanced Vertex AI Multi-Region Support**
- Automatic failover to backup regions on errors (429, 5xx)
- Intelligent default region selection based on geography
- Configurable fallback locations via environment variables
- Maintains optimal connection state after successful failover
- Comprehensive test coverage for multi-region scenarios

**✅ Provider Key Management (P1-S4-4.1)**
- OpenRouter-compatible BYOK with 5% pricing model
- AES-256-GCM encryption with Argon2id key derivation
- Zero-knowledge security design with per-API-key credential isolation
- Three fallback strategies: Gateway First, User First, User Only
- Provider-specific credential validation for all supported providers
- Provider key manager with priority-based key ordering
- REST API endpoints for provider key management
- Usage tracking and cost calculation
- Response headers (X-Key-Type, X-BYOK-Cost)
- 75%+ test coverage with security-focused tests

**✅ Valkey Storage Implementation (P1-S4-4.2b)**
- Full KVStore interface implementation for Valkey/Redis
- Pub/sub support for cache invalidation across nodes
- Transaction support with MULTI/EXEC
- Atomic operations with Lua scripts
- Batch operations with auto-pipelining
- Integration tests with real Valkey instance
- Mock tests using valkey-go mock library

**✅ Cache Implementation (P1-S4-4.2)**
- Full cache manager with hybrid caching strategies
- Response caching for chat completions and embeddings
- Streaming cache support with response reconstruction
- Cache headers (X-Cache, X-Cache-Age) in HTTP responses
- Model and provider list caching
- Cache key generation with SHA256 hashing
- Async cache writes to avoid blocking
- Distributed cache support via storage backend
- ~57% test coverage (functional but needs more tests)

**✅ Invalid Model Tracking (Latest Addition)**
- Dynamic model validation during provider registration
- Invalid model detection for 404 errors at multiple layers
- Automatic filtering of invalid models from API responses
- MarkModelInvalid/IsModelInvalid functions in catalog package
- Integration with routing fallback logic
- Prevents repeated failures with non-existent models
- Model alias resolution with ResolveModelAlias method

### Project Structure
```
starport/
├── cmd/starport/         # Single binary entry point
│   ├── main.go          # Minimal main function
│   ├── start.go         # Signal handling
│   └── run.go           # Application setup & CLI
├── internal/            # Private application code
│   ├── apikeys/        # API key management and validation ✅
│   ├── app/            # Application lifecycle ✅
│   ├── cache/          # Cache manager and strategies ✅
│   ├── chatui/         # Chat UI handler ✅
│   ├── config/         # Configuration system ✅
│   ├── httpclient/     # HTTP client with retries ✅
│   ├── models/         # Data models (presets, provider keys, etc.) ✅
│   ├── providers/      # Provider key management and connectors ✅
│   │   └── connectors/ # LLM provider connectors ✅
│   ├── proxy/          # Request proxy with caching ✅
│   ├── registry/       # Provider registry ✅
│   ├── routing/        # Model routing and fallback ✅
│   ├── server/         # HTTP server ✅
│   │   ├── dto/        # Data transfer objects
│   │   └── handlers/   # HTTP handlers ✅
│   └── storage/        # Storage abstraction ✅
├── pkg/enterprise/      # Enterprise plugin interfaces
├── Makefile            # Build automation ✅
├── docker-compose.yml  # Local development ✅
└── .github/workflows/  # CI/CD pipeline ✅
```

### Key Dependencies
- **github.com/go-chi/chi/v5**: HTTP router
- **github.com/rs/zerolog**: Structured logging
- **github.com/sethvargo/go-envconfig**: Environment config
- **github.com/joho/godotenv**: .env file support
- **github.com/fsnotify/fsnotify**: File watching for hot reload
- **gopkg.in/yaml.v3**: YAML parsing for rate limits
- **github.com/dgraph-io/badger/v4**: Embedded KV store
- **golang.org/x/crypto**: Argon2 and AES-256-GCM encryption

### Configuration Capabilities
All configuration via environment variables or .env files:
- Server settings (port, timeouts, TLS)
- Storage modes (Badger embedded or Valkey distributed)
- Provider endpoints and timeouts (6 providers configured)
- Rate limiting with hot reload
- Security settings (CORS, JWT, API keys)
- Logging configuration

### Current Status Summary

**Completed Features:**
- ✅ All infrastructure and storage components
- ✅ 8 LLM provider connectors with full streaming support
- ✅ OpenAI/OpenRouter compatible API endpoints
- ✅ Smart routing with circuit breakers and fallback
- ✅ Provider key management with AES-256-GCM encryption
- ✅ Valkey/Redis distributed storage with pub/sub
- ✅ Comprehensive caching with streaming support
- ✅ Invalid model tracking and automatic filtering
- ✅ Dynamic model discovery for multiple providers
- ✅ Authentication with hash-based API key lookup
- ✅ Ollama support with dynamic model discovery
- ✅ Natural sorting for model versions

**Known Limitations:**
1. **No rate limiting middleware** - TokenBucket model exists but middleware not implemented
2. **Streaming fallback incomplete** - Non-streaming fallback works, streaming needs implementation

**Not Yet Implemented (Phase 2):**
- Content filtering/moderation pipeline
- Preset management REST endpoints
- OpenTelemetry metrics and tracing
- Usage analytics and billing
- Admin dashboard UI
- Webhook notifications

## Document Reference

| Document | Purpose | When to Use |
|----------|---------|-------------|
| docs/TASKS.md | Task definitions, requirements, and live status (single source of truth) | Read for requirements, update for status |
| docs/ARCHITECTURE.md | Technical specifications | Reference for design decisions |
| docs/PLAN.md | Implementation roadmap | Reference for project phases |
| docs/OPERATOR-GUIDE.md | Development workflow | Reference for task execution |

### 1. Working on Your Task
You will receive a specific task ID (e.g., `P1-S1-1.2`) in your context. The spawn-agent.sh script has already:
- Verified prerequisites
- Created a workspace
- Provided the task requirements
- Given you the branch name to create

### 2. Branch Naming Convention
```bash
# Format: task/<task-id>-<brief-description>
git checkout -b task/P1-S1-1.2-project-structure-setup
```

### 3. PR Creation
When creating a PR:
- Title: `[TASK-ID] Brief description`
- Example: `[P1-S1-1.2] Set up initial project structure`
- Link to the task in docs/TASKS.md
- Include acceptance criteria checklist in PR description

### 4. Task Status Management

**docs/TASKS.md is the single source of truth for task status.**

When working on tasks:
1. **Starting work**: Update docs/TASKS.md immediately
   - Add your task to "Active Work" table
   - Mark status as "🟢 In Progress"
   
2. **Completing work**: Update docs/TASKS.md when PR is ready
   - Move task to "Recently Completed" section
   - Update "Active Work" to show "✅ Completed"
   - Add PR number
   - Mark all acceptance criteria and implementation tasks as completed [x]

3. **In your PR description**: Include implementation checklist
```markdown
## Implementation Tasks
- [x] Create directory structure
- [x] Initialize go.mod
- [x] Create Makefile
```

### 7. Testing Requirements

Every PR must include:
1. Unit tests for new code (target 90% coverage)
2. Integration tests if applicable
3. Documentation updates
4. Passing CI checks

### 8. Pull Request Guidelines

**PR Title Format**: `[P1-S1-1.2] Brief description of changes`

**PR Description**: Use the template in `.github/pull_request_template.md`

Key points:
- List all acceptance criteria as checkboxes
- Verify all tests pass before submitting
- Update docs/TASKS.md with PR number
- Note any blockers or incomplete items

## Working with Other Agents

### Avoiding Conflicts
When multiple agents work in parallel:
1. **Stay in your lane**: Only modify files related to your task
2. **Pull before push**: Always `git pull origin main` before creating your branch
3. **Communicate blockers**: Update docs/TASKS.md immediately if blocked

### Handoff Protocol
When your task blocks others:
1. Ensure your PR description clearly states what was implemented
2. Update docs/TASKS.md with accurate status
3. List any known issues or incomplete items in the PR

## Key Architectural Decisions

### Single Binary Design
- **One executable**: Server and CLI in same binary
- **Default behavior**: Running `./starport` starts the server
- **CLI via subcommands**: `./starport keys`, `./starport config`, etc.
- **Simplified deployment**: No need to manage multiple tools

### Simplified Storage Architecture
- **OSS Storage Options**:
  - **Badger** (default): Zero-dependency embedded KV store
  - **Valkey**: For multi-node deployments with shared state
- **Enterprise Requirements**:
  - **Valkey**: Required for distributed state
  - **PostgreSQL**: Only for enterprise features (users, orgs, audit logs)
- **Key Principle**: All OSS data in KV store, no SQL required

### Performance Focus
- Target: <1ms P99 latency at 10K QPS
- Go implementation for maximum concurrency
- Connection pooling and caching
- Multi-tier caching architecture

### OSS/Enterprise Separation
- Core is fully open source (GNU AGPLv3)
- Enterprise features in separate private repository
- Plugin architecture for clean separation
- Build tags for conditional compilation

## Important Operational Notes

### Authentication
- **Working correctly**: Uses hash-based lookup (API key → SHA256 → lookup hash → get key data)
- API keys stored with hash for security
- Support for Bearer tokens and X-API-Key header
- Admin scope validation implemented

### Rate Limiting Status
- **Models exist**: TokenBucket implementation in storage
- **Cache support exists**: CheckRateLimit method in cache manager
- **Missing**: HTTP middleware to enforce limits
- **To implement**: Add RateLimitMiddleware using cache.CheckRateLimit

### Ollama Integration
- **Fully implemented**: Dynamic model discovery
- **Enable with**: `STARPORT_ENABLE_OLLAMA=true`
- **Default URL**: http://localhost:11434
- **Features**: Streaming, embeddings, model metadata
- **Note**: Models appear with "ollama/" prefix

### Cache Architecture
- **Hybrid strategy**: Local + distributed caching
- **Smart invalidation**: Pub/sub for multi-node deployments
- **Streaming support**: Reconstructs responses from chunks
- **Headers**: X-Cache (HIT/MISS), X-Cache-Age

## Code Patterns

### Error Handling
```go
if err != nil {
    return fmt.Errorf("component: action failed: %w", err)
}
```

**Best Practices from Implementation:**
- Define sentinel errors as package-level variables (e.g., `var ErrNotFound = errors.New("key not found")`)
- Use constants for repeated strings to satisfy linters
- Avoid shadowing built-in identifiers (e.g., use `newValue` instead of `new`)
- For encryption: Always use crypto/rand, never math/rand
- Include validation methods on all data models
- Use regex for name validation to ensure clean data

### Logging
```go
log.Info().
    Str("component", "storage").
    Str("action", "initialize").
    Msg("initializing storage backend")
```

### Testing
```go
func TestComponentAction(t *testing.T) {
    // Arrange
    // Act  
    // Assert
}
```

### Configuration Pattern
```go
// Struct tags for env vars
type Config struct {
    Field string `env:"FIELD,default=value"`
}

// Validation method
func (c *Config) Validate() error {
    // Return descriptive errors
}
```

### Context Propagation
```go
// Always accept context as first parameter
func DoSomething(ctx context.Context, args...) error {
    // Pass context through the call chain
}
```

### Table-Driven Tests
```go
tests := []struct {
    name    string
    input   string
    want    string
    wantErr bool
}{
    // Test cases
}

for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        // Test implementation
    })
}
```

### Go Idioms and Conventions
```go
// Use Open() for connection/resource creation (not factory pattern)
func Open(config Config) (Resource, error) {
    // Following sql.Open, redis.Open conventions
}

// Use New() for simple struct creation
func NewMockStore() *MockStore {
    return &MockStore{
        data: make(map[string][]byte),
    }
}
```

**Key Learnings:**
- Prefer `Open()` over factory patterns for resource creation
- Use `time.Until()` instead of `time.Sub()` for duration calculations
- Convert if-else chains to switch statements when checking equality
- Use atomic file operations (write to temp, rename) to avoid partial reads
- **Lessons from P1-S3-3.2 (LLM Connectors):**
  - Adopt OpenRouter's `provider/model` naming convention from the start
  - Create base implementations for common patterns (e.g., OpenAICompatibleConnector)
  - Provider prefixes are part of the model ID and need to be stripped for provider-specific APIs
  - Mark unused parameters in mocks with `_ = param // param is intentionally unused in mock`
  - All providers support streaming, but not all support embeddings
  - Return appropriate errors for unsupported features rather than silent failures
  - Test both with and without provider prefixes to ensure compatibility
- **Lessons from P1-S3-3.3 (Proxy Endpoints):**
  - Implement both OpenAI (/v1) and OpenRouter (/api/v1) style endpoints
  - Transform model IDs to include provider prefix in responses
  - Connector initialization should be driven by configuration with API keys from environment
  - Fall back to mock connector when no providers are configured for development
  - SSE streaming requires proper headers and [DONE] marker
  - Request validation should happen before connector selection
- **Lessons from P1-S3-3.5 (Provider Routing):**
  - For binary applications, consolidate features into a single implementation rather than multiple options
  - All advanced routing features (latency tracking, cost optimization, sticky sessions) should be enabled by default
  - Use sensible defaults for configuration values (e.g., EMA alpha=0.2, circuit breaker threshold=3)
  - Provider health tracking needs Available field initialized to true
  - Circuit breaker needs default values when config fields are zero
  - Test coverage can be high (76%+) with comprehensive test suites
  - Sticky session cleanup is important for memory management
- **Lessons from P1-S3-3.6 (Provider Metadata):**
  - Avoid creating "enhanced" versions of endpoints - consolidate to single implementations
  - CORRECTION: /v1/models should return basic OpenAI format, /api/v1/models returns enhanced metadata
  - This maintains compatibility with OpenAI clients while providing enhanced data for OpenRouter clients
  - Use helper functions (like modelContextPtr) to handle pointer conversions cleanly
  - Provider metadata must follow OpenRouter's exact field names (may_log_prompts, not logging_policy)
  - URL decoding is needed for path parameters containing special characters (e.g., %2F for /)
  - Static metadata can achieve high test coverage (96%+) with table-driven tests
- **Lessons from P1-S3-3.7 (Dynamic Model Fetching):**
  - Use shared base implementations to avoid code duplication between similar connectors
  - Implement caching layer for API responses to reduce load and improve performance
  - Always provide fallback to static lists when dynamic fetching fails
  - Maintain backward compatibility when splitting providers (e.g., "gemini" -> "google-aistudio")
  - Thread-safe global caches need proper mutex protection
  - Consider making cache TTL configurable for different deployment scenarios
  - Separate connectors allow for provider-specific features (e.g., Vertex AI's Model Garden)
- **Lessons from P1-S4-4.1 (Provider Key Management):**
  - Make defensive copies of sensitive data (e.g., master keys) to ensure immutability
  - Use `defer func() { _ = resp.Body.Close() }()` to satisfy errcheck linter for deferred closes
  - Mark unused parameters with `_` when they're reserved for future use or interface compliance
  - Add package comments to explain partial implementations that will be integrated later
  - Implement zero-knowledge security by encrypting credentials at rest with per-API-key isolation
  - Use context.WithValue for optional behavior flags (e.g., "skip_validation" for testing)
  - Provider-specific validation should check both format and optionally verify with API calls
  - Table-driven tests can achieve high coverage (75%+) even for security-sensitive code
  - Create separate test files for different aspects (manager_test.go, validation_test.go, security_test.go)
  - Use nolint comments sparingly and with explanations for intentionally unused code
- **Lessons from P1-S4-4.2b (Valkey Storage Implementation):**
  - Import cycles can occur when packages have circular dependencies - move shared interfaces to the lower-level package
  - valkey-go API differs from standard Redis clients - always check the actual API documentation
  - Use client.Receive() for pub/sub subscriptions, not dedicated clients
  - MSET in valkey-go uses builder pattern: client.B().Mset().KeyValue(k1, v1).KeyValue(k2, v2).Build()
  - ClientOption fields are specific - not all standard Redis options exist (e.g., no MinIdleConns)
  - For batch operations without MSET support, use auto-pipelining with parallel Do() calls
  - PubSubMessage may not have all expected fields - check actual struct definition
  - Integration tests should use TEST_VALKEY_URL environment variable to skip when no instance available
  - Move PubSub interfaces to storage package to avoid circular dependencies with cache package
  - Use context.WithCancel for managing pub/sub subscription lifecycles
- **Lessons from Invalid Model Tracking Implementation:**
  - Add 404 detection at multiple layers (proxy, routing, cache) to catch all paths
  - Use catalog package for centralized invalid model tracking
  - Filter invalid models during GetModelsByProviderWithDynamic to prevent them from appearing in API
  - Mark models as invalid immediately when 404 is detected to prevent repeated failures
  - Add ResolveModelAlias to catalog for handling model aliases and canonical names
  - Integration with existing routing fallback logic is seamless
  - Test invalid model filtering comprehensively
  - Consider implementing streaming fallback support (currently only non-streaming has fallback)

## Commands to Remember

For comprehensive development commands and workflow, see [DEVELOPMENT.md](../DEVELOPMENT.md).

### Essential Commands

```bash
# Development
make dev              # Start with hot reload
make test             # Run tests
make check            # Run all checks

# API key management  
./starport keys create --name dev-key  # Create new API key
./starport keys list                   # List all keys
./starport keys revoke <key-id>        # Revoke a key

# Testing endpoints
curl http://localhost:3333/api/v1/models  # List all models
curl http://localhost:3333/health/live    # Health check

# Enable Ollama
export STARPORT_ENABLE_OLLAMA=true
export STARPORT_OLLAMA_BASE_URL=http://localhost:11434
```

## PR Checklist Template

```markdown
## PR Checklist
- [ ] Task ID in PR title and branch name
- [ ] Links to task in docs/TASKS.md
- [ ] Meets all acceptance criteria
- [ ] Tests added/updated (90% coverage)
- [ ] Documentation updated
- [ ] No linting errors
- [ ] Security considerations addressed
- [ ] Performance impact assessed
```

## Troubleshooting

### Common Issues
1. **Prerequisites not met**: Check docs/TASKS.md, update blockers section
2. **Merge conflicts**: Pull latest main, preserve both changes
3. **Tests failing**: Check if related to your changes or pre-existing
4. **Can't find files**: May need to create them per docs/ARCHITECTURE.md
5. **Gosec failures**: 
   - Check for false positives (e.g., G407 for crypto/rand usage)
   - Use `#nosec G###` with explanation for false positives
   - Run locally with `go install github.com/securego/gosec/v2/cmd/gosec@latest`
6. **Context file in PR**: Remove context-*.txt files before committing

### Getting Help
- Check existing PRs for similar implementations
- Reference docs/ARCHITECTURE.md for design decisions
- Update docs/TASKS.md blocked tasks table with specific blockers

## Quick Reference for Next Tasks

### For Storage Implementation (P1-S2-2.X)
- Storage interface should support both Badger and Valkey
- Use the existing config structs in `internal/config/config.go`
- Follow the context propagation pattern for all methods
- Include TTL support for rate limiting
- See docs/ARCHITECTURE.md Section 16 for storage details
- **Lessons from P1-S2-2.1:**
  - Mock implementation should be thread-safe with proper mutex usage
  - Handle TTL expiration checks in read operations
  - Transaction isolation is critical - avoid deadlocks by releasing locks before calling store methods
  - Use type-specific serialization helpers (e.g., SerializeInt64) for atomic operations
- **Lessons from P1-S2-2.3:**
  - Create models in `internal/models/` package for data structures
  - Use AES-256-GCM for encryption with crypto/rand for nonce generation
  - Gosec may report false positives for crypto/rand usage - use `#nosec` with explanation
  - Include validation methods on all models
  - Storage key helpers should follow consistent patterns (prefix:id format)
  - Test coverage >90% is achievable with table-driven tests

### For LLM Connector Implementation (P1-S3-3.X)
- Connectors go in `internal/connectors/` package
- Each provider gets its own file (openai.go, anthropic.go, etc.)
- Use the provider configs from `internal/config/config.go`
- Implement streaming support from the start
- See docs/ARCHITECTURE.md Section 8 for routing architecture
- **Lessons from P1-S3-3.1:**
  - Add package comment to satisfy linters (e.g., `// Package connectors provides interfaces and types for LLM provider integrations`)
  - For mock implementations, mark unused parameters with `_ = param // param is intentionally unused in mock`
  - Use simple switch statement in NewConnector() function instead of Java-style factory pattern - more idiomatic Go
  - Include retry logic helpers in error types (IsRetryable method)
  - Streaming interface should use io.EOF to signal completion
  - Provider config should include connection pooling and retry settings with exponential backoff
  - Test coverage >90% is achievable with comprehensive mock implementation
- **Lessons from P1-S3-3.2:**
  - All model IDs must use `provider/model` format for OpenRouter compatibility
  - Azure requires stripping the provider prefix when building deployment URLs
  - Create shared base implementations (e.g., OpenAICompatibleConnector) to reduce duplication
  - Not all providers support embeddings - return clear errors for unsupported features
  - Provider-specific error formats need custom parsing in handleError methods
  - Health checks should use prefixed model IDs
  - Static model lists should include update dates as comments

### For Testing
- Aim for 90% coverage on new code
- Use table-driven tests for multiple scenarios
- Mock external dependencies
- Test files go alongside implementation files
- Run `make test-coverage` to check coverage

## Questions?
- Architecture questions → Check docs/ARCHITECTURE.md
- Timeline questions → Check docs/PLAN.md  
- Task details → Check docs/TASKS.md
- Implementation patterns → Check existing code or ask in PR
- Execution order → Check docs/OPERATOR-GUIDE.md
- Current codebase status → Check this file's "Current Codebase Status" section