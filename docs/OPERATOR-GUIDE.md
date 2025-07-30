# Operator Execution Guide

**Quick Start**: Run `./spawn-agent.sh P1-S1-1.2` to start the next available task.

## How It Works

1. **Check TASKS.md** to see which tasks are ready (see Phase 1 Progress section)
2. **Run spawn-agent.sh** with the task ID
3. **Script handles everything**: workspace setup, context generation, prerequisite checking
4. **Agents update TASKS.md** automatically

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

#### Step 2: Project Structure (after Step 1 PR merged)

```bash
./spawn-agent.sh P1-S1-1.2
# Auto-creates: ~/starport-development/starport-structure/
# Creates: Directory structure, cmd/starport/main.go, Makefile
# Time: ~4 hours
```

### Day 1 Afternoon: Development Environment

#### Step 3: Development Environment (after P1-S1-1.2 is merged)

```bash
./spawn-agent.sh P1-S1-1.3
# Auto-creates: ~/starport-development/starport-devops/
# Creates: docker-compose.yml, GitHub Actions, pre-commit hooks
# Time: ~6 hours
```

#### Step 4: HTTP Server Foundation (after P1-S1-1.2 is merged)

```bash
./spawn-agent.sh P1-S1-1.4
# Auto-creates: ~/starport-development/starport-http/
# Creates: HTTP server with chi router, health checks
# Time: ~8 hours
```

#### Step 5: Configuration System (after P1-S1-1.4 is merged)

```bash
./spawn-agent.sh P1-S1-1.5
# Auto-creates: ~/starport-development/starport-config/
# Creates: Configuration system with viper
# Time: ~6 hours
```

### Day 2: Storage Layer

#### Step 6: Storage Interface (after P1-S1-1.5 is merged)

```bash
./spawn-agent.sh P1-S2-2.1
# Auto-creates: ~/starport-development/starport-storage-interface/
# Creates: Storage interface definitions
# Time: ~4 hours
```

#### Step 7: Storage Implementations (after P1-S2-2.1 is merged)

**Can run in parallel:**
```bash
# Terminal 1
./spawn-agent.sh P1-S2-2.2
# Auto-creates: ~/starport-development/starport-badger/
# Creates: Badger DB implementation
# Time: ~6 hours

# Terminal 2
./spawn-agent.sh P1-S2-2.3
# Auto-creates: ~/starport-development/starport-models/
# Creates: Core storage models
# Time: ~4 hours
```

### Day 3: LLM Proxy Core

#### Step 8: Connector Interface (after P1-S1-1.4 is merged)

```bash
./spawn-agent.sh P1-S3-3.1
# Auto-creates: ~/starport-development/starport-connector-interface/
# Creates: Model connector interface
# Time: ~4 hours
```

#### Step 9: Provider Integration (after P1-S3-3.1 is merged)

**Can run in parallel:**
```bash
# Terminal 1
./spawn-agent.sh P1-S3-3.2
# Auto-creates: ~/starport-development/starport-connectors/
# Creates: OpenAI & Anthropic connectors
# Time: ~8 hours

# Terminal 2
./spawn-agent.sh P1-S3-3.3
# Auto-creates: ~/starport-development/starport-proxy/
# Creates: Proxy endpoints
# Time: ~10 hours
```

#### Step 10: Routing System (after P1-S3-3.2 is merged)

```bash
./spawn-agent.sh P1-S3-3.4
# Auto-creates: ~/starport-development/starport-routing/
# Creates: Advanced routing system
# Time: ~8 hours
```

### Day 4: Advanced Features

#### Step 11: Security & Storage Features (after dependencies met)

**Can run in parallel after P1-S2-2.3:**
```bash
# Terminal 1
./spawn-agent.sh P1-S4-4.1
# Auto-creates: ~/starport-development/starport-provider-keys/
# Creates: Provider key implementation (includes BYOK)
# Time: ~6 hours

# Terminal 2
./spawn-agent.sh P1-S4-4.4
# Auto-creates: ~/starport-development/starport-presets/
# Creates: Preset management
# Time: ~4 hours
```

#### Step 12: Proxy Features (after P1-S3-3.3 is merged)

**Can run in parallel:**
```bash
# Terminal 1
./spawn-agent.sh P1-S4-4.2
# Auto-creates: ~/starport-development/starport-cache/
# Creates: Caching system
# Time: ~6 hours

# Terminal 2
./spawn-agent.sh P1-S4-4.3
# Auto-creates: ~/starport-development/starport-filters/
# Creates: Content filtering
# Time: ~6 hours
```

## Quick Reference Card

### Can Run Immediately
- `P1-S1-1.1` - First task, no dependencies

### Foundation Dependencies
- `P1-S1-1.2` - After 1.1
- `P1-S1-1.3` - After 1.2
- `P1-S1-1.4` - After 1.2
- `P1-S1-1.5` - After 1.4

### Storage Dependencies
- `P1-S2-2.1` - After 1.5
- `P1-S2-2.2` - After 2.1 (parallel with 2.3)
- `P1-S2-2.3` - After 2.1 (parallel with 2.2)

### LLM Proxy Dependencies
- `P1-S3-3.1` - After 1.4
- `P1-S3-3.2` - After 3.1 (parallel with 3.3)
- `P1-S3-3.3` - After 3.1 (parallel with 3.2)
- `P1-S3-3.4` - After 3.2

### Feature Dependencies
- `P1-S4-4.1` - After 2.3 (parallel with 4.4)
- `P1-S4-4.2` - After 3.3 (parallel with 4.3)
- `P1-S4-4.3` - After 3.3 (parallel with 4.2)
- `P1-S4-4.4` - After 2.3 (parallel with 4.1)

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
  v
P1-S1-1.2 (Project Structure)
  |
  +---> P1-S1-1.3 (Dev Environment)
  |
  +---> P1-S1-1.4 (HTTP Server) -----> P1-S3-3.1 (Connector Interface)
            |                                    |
            v                                    +---> P1-S3-3.2 (Providers)
          P1-S1-1.5 (Configuration)              |           |
            |                                    |           v
            v                                    |     P1-S3-3.4 (Routing)
          P1-S2-2.1 (Storage Interface)          |
            |                                    +---> P1-S3-3.3 (Proxy)
            +---> P1-S2-2.2 (Badger)                        |
            |                                               +---> P1-S4-4.2 (Cache)
            +---> P1-S2-2.3 (Models)                        |
                    |                                       +---> P1-S4-4.3 (Filters)
                    +---> P1-S4-4.1 (BYOK)
                    |
                    +---> P1-S4-4.4 (Presets)
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

**Foundation:**
- `P1-S1-1.1` → `~/starport-development/starport-init/`
- `P1-S1-1.2` → `~/starport-development/starport-structure/`
- `P1-S1-1.3` → `~/starport-development/starport-devops/`
- `P1-S1-1.4` → `~/starport-development/starport-http/`
- `P1-S1-1.5` → `~/starport-development/starport-config/`

**Storage:**
- `P1-S2-2.1` → `~/starport-development/starport-storage-interface/`
- `P1-S2-2.2` → `~/starport-development/starport-badger/`
- `P1-S2-2.3` → `~/starport-development/starport-models/`

**LLM Proxy:**
- `P1-S3-3.1` → `~/starport-development/starport-connector-interface/`
- `P1-S3-3.2` → `~/starport-development/starport-connectors/`
- `P1-S3-3.3` → `~/starport-development/starport-proxy/`
- `P1-S3-3.4` → `~/starport-development/starport-routing/`

**Features:**
- `P1-S4-4.1` → `~/starport-development/starport-provider-keys/`
- `P1-S4-4.2` → `~/starport-development/starport-cache/`
- `P1-S4-4.3` → `~/starport-development/starport-filters/`
- `P1-S4-4.4` → `~/starport-development/starport-presets/`

### Clean Up After Merge
Once a PR is merged, you can remove the workspace:
```bash
rm -rf ~/starport-development/starport-init/
```

## Parallel Execution Tips

### Running Multiple Agents
The spawn-agent.sh script creates separate workspaces automatically, so you can run multiple agents in parallel:

```bash
# Terminal 1 - Storage team
./spawn-agent.sh P1-S2-2.2

# Terminal 2 - API team  
./spawn-agent.sh P1-S3-3.2

# Terminal 3 - Features team
./spawn-agent.sh P1-S4-4.1
```

Each agent gets its own Git clone to avoid conflicts. The script handles all workspace management.

### Avoiding Conflicts
- Each task modifies different packages/files
- Agents update TASKS.md to coordinate
- Use PR comments for cross-team communication

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
- Read TASKS.md in detail

Everything you need is automated!