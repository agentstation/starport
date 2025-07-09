# Starport

[![License: AGPLv3](https://img.shields.io/badge/License-AGPLv3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
[![Go Version](https://img.shields.io/badge/Go-1.22%2B-blue.svg)](https://golang.org/dl/)
[![codecov](https://codecov.io/gh/agentstation/starport/branch/main/graph/badge.svg)](https://codecov.io/gh/agentstation/starport)

> **🚧 Status: Alpha - 85% Feature Complete** - Core features implemented, authentication system in progress. See [roadmap](#roadmap--feature-comparison) for details.

High-performance LLM gateway with unified access to multiple model providers.

## Overview

Starport is a high-performance, self-hosted LLM gateway that provides unified access to multiple AI providers through a single API. Think of it as an open-source, self-hosted alternative to OpenRouter with additional enterprise features.

### Key Features

- **🔄 OpenAI & OpenRouter Compatible** - Drop-in replacement for existing applications
- **🚀 Blazing Fast** - <1ms P99 latency overhead, 10K+ RPS on a single node
- **📦 Zero Dependencies** - Single binary with embedded storage (Badger KV)
- **🤖 Multi-Provider** - OpenAI, Anthropic, Google, Groq, Mistral, Azure OpenAI
- **🧠 Smart Routing** - Automatic failover, latency-based, cost-aware routing
- **🔐 BYOK Support** - Bring your own keys with zero-knowledge security
- **💾 Advanced Caching** - Multi-tier caching with TTL and invalidation
- **🛡️ Enterprise Ready** - Rate limiting, content filtering, audit logs (Enterprise)

### Why Starport?

Unlike managed services, Starport gives you:
- **Complete Control** - Your infrastructure, your rules, your data
- **No Vendor Lock-in** - Open source under GNU AGPLv3 license
- **Cost Effective** - No markup on API calls, use your own keys
- **Privacy First** - Data never leaves your infrastructure
- **Customizable** - Plugin architecture for custom providers and features

### Starport vs OpenRouter

| Aspect | OpenRouter | Starport |
|--------|------------|-----------|
| **Deployment** | Managed SaaS | Self-hosted |
| **Pricing** | Per-token charges | Free (OSS) + Enterprise |
| **Latency** | ~10-50ms overhead | <1ms overhead |
| **Data Privacy** | Data passes through OpenRouter | Data stays in your infrastructure |
| **Customization** | Limited | Full control via plugins |
| **Provider Keys** | Optional (BYOK) | Required (BYOK only) |
| **Open Source** | No | Yes (GNU AGPLv3) |

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

## Roadmap & Feature Comparison

### Development Status

**Phase**: Implementation Phase 1  
**Current Focus**: Core Features & Authentication  
**Progress**: 17 of 20 Phase 1 tasks complete (85%)

### Feature Comparison: Starport vs OpenRouter

| Feature | OpenRouter | Starport OSS | Starport Enterprise | Status |
|---------|------------|--------------|---------------------|---------|
| **Core API** |
| OpenAI-compatible API | ✅ | ✅ | ✅ | ✅ Complete |
| OpenRouter-compatible API | ✅ | ✅ | ✅ | ✅ Complete |
| Unified provider access | ✅ | ✅ | ✅ | ✅ Complete |
| Streaming support | ✅ | ✅ | ✅ | ✅ Complete |
| Function calling | ✅ | ✅ | ✅ | ✅ Complete |
| **Providers** |
| OpenAI | ✅ | ✅ | ✅ | ✅ Complete |
| Anthropic | ✅ | ✅ | ✅ | ✅ Complete |
| Google (Gemini/Vertex) | ✅ | ✅ | ✅ | ✅ Complete |
| Groq | ✅ | ✅ | ✅ | ✅ Complete |
| Mistral | ✅ | ✅ | ✅ | ✅ Complete |
| Azure OpenAI | ✅ | ✅ | ✅ | ✅ Complete |
| 100+ other providers | ✅ | 🚧 | 🚧 | 🔄 Plugin system |
| **Routing & Performance** |
| Model routing/fallback | ✅ | ✅ | ✅ | ✅ Complete |
| Latency-based routing | ✅ | ✅ | ✅ | ✅ Complete |
| Cost optimization | ✅ | ✅ | ✅ | ✅ Complete |
| Circuit breakers | ❌ | ✅ | ✅ | ✅ Complete |
| Sticky sessions | ❌ | ✅ | ✅ | ✅ Complete |
| **BYOK (Bring Your Own Key)** |
| BYOK support | ✅ | ✅ | ✅ | ✅ Complete |
| 5% pricing model | ✅ | ✅ | ✅ | ✅ Complete |
| Zero-knowledge security | ❌ | ✅ | ✅ | ✅ Complete |
| Per-API-key isolation | ❌ | ✅ | ✅ | ✅ Complete |
| **Caching** |
| Response caching | ✅ | ✅ | ✅ | ✅ Complete |
| Multi-tier cache | ❌ | ✅ | ✅ | ✅ Complete |
| Distributed cache | ❌ | ✅ | ✅ | ✅ Complete |
| **Authentication** |
| API key auth | ✅ | 🚧 | ✅ | 🚧 In Progress |
| OAuth/SSO | ✅ | ❌ | ✅ | 📅 Phase 2 |
| Team management | ✅ | ❌ | ✅ | 📅 Phase 2 |
| **Rate Limiting** |
| Credit-based | ✅ | ❌ | ✅ | 📅 Phase 2 |
| Time-based limits | ❌ | ✅ | ✅ | ✅ Complete |
| Token-based limits | ❌ | ✅ | ✅ | ✅ Complete |
| **Advanced Features** |
| Web search | ✅ ($4/1K) | 🚧 | ✅ | 📅 Phase 2 |
| Content filtering | ✅ | 🚧 | ✅ | 🚧 In Progress |
| Preset management | ❌ | 🚧 | ✅ | 🚧 In Progress |
| Multi-modal (images) | ✅ | 🚧 | ✅ | 📅 Phase 2 |
| **Operations** |
| Self-hosted | ❌ | ✅ | ✅ | ✅ Complete |
| Zero dependencies | ❌ | ✅ | ✅ | ✅ Complete |
| Single binary | ❌ | ✅ | ✅ | ✅ Complete |
| <1ms latency overhead | ❌ | ✅ | ✅ | ✅ Complete |
| Prometheus metrics | ❌ | ✅ | ✅ | 📅 Phase 2 |
| **Enterprise** |
| RBAC | ❌ | ❌ | ✅ | 📅 Phase 2 |
| Audit logging | ❌ | ❌ | ✅ | 📅 Phase 2 |
| Analytics dashboard | ✅ | ❌ | ✅ | 📅 Phase 2 |
| SLA support | ✅ | ❌ | ✅ | 📅 Phase 2 |

**Legend:**
- ✅ Complete/Available
- 🚧 In Progress
- 📅 Planned
- ❌ Not Available
- 🔄 Different Approach

### Current Sprint (Phase 1 Completion)

Currently implementing:
- **P1-S4-4.3**: Content Filtering Pipeline
- **P1-S4-4.4**: Preset Management System  
- **P1-S5-5.1**: Authentication System with uuidkey

### Upcoming Features (Phase 2)

1. **Enhanced Storage** - PostgreSQL for enterprise features
2. **Advanced Routing** - Custom routing rules and A/B testing
3. **Multi-modal Support** - Image and document processing
4. **Web Search Integration** - Built-in web search capabilities
5. **Analytics & Monitoring** - Usage analytics and cost tracking
6. **Enterprise Features** - SSO, RBAC, audit logs

### Migration from OpenRouter

Switching from OpenRouter to Starport is seamless:

```python
# OpenRouter
client = OpenAI(
    base_url="https://openrouter.ai/api/v1",
    api_key=OPENROUTER_API_KEY
)

# Starport (same code, different URL)
client = OpenAI(
    base_url="http://localhost:8080/api/v1",
    api_key=STARPORT_API_KEY
)
```

All OpenRouter features work identically:
- Same request/response format
- Same model IDs (provider/model)
- Same streaming protocol
- Same error handling

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

GNU AGPLv3 - See LICENSE file for details.

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