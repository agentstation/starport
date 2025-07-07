# Starport SCRUM Task Breakdown

This document provides SCRUM-optimized user stories for implementing Starport. Each story is designed for Claude Code execution with clear requirements, technical details, and PR guidelines.

## Task ID Format
`[PHASE]-[SUBPHASE]-[TASK]` (e.g., P1-S1-1.2)

## Task Status Legend
- 🔴 **Blocked** - Has unmet dependencies
- 🟡 **Ready** - Can be started
- 🟢 **In Progress** - Being worked on
- ✅ **Complete** - Merged to main

## Parallel Execution Matrix

### Subphase 1.1 - Foundation Sprint
| Group | Tasks | Dependencies | Status |
|-------|-------|--------------|--------|
| A | P1-S1-1.1 | None | 🟡 Ready |
| B | P1-S1-1.2 | P1-S1-1.1 | 🔴 Blocked |
| C | P1-S1-1.6, P1-S1-1.7 | P1-S1-1.1 | 🔴 Blocked |

### Subphase 1.2 - Core Development Sprint  
| Group | Tasks | Dependencies | Status |
|-------|-------|--------------|--------|
| A | P1-S1-1.3 | P1-S1-1.2 | 🔴 Blocked |
| B | P1-S1-1.4 | P1-S1-1.2 | 🔴 Blocked |
| C | P1-S1-1.5 | P1-S1-1.4 | 🔴 Blocked |

### Subphase 1.3 - Storage Sprint
| Group | Tasks | Dependencies | Status |
|-------|-------|--------------|--------|
| A | P1-S2-2.1 | P1-S1-1.5 | 🔴 Blocked |
| B | P1-S2-2.3 | P1-S2-2.1 | 🔴 Blocked |
| C | P1-S2-2.2 | P1-S2-2.1 | 🔴 Blocked |

---

## Phase 1: Core Foundation

### Subphase 1.1: Project Setup & Basic Structure

---

### 🎯 P1-S1-1.1: Repository Initialization
**Status**: 🟡 Ready  
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
- [ ] Create GitHub repository `agentstation/starport`
- [ ] Initialize go.mod with module path
- [ ] Create .gitignore for Go projects
- [ ] Add LICENSE file (MIT)
- [ ] Create initial README.md with badges
- [ ] Set up branch protection for main
- [ ] Configure repository settings
```

#### Acceptance Criteria
- [ ] Repository accessible at github.com/agentstation/starport
- [ ] `go mod download` works without errors
- [ ] README displays project name and description
- [ ] License badge shows MIT
- [ ] Main branch protected from direct pushes

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
**Status**: 🔴 Blocked  
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
- [ ] Create directory structure per ARCHITECTURE.md
- [ ] Add placeholder README.md in each directory
- [ ] Create cmd/starport/main.go with CLI framework
- [ ] Implement `version` command
- [ ] Create Makefile with standard targets
- [ ] Add go.work for workspace management
```

#### Code Template
```go
// cmd/starport/main.go
package main

import (
    "fmt"
    "os"
    
    "github.com/urfave/cli/v2"
)

var (
    version = "dev"
    commit  = "none"
    date    = "unknown"
)

func main() {
    app := &cli.App{
        Name:    "starport",
        Usage:   "High-performance LLM gateway",
        Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
        Commands: []*cli.Command{
            {
                Name:    "serve",
                Aliases: []string{"server"},
                Usage:   "Run the gateway server",
                Action:  runServer,
            },
            // Add more commands here
        },
        Action: runServer, // Default action
    }
    
    if err := app.Run(os.Args); err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }
}

func runServer(c *cli.Context) error {
    fmt.Println("Starting Starport server...")
    // TODO: Implement server
    return nil
}
```

#### Acceptance Criteria
- [ ] `make build` produces starport binary
- [ ] `./starport version` shows version info
- [ ] `./starport serve` prints startup message
- [ ] All directories have README files
- [ ] Makefile has build, test, clean, fmt, lint targets

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
- [ ] Create docker-compose.yml for local services
- [ ] Set up GitHub Actions workflow
- [ ] Configure golangci-lint
- [ ] Add security scanning (gosec)
- [ ] Set up code coverage with codecov
- [ ] Create .env.example
- [ ] Configure pre-commit hooks
```

#### Acceptance Criteria
- [ ] `docker-compose up` starts all services
- [ ] GitHub Actions runs on every push
- [ ] Code coverage badge in README
- [ ] Pre-commit hooks format code
- [ ] Security scan passes

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

#### Implementation Tasks
```markdown
- [ ] Implement HTTP server with chi
- [ ] Create middleware chain
- [ ] Add health check endpoints
- [ ] Implement graceful shutdown
- [ ] Add request/response logging
- [ ] Configure timeouts
- [ ] Add metrics middleware preparation
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
- [ ] Server starts on configured port
- [ ] Health checks return 200 OK
- [ ] Request IDs in logs
- [ ] Graceful shutdown works
- [ ] Unit tests pass with >90% coverage

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
  - YAML configuration files
  - Environment variables (override)
  - Command-line flags (override)
  
Features:
  - Validation
  - Hot reload for specific settings
  - Type safety
  - Default values
```

#### Implementation Tasks
```markdown
- [ ] Define configuration structures
- [ ] Implement YAML loading with viper
- [ ] Add environment variable mapping
- [ ] Create validation logic
- [ ] Implement hot reload for rate limits
- [ ] Add configuration documentation
- [ ] Create example configurations
```

#### Acceptance Criteria
- [ ] Config loads from file and env
- [ ] Validation prevents bad configs
- [ ] Hot reload updates rate limits
- [ ] Tests cover all config scenarios
- [ ] Documentation explains all options

---

## Phase 1, Subphase 1.2: Storage & Models

### 🎯 P1-S2-2.1: Storage Interface Definition
**Status**: 🔴 Blocked by P1-S1-1.5  
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
- [ ] Define KVStore interface
- [ ] Create error types
- [ ] Add serialization helpers
- [ ] Create factory pattern
- [ ] Add context support
- [ ] Define transaction interface
- [ ] Create mock implementation for testing
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

// Factory function
func NewKVStore(config Config) (KVStore, error) {
    switch config.Type {
    case "badger":
        return NewBadgerStore(config.Badger)
    case "valkey":
        return NewValkeyStore(config.Valkey)
    default:
        return nil, fmt.Errorf("unknown storage type: %s", config.Type)
    }
}
```

#### Acceptance Criteria
- [ ] Interface covers all use cases
- [ ] Mock implementation works
- [ ] Error types are clear
- [ ] Context properly propagated
- [ ] 100% test coverage on interface

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

## Task Templates

### Backend Task Template
```markdown
### 🎯 [TASK-ID]: [Task Title]
**Status**: 🟡 Ready / 🔴 Blocked / 🟢 In Progress  
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