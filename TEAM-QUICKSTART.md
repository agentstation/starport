# Team Quick Start Guide

## 🚀 Start Here - Ready to Code!

### Current Status
- **Phase**: Implementation Ready
- **Status**: Architecture complete, ready for development
- **First Task**: P1-S1-1.1 (Repository Initialization)

### Quick Checks Before Starting
```bash
# Verify documentation consistency
grep -r "P1-W" . --exclude-dir=.git  # Should return nothing
```

### Your First Steps
1. Check available tasks in TASKS.md (look for 🟡 Ready)
2. Use `./spawn-agent.sh <TASK-ID>` to get started
3. The script automatically creates a workspace and provides context

## 📋 Day 1 Task Assignments

### Storage Team
```bash
# Morning: Repository setup
Task: P1-S1-1.1 - Repository Initialization
Branch: task/P1-S1-1.1-repo-init

# Afternoon: Project structure  
Task: P1-S1-1.2 - Project Structure Setup
Branch: task/P1-S1-1.2-project-structure
```

### API Team
```bash
# Morning: Wait for 1.2, review architecture
# Afternoon: Start HTTP server
Task: P1-S1-1.4 - HTTP Server Foundation
Branch: task/P1-S1-1.4-http-server
```

### DevOps Team
```bash
# Morning: Wait for 1.2, prepare CI/CD
# Afternoon: Development environment
Task: P1-S1-1.3 - Development Environment
Branch: task/P1-S1-1.3-dev-environment
```

### Docs Team
```bash
# Morning: After 1.1, start docs
Task: P1-S1-1.6 - OpenAPI Foundation
Branch: task/P1-S1-1.6-openapi-foundation

Task: P1-S1-1.7 - Documentation Infrastructure  
Branch: task/P1-S1-1.7-docs-infrastructure
```

## 🔧 Essential Commands

### Starting Work
```bash
# 1. Update main
git checkout main
git pull origin main

# 2. Create task branch
git checkout -b task/P1-S1-1.1-repo-init

# 3. Update task status in TASKS.md
# Change: **Status**: 🟡 Ready
# To:     **Status**: 🟢 In Progress
```

### Submitting PR
```bash
# 1. Commit with task ID
git add .
git commit -m "[P1-S1-1.1] Initialize repository with Go module"

# 2. Push branch
git push origin task/P1-S1-1.1-repo-init

# 3. Create PR with title format
[P1-S1-1.1] Repository initialization with Go module structure
```

## 📊 Daily Coordination

### Morning (9 AM)
1. Check COORDINATION.md for updates
2. Find your next task in TASKS.md (🟡 Ready)
3. Update COORDINATION.md with your current work

### End of Day (5 PM)
1. Update task status in your PR
2. Update COORDINATION.md with progress
3. Flag any blockers

## 🎯 Phase 1 Goals

### Must Complete
- [ ] P1-S1-1.1 - Repository setup
- [ ] P1-S1-1.2 - Project structure
- [ ] P1-S1-1.3 - Dev environment

### Should Complete  
- [ ] P1-S1-1.4 - HTTP server
- [ ] P1-S1-1.6 - OpenAPI foundation
- [ ] P1-S1-1.7 - Docs infrastructure

### Nice to Have
- [ ] P1-S1-1.5 - Configuration system
- [ ] Start Subphase 1.2 storage tasks

## ⚠️ Important Reminders

1. **Check Dependencies**: Never start a 🔴 Blocked task
2. **Small PRs**: Keep changes focused (< 500 lines)
3. **Test Coverage**: Include tests from day 1
4. **Mock Dependencies**: Don't wait - mock what you need
5. **Communicate**: Update COORDINATION.md twice daily

## 🔍 Where to Find Things

- **Architecture**: ARCHITECTURE.md (source of truth)
- **Timeline**: PLAN.md (but extend mentally by 50%)
- **Tasks**: TASKS.md (the scrum tasks to complete)
- **Workflow**: CLAUDE.md (how to work)
- **Coordination**: COORDINATION.md (daily updates)

## 🚨 If Blocked

1. Check if you can mock the dependency
2. Find another 🟡 Ready task in your area
3. Update COORDINATION.md with blocker
4. Help review another team's PR
5. Work on documentation/tests

---

**Ready to start? Pick your first task from above and let's build Starport! 🚀**