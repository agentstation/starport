# Supported Models

This document provides a comprehensive list of all models supported by Starport, organized by provider.

> **Last Updated**: January 2025

## Quick Reference

All models use the OpenRouter-compatible format: `provider/model-name`

## OpenAI

### Latest Models (2025)

| Model | Context | Price (per 1K tokens) | Key Features |
|-------|---------|----------------------|--------------|
| `openai/o4-mini` | 200K | $0.0011/$0.0044 | Next generation reasoning model |
| `openai/o3` | 200K | $0.002/$0.008 | Advanced reasoning model |
| `openai/o3-pro` | 200K | $0.02/$0.08 | Pro version with enhanced capabilities |
| `openai/o3-mini` | 200K | $0.0011/$0.0044 | Lightweight o3 variant |
| `openai/gpt-4.5-preview` | 128K | $0.075/$0.15 | Enhanced GPT-4 with better performance |
| `openai/gpt-4.1` | 1M+ | $0.002/$0.008 | Extended context GPT-4 |
| `openai/gpt-4o` | 128K | $0.0025/$0.01 | Optimized GPT-4 variant |
| `openai/gpt-4o-mini` | 128K | $0.00015/$0.0006 | Cost-effective mini version |

### Previous Generation

| Model | Context | Price (per 1K tokens) | Key Features |
|-------|---------|----------------------|--------------|
| `openai/gpt-4-turbo` | 128K | $0.01/$0.03 | Vision support, faster than GPT-4 |
| `openai/gpt-4` | 8K | $0.03/$0.06 | Original GPT-4 |
| `openai/gpt-3.5-turbo` | 16K | $0.0005/$0.0015 | Fast, cost-effective |

**Key Features:**
- Function calling and tools support
- JSON mode and structured outputs
- Vision capabilities (select models)
- Web search (preview models)
- Streaming support

## Anthropic

### Latest Models (2025)

| Model | Context | Price (per 1K tokens) | Key Features |
|-------|---------|----------------------|--------------|
| `anthropic/claude-4-sonnet` | 200K | $0.006/$0.03 | Claude 4 Sonnet - balanced performance |
| `anthropic/claude-4-opus` | 200K | $0.015/$0.075 | Claude 4 Opus - most advanced model |
| `anthropic/claude-3.5-sonnet` | 200K | $0.003/$0.015 | Previous gen, still excellent |
| `anthropic/claude-3.5-haiku` | 200K | $0.0008/$0.004 | Fast, lightweight Claude 3.5 |

*Note: Claude 4 Haiku expected soon to complete the Claude 4 family*

### Previous Generation

| Model | Context | Price (per 1K tokens) | Key Features |
|-------|---------|----------------------|--------------|
| `anthropic/claude-3-opus-20240229` | 200K | $0.015/$0.075 | Most capable Claude 3 |
| `anthropic/claude-3-sonnet-20240229` | 200K | $0.003/$0.015 | Balanced Claude 3 |
| `anthropic/claude-3-haiku-20240307` | 200K | $0.00025/$0.00125 | Fastest Claude 3 |

**Key Features:**
- 200K context window across all models
- Vision capabilities
- Excellent instruction following
- No system prompt (use first user message)

## Google

### Gemini 2.5 Series (Latest)

| Model | Context | Price (per 1K tokens) | Key Features |
|-------|---------|----------------------|--------------|
| `google/gemini-2.5-pro` | 1M+ | $0.00125/$0.01 | Most capable Gemini |
| `google/gemini-2.5-flash` | 1M+ | $0.0003/$0.0025 | Fast multimodal model |
| `google/gemini-2.0-flash-001` | 1M+ | $0.0001/$0.0004 | Previous gen flash model |

### Google AI Studio

| Model | Context | Price (per 1K tokens) | Key Features |
|-------|---------|----------------------|--------------|
| `google-aistudio/gemini-1.5-pro` | 1M+ | $0.0035/$0.0105 | Free tier available |
| `google-aistudio/gemini-1.5-flash` | 1M+ | $0.00035/$0.00105 | Optimized for speed |

### Google Vertex AI

| Model | Context | Price (per 1K tokens) | Key Features |
|-------|---------|----------------------|--------------|
| `google-vertexai/gemini-1.5-pro` | 1M+ | $0.00125/$0.00375 | Enterprise SLAs |
| `google-vertexai/gemini-1.5-flash` | 1M+ | $0.000125/$0.000375 | Enterprise flash model |
| `google-vertexai/claude-3-opus@20240229` | 200K | $0.015/$0.075 | Claude via Model Garden |
| `google-vertexai/claude-3-sonnet@20240229` | 200K | $0.003/$0.015 | Claude via Model Garden |

**Key Features:**
- 1M+ token context window (Gemini models)
- Native multimodal support (text, image, video, audio)
- Model Garden access for third-party models
- Enterprise features on Vertex AI

## Meta (Llama)

### Latest Models (2025)

| Model | Context | Price (per 1K tokens) | Key Features |
|-------|---------|----------------------|--------------|
| `meta-llama/llama-4-maverick` | 1M+ | $0.00015/$0.0006 | Llama 4 - largest model |
| `meta-llama/llama-4-scout` | 1M+ | $0.00008/$0.0003 | Llama 4 - efficient variant |
| `meta-llama/llama-3.3-70b-instruct` | 131K | $0.000038/$0.00012 | Latest Llama 3 release |
| `meta-llama/llama-3.2-90b-vision-instruct` | 131K | $0.0012/$0.0012 | Vision capabilities |
| `meta-llama/llama-3.2-11b-vision-instruct` | 131K | $0.000049/$0.000049 | Smaller vision model |
| `meta-llama/llama-3.1-70b-versatile` | 131K | $0.00059/$0.00079 | Via Groq (fast) |

**Key Features:**
- Open source models
- Vision support in 3.2 series
- Available on multiple providers
- Strong multilingual support

## Groq

| Model | Context | Price (per 1K tokens) | Key Features |
|-------|---------|----------------------|--------------|
| `groq/llama-3.1-70b-versatile` | 131K | $0.00059/$0.00079 | Ultra-fast Llama on LPU |
| `groq/mixtral-8x7b-32768` | 32K | $0.00027/$0.00027 | Fast Mixtral inference |

**Key Features:**
- Ultra-low latency (LPU hardware)
- Open source models only
- High throughput
- Limited rate limits on free tier

## Mistral

### Latest Models

| Model | Context | Price (per 1K tokens) | Key Features |
|-------|---------|----------------------|--------------|
| `mistralai/devstral-medium` | 131K | $0.0004/$0.002 | Code generation specialist |
| `mistralai/devstral-small` | 131K | $0.00009/$0.0003 | Lightweight code model |
| `mistral/mistral-large-latest` | 128K | $0.002/$0.006 | Most capable Mistral |
| `mistral/mistral-medium-latest` | 32K | $0.0027/$0.0081 | Balanced performance |

**Key Features:**
- Function calling support
- JSON mode
- Code-specialized models (Devstral)
- European data residency option

## Azure OpenAI

| Model | Context | Price (per 1K tokens) | Key Features |
|-------|---------|----------------------|--------------|
| `azure/gpt-4-turbo` | 128K | $0.01/$0.03 | GPT-4 Turbo on Azure |
| `azure/gpt-4` | 8K | $0.03/$0.06 | GPT-4 on Azure |
| `azure/gpt-3.5-turbo` | 16K | $0.0005/$0.0015 | GPT-3.5 on Azure |

**Key Features:**
- Enterprise compliance (SOC2, HIPAA, etc.)
- VNet integration
- Managed identity support
- Content filtering built-in

## Other Notable Models

### xAI (Grok)
- `x-ai/grok-4` - 256K context, $0.003/$0.015 - Latest Grok model
- `x-ai/grok-3` - 131K context, $0.003/$0.015 - Advanced reasoning
- `x-ai/grok-3-mini` - 131K context, $0.0003/$0.0005 - Efficient variant
- `x-ai/grok-2-1212` - 131K context, $0.002/$0.01 - Previous generation

### Cohere
- `cohere/command-r-plus` - 128K context, $0.0025/$0.01 - Flagship model
- `cohere/command-r` - 128K context, $0.00015/$0.0006 - Efficient model
- `cohere/command-r7b-12-2024` - 128K context, $0.0000375/$0.00015 - Lightweight

### DeepSeek
- `deepseek/deepseek-r1` - 128K context, $0.00045/$0.00215 - Advanced reasoning model
- `deepseek/deepseek-r1-distill-llama-70b` - 131K context, $0.0001/$0.0004 - Distilled version
- `deepseek/deepseek-v3-base` - 163K context, free tier available

### Qwen
- `qwen/qwen-2.5-72b-instruct` - 32K context, $0.00012/$0.00039
- `qwen/qwen-2.5-coder-32b-instruct` - Code-specialized model

### 01.AI
- `01-ai/yi-large` - 32K context, $0.003/$0.003 - Chinese-English bilingual

## Model Selection Guide

### For Advanced Reasoning
1. `anthropic/claude-4-opus` - Claude 4 Opus, most advanced
2. `openai/o4-mini` - Next-gen reasoning model
3. `deepseek/deepseek-r1` - Specialized reasoning model
4. `openai/o3` - Strong reasoning capabilities
5. `anthropic/claude-4-sonnet` - Claude 4 balanced reasoning

### For Long Context
1. `google/gemini-2.5-pro` - 1M+ tokens
2. `meta-llama/llama-4-maverick` - 1M+ tokens
3. `anthropic/claude-4-opus` - 200K tokens
4. `anthropic/claude-4-sonnet` - 200K tokens
5. `openai/o3` - 200K tokens

### For Speed
1. `groq/llama-3.1-70b-versatile` - Fastest (LPU hardware)
2. `google/gemini-2.5-flash` - Optimized for speed
3. `anthropic/claude-3.5-haiku` - Fast Claude variant

### For Cost Efficiency
1. `google/gemini-2.0-flash-001` - $0.0001/$0.0004
2. `openai/gpt-4o-mini` - $0.00015/$0.0006
3. `meta-llama/llama-3.3-70b-instruct` - $0.000038/$0.00012

### For Code Generation
1. `mistralai/devstral-medium` - Specialized for code
2. `openai/o3` - Strong reasoning for complex code
3. `qwen/qwen-2.5-coder-32b-instruct` - Open code model

### For Multimodal (Vision)
1. `google/gemini-2.5-pro` - Best multimodal support
2. `openai/gpt-4o` - Vision capabilities
3. `meta-llama/llama-3.2-90b-vision-instruct` - Open vision model

## Usage Examples

### Basic Chat Request
```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "anthropic/claude-3.5-sonnet",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### With Model Routing
```bash
# Let Starport choose the best available model
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "models": ["openai/gpt-4.5-preview", "anthropic/claude-3.5-sonnet"],
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### Provider Preferences
```bash
# Prefer certain providers
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llama-3.3-70b-instruct",
    "provider": {
      "order": ["groq", "meta-llama"],
      "require_parameters": true
    },
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

## Provider Configuration

Each provider requires specific environment variables:

### OpenAI
```bash
STARPORT_OPENAI_API_KEY=sk-...
STARPORT_OPENAI_BASE_URL=https://api.openai.com/v1  # Optional
```

### Anthropic
```bash
STARPORT_ANTHROPIC_API_KEY=sk-ant-...
STARPORT_ANTHROPIC_BASE_URL=https://api.anthropic.com  # Optional
```

### Google AI Studio
```bash
STARPORT_GOOGLE_AISTUDIO_API_KEY=...
```

### Google Vertex AI
```bash
STARPORT_GOOGLE_VERTEXAI_PROJECT=your-project-id
STARPORT_GOOGLE_VERTEXAI_LOCATION=us-central1
# Uses Application Default Credentials
```

### Groq
```bash
STARPORT_GROQ_API_KEY=gsk_...
```

### Mistral
```bash
STARPORT_MISTRAL_API_KEY=...
```

### Azure OpenAI
```bash
STARPORT_AZURE_API_KEY=...
STARPORT_AZURE_ENDPOINT=https://YOUR_RESOURCE.openai.azure.com
```

## Notes

1. **Pricing**: Prices shown are for prompt/completion per 1K tokens. Prices are indicative and may vary.

2. **Context Windows**: Maximum context sizes. Actual available context may be less due to system prompts.

3. **Model Availability**: Some models require waitlist access or specific agreements with providers.

4. **Dynamic Models**: Anthropic, Google, and Groq support dynamic model fetching. New models appear automatically.

5. **BYOK Support**: All providers support "Bring Your Own Key" with 5% platform fee when enabled.

6. **Streaming**: All models support streaming responses for real-time output.

7. **Rate Limits**: Each provider has different rate limits. Starport handles retries and failover automatically.

8. **Free Tiers**: Several providers offer free tier access with limited usage (marked in tables).