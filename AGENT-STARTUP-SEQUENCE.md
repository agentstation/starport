# Agent Startup Sequence (For Operators)

**Quick Start**: See [OPERATOR-GUIDE.md](OPERATOR-GUIDE.md) for the complete execution order without needing to check other files.

## Overview

This document explains the task dependencies and execution order for spawning Claude Code agents.

## Execution Order

### Phase 1: Foundation (Start Here)
```bash
# Must complete first
./spawn-agent.sh P1-S1-1.1
```

### Phase 2: Parallel Structure & Docs (After 1.1 merged)
```bash
# Can run simultaneously in different terminals
./spawn-agent.sh P1-S1-1.2  # Project structure
./spawn-agent.sh P1-S1-1.6  # OpenAPI docs
./spawn-agent.sh P1-S1-1.7  # Docs infrastructure
```

### Phase 3: Dev Environment & Server (After 1.2 merged)
```bash
# Can run simultaneously
./spawn-agent.sh P1-S1-1.3  # Dev environment
./spawn-agent.sh P1-S1-1.4  # HTTP server
```

### Phase 4: Configuration (After 1.4 merged)
```bash
./spawn-agent.sh P1-S1-1.5  # Configuration system
```

### Phase 5: Storage Layer (After 1.5 merged)
```bash
# First: Interface definition
./spawn-agent.sh P1-S2-2.1  # Storage interface

# Then: Implementations (can run simultaneously)
./spawn-agent.sh P1-S2-2.2  # Badger implementation
./spawn-agent.sh P1-S2-2.3  # Data models
```

## How the Spawn Script Works

1. Run `./spawn-agent.sh <TASK-ID>`
2. Copy the complete context it outputs
3. Paste into Claude Code when starting
4. Or save to file: `./spawn-agent.sh P1-S1-1.1 > context-P1-S1-1.1.txt`

Each agent receives:
- Task description and requirements
- Files to read with full paths
- Dependencies and prerequisites
- Branch name to create
- Awareness of parallel work

## Tips for Operators

- **Check PR Status**: Don't start dependent tasks until prerequisites are merged
- **Use Multiple Terminals**: Each agent needs its own terminal
- **Pull Latest**: Always `git pull origin main` before starting new agents
- **Follow the Order**: Tasks have dependencies that must be respected

For a simpler, linear guide, see [OPERATOR-GUIDE.md](OPERATOR-GUIDE.md).