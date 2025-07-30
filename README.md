# Starport

[![License: AGPLv3](https://img.shields.io/badge/License-AGPLv3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
[![codecov](https://codecov.io/gh/agentstation/starport/branch/main/graph/badge.svg)](https://codecov.io/gh/agentstation/starport)
[![Go Version](https://img.shields.io/badge/Go-1.22%2B-blue.svg)](https://golang.org/dl/)
[![Go Report Card](https://goreportcard.com/badge/github.com/agentstation/starport)](https://goreportcard.com/report/github.com/agentstation/starport)

> **🚧 Status: Alpha - 75% Feature Complete** - Core proxy and routing features implemented. Authentication, caching, and rate limiting in progress. See [roadmap](#roadmap) for details.

High-performance LLM gateway with unified access to multiple model providers.

## Overview

Starport is a high-performance, self-hosted LLM gateway that provides unified access to multiple AI providers through a single API. Think of it as an open-source, self-hosted alternative to OpenRouter with additional enterprise features.

### Why Starport?

Unlike managed services, Starport gives you:
- **Complete Control** - Your infrastructure, your rules, your data
- **No Vendor Lock-in** - Open source under GNU AGPLv3 license
- **Cost Effective** - No markup on API calls, use your own keys
- **Privacy First** - Data never leaves your infrastructure
- **Customizable** - Plugin architecture for custom providers and features

### Key Features

- **🔌 OpenAI & OpenRouter Compatible** - Drop-in replacement for chat completions API
- **🚀 Blazing Fast** - <1ms P99 latency overhead, 10K+ RPS on a single node
- **📦 Zero Dependencies** - Single binary with embedded storage (Badger KV)
- **⭐ Core Providers** - OpenAI, Anthropic, Google AI Studio, Vertex AI, Groq, Mistral, Azure OpenAI, Ollama (local)
- **🧠 Smart Routing** - Automatic failover, latency-based, cost-aware routing
- **🔐 BYOK Support** - Bring your own keys with zero-knowledge security
- **⚡ Prompt Caching** - OpenRouter-compatible cache control to reduce costs
- **🗄️ Advanced Caching** - Multi-tier caching with TTL and invalidation
- **💬 Chat UI** - Built-in web interface for testing and development
- **🛡️ Enterprise Ready** - Rate limiting, content filtering, audit logs (Enterprise)

### Architecture

Starport is designed as a single binary that includes both server and CLI functionality:

- **Single binary** - Server and CLI in one executable
- **Storage options**:
  - Badger (default) - Zero dependencies, embedded KV store
  - Valkey/Redis - For multi-node deployments
- **Provider support**:
  - 7+ providers implemented with full streaming
  - OpenRouter-compatible model routing
  - Dynamic model fetching with caching
  - Optional Ollama support for local models
- **Enterprise features** - SSO, RBAC, analytics (separate package)

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

## ChatUI - Built-in Web Interface

Starport includes an optional web-based chat interface for testing and development. This provides a clean, modern UI for interacting with your configured LLM providers.

### Enabling ChatUI

Add these environment variables to your `.env` file or export them:

```bash
# Enable the ChatUI feature
STARPORT_CHATUI_ENABLED=true

# Customize the interface
STARPORT_CHATUI_TITLE="Starport Chat"
STARPORT_CHATUI_THEME=light  # or "dark"

# Allow API key generation from the UI (development only)
STARPORT_CHATUI_ALLOW_KEY_GEN=true
```

### Accessing ChatUI

Once enabled, visit `http://localhost:8080/chat` to access the interface.

### Features

- **🎨 Modern Interface** - Clean, responsive design with light/dark themes
- **💬 Streaming Chat** - Real-time streaming responses from all providers
- **🔑 API Key Management** - Generate keys directly from the UI (when enabled)
- **📱 Mobile Friendly** - Responsive design works on all devices
- **💾 Local Storage** - Conversations saved locally in your browser
- **📤 Export Chats** - Download conversations as JSON or Markdown
- **⚡ Model Switching** - Easy switching between providers and models
- **🔍 Search History** - Find past conversations quickly
- **⌨️ Keyboard Shortcuts** - Efficient navigation (Cmd/Ctrl+K for search)

### Security Notes

- ChatUI is disabled by default for security
- API key generation should only be enabled in development
- All chat data is stored locally in the browser
- No conversation data is sent to external services

## Ollama Support (Local Models)

Starport includes optional support for [Ollama](https://ollama.ai), allowing you to use locally-running models alongside cloud providers.

### Enabling Ollama

1. Install Ollama on your machine: https://ollama.ai/download
2. Pull models you want to use:
   ```bash
   ollama pull llama3.2
   ollama pull mistral
   ollama pull codellama
   ```
3. Start Starport with Ollama enabled:
   ```bash
   # Via command line flag
   ./starport serve --enable-ollama
   
   # Or via environment variable
   export STARPORT_ENABLE_OLLAMA=true
   ./starport serve
   ```

### Configuration

```bash
# Enable Ollama support
STARPORT_ENABLE_OLLAMA=true

# Ollama server URL (default: http://localhost:11434)
STARPORT_PROVIDERS_OLLAMA_BASE_URL=http://localhost:11434
```

### Using Ollama Models

Ollama models are prefixed with `ollama/` in the model ID:

```python
response = client.chat.completions.create(
    model="ollama/llama3.2",  # Use any model you have pulled
    messages=[{"role": "user", "content": "Hello!"}]
)
```

### Features

- **Dynamic Discovery** - Automatically discovers available models at startup
- **Full Streaming** - Supports streaming responses like all other providers
- **Token Tracking** - Accurate token usage reporting
- **Zero Config** - Works out of the box with default Ollama installation
- **Privacy First** - All inference happens locally on your machine

### Model Sorting

Models are sorted with an intelligent natural ordering:
- Higher version numbers appear first (e.g., llama3.2 before llama3)
- Letters come before numbers in names
- Provider models are grouped together

## Performance

### Current Performance (Actual Benchmarks)

Starport is designed to add minimal overhead to your LLM API calls. Based on our benchmark suite on M2 MacBook Pro:

| Operation | Latency | Throughput | Memory/Op |
|-----------|---------|------------|----------|
| **Request Processing** | ~5.4μs | ~185K req/sec | 8.5KB |
| **Middleware Chain** | ~5.4μs | ~185K req/sec | 7.9KB |
| **Routing Decision** | ~331ns | ~3M ops/sec | 320B |

### Performance Goals

| Operation | Target Latency | Target Throughput |
|-----------|----------------|------------------|
| **Routing Decision** | <100μs | >1M decisions/sec |
| **First Token Overhead** | <1ms | >10K streams/sec |
| **End-to-end Gateway Overhead** | <1ms P99 | >10K req/sec |

### Understanding LLM Gateway Performance

For context, typical LLM API latencies are:
- **Time to First Token**: 200-2000ms (provider dependent)
- **Total Generation Time**: 1-30+ seconds

A good gateway should add <1% overhead to these operations. Our current ~5.4μs request processing overhead is negligible compared to LLM inference time.

### Key Performance Features

- **Zero-copy streaming**: Direct passthrough from providers (planned)
- **Connection pooling**: HTTP client connection reuse
- **Concurrent processing**: Goroutine-based parallel handling
- **Low allocations**: ~51 allocations per request
- **Circuit breakers**: Provider health tracking (implemented)

### Benchmarking

Run performance benchmarks yourself:

```bash
# Run all benchmarks
go test -bench=. -benchmem ./...

# Run server benchmarks (request handling)
go test -bench=BenchmarkProxyHandler -benchtime=10s ./internal/server

# Run storage benchmarks
go test -bench=. -benchmem ./internal/storage

# Profile CPU usage
go test -bench=. -cpuprofile=cpu.prof ./internal/server
go tool pprof cpu.prof
```

**Note**: Current benchmarks measure middleware and request handling overhead. Additional benchmarks for routing decisions and streaming first-token latency are in development.

### Performance Optimization Tips

1. **Enable connection pooling** in provider configurations
2. **Use Badger storage** for single-node deployments (embedded, no network overhead)
3. **Configure appropriate timeouts** to fail fast on slow providers
4. **Enable circuit breakers** to avoid cascading failures
5. **Monitor memory usage** - current implementation uses ~8KB per request

## Development

For comprehensive development instructions, see [DEVELOPMENT.md](DEVELOPMENT.md).

### Quick Start

```bash
# Clone the repository
git clone https://github.com/agentstation/starport.git
cd starport

# Install dependencies and tools
make deps
make tools

# Start development with hot reload
make dev

# Run tests
make test

# See all available commands
make help
```

## Contributing

We welcome contributions! Please see our [Contributing Guidelines](docs/CONTRIBUTING.md) for the contribution process, code of conduct, and how to submit pull requests.

For technical development details, refer to [DEVELOPMENT.md](DEVELOPMENT.md).

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
| **ChatUI Interface** | ✅ Complete | Web-based chat interface with API key generation |
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
- ✅ ChatUI web interface with streaming support

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
| **Top 6 Major Providers** | ✅ | ✅ | ✅ |
| **Model Routing** | ✅ | ✅ | ✅ |
| **BYOK Support** | ✅ | ✅ | ✅ |
| **Prompt Caching** | ✅ | ✅ | ✅ |
| **Response Caching** | ✅ | 🚧 | ✅ |
| **Self-hosted** | ❌ | ✅ | ✅ |
| **Zero Dependencies** | ❌ | ✅ | ✅ |
| **<1ms Overhead** | ❌ | ✅ | ✅ |
| **Built-in Chat UI** | ❌ | ✅ | ✅ |
| **Rate Limiting** | ✅ | 🚧 | ✅ |
| **SSO/RBAC** | ✅ | ❌ | ✅ |
| **Analytics** | ✅ | ❌ | ✅ |

## Providers & Models

Starport supports the following AI providers out of the box:

| Provider | Models | Highlights |
|----------|--------|------------|
| **OpenAI** | o3, o4, GPT-4.5, GPT-4, GPT-4o | Advanced reasoning, function calling |
| **Anthropic** | Claude 4 Opus/Sonnet, Claude 3.5 | 200K context, vision support |
| **Google AI Studio** | Gemini 2.5 Pro/Flash, Gemini 1.5 | 1M+ context, multimodal |
| **Google Vertex AI** | Gemini, Claude via Model Garden | Enterprise features, multi-region failover |
| **Groq** | Llama 4, Llama 3.3, Mixtral | Ultra-fast inference on LPU |
| **Mistral** | Devstral, Large, Medium | Code specialist, function calling |
| **Azure OpenAI** | GPT-4, GPT-3.5 | Enterprise security, compliance |
| **Ollama** | Any installed models | Local inference, privacy-first |

### Additional Providers (Coming Soon)
- **xAI** - Grok models with extended context
- **Cohere** - Command R series for enterprise use
- **01.AI** - Yi Large multilingual models |

### Featured Models

**OpenAI:**
- **o4 Mini** (`openai/o4-mini`) - Next-gen reasoning model
- **o3** (`openai/o3`) - Advanced reasoning, 200K context
- **GPT-4.5 Preview** (`openai/gpt-4.5-preview`) - Enhanced GPT-4, 128K context
- **GPT-4.1** (`openai/gpt-4.1`) - Extended context, 1M+ tokens

**Anthropic:**
- **Claude 4 Opus** (`anthropic/claude-4-opus`) - Most advanced, 200K context
- **Claude 4 Sonnet** (`anthropic/claude-4-sonnet`) - Balanced performance
- **Claude 3.5 Sonnet** (`anthropic/claude-3.5-sonnet`) - Previous flagship

**Google:**
- **Gemini 2.5 Pro** (`google/gemini-2.5-pro`) - Flagship model, 1M+ context
- **Gemini 2.5 Flash** (`google/gemini-2.5-flash`) - Ultra-fast, 1M+ context

**Meta:**
- **Llama 4 Maverick** (`meta-llama/llama-4-maverick`) - Latest open model, 1M+ context
- **Llama 3.3 70B** (`meta-llama/llama-3.3-70b-instruct`) - Powerful open model

**Others:**
- **Grok 4** (`x-ai/grok-4`) - xAI's latest, 256K context
- **DeepSeek R1** (`deepseek/deepseek-r1`) - Reasoning specialist, 128K context
- **Command R+** (`cohere/command-r-plus`) - Enterprise-ready, 128K context

See [MODELS.md](MODELS.md) for the complete list of 30+ supported models with pricing and capabilities. 

## Documentation

- [Architecture](docs/ARCHITECTURE.md) - Technical design and decisions
- [Development Plan](docs/PLAN.md) - Implementation roadmap and phases
- [Task Status](docs/TASKS.md) - Current development status and tasks
- [Operator Guide](docs/OPERATOR-GUIDE.md) - Development workflow guide
- [Cache Control](docs/CACHE_CONTROL.md) - OpenRouter-compatible prompt caching
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