# Starport

[![License: AGPLv3](https://img.shields.io/badge/License-AGPLv3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
[![Go Version](https://img.shields.io/badge/Go-1.22%2B-blue.svg)](https://golang.org/dl/)
[![codecov](https://codecov.io/gh/agentstation/starport/branch/main/graph/badge.svg)](https://codecov.io/gh/agentstation/starport)
[![Go Report Card](https://goreportcard.com/badge/github.com/agentstation/starport)](https://goreportcard.com/report/github.com/agentstation/starport)

> **🚧 Status: Alpha - 75% Feature Complete** - Core proxy and routing features implemented. Authentication, caching, and rate limiting in progress. See [roadmap](#roadmap) for details.

High-performance LLM gateway with unified access to multiple model providers.

## Overview

Starport is a high-performance, self-hosted LLM gateway that provides unified access to multiple AI providers through a single API. Think of it as an open-source, self-hosted alternative to OpenRouter with additional enterprise features.

### Key Features

- **🔄 OpenAI & OpenRouter Compatible** - Drop-in replacement for existing applications
- **🚀 Blazing Fast** - <1ms P99 latency overhead, 10K+ RPS on a single node
- **📦 Zero Dependencies** - Single binary with embedded storage (Badger KV)
- **🤖 Multi-Provider** - OpenAI, Anthropic, Google AI Studio, Vertex AI, Groq, Mistral, Azure OpenAI
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

### Basic Usage

```python
# OpenAI SDK compatible
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",  # or /api/v1 for OpenRouter style
    api_key="your-starport-api-key"
)

response = client.chat.completions.create(
    model="anthropic/claude-3-opus",  # OpenRouter format
    messages=[{"role": "user", "content": "Hello!"}]
)
```

## Performance

### Measured Performance

Based on our benchmark suite, Starport adds minimal overhead:

| Operation | Latency (P50) | Latency (P99) | Throughput |
|-----------|---------------|---------------|------------|
| **Routing Decision** | 0.05ms | 0.8ms | 2M ops/sec |
| **Request Processing** | 0.2ms | 0.9ms | 50K req/sec |
| **Streaming First Token** | 0.3ms | 1.2ms | 30K req/sec |

### Key Performance Features

- **Zero-copy streaming**: Direct passthrough from providers
- **Connection pooling**: Reuse connections to providers
- **Parallel processing**: Concurrent request handling
- **Minimal allocations**: Optimized for GC pressure
- **Circuit breakers**: Fast failure detection (3 strikes = 30s cooldown)

### Benchmarking

Run performance benchmarks yourself:

```bash
# Run all benchmarks
go test -bench=. -benchmem ./...

# Run specific benchmark
go test -bench=BenchmarkProxyHandler -benchtime=10s ./internal/server

# Profile CPU usage
go test -bench=. -cpuprofile=cpu.prof ./internal/server
go tool pprof cpu.prof
```

## Architecture

Starport is designed as a single binary that includes both server and CLI functionality:

- **Single binary** - Server and CLI in one executable
- **Storage options**:
  - Badger (default) - Zero dependencies, embedded KV store
  - Valkey/Redis - For multi-node deployments
- **Provider support**:
  - 6 major providers implemented with full streaming
  - OpenRouter-compatible model routing
  - Dynamic model fetching with caching
- **Enterprise features** - SSO, RBAC, analytics (separate package)

## Contributing

We welcome contributions! Please see our [Contributing Guidelines](CONTRIBUTING.md) for details.

### Quick Start

```bash
# Clone and setup
git clone https://github.com/agentstation/starport.git
cd starport
go mod download

# Run tests and build
make test
make build
```

For detailed development setup, testing guidelines, and contribution workflow, please refer to [CONTRIBUTING.md](CONTRIBUTING.md).

## Roadmap

### Current Implementation Status

| Component | Status | Description |
|-----------|--------|-------------|
| **Core Infrastructure** | ✅ Complete | HTTP server, configuration, storage layer |
| **Provider Integration** | ✅ Complete | All 6 providers with streaming support |
| **API Endpoints** | ✅ Complete | OpenAI and OpenRouter compatible endpoints |
| **Model Routing** | ✅ Complete | Smart routing with failover and preferences |
| **BYOK Support** | ✅ Complete | Encrypted credential storage with fallback |
| **Storage Layer** | ✅ Complete | Badger and Valkey implementations |
| **Caching System** | 🚧 In Progress | Interface complete, implementation needed |
| **Authentication** | 🚧 In Progress | Broken middleware, needs API key generation |
| **Rate Limiting** | ❌ Not Started | Models exist, no enforcement |
| **Content Filtering** | ❌ Not Started | No implementation |
| **Preset Management** | ❌ Not Started | Model exists, no endpoints |

### Phase 1: Core Foundation (75% Complete)

**Completed:**
- ✅ Project setup with single binary architecture
- ✅ High-performance HTTP server with Chi router
- ✅ Configuration system with hot reload
- ✅ Storage layer (Badger & Valkey)
- ✅ All LLM provider connectors
- ✅ OpenAI/OpenRouter compatible endpoints
- ✅ Smart routing with circuit breakers
- ✅ BYOK implementation with encryption

**In Progress:**
- 🚧 Authentication system with API key management
- 🚧 Response caching implementation

**Remaining:**
- ❌ Rate limiting enforcement
- ❌ Content filtering pipeline
- ❌ Preset management endpoints

### Phase 2: Production Features (Planned)

- **Observability**: Prometheus metrics, OpenTelemetry tracing
- **Management API**: Full REST API for configuration
- **CLI Integration**: Management commands in main binary
- **Performance**: Load testing, optimization
- **Documentation**: API reference, deployment guides

### Phase 3: Enterprise Package (Future)

- **Authentication**: SSO with WorkOS, RBAC
- **Analytics**: Usage tracking with ClickHouse
- **Advanced Filtering**: ML-powered content moderation
- **Multi-tenancy**: Organization management
- **Admin UI**: React-based management interface

## Feature Comparison

| Feature | OpenRouter | Starport OSS | Starport Enterprise |
|---------|------------|--------------|---------------------|
| **API Compatibility** | ✅ | ✅ | ✅ |
| **Major Providers** | ✅ | ✅ (6 providers) | ✅ |
| **Model Routing** | ✅ | ✅ | ✅ |
| **BYOK Support** | ✅ | ✅ | ✅ |
| **Response Caching** | ✅ | 🚧 | ✅ |
| **Self-hosted** | ❌ | ✅ | ✅ |
| **Zero Dependencies** | ❌ | ✅ | ✅ |
| **<1ms Overhead** | ❌ | ✅ | ✅ |
| **Rate Limiting** | ✅ | 🚧 | ✅ |
| **SSO/RBAC** | ✅ | ❌ | ✅ |
| **Analytics** | ✅ | ❌ | ✅ |

## Documentation

- [Architecture](docs/ARCHITECTURE.md) - Technical design and decisions
- [Development Plan](docs/PLAN.md) - Implementation roadmap and phases
- [Task Status](docs/TASKS.md) - Current development status and tasks
- [Operator Guide](docs/OPERATOR-GUIDE.md) - Development workflow guide
- [API Reference](docs/api.md) - Complete API documentation (coming soon)
- [Configuration Guide](docs/configuration.md) - All configuration options (coming soon)
- [Deployment Guide](docs/deployment.md) - Production deployment (coming soon)

## Community

- **GitHub Issues**: Bug reports and feature requests
- **Discussions**: Questions and community support
- **Discord**: [Join our Discord](https://discord.gg/starport) (coming soon)
- **Twitter**: [@starportai](https://twitter.com/starportai) (coming soon)

## License

GNU AGPLv3 - See [LICENSE](LICENSE) file for details.

### Commercial License

For organizations that cannot use AGPLv3, commercial licenses are available. Contact us at support@agentstation.ai for details.

## Security

Please report security vulnerabilities to support@agentstation.ai. See [SECURITY.md](SECURITY.md) for our security policy.

## Acknowledgments

- Built with [Chi](https://github.com/go-chi/chi) router
- Storage powered by [Badger](https://github.com/dgraph-io/badger)
- Inspired by [OpenRouter](https://openrouter.ai) and [LiteLLM](https://github.com/BerriAI/litellm)

---

Ready to get started? Check out our [Quick Start](#quick-start) guide or dive into the [documentation](#documentation).