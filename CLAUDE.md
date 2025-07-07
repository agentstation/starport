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
**Progress**: 1 of 16 tasks complete (P1-S1-1.1 ✅)

## Document Reference

| Document | Purpose | When to Use |
|----------|---------|-------------|
| TASKS.md | Task definitions, requirements, and live status (single source of truth) | Read for requirements, update for status |
| ARCHITECTURE.md | Technical specifications | Reference for design decisions |

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


## Questions?
- Architecture questions → Check ARCHITECTURE.md
- Timeline questions → Check PLAN.md  
- Task details → Check TASKS.md
- Implementation patterns → Check existing code or ask in PR
- Execution order → Check OPERATOR-GUIDE.md