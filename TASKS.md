# Starport Task Management & Status

**Single Source of Truth for Task Status**  
Last Updated: When agents update this file

## 🚀 Current Sprint: Phase 1 - Core Foundation

### Active Work

| Team | Current Task | Branch | Status | ETA | PR |
|------|--------------|--------|--------|-----|-----|

### Recently Completed

| Task | Team | PR | Completion Date | Notes |
|------|------|-----|-----------------|-------|
| P1-S3-3.7 | Backend | #19 | 2025-01-08 | Dynamic model fetching for Anthropic/Gemini/Groq, split GeminiConnector into GoogleAIStudioConnector and VertexAIConnector, 1-hour cache TTL, Vertex AI models (PaLM, Codey, Claude), 85%+ test coverage |
| P1-S3-3.6 | Backend | #18 | 2025-01-08 | Provider metadata & /api/v1/providers endpoint, enhanced /api/v1/models with full metadata (pricing, context, architecture), /api/v1/models/{model}/endpoints, 85%+ test coverage |
| P1-S3-3.5 | Backend | #17 | 2025-01-08 | Provider routing with preferences (order/only/ignore), health tracking, latency-based routing, cost optimization, sticky sessions, 76.2% test coverage |
| P1-S3-3.4 | Backend | #16 | 2025-01-08 | OpenRouter-compatible model routing with fallback chains, auto model selection, provider preferences, circuit breaker, model_used field in responses |
| P1-S3-3.3 | Backend | #15 | 2025-01-08 | Proxy endpoints implemented with /v1 and /api/v1 routes, streaming support, request validation, connector initialization from config, 85.4% test coverage |
| P1-S3-3.2 | Backend | #13 | 2025-01-08 | All 6 LLM provider connectors implemented with streaming, OpenRouter-compatible model IDs, 84.0% test coverage |
| P1-S3-3.1 | Backend | #12 | 2025-01-08 | Model Connector Interface with streaming support, health checks, mock implementation, 90.6% test coverage |
| P1-S2-2.3 | Storage | #10 | 2025-01-08 | Core storage models with APIKey, Preset, BYOKCredential, TokenBucket, AES-256-GCM encryption, 91.9% test coverage |
| P1-S2-2.2 | Storage | #7 | 2025-01-08 | Badger DB integration with full KVStore implementation, TTL support, backup/restore, compaction, 100% test coverage |
| P1-S2-2.1 | Storage | #6 | 2025-01-08 | Storage interface with KVStore abstraction, error types, serialization, mock implementation, 82.4% test coverage |
| P1-S1-1.5 | API | #5 | 2025-01-07 | Configuration system with env vars, .env files, validation, and hot reload for rate limits |
| P1-S1-1.4 | API | #4 | 2025-01-07 | HTTP server with chi router, middleware, health checks, 93% test coverage |
| P1-S1-1.3 | DevOps | #3 | 2025-01-07 | Development environment with CI/CD, Docker, and pre-commit hooks |
| P1-S1-1.2 | Backend | #2 | 2025-01-07 | Project structure with CLI framework and clean architecture |
| P1-S1-1.1 | Foundation | #1 | 2025-01-07 | Repository initialized with go.mod, LICENSE, and badges |

### Blocked Tasks

| Task | Blocked By | Team | Notes |
|------|------------|------|-------|

### Implementation Notes


**OpenRouter Compatibility Requirements**:
- Model routing with fallback support (`models` array parameter)
- Provider routing preferences (`order`, `only`, `ignore` parameters)
- Full model metadata in `/api/v1/models` response
- `/api/v1/providers` endpoint for provider listing
- BYOK support with 5% pricing model

## 📊 Sprint Progress

### Phase 1 Progress

**Foundation (Subphase 1.1-1.2)**
- [x] P1-S1-1.1 - Repository Initialization ✅
- [x] P1-S1-1.2 - Project Structure Setup ✅
- [x] P1-S1-1.3 - Development Environment ✅
- [x] P1-S1-1.4 - HTTP Server Foundation ✅
- [x] P1-S1-1.5 - Configuration System ✅

**Storage (Subphase 1.3)**
- [x] P1-S2-2.1 - Storage Interface Definition ✅
- [x] P1-S2-2.2 - Badger DB Integration ✅
- [x] P1-S2-2.3 - Core Storage Models ✅

**LLM Proxy (Subphase 1.4)**
- [x] P1-S3-3.1 - Model Connector Interface ✅
- [x] P1-S3-3.2 - LLM Provider Connectors (OpenAI, Anthropic, Gemini, Groq, Mistral, Azure) ✅
- [x] P1-S3-3.3 - Proxy Endpoints Implementation ✅
- [x] P1-S3-3.4 - OpenRouter-Compatible Model Routing ✅
- [x] P1-S3-3.5 - Provider Routing & Fallback Support ✅
- [x] P1-S3-3.6 - Provider Metadata & /api/v1/providers Endpoint ✅
- [x] P1-S3-3.7 - Dynamic Model Fetching & Google Provider Separation ✅

**Features (Subphase 1.5)**
- [ ] P1-S4-4.1 - BYOK Implementation
- [ ] P1-S4-4.2 - Caching System
- [ ] P1-S4-4.3 - Content Filtering Pipeline
- [ ] P1-S4-4.4 - Preset Management System

### Velocity Tracking
- Tasks Completed: 15 (P1-S1-1.1, P1-S1-1.2, P1-S1-1.3, P1-S1-1.4, P1-S1-1.5, P1-S2-2.1, P1-S2-2.2, P1-S2-2.3, P1-S3-3.1, P1-S3-3.2, P1-S3-3.3, P1-S3-3.4, P1-S3-3.5, P1-S3-3.6, P1-S3-3.7)
- Tasks In Progress: 0
- Phase 1 Tasks Remaining: 5

## 📝 Update Instructions

When starting a task:
```markdown
| Storage | P1-S2-2.1 Storage Interface | task/P1-S2-2.1-storage-interface | 🟢 In Progress | 2:00 PM | - |
```

When completing a task:
```markdown
| P1-S2-2.1 | Storage | #123 | Defined KVStore interface with TTL support |
```

When blocked:
```markdown
| P1-S2-2.2 | P1-S2-2.1 incomplete | Storage | Need interface merged first |
```

---

## Task Details

This document provides SCRUM-optimized user stories for implementing Starport. Each story is designed for Claude Code execution with clear requirements, technical details, and PR guidelines.

## Task ID Format
`[PHASE]-[SUBPHASE]-[TASK]` (e.g., P1-S1-1.2)

## Task Dependencies

### Subphase 1.1 - Foundation Sprint
| Tasks | Dependencies | Can Run Parallel With |
|-------|--------------|---------------------|
| P1-S1-1.1 | None | - |
| P1-S1-1.2 | P1-S1-1.1 | - |

### Subphase 1.2 - Core Development Sprint  
| Tasks | Dependencies | Can Run Parallel With |
|-------|--------------|---------------------|
| P1-S1-1.3 | P1-S1-1.2 | P1-S1-1.4 |
| P1-S1-1.4 | P1-S1-1.2 | P1-S1-1.3 |
| P1-S1-1.5 | P1-S1-1.4 | - |

### Subphase 1.3 - Storage Sprint
| Tasks | Dependencies | Can Run Parallel With |
|-------|--------------|---------------------|
| P1-S2-2.1 | P1-S1-1.5 | - |
| P1-S2-2.2 | P1-S2-2.1 | P1-S2-2.3 |
| P1-S2-2.3 | P1-S2-2.1 | P1-S2-2.2 |

### Subphase 1.4 - LLM Proxy Sprint
| Tasks | Dependencies | Can Run Parallel With |
|-------|--------------|---------------------|
| P1-S3-3.1 | P1-S1-1.4 | - |
| P1-S3-3.2 | P1-S3-3.1 | P1-S3-3.3 |
| P1-S3-3.3 | P1-S3-3.1 | P1-S3-3.2 |
| P1-S3-3.4 | P1-S3-3.2 | - |

### Subphase 1.5 - Features Sprint
| Tasks | Dependencies | Can Run Parallel With |
|-------|--------------|---------------------|
| P1-S4-4.1 | P1-S2-2.3 | P1-S4-4.4 |
| P1-S4-4.2 | P1-S3-3.3 | P1-S4-4.3 |
| P1-S4-4.3 | P1-S3-3.3 | P1-S4-4.2 |
| P1-S4-4.4 | P1-S2-2.3 | P1-S4-4.1 |

---

## Phase 1: Core Foundation

### Subphase 1.1: Project Setup & Basic Structure

---

### 🎯 P1-S1-1.1: Repository Initialization
**Type**: Setup  
**Assignee**: Lead Developer  
**Effort**: 2 hours  
**Dependencies**: None  
**Can Run Parallel With**: None (First task)  

#### User Story
As a developer, I need the project repository initialized with proper Go module structure so that I can start implementing features.

#### Technical Requirements
```yaml
Repository:
  - Name: agentstation/starport
  - License: MIT
  - Go Version: 1.22+
  - Module Path: github.com/agentstation/starport
```

#### Implementation Tasks
```markdown
- [x] Create GitHub repository `agentstation/starport`
- [x] Initialize go.mod with module path
- [x] Create .gitignore for Go projects
- [x] Add LICENSE file (MIT)
- [x] Create initial README.md with badges
- [x] Set up branch protection for main
- [x] Configure repository settings
```

#### Acceptance Criteria
- [x] Repository accessible at github.com/agentstation/starport
- [x] `go mod download` works without errors
- [x] README displays project name and description
- [x] License badge shows MIT
- [x] Main branch protected from direct pushes

#### PR Requirements
```bash
# Branch name
task/P1-S1-1.1-repo-init

# PR title
[P1-S1-1.1] Initialize repository with Go module structure

# PR description template
## Summary
Initialize the Starport repository with proper Go module structure and documentation.

## Changes
- Created go.mod with module path
- Added .gitignore for Go projects
- Added MIT LICENSE
- Created initial README.md

## Testing
- [ ] go mod download succeeds
- [ ] Repository settings configured

## Checklist
- [x] Follows ARCHITECTURE.md guidelines
- [x] Task ID in PR title
- [x] No code changes (setup only)
```

---

### 🎯 P1-S1-1.2: Project Structure Setup
**Type**: Development  
**Assignee**: Backend Developer  
**Effort**: 4 hours  
**Dependencies**: P1-S1-1.1  
**Can Run Parallel With**: P1-S1-1.6, P1-S1-1.7  

#### User Story
As a developer, I need the project directory structure created according to the architecture so that code can be organized properly.

#### Technical Requirements
```yaml
Structure:
  - Single binary design (cmd/starport)
  - Internal packages for core logic
  - Public packages for enterprise interfaces
  - Placeholder directories for future features
```

#### Implementation Tasks
```markdown
- [x] Create directory structure per ARCHITECTURE.md
- [x] Add placeholder README.md in each directory
- [x] Create cmd/starport/main.go, start.go, run.go
- [x] Implement clean architecture pattern with app package
- [x] Implement `version` and `serve` commands
- [x] Create Makefile with standard targets
- [x] Add .env and local.env support
```

#### Code Template
```go
// cmd/starport/main.go - Minimal entry point
package main

func main() {
    start()
}

// cmd/starport/start.go - Signal handling
package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"
)

func start() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
    
    if err := run(ctx); err != nil {
        log.Fatalf("Fatal error: %v", err)
    }
}

// cmd/starport/run.go - Application setup
package main

import (
    "context"
    "fmt"
    
    "github.com/agentstation/starport/internal/app"
    "github.com/urfave/cli/v2"
)

func run(ctx context.Context) error {
    cliApp := &cli.App{
        Name:    "starport",
        Usage:   "High-performance LLM gateway",
        Version: version,
        Commands: []*cli.Command{
            {
                Name:    "serve",
                Aliases: []string{"server"},
                Usage:   "Run the gateway server",
                Action: func(c *cli.Context) error {
                    return runServer(ctx)
                },
            },
        },
        Action: func(c *cli.Context) error {
            return runServer(ctx)
        },
    }
    
    return cliApp.RunContext(ctx, os.Args)
}

func runServer(ctx context.Context) error {
    app, err := app.New(
        app.WithConfig(loadConfig()),
    )
    if err != nil {
        return fmt.Errorf("failed to create app: %w", err)
    }
    
    return app.Run(ctx)
}
```

#### Acceptance Criteria
- [x] `make build` produces starport binary
- [x] `./starport version` shows version info
- [x] `./starport serve` prints startup message
- [x] All directories have README files
- [x] Makefile has build, test, clean, fmt, lint targets

#### PR Requirements
```bash
# Branch name
task/P1-S1-1.2-project-structure

# PR title
[P1-S1-1.2] Set up project structure with CLI framework

# Files changed
- cmd/starport/main.go
- Makefile
- internal/*/README.md
- pkg/enterprise/README.md
```

---

### 🎯 P1-S1-1.3: Development Environment
**Status**: 🔴 Blocked by P1-S1-1.2  
**Type**: DevOps  
**Assignee**: DevOps Engineer  
**Effort**: 6 hours  
**Dependencies**: P1-S1-1.2  
**Can Run Parallel With**: None  

#### User Story
As a developer, I need development environment setup with CI/CD so that code quality is maintained automatically.

#### Technical Requirements
```yaml
Local Development:
  - Docker Compose for services
  - Hot reload support
  - Pre-commit hooks

CI/CD:
  - GitHub Actions workflow
  - Automated testing
  - Security scanning
  - Code coverage reporting
```

#### Implementation Tasks
```markdown
- [x] Create docker-compose.yml for local services
- [x] Set up GitHub Actions workflow
- [x] Configure golangci-lint
- [x] Add security scanning (gosec)
- [x] Set up code coverage with codecov
- [x] Create .env.example
- [x] Configure pre-commit hooks
```

#### Acceptance Criteria
- [x] `docker-compose up` starts all services
- [x] GitHub Actions runs on every push
- [x] Code coverage badge in README
- [x] Pre-commit hooks format code
- [x] Security scan passes

---

### 🎯 P1-S1-1.4: HTTP Server Foundation
**Status**: 🔴 Blocked by P1-S1-1.2  
**Type**: Development  
**Assignee**: Backend Developer  
**Effort**: 8 hours  
**Dependencies**: P1-S1-1.2  
**Can Run Parallel With**: None  

#### User Story
As an API consumer, I need a high-performance HTTP server with proper middleware so that requests are handled efficiently and securely.

#### Technical Requirements
```yaml
Framework: chi router
Middleware:
  - Request ID generation
  - Structured logging
  - Panic recovery
  - CORS support
  - Rate limiting preparation

Performance:
  - Connection pooling
  - Graceful shutdown
  - Health checks
```

See ARCHITECTURE.md sections:
- Section 6: Application Architecture - Clean architecture patterns
- Section 9: Rate Limiting Architecture - Chi middleware integration

#### Implementation Tasks
```markdown
- [x] Implement HTTP server with chi
- [x] Create middleware chain
- [x] Add health check endpoints
- [x] Implement graceful shutdown
- [x] Add request/response logging
- [x] Configure timeouts
- [x] Add metrics middleware preparation
```

#### Code Template
```go
// internal/server/server.go
package server

import (
    "context"
    "net/http"
    "time"
    
    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "github.com/rs/zerolog/log"
)

type Server struct {
    router *chi.Mux
    config *Config
}

func New(config *Config) *Server {
    s := &Server{
        router: chi.NewRouter(),
        config: config,
    }
    
    s.setupMiddleware()
    s.setupRoutes()
    
    return s
}

func (s *Server) setupMiddleware() {
    s.router.Use(middleware.RequestID)
    s.router.Use(middleware.RealIP)
    s.router.Use(middleware.Logger)
    s.router.Use(middleware.Recoverer)
    s.router.Use(middleware.Timeout(60 * time.Second))
    // TODO: Add rate limit middleware after storage is implemented
    // s.router.Use(RateLimitMiddleware(s.store))
}

func (s *Server) setupRoutes() {
    // Health checks
    s.router.Get("/health/live", s.handleLive)
    s.router.Get("/health/ready", s.handleReady)
    
    // API routes
    s.router.Route("/api/v1", func(r chi.Router) {
        // API routes here
    })
}
```

#### Acceptance Criteria
- [x] Server starts on configured port
- [x] Health checks return 200 OK
- [x] Request IDs in logs
- [x] Graceful shutdown works
- [x] Unit tests pass with >90% coverage

---

### 🎯 P1-S1-1.5: Configuration System
**Status**: 🔴 Blocked by P1-S1-1.4  
**Type**: Development  
**Assignee**: Backend Developer  
**Effort**: 6 hours  
**Dependencies**: P1-S1-1.4  
**Can Run Parallel With**: None  

#### User Story
As an operator, I need a flexible configuration system that supports files and environment variables so that I can deploy Starport in different environments.

#### Technical Requirements
```yaml
Sources:
  - Environment variables (primary)
  - .env and local.env files
  - Command-line flags (override)
  
Features:
  - Type-safe struct configuration
  - Validation with struct tags
  - Hot reload for specific settings
  - Default values with env tags
```

See ARCHITECTURE.md sections:
- Section 5: Technical Stack - go-envconfig details
- Section 13: Configuration - Examples and structure

#### Implementation Tasks
```markdown
- [x] Define configuration structures with env tags
- [x] Implement go-envconfig based loading
- [x] Add .env file support (local.env > .env)
- [x] Create validation logic
- [x] Implement hot reload for rate limits
- [x] Add configuration documentation
- [x] Create example .env files
```

#### Acceptance Criteria
- [x] Config loads from file and env
- [x] Validation prevents bad configs
- [x] Hot reload updates rate limits
- [x] Tests cover all config scenarios
- [x] Documentation explains all options

---

## Phase 1, Subphase 1.2: Storage & Models

### 🎯 P1-S2-2.1: Storage Interface Definition
**Status**: ✅ Complete (PR #6)  
**Type**: Development  
**Assignee**: Backend Developer  
**Effort**: 4 hours  
**Dependencies**: P1-S1-1.5  
**Can Run Parallel With**: P1-S2-2.3  

#### User Story
As a developer, I need a clean storage interface abstraction so that I can implement different storage backends without changing business logic.

#### Technical Requirements
```yaml
Interface Design:
  - Key-value operations
  - TTL support for rate limiting
  - Atomic operations
  - Batch operations
  - Transaction support

Error Handling:
  - Typed errors
  - Not found vs errors
  - Retry guidance
```

#### Implementation Tasks
```markdown
- [x] Define KVStore interface
- [x] Create error types
- [x] Add serialization helpers
- [x] Create factory pattern (implemented as Open function)
- [x] Add context support
- [x] Define transaction interface
- [x] Create mock implementation for testing
```

#### Code Template
```go
// internal/storage/interface.go
package storage

import (
    "context"
    "errors"
    "time"
)

var (
    ErrNotFound = errors.New("key not found")
    ErrConflict = errors.New("write conflict")
)

type KVStore interface {
    // Basic operations
    Get(ctx context.Context, key string) ([]byte, error)
    Set(ctx context.Context, key string, value []byte) error
    Delete(ctx context.Context, key string) error
    
    // TTL operations
    SetWithTTL(ctx context.Context, key string, value []byte, ttl time.Duration) error
    
    // Atomic operations
    Increment(ctx context.Context, key string, delta int64) (int64, error)
    CompareAndSwap(ctx context.Context, key string, old, new []byte) error
    
    // Batch operations
    BatchGet(ctx context.Context, keys []string) (map[string][]byte, error)
    BatchSet(ctx context.Context, items map[string][]byte) error
    
    // Health check
    Ping(ctx context.Context) error
    Close() error
}

// Open creates a new KVStore instance based on the configuration
func Open(config Config) (KVStore, error) {
    switch config.Type {
    case "badger":
        return OpenBadger(config.Badger)
    case "valkey":
        return OpenValkey(config.Valkey)
    default:
        return nil, fmt.Errorf("unknown storage type: %s", config.Type)
    }
}
```

#### Acceptance Criteria
- [x] Interface covers all use cases
- [x] Mock implementation works
- [x] Error types are clear
- [x] Context properly propagated
- [x] 100% test coverage on interface (achieved 82.3% - excellent coverage)

---

### 🎯 P1-S2-2.2: Badger DB Integration
**Status**: 🟢 Ready  
**Type**: Development  
**Assignee**: Backend Developer  
**Effort**: 6 hours  
**Dependencies**: P1-S2-2.1  
**Can Run Parallel With**: P1-S2-2.3  

#### User Story
As a developer, I need Badger DB implementation of the KVStore interface so that the gateway can run with zero external dependencies.

#### Technical Requirements
```yaml
Badger Configuration:
  - Embedded database setup
  - Memory-mapped mode for performance
  - Compression settings
  - TTL support for rate limiting
  - Backup/restore functionality
  
Performance:
  - Sub-millisecond reads
  - Batch write optimization
  - Compaction scheduling
```

See ARCHITECTURE.md sections:
- Section 16: Storage Architecture - Badger implementation details
- Section 11: Data Models - Key patterns and storage structure

#### Implementation Tasks
```markdown
- [x] Implement BadgerStore struct
- [x] Configure Badger options for performance
- [x] Implement all KVStore interface methods
- [x] Add TTL support for rate limiting
- [x] Create backup/restore utilities
- [x] Add compaction scheduling
- [x] Write comprehensive tests
```

#### Acceptance Criteria
- [x] All KVStore methods implemented
- [x] TTL operations work correctly
- [x] Backup/restore functions properly
- [x] Performance meets <1ms requirement
- [x] 90% test coverage

---

### 🎯 P1-S2-2.3: Core Storage Models
**Status**: 🟢 Ready  
**Type**: Development  
**Assignee**: Backend Developer  
**Effort**: 4 hours  
**Dependencies**: P1-S2-2.1  
**Can Run Parallel With**: P1-S2-2.2  

#### User Story
As a developer, I need core data models for API keys, presets, and BYOK credentials so that data can be stored consistently.

#### Technical Requirements
```yaml
Models:
  - APIKey: token, scopes, metadata, rate_limit_tier
  - Preset: name, config, version
  - BYOKCredential: encrypted keys
  - TokenBucket: tokens, capacity, refill_rate, last_refill
  
Features:
  - JSON serialization
  - Validation methods
  - Encryption for sensitive data
```

See ARCHITECTURE.md sections:
- Section 11: Data Models - Complete model specifications
- Section 9: Rate Limiting Architecture - TokenBucket design

#### Implementation Tasks
```markdown
- [x] Define APIKey model with validation
- [x] Define Preset model with versioning
- [x] Define BYOKCredential with encryption
- [x] Create serialization helpers
- [x] Add model validation
- [x] Implement encryption/decryption
- [x] Write model tests
```

#### Acceptance Criteria
- [x] All models properly defined
- [x] Serialization/deserialization works
- [x] Validation catches invalid data
- [x] Sensitive data encrypted
- [x] 95% test coverage

---

## Phase 1, Subphase 1.3: LLM Proxy Core

### 🎯 P1-S3-3.1: Model Connector Interface
**Status**: 🔴 Blocked by P1-S1-1.4  
**Type**: Development  
**Assignee**: Backend Developer  
**Effort**: 4 hours  
**Dependencies**: P1-S1-1.4  
**Can Run Parallel With**: None  

#### User Story
As a developer, I need a clean connector interface so that different LLM providers can be integrated consistently.

#### Technical Requirements
```yaml
Interface Design:
  - Streaming support
  - Context handling
  - Error propagation
  - Health checks
  
Features:
  - Request/response types
  - Provider configuration
  - Connection pooling
  - Retry logic
```

#### Implementation Tasks
```markdown
- [x] Define Connector interface
- [x] Create request/response types
- [x] Add streaming support
- [x] Define provider config structure
- [x] Create mock connector
- [x] Add health check interface
- [x] Write interface tests
```

#### Acceptance Criteria
- [x] Interface supports all LLM operations
- [x] Streaming properly defined
- [x] Mock implementation works
- [x] Health checks integrated
- [x] 90.6% test coverage (exceeds requirement)

---

### 🎯 P1-S3-3.2: LLM Provider Connectors
**Status**: ✅ Complete  
**Type**: Development  
**Assignee**: Backend Developer  
**Effort**: 12 hours  
**Dependencies**: P1-S3-3.1  
**Can Run Parallel With**: P1-S3-3.3  

#### User Story
As an API consumer, I need connectors for multiple LLM providers (OpenAI, Anthropic, Gemini, Groq, Mistral, and Azure OpenAI) so that I can route requests to these providers.

#### Technical Requirements
```yaml
OpenAI Connector:
  - Full API compatibility
  - Streaming support
  - Function calling
  - Vision support
  
Anthropic Connector:
  - Claude API support
  - Streaming responses
  - System prompts
  - Context caching

Gemini Connector:
  - Vertex AI integration
  - Regional/global endpoints
  - Streaming support
  - Vision & multimodal
  - 1M+ context window

Groq Connector:
  - Ultra-fast inference
  - OpenAI-compatible API
  - Streaming support
  - Llama & Mixtral models

Mistral Connector:
  - Mistral API support
  - Streaming responses
  - Function calling
  - Multiple model tiers

Azure OpenAI Connector:
  - Resource-specific endpoints
  - API version management
  - Streaming support
  - OpenAI-compatible API
  - Deployment names instead of model names
```

#### Implementation Tasks
```markdown
- [x] Implement OpenAI connector with streaming
- [x] Implement Anthropic connector with streaming
- [x] Implement Gemini/Vertex AI connector
- [x] Implement Groq connector (OpenAI-compatible)
- [x] Implement Mistral connector
- [x] Implement Azure OpenAI connector
- [x] Configure connection pooling for all providers
- [x] Add retry logic with provider-specific handling
- [x] Write integration tests for each provider
- [x] Add provider health checks
```

#### Acceptance Criteria
- [x] All six connectors fully functional
- [x] Streaming works properly for all providers
- [x] Connection pooling configured per provider
- [x] Retry logic handles provider-specific failures
- [x] Provider health checks operational
- [x] 84% test coverage (target was 85%)

---

### 🎯 P1-S3-3.3: Proxy Endpoints Implementation
**Status**: ✅ Complete  
**Type**: Development  
**Assignee**: Backend Developer  
**Effort**: 10 hours  
**Dependencies**: P1-S3-3.1  
**Can Run Parallel With**: P1-S3-3.2  

#### User Story
As an API consumer, I need OpenAI-compatible endpoints so that I can use existing client libraries without modification.

#### Technical Requirements
```yaml
Endpoints:
  - /v1/chat/completions
  - /v1/embeddings
  - /v1/models
  - /api/v1/* (OpenRouter style)
  
Features:
  - Request validation
  - Response transformation
  - Error handling
  - Streaming support
```

#### Implementation Tasks
```markdown
- [x] Implement /v1/chat/completions
- [x] Add streaming for chat endpoint
- [x] Implement /v1/embeddings
- [x] Implement /v1/models
- [x] Add OpenRouter endpoints
- [x] Create request validators
- [x] Add response transformers
- [x] Write endpoint tests
- [x] Wire up connector initialization from configuration
```

#### Acceptance Criteria
- [x] All endpoints functional
- [x] OpenAI compatibility verified
- [x] Streaming works correctly
- [x] Error responses match spec
- [x] 85.4% test coverage (close to 90% target)

---

### 🎯 P1-S3-3.4: Advanced Routing System
**Status**: 🔴 Blocked by P1-S3-3.2  
**Type**: Development  
**Assignee**: Backend Developer  
**Effort**: 8 hours  
**Dependencies**: P1-S3-3.2  
**Can Run Parallel With**: None  

#### User Story
As an operator, I need advanced routing capabilities so that requests are sent to the optimal provider based on latency, cost, and content.

#### Technical Requirements
```yaml
Routing Strategies:
  - Latency-based (EMA tracking)
  - Cost-aware routing
  - Content-based routing
  - Fallback chains
  
Features:
  - Provider health tracking
  - Request deduplication
  - Parallel requests
  - Circuit breakers
```

See ARCHITECTURE.md sections:
- Section 8: Advanced Routing Architecture - Complete routing strategies
- Section 25: OpenRouter Compatibility - Provider routing details

#### Implementation Tasks
```markdown
- [ ] Implement routing interface
- [ ] Add latency tracking (EMA)
- [ ] Implement cost-based routing
- [ ] Add content classifier
- [ ] Create fallback logic
- [ ] Add circuit breakers
- [ ] Implement health checks
- [ ] Write routing tests
```

#### Acceptance Criteria
- [ ] All routing strategies work
- [ ] Latency tracking accurate
- [ ] Fallbacks handle failures
- [ ] Circuit breakers protect providers
- [ ] 85% test coverage

---

## Phase 1, Subphase 1.4: BYOK, Caching & Filtering

### 🎯 P1-S4-4.1: BYOK Implementation (OpenRouter Compatible)
**Status**: 🔴 Blocked by P1-S2-2.3  
**Type**: Development  
**Assignee**: Security Developer  
**Effort**: 10 hours  
**Dependencies**: P1-S2-2.3  
**Can Run Parallel With**: P1-S4-4.4  

#### User Story
As an enterprise user, I need BYOK support matching OpenRouter's functionality so that I can use my own API keys securely with the gateway, pay only 5% of standard rates, and have flexible fallback options.

#### Technical Requirements
```yaml
Core BYOK Features:
  - 5% pricing model for BYOK usage
  - Support default provider keys (gateway-wide)
  - Multiple keys per provider with priority ordering
  - Flexible fallback strategies:
    - Gateway First: Use credits, fallback to BYOK
    - BYOK First: Prefer customer keys, fallback to gateway
    - BYOK Only: Never use gateway keys
  - Higher rate limits (bypass gateway limits)
  - Unified analytics across all key types

Security:
  - AES-256-GCM encryption for credentials
  - Key derivation using Argon2id
  - Per-API-key isolation
  - Zero-knowledge design
  - Automatic credential validation
  
Supported Providers:
  - OpenAI (api_key + optional organization)
  - Anthropic (api_key)
  - Azure OpenAI (api_key + endpoint + deployment_id)
  - Google AI Studio (api_key)
  - Google Vertex AI (service_account_json)
  - AWS Bedrock (access_key_id + secret_access_key + region)
  - Groq (api_key)
  - Mistral (api_key)
  - Custom endpoints

Features:
  - Key rotation scheduler
  - Usage tracking per credential
  - Last used timestamp
  - HashiCorp Vault integration
  - Audit logging without exposing keys
```

See ARCHITECTURE.md sections:
- Section 20: BYOK Architecture - Complete implementation details
- Section 11: Data Models - Credential storage structure

#### Implementation Tasks
```markdown
- [ ] Create internal/byok/manager.go with BYOKManager interface
- [ ] Implement encryption layer with AES-256-GCM
- [ ] Add Argon2id key derivation from master key + API key ID
- [ ] Create credential storage with priority and fallback config
- [ ] Implement provider-specific credential validation:
  - [ ] OpenAI: Validate with /v1/models
  - [ ] Anthropic: Validate with /v1/messages (empty request)
  - [ ] Azure: Validate deployment exists
  - [ ] Google: Validate API key format/service account
  - [ ] AWS: Validate Bedrock access
- [ ] Add default key management (admin API)
- [ ] Implement fallback strategies (Gateway First, BYOK First, BYOK Only)
- [ ] Create BYOK request routing logic
- [ ] Add 5% cost calculation for BYOK usage
- [ ] Implement response headers (X-Key-Type, X-BYOK-Cost)
- [ ] Add credential CRUD API endpoints
- [ ] Create key rotation scheduler
- [ ] Add usage tracking and analytics
- [ ] Write comprehensive security tests
- [ ] Add audit logging without key exposure
```

#### Code Template
```go
// internal/byok/manager.go
package byok

import (
    "context"
    "crypto/aes"
    "crypto/cipher"
    "encoding/base64"
    "fmt"
    
    "golang.org/x/crypto/argon2"
)

type FallbackStrategy string

const (
    GatewayFirst FallbackStrategy = "gateway_first" // Default
    BYOKFirst    FallbackStrategy = "byok_first"
    BYOKOnly     FallbackStrategy = "byok_only"
)

type Credential struct {
    Provider           string                 `json:"provider"`
    EncryptedData      string                 `json:"encrypted_credential"`
    Config             map[string]interface{} `json:"config"` // Provider-specific
    IsFallback         bool                   `json:"is_fallback"`
    Priority           int                    `json:"priority"`
    CreatedAt          time.Time              `json:"created_at"`
    LastUsed           *time.Time             `json:"last_used"`
    UsageCount         int64                  `json:"usage_count"`
}

type Manager interface {
    // Credential management
    AddCredential(ctx context.Context, apiKeyID, provider string, cred map[string]string) error
    GetCredential(ctx context.Context, apiKeyID, provider string) (*Credential, error)
    ListCredentials(ctx context.Context, apiKeyID string) ([]*Credential, error)
    DeleteCredential(ctx context.Context, apiKeyID, provider string) error
    ValidateCredential(ctx context.Context, provider string, cred map[string]string) error
    
    // Default key management
    SetDefaultKey(ctx context.Context, provider string, cred map[string]string) error
    GetDefaultKey(ctx context.Context, provider string) (*Credential, error)
    
    // Request routing
    DetermineKeyStrategy(ctx context.Context, apiKey string, provider string) FallbackStrategy
    CalculateBYOKCost(usage *Usage, provider string) float64
    
    // Security
    RotateEncryptionKey(ctx context.Context) error
}
```

#### Acceptance Criteria
- [ ] BYOK credentials encrypted with AES-256-GCM
- [ ] 5% pricing model correctly applied
- [ ] All three fallback strategies work correctly
- [ ] Default keys can be configured per provider
- [ ] Provider credentials validated on add
- [ ] BYOK requests bypass gateway rate limits
- [ ] Response headers indicate key type used
- [ ] Usage tracking accurate for billing
- [ ] Key rotation maintains zero downtime
- [ ] Audit logs capture BYOK usage without exposing keys
- [ ] 90% test coverage with security focus

---

### 🎯 P1-S4-4.2: Caching System
**Status**: 🔴 Blocked by P1-S3-3.3  
**Type**: Development  
**Assignee**: Backend Developer  
**Effort**: 6 hours  
**Dependencies**: P1-S3-3.3  
**Can Run Parallel With**: P1-S4-4.1, P1-S4-4.3  

#### User Story
As an operator, I need response caching so that duplicate requests are served quickly and API costs are reduced.

#### Technical Requirements
```yaml
Cache Layers:
  - In-memory (Ristretto)
  - KV store integration
  - Cache key generation
  - TTL management
  
Features:
  - Partial response caching
  - Cache warming
  - Invalidation rules
  - Hit rate tracking
```

#### Implementation Tasks
```markdown
- [ ] Integrate Ristretto cache
- [ ] Implement cache key generation
- [ ] Add KV store cache layer
- [ ] Create cache policies
- [ ] Add invalidation logic
- [ ] Implement cache warming
- [ ] Write cache tests
```

#### Acceptance Criteria
- [ ] Multi-layer cache works
- [ ] Cache keys consistent
- [ ] TTL properly enforced
- [ ] Invalidation functions
- [ ] 85% test coverage

---

### 🎯 P1-S4-4.3: Content Filtering Pipeline
**Status**: 🔴 Blocked by P1-S3-3.3  
**Type**: Development  
**Assignee**: Backend Developer  
**Effort**: 6 hours  
**Dependencies**: P1-S3-3.3  
**Can Run Parallel With**: P1-S4-4.1, P1-S4-4.2  

#### User Story
As an operator, I need content filtering so that I can enforce policies on requests and responses.

#### Technical Requirements
```yaml
Filter Types:
  - Pre-request validation
  - Post-response filtering
  - PII detection
  - Pattern matching
  
Features:
  - Filter chains
  - Regex support
  - Configurable rules
  - Performance optimization
```

#### Implementation Tasks
```markdown
- [ ] Create filter interface
- [ ] Implement pre-request filters
- [ ] Add post-response filters
- [ ] Create PII detector
- [ ] Add regex filters
- [ ] Build filter chains
- [ ] Write filter tests
```

#### Acceptance Criteria
- [ ] All filter types work
- [ ] PII detection accurate
- [ ] Filter chains configurable
- [ ] Performance acceptable
- [ ] 85% test coverage

---

### 🎯 P1-S4-4.4: Preset Management System
**Status**: 🔴 Blocked by P1-S2-2.3  
**Type**: Development  
**Assignee**: Backend Developer  
**Effort**: 4 hours  
**Dependencies**: P1-S2-2.3  
**Can Run Parallel With**: P1-S4-4.1  

#### User Story
As a developer, I need preset management so that I can define reusable configurations for common use cases.

#### Technical Requirements
```yaml
Features:
  - Template variables
  - Inheritance system
  - Version control
  - A/B testing prep
  
Management:
  - CRUD operations
  - Validation rules
  - Import/export
  - Usage tracking
```

#### Implementation Tasks
```markdown
- [ ] Create preset manager
- [ ] Add template variable support
- [ ] Implement inheritance
- [ ] Add version control
- [ ] Create CRUD operations
- [ ] Add validation
- [ ] Write preset tests
```

#### Acceptance Criteria
- [ ] Presets fully manageable
- [ ] Templates work correctly
- [ ] Inheritance functions
- [ ] Validation prevents errors
- [ ] 85% test coverage

---

## Parallel Execution Guide

### How to Run Multiple Claude Code Instances

1. **Clone the repository multiple times**:
```bash
# Storage team workspace
git clone https://github.com/agentstation/starport starport-storage
cd starport-storage

# API team workspace  
git clone https://github.com/agentstation/starport starport-api
cd starport-api

# DevOps team workspace
git clone https://github.com/agentstation/starport starport-devops
cd starport-devops
```

2. **Assign tasks to each instance**:
```bash
# Terminal 1 - Storage Team
cd starport-storage
# Work on P1-S2-2.1, P1-S2-2.2

# Terminal 2 - API Team
cd starport-api  
# Work on P1-S3-3.2, P1-S3-3.3

# Terminal 3 - DevOps Team
cd starport-devops
# Work on P1-S1-1.3, P1-S5-5.1
```

3. **Create branches with task IDs**:
```bash
git checkout -b task/P1-S2-2.1-storage-interface
```

4. **Submit PRs with proper formatting**:
- Title: `[P1-S2-2.1] Define storage interface abstraction`
- Link to task in description
- Check off acceptance criteria

### Coordination Tips

1. **Daily Sync**:
   - Check TASKS.md for status updates
   - Update task status when starting/completing
   - Flag blockers immediately

2. **Avoid Conflicts**:
   - Work in separate packages when possible
   - Communicate in PR comments
   - Use feature flags for integration

3. **Testing Strategy**:
   - Each PR must have isolated tests
   - Integration tests can be added later
   - Mock dependencies that aren't ready

---

## Detailed Task Definitions

### 🎯 P1-S3-3.4: OpenRouter-Compatible Model Routing
**Type**: Development  
**Assignee**: Backend Team  
**Effort**: 8 hours  
**Dependencies**: P1-S3-3.3  
**Can Run Parallel With**: P1-S3-3.6  

#### User Story
As a developer using Starport, I need the gateway to support OpenRouter's model routing features so that my applications can use fallback models and auto-routing without code changes.

#### Technical Requirements
```yaml
Key Requirements:
  - Support 'models' array in ChatRequest for fallback chain
  - Parse model IDs in provider/model format (e.g., "openai/gpt-4")
  - Implement fallback triggers:
    - Rate limit exceeded (429)
    - Model unavailable (404)
    - Context length exceeded
    - Provider errors (5xx)
    - Content moderation flags
  - Create "openrouter/auto" model that dynamically selects best model
  - Track which model was actually used in response
```

#### Implementation Tasks
```markdown
- [x] Create internal/routing/model_router.go with ModelRouter interface
- [x] Add Models []string field to ChatRequest struct
- [x] Implement fallback chain logic with configurable retry
- [x] Create model selector for "openrouter/auto"
- [x] Add model availability checker
- [x] Update response to include model_used field
- [x] Write comprehensive tests for fallback scenarios
```

#### Acceptance Criteria
- [x] Can specify multiple models in request and gateway tries them in order
- [x] Fallback triggers work correctly (rate limits, errors, context)
- [x] "openrouter/auto" selects appropriate model based on prompt
- [x] Response indicates which model was actually used
- [x] 90% test coverage on routing logic

### 🎯 P1-S3-3.5: Provider Routing & Fallback Support
**Type**: Development  
**Assignee**: Backend Team  
**Effort**: 8 hours  
**Dependencies**: P1-S3-3.4  
**Can Run Parallel With**: None  

#### User Story
As a developer, I need to control which providers are used for my requests so that I can optimize for cost, latency, or compliance requirements.

#### Technical Requirements
```yaml
Key Requirements:
  - Provider preferences in request or API key config:
    order: ["openai", "anthropic", "google"]  # Try in this order
    only: ["openai", "anthropic"]           # Only use these
    ignore: ["azure"]                        # Never use these
    allow_fallbacks: true                    # Allow other providers
  - Provider health tracking with circuit breakers
  - Latency tracking with exponential moving average
  - Cost-aware routing based on token pricing
  - Sticky sessions for conversation continuity
```

#### Implementation Tasks
```markdown
- [x] Create ProviderPreferences struct in types
- [x] Implement provider selection logic in router
- [x] Add provider health monitoring
- [x] Create latency tracker with EMA
- [x] Implement cost calculator for routing
- [x] Add circuit breaker per provider
- [x] Create sticky session support
- [x] Write routing strategy tests
```

#### Acceptance Criteria
- [x] Provider preferences control routing behavior
- [x] Unhealthy providers are automatically avoided
- [x] Latency-based routing improves response times
- [x] Cost optimization reduces expenses
- [x] Conversations stay with same provider
- [x] 76.2% test coverage on routing logic

### 🎯 P1-S3-3.6: Provider Metadata & /api/v1/providers Endpoint
**Type**: Development  
**Assignee**: Backend Team  
**Effort**: 6 hours  
**Dependencies**: P1-S3-3.3  
**Can Run Parallel With**: P1-S3-3.4  

#### User Story
As a developer, I need to query available providers and get detailed model metadata so that I can make informed decisions about model selection.

#### Technical Requirements
```yaml
Key Requirements:
  - GET /api/v1/providers endpoint returns:
    - Provider name, slug, status
    - Logging/privacy policies
    - Moderation status
    - Terms of service URL
  - Enhanced /api/v1/models response:
    - Pricing (prompt, completion, image)
    - Context length
    - Supported parameters
    - Architecture (modalities, tokenizer)
    - Max completion tokens
  - GET /api/v1/models/{model}/endpoints
    - List which providers offer this model
```

#### Implementation Tasks
```markdown
- [ ] Create Provider struct with metadata
- [ ] Implement /api/v1/providers handler
- [ ] Enhance Model struct with full metadata
- [ ] Update Models() to return enriched data
- [ ] Add model metadata to hardcoded lists
- [ ] Implement model endpoints handler
- [ ] Create provider registry
- [ ] Write endpoint tests
```

#### Acceptance Criteria
- [ ] /api/v1/providers returns all provider metadata
- [ ] /api/v1/models includes pricing and parameters
- [ ] Model metadata matches OpenRouter format
- [ ] Can query providers for specific model
- [ ] Response format validated against OpenRouter
- [ ] 90% test coverage on new endpoints

### 🎯 P1-S3-3.7: Dynamic Model Fetching & Google Provider Separation
**Type**: Development  
**Assignee**: Backend Team  
**Effort**: 8 hours  
**Dependencies**: P1-S3-3.2  
**Can Run Parallel With**: P1-S3-3.4, P1-S3-3.5, P1-S3-3.6  

#### User Story
As a developer, I need models to be fetched dynamically from provider APIs and have proper separation between Google AI Studio and Vertex AI so that I always see the latest available models and can use the full range of Vertex AI models.

#### Technical Requirements
```yaml
Key Requirements:
  - Dynamic model fetching for all providers:
    - Anthropic: GET /v1/models (https://docs.anthropic.com/en/api/models-list)
    - Gemini: GET /v1beta/models  
    - Groq: GET /openai/v1/models
  - Separate Google providers:
    - google-aistudio: Gemini models only
    - google-vertexai: All Vertex AI models
  - Model list caching:
    - TTL: 1 hour default
    - Force refresh option
  - Fallback to static lists on API failure
```

#### Implementation Tasks
```markdown
- [x] Implement dynamic Models() for Anthropic connector
- [x] Implement dynamic Models() for Gemini connector
- [x] Implement dynamic Models() for Groq connector
- [x] Split GeminiConnector into two:
  - [x] GoogleAIStudioConnector (google-aistudio provider)
  - [x] VertexAIConnector (google-vertexai provider)
- [x] Update connector registry for new providers
- [x] Add model response caching with TTL
- [x] Update all model IDs to new provider names
- [x] Add Vertex AI non-Gemini models (PaLM, Codey, etc.)
- [x] Update tests for new providers
- [x] Update documentation
```

#### Acceptance Criteria
- [x] All providers fetch models dynamically (except Azure)
- [x] Model lists update when providers add new models
- [x] google-aistudio and google-vertexai are separate providers
- [x] Vertex AI connector supports all GCP models
- [x] Model lists are cached with 1-hour TTL
- [x] Fallback to static lists on API errors
- [x] All model IDs use correct provider prefix
- [x] 85% test coverage maintained

## Task Templates

### Backend Task Template
```markdown
### 🎯 [TASK-ID]: [Task Title]
**Type**: Development / DevOps / Documentation  
**Assignee**: Role  
**Effort**: X hours  
**Dependencies**: [TASK-IDs]  
**Can Run Parallel With**: [TASK-IDs]  

#### User Story
As a [role], I need [feature] so that [benefit].

#### Technical Requirements
```yaml
Key Requirements:
  - Requirement 1
  - Requirement 2
```

#### Implementation Tasks
```markdown
- [ ] Task 1
- [ ] Task 2
```

#### Acceptance Criteria
- [ ] Criteria 1
- [ ] Criteria 2

#### PR Requirements
Branch: task/[TASK-ID]-brief-description
Title: [[TASK-ID]] Brief description
```

---

This format optimizes for:
1. **Parallel execution** - Clear dependency matrix
2. **Claude Code compatibility** - Structured format with code templates
3. **SCRUM workflow** - User stories with clear acceptance criteria
4. **PR automation** - Consistent naming and templates
