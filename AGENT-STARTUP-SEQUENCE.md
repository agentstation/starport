# Agent Startup Sequence (For Operators)

**Quick Start**: See [OPERATOR-GUIDE.md](OPERATOR-GUIDE.md) for the complete execution order without needing to check other files.

## Overview

This document explains the task dependencies and execution order for spawning Claude Code agents.

## Execution Order

### Phase 1: Foundation (Start Here)
```bash
# Must complete first
./spawn-agent.sh P1-S1-1.1  # Repository initialization
```

### Phase 2: Project Structure (After 1.1 merged)
```bash
./spawn-agent.sh P1-S1-1.2  # Project structure
```

### Phase 3: Core Development (After 1.2 merged)
```bash
# Can run simultaneously
./spawn-agent.sh P1-S1-1.3  # Dev environment
./spawn-agent.sh P1-S1-1.4  # HTTP server
```

### Phase 4: Configuration (After 1.4 merged)
```bash
./spawn-agent.sh P1-S1-1.5  # Configuration system
```

### Phase 5: Storage & Connectors (After dependencies met)
```bash
# Storage (after 1.5):
./spawn-agent.sh P1-S2-2.1  # Storage interface

# Connectors (after 1.4):
./spawn-agent.sh P1-S3-3.1  # Connector interface
```

### Phase 6: Implementations (After interfaces merged)
```bash
# Storage (after 2.1) - can run simultaneously:
./spawn-agent.sh P1-S2-2.2  # Badger implementation
./spawn-agent.sh P1-S2-2.3  # Data models

# Providers (after 3.1) - can run simultaneously:
./spawn-agent.sh P1-S3-3.2  # OpenAI & Anthropic connectors
./spawn-agent.sh P1-S3-3.3  # Proxy endpoints
```

### Phase 7: Advanced Systems (After implementations)
```bash
# Routing (after 3.2):
./spawn-agent.sh P1-S3-3.4  # Advanced routing

# Features (can run simultaneously):
# After 2.3:
./spawn-agent.sh P1-S4-4.1  # BYOK implementation
./spawn-agent.sh P1-S4-4.4  # Preset management

# After 3.3:
./spawn-agent.sh P1-S4-4.2  # Caching system
./spawn-agent.sh P1-S4-4.3  # Content filtering
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