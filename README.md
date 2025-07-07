# Starport

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.22%2B-blue.svg)](https://golang.org/dl/)

> **🚧 Status: Implementation Started** - Repository initialized with Go module structure. See [TASKS.md](TASKS.md) to start contributing.

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
- [TASKS.md](TASKS.md) - Development tasks and live status
- [OPERATOR-GUIDE.md](OPERATOR-GUIDE.md) - How to execute tasks

## Development Status

**Phase**: Implementation Started  
**Current Focus**: Phase 1 - Core Foundation  
**Progress**: 1 of 16 tasks complete (P1-S1-1.1 ✅)

- [TASKS.md](TASKS.md) - Task requirements and live status tracking

## Contributing

This project uses parallel development with multiple Claude Code instances. 

### Quick Start for Operators
```bash
# Start here - see OPERATOR-GUIDE.md for complete instructions
./spawn-agent.sh P1-S1-1.1
```

### Key Resources

- [OPERATOR-GUIDE.md](OPERATOR-GUIDE.md) - **Start here!** How to execute tasks
- [TASKS.md](TASKS.md) - Task requirements and live status tracking
- [ARCHITECTURE.md](ARCHITECTURE.md) - Technical specifications
- [PLAN.md](PLAN.md) - Implementation roadmap
- [CLAUDE.md](CLAUDE.md) - Agent-specific instructions
- `spawn-agent.sh` - Script to spawn agents with proper context

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

Ready to build the future of LLM infrastructure? Check [OPERATOR-GUIDE.md](OPERATOR-GUIDE.md) to get started!