# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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
**Phase**: Implementation Phase 1
**Progress**: 5 of 16 tasks complete

## Current Codebase Status

### Completed Components (Phase 1, Subphase 1.1-1.2)

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
- Support for 6 LLM providers: OpenAI, Anthropic, Gemini, Groq, Mistral, Azure OpenAI

### Project Structure
```
starport/
├── cmd/starport/         # Single binary entry point
│   ├── main.go          # Minimal main function
│   ├── start.go         # Signal handling
│   └── run.go           # Application setup & CLI
├── internal/            # Private application code
│   ├── app/            # Application lifecycle
│   ├── config/         # Configuration system ✅
│   └── server/         # HTTP server ✅
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

### Configuration Capabilities
All configuration via environment variables or .env files:
- Server settings (port, timeouts, TLS)
- Storage modes (Badger embedded or Valkey distributed)
- Provider endpoints and timeouts (6 providers configured)
- Rate limiting with hot reload
- Security settings (CORS, JWT, API keys)
- Logging configuration

### Next Tasks Ready to Implement
Based on completed prerequisites:
1. **P1-S2-2.1**: Storage Interface Definition (depends on config ✅)
2. **P1-S3-3.1**: Model Connector Interface (depends on server ✅)

These can be worked on in parallel by different agents.

## Document Reference

| Document | Purpose | When to Use |
|----------|---------|-------------|
| TASKS.md | Task definitions, requirements, and live status (single source of truth) | Read for requirements, update for status |
| ARCHITECTURE.md | Technical specifications | Reference for design decisions |

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
- Link to the task in TASKS.md
- Include acceptance criteria checklist in PR description

### 4. Task Status Management

**TASKS.md is the single source of truth for task status.**

When working on tasks:
1. **Starting work**: Update TASKS.md immediately
   - Add your task to "Active Work" table
   - Mark status as "🟢 In Progress"
   
2. **Completing work**: Update TASKS.md when PR is ready
   - Move task to "Recently Completed" section
   - Update "Active Work" to show "✅ Completed"
   - Add PR number

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
- Update TASKS.md with PR number
- Note any blockers or incomplete items

## Working with Other Agents

### Avoiding Conflicts
When multiple agents work in parallel:
1. **Stay in your lane**: Only modify files related to your task
2. **Pull before push**: Always `git pull origin main` before creating your branch
3. **Communicate blockers**: Update TASKS.md immediately if blocked

### Handoff Protocol
When your task blocks others:
1. Ensure your PR description clearly states what was implemented
2. Update TASKS.md with accurate status
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
- Core is fully open source (MIT)
- Enterprise features in separate private repository
- Plugin architecture for clean separation
- Build tags for conditional compilation

## Code Patterns

### Error Handling
```go
if err != nil {
    return fmt.Errorf("component: action failed: %w", err)
}
```

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

## Commands to Remember

```bash
# These commands will work after initial implementation:

# Build (after go.mod exists)
make build
make build-enterprise

# Test (after tests are written)
make test
make test-coverage
make test-integration

# Run (after binary is built)
./starport serve
./starport keys create --name dev-key

# Development (after Makefile exists)
make dev  # Hot reload
make fmt  # Format code
make lint # Lint code
```

## PR Checklist Template

```markdown
## PR Checklist
- [ ] Task ID in PR title and branch name
- [ ] Links to task in TASKS.md
- [ ] Meets all acceptance criteria
- [ ] Tests added/updated (90% coverage)
- [ ] Documentation updated
- [ ] No linting errors
- [ ] Security considerations addressed
- [ ] Performance impact assessed
```

## Troubleshooting

### Common Issues
1. **Prerequisites not met**: Check TASKS.md, update blockers section
2. **Merge conflicts**: Pull latest main, preserve both changes
3. **Tests failing**: Check if related to your changes or pre-existing
4. **Can't find files**: May need to create them per ARCHITECTURE.md

### Getting Help
- Check existing PRs for similar implementations
- Reference ARCHITECTURE.md for design decisions
- Update TASKS.md blocked tasks table with specific blockers

## Quick Reference for Next Tasks

### For Storage Implementation (P1-S2-2.X)
- Storage interface should support both Badger and Valkey
- Use the existing config structs in `internal/config/config.go`
- Follow the context propagation pattern for all methods
- Include TTL support for rate limiting
- See ARCHITECTURE.md Section 16 for storage details

### For LLM Connector Implementation (P1-S3-3.X)
- Connectors go in `internal/connectors/` package
- Each provider gets its own file (openai.go, anthropic.go, etc.)
- Use the provider configs from `internal/config/config.go`
- Implement streaming support from the start
- See ARCHITECTURE.md Section 8 for routing architecture

### For Testing
- Aim for 90% coverage on new code
- Use table-driven tests for multiple scenarios
- Mock external dependencies
- Test files go alongside implementation files
- Run `make test-coverage` to check coverage

## Questions?
- Architecture questions → Check ARCHITECTURE.md
- Timeline questions → Check PLAN.md  
- Task details → Check TASKS.md
- Implementation patterns → Check existing code or ask in PR
- Execution order → Check OPERATOR-GUIDE.md
- Current codebase status → Check this file's "Current Codebase Status" section