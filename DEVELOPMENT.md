# Development Guide

This guide covers everything you need to know to develop on Starport.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Initial Setup](#initial-setup)
- [Development Workflow](#development-workflow)
- [Make Commands Reference](#make-commands-reference)
- [Testing](#testing)
- [Code Style](#code-style)
- [Building & Releasing](#building--releasing)
- [Common Tasks](#common-tasks)
- [Troubleshooting](#troubleshooting)

## Prerequisites

- **Go 1.22+** - [Install Go](https://golang.org/dl/)
- **Git** - For version control
- **Make** - For running build commands
- **Docker** (optional) - For integration testing with Valkey
- **Air** (optional) - For hot reload development

## Initial Setup

1. **Clone the repository**:
   ```bash
   git clone https://github.com/agentstation/starport.git
   cd starport
   ```

2. **Install dependencies**:
   ```bash
   make deps          # Download Go modules
   make tools         # Install development tools (Air, goimports, golangci-lint)
   ```

3. **Set up your environment** (optional):
   ```bash
   cp .env.example .env
   # Edit .env with your provider API keys
   ```

## Development Workflow

### Hot Reload Development (Recommended)

The fastest way to develop is using Air for automatic rebuilds:

```bash
make dev   # Starts server with hot reload on file changes
```

This watches all Go, HTML, CSS, JS, YAML, and JSON files. Any change triggers an automatic rebuild and restart.

### Manual Development

If you prefer manual control:

```bash
make build         # Build the binary
make run           # Build and run
./starport serve   # Run existing binary
```

## Make Commands Reference

Run `make help` to see all available commands with descriptions.

### 🚀 Development Commands

| Command | Description |
|---------|-------------|
| `make dev` | Start development server with hot-reloading (recommended) |
| `make run` | Build and run the server (no hot reload) |
| `make build-run` | Build and run in one command |

### 🔨 Build Commands

| Command | Description |
|---------|-------------|
| `make build` | Build the standard binary |
| `make build-race` | Build with race detector enabled |
| `make release` | Build optimized production binary |

### 🧪 Testing & Quality

| Command | Description |
|---------|-------------|
| `make test` | Run all tests |
| `make tests` | Alias for test |
| `make test-race` | Run tests with race detector |
| `make test-coverage` | Generate coverage report (coverage.html) |
| `make test-integration` | Run integration tests (requires Docker) |
| `make check` | Run format, lint, and tests |
| `make check-race` | Run all checks with race detection |

### 🎨 Code Formatting

| Command | Description |
|---------|-------------|
| `make format` | Format code using goimports |
| `make fmt` | Alias for format |
| `make lint` | Run golangci-lint |

### 🛠️ Tools

| Command | Description |
|---------|-------------|
| `make tools` | Install all development tools |
| `make install-air` | Install Air for hot-reloading |

### 🐳 Docker

| Command | Description |
|---------|-------------|
| `make dev-docker` | Start development environment |
| `make dev-docker-logs` | View docker-compose logs |
| `make dev-docker-stop` | Stop docker environment |
| `make dev-docker-clean` | Clean docker volumes |

### 🧹 Utilities

| Command | Description |
|---------|-------------|
| `make clean` | Clean build artifacts |
| `make deps` | Install/update dependencies |
| `make version` | Show version information |
| `make ford` | Generate catalog.json |

## Testing

### Running Tests

```bash
# Run all tests
make test

# Run with race detector
make test-race

# Generate coverage report
make test-coverage
# Open coverage.html in your browser

# Run specific package tests
go test -v ./internal/server/...

# Run integration tests (requires Docker)
make test-integration
```

### Writing Tests

- Place test files next to the code they test (`foo_test.go` next to `foo.go`)
- Use table-driven tests for multiple cases
- Aim for 90% coverage on new code
- Mock external dependencies
- Use the `testutil` package for common test helpers

Example test structure:
```go
func TestFeature(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {"valid input", "test", "TEST", false},
        {"empty input", "", "", true},
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := Feature(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("Feature() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if got != tt.want {
                t.Errorf("Feature() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

## Code Style

### Formatting

Code is automatically formatted using `goimports` (or `go fmt` as fallback):

```bash
make format   # Format all Go code
```

### Linting

We use `golangci-lint` for code quality:

```bash
make lint     # Run all linters
```

### Conventions

- Follow [Effective Go](https://golang.org/doc/effective_go)
- Use meaningful variable names
- Add comments for exported functions
- Keep functions small and focused
- Handle errors explicitly
- Use structured logging with zerolog

### Error Handling

```go
// Wrap errors with context
if err != nil {
    return fmt.Errorf("component: action failed: %w", err)
}

// Define package-level sentinel errors
var ErrNotFound = errors.New("key not found")
```

### Logging

```go
log.Info().
    Str("component", "storage").
    Str("action", "initialize").
    Msg("initializing storage backend")
```

## Building & Releasing

### Local Build

```bash
# Development build
make build

# Production build (optimized, stripped)
make release

# Build with race detector
make build-race
```

### Cross-Platform Build

```bash
# macOS
GOOS=darwin GOARCH=amd64 make build

# Linux
GOOS=linux GOARCH=amd64 make build

# Windows
GOOS=windows GOARCH=amd64 make build
```

### Docker Build

```bash
docker build -t starport:latest .
```

## Common Tasks

### Adding a New Provider

1. Create connector in `internal/providers/connectors/`
2. Implement the `Connector` interface
3. Add to registry in `internal/registry/`
4. Add configuration in `internal/config/`
5. Update documentation

### Updating Dependencies

```bash
go get -u github.com/some/package
make deps   # Run go mod tidy
```

### Debugging

1. **Enable debug logging**:
   ```bash
   export STARPORT_LOG_LEVEL=debug
   ```

2. **Use delve debugger**:
   ```bash
   dlv debug ./cmd/starport -- serve
   ```

3. **Profile performance**:
   ```bash
   go test -bench=. -cpuprofile=cpu.prof
   go tool pprof cpu.prof
   ```

## Troubleshooting

### Air not found

```bash
make install-air
# Or manually:
go install github.com/cosmtrek/air@latest
```

### Port already in use

```bash
# Find process using port 8080
lsof -i :8080
# Kill it
kill -9 <PID>
```

### Test failures

1. Check if integration tests need Docker:
   ```bash
   docker-compose up -d valkey
   ```

2. Clear test cache:
   ```bash
   go clean -testcache
   ```

### Build errors

1. Ensure Go version is 1.22+:
   ```bash
   go version
   ```

2. Clean and rebuild:
   ```bash
   make clean
   make deps
   make build
   ```

## Additional Resources

- [Architecture Documentation](docs/ARCHITECTURE.md) - System design
- [Contributing Guidelines](docs/CONTRIBUTING.md) - Contribution process
- [API Documentation](docs/api.md) - API reference
- [Task Tracking](docs/TASKS.md) - Current development status

## Getting Help

- Check existing [GitHub Issues](https://github.com/agentstation/starport/issues)
- Join our [Discord](https://discord.gg/starport) (coming soon)
- Review the [documentation](docs/)

---

Happy coding! 🚀