# Starport

> **🚧 Status: Pre-Implementation** - This repository contains comprehensive documentation and is ready for development to begin. See [TASKS.md](TASKS.md) to start contributing.

High-performance LLM gateway with unified access to multiple model providers.

## Overview

Starport is an open-source LLM gateway that provides:
- **OpenAI & OpenRouter API compatibility** - Drop-in replacement for existing code
- **Zero-dependency deployment** - Embedded Badger KV store by default
- **Multi-provider support** - OpenAI, Anthropic, and more
- **Advanced routing** - Latency-based, cost-aware, and content-based routing
- **Built-in features** - Rate limiting, caching, BYOK, preset management

## Quick Start

```bash
# Run with embedded storage (no dependencies)
docker run -p 8080:8080 ghcr.io/agentstation/starport:latest

# Or download the binary
wget https://github.com/agentstation/starport/releases/latest/starport
chmod +x starport
./starport
```

Your gateway is now running at `http://localhost:8080`!

## Architecture

- **Single binary** - Server and CLI in one executable
- **Storage options**:
  - Badger (default) - Zero dependencies, embedded KV store
  - Valkey - For multi-node deployments
- **Enterprise features** - SSO, RBAC, analytics (separate package)

## Documentation

- [ARCHITECTURE.md](ARCHITECTURE.md) - Technical design and decisions
- [PLAN.md](PLAN.md) - Implementation roadmap
- [TASKS.md](TASKS.md) - Development tasks
- [TEAM-QUICKSTART.md](TEAM-QUICKSTART.md) - Getting started guide

## Development Status

**Phase**: Implementation Ready  
**Current Focus**: Phase 1 - Core Foundation

See [TASKS.md](TASKS.md) for available tasks and [COORDINATION.md](COORDINATION.md) for current progress.

## Contributing

This project uses parallel development with multiple Claude Code instances. 

### Quick Start for Operators
```bash
# Start here - see OPERATOR-GUIDE.md for complete instructions
./spawn-agent.sh P1-S1-1.1
```

### Documentation

#### 👤 For Human Operators
- [OPERATOR-GUIDE.md](OPERATOR-GUIDE.md) - **Start here!** Simple execution guide
- [AGENT-STARTUP-SEQUENCE.md](AGENT-STARTUP-SEQUENCE.md) - Detailed task dependencies and order
- [TEAM-QUICKSTART.md](TEAM-QUICKSTART.md) - Quick start for human contributors
- `spawn-agent.sh` - Script to spawn agents with proper context

#### 🤖 For Claude Code Agents  
- [CLAUDE.md](CLAUDE.md) - Agent workflow and documentation usage
- [TASKS.md](TASKS.md) - Detailed task requirements
- [ARCHITECTURE.md](ARCHITECTURE.md) - Technical specifications
- [PLAN.md](PLAN.md) - Implementation roadmap

#### 📚 For Both
- [PARALLEL-EXECUTION.md](PARALLEL-EXECUTION.md) - Understanding parallel development
- [COORDINATION.md](COORDINATION.md) - Sprint status tracking

## License

MIT - See LICENSE file for details.

## Performance Targets

- <1ms P99 latency overhead
- 10K+ requests per second
- 50K+ RPS with horizontal scaling

## Compatibility

Starport is designed as a drop-in replacement for:
- OpenAI API
- OpenRouter API
- Most LLM client libraries

---

Ready to build the future of LLM infrastructure? Check [TEAM-QUICKSTART.md](TEAM-QUICKSTART.md) to get started!