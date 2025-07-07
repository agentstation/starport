# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Context

**Project**: Starport - High-Performance LLM Gateway
**Phase**: Implementation Ready
**Status**: Architecture complete, ready for development

## Document Hierarchy

```
ARCHITECTURE.md → PLAN.md → TASKS.md
     ↓              ↓           ↓
  Design        Schedule    Executable
  Decisions     & Phases    Work Items
```

## Claude Code Workflow

### 1. Task Selection
When starting work:
1. Check TASKS.md for available tasks
2. Look for tasks with status `ready` and no blocking dependencies
3. Select tasks that match your assigned area (backend, frontend, devops, etc.)
4. Use the task ID format: `PHASE-SUBPHASE-TASK` (e.g., `P1-S1-1.2`)

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

### 4. Parallel Execution Guidelines

#### Safe for Parallel Work
These task groups can be worked on simultaneously by different Claude Code instances:

**Subphase 1.1 Parallel Groups:**
- Group A: Repository setup (1.1)
- Group B: Documentation setup (1.6, 1.7) - after 1.1

**Subphase 1.2 Parallel Groups:**
- Group A: Project structure (1.2) - after 1.1
- Group B: Development environment (1.3) - after 1.2
- Group C: HTTP server (1.4) - after 1.2

**Subphase 1.3+ Parallel Groups:**
- Group A: OpenAI connector (3.2)
- Group B: Anthropic connector (3.3)
- Group C: Management API (6.1)
- Group D: CLI implementation (6.2)

#### Sequential Dependencies
These must be done in order:
1. Storage interface (2.1) → Badger implementation (2.2)
2. HTTP server (1.4) → Configuration system (1.5)
3. Connectors (3.2, 3.3) → Routing engine (3.4) → API endpoints (3.5)

### 5. Task Assignment via Claude Code

To run multiple Claude Code instances in parallel:

```bash
# Terminal 1 - Backend Storage Team
claude-code --task-filter "storage|badger|kv" --branch-prefix "task/" TASKS.md

# Terminal 2 - API Team  
claude-code --task-filter "api|endpoint|openai|anthropic" --branch-prefix "task/" TASKS.md

# Terminal 3 - DevOps Team
claude-code --task-filter "docker|ci|deployment" --branch-prefix "task/" TASKS.md

# Terminal 4 - Documentation Team
claude-code --task-filter "docs|openapi|sdk" --branch-prefix "task/" TASKS.md
```

### 6. Task Status Management

**COORDINATION.md is the single source of truth for task status.**

When working on tasks:
1. **Starting work**: Update COORDINATION.md immediately
   - Add your task to "Active Work" table
   - Mark status as "🟢 In Progress"
   
2. **Completing work**: Update COORDINATION.md when PR is ready
   - Move task to "Completed Today" section
   - Update "Active Work" to show "✅ Completed"
   - Add PR number

3. **In your PR description**: Include implementation checklist
```markdown
## Implementation Tasks
- [x] Create directory structure
- [x] Initialize go.mod
- [x] Create Makefile
```

**Note**: Do NOT update status in TASKS.md - it's for task definitions only.

### 7. Testing Requirements

Every PR must include:
1. Unit tests for new code (target 90% coverage)
2. Integration tests if applicable
3. Documentation updates
4. Passing CI checks

### 8. Code Review Process

1. Self-review checklist:
   - [ ] Follows architecture in ARCHITECTURE.md
   - [ ] Meets acceptance criteria in TASKS.md
   - [ ] Has appropriate tests
   - [ ] Documentation updated
   - [ ] No security vulnerabilities

2. Tag reviewers based on area:
   - Backend: `@backend-team`
   - Frontend: `@frontend-team`
   - DevOps: `@devops-team`

## Parallel Development Configuration

### Claude Code Config Templates

Create these config files for different team roles:

#### `.claude/backend.yaml`
```yaml
name: backend-developer
focus_areas:
  - internal/
  - pkg/
  - cmd/
task_patterns:
  - storage
  - api
  - connector
  - routing
branch_prefix: task/
auto_test: true
test_command: go test ./...
```

#### `.claude/frontend.yaml`
```yaml
name: frontend-developer
focus_areas:
  - web/
  - internal/api/
task_patterns:
  - ui
  - react
  - frontend
branch_prefix: task/
auto_test: true
test_command: cd web && npm test
```

#### `.claude/devops.yaml`
```yaml
name: devops-engineer
focus_areas:
  - .github/
  - docker/
  - helm/
  - scripts/
task_patterns:
  - ci
  - deployment
  - docker
  - kubernetes
branch_prefix: task/
```

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

## Troubleshooting Parallel Development

### Merge Conflicts
1. Always pull latest main before starting work
2. Keep PRs small and focused
3. Communicate in #dev-coordination channel

### Dependency Conflicts
1. Check TASKS.md dependency graph
2. Coordinate with other developers
3. Use feature flags for incomplete dependencies

### Testing Failures
1. Run tests locally before pushing
2. Check CI logs for details
3. Ensure test data is isolated

## For Human Operators: Spawning Agents

To spawn Claude Code agents:
```bash
./spawn-agent.sh <TASK-ID>
```
Example: `./spawn-agent.sh P1-S1-1.1`

See AGENT-STARTUP-SEQUENCE.md for task dependencies and order.

## For Claude Code Agents: How to Use Documentation

### Initial Context Loading
When starting work, agents should:
1. Read CLAUDE.md (this file) for workflow
2. Check ARCHITECTURE.md for technical decisions
3. Review TASKS.md for your assigned task
4. Check COORDINATION.md for current status

### Task Execution Flow
```
1. IMMEDIATELY update COORDINATION.md - mark task 'In Progress'
   ↓
2. Find task in TASKS.md
   ↓
3. Read technical requirements
   ↓
4. Check ARCHITECTURE.md for relevant sections
   ↓
5. Implement according to acceptance criteria
   ↓
6. Update COORDINATION.md - mark 'PR Submitted' with PR #
   ↓
7. Create and submit PR on GitHub
```

### Key Files for Agents

| File | Purpose | When to Read |
|------|---------|--------------|
| CLAUDE.md | Workflow and patterns | First, always |
| TASKS.md | Task details and requirements | Before starting work |
| ARCHITECTURE.md | Technical specifications | When implementing |
| COORDINATION.md | Current status and blockers | Before starting, hourly |
| PLAN.md | Overall timeline | For context only |
| AGENT-STARTUP-SEQUENCE.md | Agent coordination | For operators, not agents |

### Context Awareness
Agents should be aware of:
- Their specific task ID and requirements
- Completed dependencies
- Other agents working in parallel
- Which directories they should work in
- Expected duration of their task

## Questions?
- Architecture questions → Check ARCHITECTURE.md
- Timeline questions → Check PLAN.md  
- Task details → Check TASKS.md
- Implementation patterns → Check existing code or ask in PR
- Agent coordination → Check AGENT-STARTUP-SEQUENCE.md