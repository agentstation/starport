# Configuration Package

This package provides a comprehensive configuration system for Starport with support for:

- Environment variables
- `.env` files (with `local.env` taking precedence)
- Type-safe configuration with validation
- Hot reload for rate limit rules

## Configuration Loading

The configuration is loaded in the following order of precedence:

1. Environment variables
2. `local.env` file (if exists)
3. `.env` file (if exists)
4. Default values

## Configuration Structure

The main configuration struct includes:

- **Server**: HTTP server settings (port, timeouts, etc.)
- **Storage**: Storage backend configuration (Badger or Valkey)
- **Providers**: LLM provider settings (OpenAI, Anthropic, Google AI Studio, Vertex AI, Groq, Mistral, Azure, Ollama)
- **RateLimiting**: Rate limiting configuration with hot reload support
- **Security**: Security settings (TLS, CORS, JWT)
- **Logging**: Logging configuration

## Environment Variables

All configuration can be set via environment variables with the `STARPORT_` prefix:

```bash
STARPORT_SERVER_PORT=8080
STARPORT_STORAGE_MODE=badger
STARPORT_LOGGING_LEVEL=info
```

See `.env.example` for a complete list of available variables.

## Hot Reload

Rate limit rules can be hot-reloaded from a YAML file without restarting the server:

```yaml
# config/rate_limits.yaml
version: "1.0"
rules:
  "sk-premium-key":
    requests_per_minute: 600
    tokens_per_minute: 1000000
models:
  "gpt-4":
    requests_per_minute: 20
    tokens_per_minute: 40000
```

Enable hot reload by setting:
```bash
STARPORT_RATE_LIMITING_ENABLE_HOT_RELOAD=true
STARPORT_RATE_LIMITING_CONFIG_PATH=./config/rate_limits.yaml
```

## Usage

```go
// Load configuration
cfg, err := config.LoadWithDefaults(ctx)
if err != nil {
    log.Fatal(err)
}

// Initialize hot reloader if enabled
if cfg.RateLimiting.EnableHotReload {
    hotReloader, err := config.NewHotReloader(
        cfg.RateLimiting.ConfigPath,
        cfg.RateLimiting.ReloadCheckInterval,
    )
    if err == nil {
        hotReloader.Start(ctx)
        defer hotReloader.Stop()
    }
}
```

## Validation

All configuration is validated on load. The validation includes:

- Port numbers must be between 1-65535
- Timeouts must be positive
- Storage modes must be "badger" or "valkey"
- Log levels must be valid (trace, debug, info, warn, error, fatal, panic)
- TLS certificate paths must exist if TLS is enabled
- Rate limit values must be non-negative