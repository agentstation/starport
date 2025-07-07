# Operator Execution Guide

This guide shows exactly which agents to spawn and when. No need to check other files.

## Important: Automatic Workspace Setup

The `spawn-agent.sh` script **automatically creates separate clones** for each agent to avoid Git conflicts. You don't need to manage this manually!

## Phase 1: Foundation Setup

### Day 1 Morning: Start Here

#### Step 1: Initialize Repository
```bash
# From your main starport directory
./spawn-agent.sh P1-S1-1.1
# Script will create: ~/starport-development/starport-init/
```
**Creates**: go.mod, LICENSE, updates README  
**Time**: ~2 hours  
**Wait for**: PR to be merged before proceeding  

#### Step 2: Launch Parallel Tasks (after Step 1 PR merged)
Open 3 terminal windows and run:

```bash
# Terminal 1: Project Structure
./spawn-agent.sh P1-S1-1.2
# Auto-creates: ~/starport-development/starport-structure/
# Creates: Directory structure, cmd/starport/main.go, Makefile
# Time: ~4 hours

# Terminal 2: OpenAPI Documentation  
./spawn-agent.sh P1-S1-1.6
# Auto-creates: ~/starport-development/starport-openapi/
# Creates: docs/openapi/ with API specifications
# Time: ~6 hours

# Terminal 3: Documentation Infrastructure
./spawn-agent.sh P1-S1-1.7
# Auto-creates: ~/starport-development/starport-docs/
# Creates: Documentation system setup
# Time: ~4 hours
```

### Day 1 Afternoon: Development Environment

#### Step 3: After P1-S1-1.2 is merged
Open 2 terminal windows:

```bash
# Terminal 1: Development Environment
./spawn-agent.sh P1-S1-1.3
# Creates: docker-compose.yml, GitHub Actions, pre-commit hooks
# Time: ~6 hours

# Terminal 2: HTTP Server Foundation
./spawn-agent.sh P1-S1-1.4
# Creates: HTTP server with chi router, health checks
# Time: ~8 hours
```

### Day 2: Configuration & Storage

#### Step 4: After P1-S1-1.4 is merged
```bash
./spawn-agent.sh P1-S1-1.5
# Creates: Configuration system with viper
# Time: ~6 hours
```

#### Step 5: After P1-S1-1.5 is merged
```bash
./spawn-agent.sh P1-S2-2.1
# Creates: Storage interface definitions
# Time: ~4 hours
```

#### Step 6: After P1-S2-2.1 is merged
Open 2 terminals:

```bash
# Terminal 1: Badger Implementation
./spawn-agent.sh P1-S2-2.2
# Creates: Embedded KV store implementation
# Time: ~8 hours

# Terminal 2: Data Models
./spawn-agent.sh P1-S2-2.3
# Creates: Core data structures
# Time: ~6 hours
```

## Quick Reference Card

### Can Run Immediately
- `P1-S1-1.1` - First task, no dependencies

### Can Run After 1.1
- `P1-S1-1.2` - Project structure
- `P1-S1-1.6` - OpenAPI docs (parallel safe)
- `P1-S1-1.7` - Docs infrastructure (parallel safe)

### Can Run After 1.2
- `P1-S1-1.3` - Dev environment
- `P1-S1-1.4` - HTTP server

### Can Run After 1.4
- `P1-S1-1.5` - Configuration

### Can Run After 1.5
- `P1-S2-2.1` - Storage interface

### Can Run After 2.1
- `P1-S2-2.2` - Badger implementation
- `P1-S2-2.3` - Data models (parallel safe)

## Status Tracking

### How to Check Progress
```bash
# Pull latest changes
git pull origin main

# Check what's been completed
ls -la cmd/starport/main.go  # If exists, P1-S1-1.2 is done
ls -la go.mod                 # If exists, P1-S1-1.1 is done
```

### Common Issues

**"Unknown task ID"**
- Check you typed the task ID correctly (e.g., P1-S1-1.1)

**"Prerequisite not met"**
- The required previous task hasn't been merged yet
- Check GitHub PRs or pull latest main

**Context too long to paste**
- Use the file option: `./spawn-agent.sh P1-S1-1.1 > context-P1-S1-1.1.txt`
- Then tell Claude: "Read context-P1-S1-1.1.txt"

## Parallel Execution Map

```
START
  |
  v
P1-S1-1.1 (Repository Init)
  |
  +---> P1-S1-1.2 (Project Structure)
  |         |
  |         +---> P1-S1-1.3 (Dev Environment)
  |         |
  |         +---> P1-S1-1.4 (HTTP Server)
  |                   |
  |                   v
  |                 P1-S1-1.5 (Configuration)
  |                   |
  |                   v
  |                 P1-S2-2.1 (Storage Interface)
  |                   |
  |                   +---> P1-S2-2.2 (Badger)
  |                   |
  |                   +---> P1-S2-2.3 (Models)
  |
  +---> P1-S1-1.6 (OpenAPI Docs)
  |
  +---> P1-S1-1.7 (Docs Infrastructure)
```

## Automatic Workspace Management

The `spawn-agent.sh` script handles all workspace management for you:

### What it Does
1. **Creates `~/starport-development/`** as the workspace root
2. **Clones separate directories** for each task automatically
3. **Names workspaces clearly**: `starport-init`, `starport-structure`, etc.
4. **Handles existing workspaces**: Options to reuse or recreate
5. **Pulls latest changes** when reusing workspaces

### Workspace Locations
All agent workspaces are created under `~/starport-development/`:
- `P1-S1-1.1` → `~/starport-development/starport-init/`
- `P1-S1-1.2` → `~/starport-development/starport-structure/`
- `P1-S1-1.6` → `~/starport-development/starport-openapi/`
- `P1-S1-1.7` → `~/starport-development/starport-docs/`

### Clean Up After Merge
Once a PR is merged, you can remove the workspace:
```bash
rm -rf ~/starport-development/starport-init/
```

## Summary

1. **Run `./spawn-agent.sh`** - it handles workspace setup automatically
2. Always start with `P1-S1-1.1`
3. Check dependencies before spawning agents
4. Use multiple terminals for parallel tasks
5. Wait for PRs to merge before dependent tasks
6. Each spawn command gives complete context - just copy and paste

No need to:
- Manually clone repositories
- Manage workspace directories
- Read TASKS.md

Everything you need is automated!