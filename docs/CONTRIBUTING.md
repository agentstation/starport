# Contributing to Starport

First off, thank you for considering contributing to Starport! It's people like you that make Starport such a great tool. We welcome contributions from everyone, regardless of their experience level.

This document provides guidelines and instructions for contributing to the project. Following these guidelines helps to communicate that you respect the time of the developers managing and developing this open source project. In return, they should reciprocate that respect in addressing your issue, assessing changes, and helping you finalize your pull requests.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [What We're Looking For](#what-were-looking-for)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [How to Contribute](#how-to-contribute)
- [Development Workflow](#development-workflow)
- [Testing Guidelines](#testing-guidelines)
- [Code Style](#code-style)
- [Commit Messages](#commit-messages)
- [Pull Request Process](#pull-request-process)
- [Reporting Issues](#reporting-issues)
- [Security Vulnerabilities](#security-vulnerabilities)
- [Community](#community)
- [License](#license)

## Code of Conduct

This project and everyone participating in it is governed by our [Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold this code. Please report unacceptable behavior to support@agentstation.ai.

In summary:
- Be respectful and inclusive
- Welcome newcomers and help them get started
- Focus on what is best for the community
- Show empathy towards other community members

## What We're Looking For

Starport is an open source project and we love to receive contributions from our community! There are many ways to contribute, from writing tutorials or blog posts, improving the documentation, submitting bug reports and feature requests, or writing code which can be incorporated into Starport itself.

We're particularly interested in:
- **Bug fixes**: Help us squash bugs and improve stability
- **Performance improvements**: Make Starport even faster
- **Provider integrations**: Add support for new LLM providers
- **Documentation**: Help others understand and use Starport
- **Tests**: Improve our test coverage and reliability
- **Examples**: Create examples showing how to use Starport

## Getting Started

1. **Fork the repository** on GitHub
2. **Clone your fork** locally:
   ```bash
   git clone https://github.com/your-username/starport.git
   cd starport
   ```
3. **Add the upstream remote**:
   ```bash
   git remote add upstream https://github.com/agentstation/starport.git
   ```

## Development Setup

### Prerequisites

- Go 1.22 or later
- Make
- Docker (optional, for integration tests)
- golangci-lint (for linting)

### Initial Setup

```bash
# Install dependencies
go mod download

# Install development tools
make tools

# Run tests to verify setup
make test

# Build the binary
make build
```

### Local Development

```bash
# Run with hot reload (requires air)
make dev

# Or run directly
go run ./cmd/starport serve

# Run with custom config
STARPORT_SERVER_PORT=8081 ./starport serve
```

### Using Docker

```bash
# Build Docker image
make docker-build

# Run with Docker Compose
make dev-docker

# Run integration tests
make test-integration
```

## How to Contribute

### Types of Contributions

- **Bug Fixes**: Fix identified bugs and add tests
- **Features**: Implement new features with tests and documentation
- **Documentation**: Improve or add documentation
- **Tests**: Add missing tests or improve test coverage
- **Performance**: Optimize code for better performance
- **Refactoring**: Improve code quality and maintainability

### Finding Issues

- Check [open issues](https://github.com/agentstation/starport/issues)
- Look for issues labeled `good first issue` or `help wanted`
- Ask in discussions if you need help finding something to work on

## Development Workflow

### 1. Create a Feature Branch

```bash
# Update your local master
git checkout master
git pull upstream master

# Create a feature branch
git checkout -b feature/your-feature-name
```

### 2. Make Your Changes

- Write clean, idiomatic Go code
- Add tests for new functionality
- Update documentation as needed
- Ensure all tests pass

### 3. Run Tests and Linters

```bash
# Run all tests
make test

# Run tests with coverage
make test-coverage

# Run linters
make lint

# Run specific tests
go test -v -run TestName ./internal/package
```

### 4. Commit Your Changes

Follow our commit message conventions (see below).

### 5. Push and Create PR

```bash
# Push your branch
git push origin feature/your-feature-name

# Create a pull request on GitHub
```

## Testing Guidelines

### Test Requirements

- All new features must include tests
- Bug fixes should include a test that reproduces the bug
- Aim for 90% test coverage on new code
- Use table-driven tests where appropriate

### Running Tests

```bash
# Unit tests
make test

# Integration tests (requires Docker)
make test-integration

# Benchmarks
make bench

# Specific package
go test -v ./internal/server

# With race detector
go test -race ./...
```

### Writing Tests

```go
// Example table-driven test
func TestFeature(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {
            name:  "valid input",
            input: "test",
            want:  "TEST",
        },
        {
            name:    "empty input",
            input:   "",
            wantErr: true,
        },
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

### Go Code

- Follow standard Go conventions
- Use `gofmt` for formatting
- Follow [Effective Go](https://golang.org/doc/effective_go.html)
- Use meaningful variable and function names
- Add comments for exported functions and types
- Avoid deep nesting - prefer early returns

### Linting

We use `golangci-lint` with a custom configuration:

```bash
# Run all linters
make lint

# Run specific linter
golangci-lint run --enable=gocyclo

# Auto-fix issues
golangci-lint run --fix
```

### Error Handling

```go
// Good: Wrap errors with context
if err := doSomething(); err != nil {
    return fmt.Errorf("failed to do something: %w", err)
}

// Good: Define sentinel errors
var ErrNotFound = errors.New("resource not found")

// Bad: Generic error messages
return errors.New("error occurred")
```

## Commit Messages

We follow the [Conventional Commits](https://www.conventionalcommits.org/) specification:

### Format

```
<type>(<scope>): <subject>

<body>

<footer>
```

### Types

- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `style`: Code style changes (formatting, etc.)
- `refactor`: Code refactoring
- `perf`: Performance improvements
- `test`: Adding or updating tests
- `chore`: Maintenance tasks
- `ci`: CI/CD changes

### Examples

```bash
# Feature
git commit -m "feat(routing): add latency-based provider selection"

# Bug fix
git commit -m "fix(auth): correctly validate API key format"

# Documentation
git commit -m "docs: update contributing guidelines"

# Multiple line commit
git commit -m "feat(cache): implement distributed caching

- Add Redis support for multi-node deployments
- Implement cache invalidation via pub/sub
- Add cache metrics for monitoring

Closes #123"
```

## Pull Request Process

### Before Submitting

1. **Update from upstream**:
   ```bash
   git fetch upstream
   git rebase upstream/master
   ```

2. **Run all tests and linters**:
   ```bash
   make test
   make lint
   ```

3. **Update documentation** if needed

4. **Add tests** for new functionality

### PR Guidelines

- **Title**: Use a clear, descriptive title
- **Description**: Explain what changes you made and why
- **Testing**: Describe how you tested your changes
- **Screenshots**: Include if relevant (UI changes)
- **Breaking Changes**: Clearly mark if applicable

### PR Template

```markdown
## Description
Brief description of changes

## Type of Change
- [ ] Bug fix
- [ ] New feature
- [ ] Breaking change
- [ ] Documentation update

## Testing
- [ ] Unit tests pass
- [ ] Integration tests pass
- [ ] Manual testing completed

## Checklist
- [ ] Code follows project style guidelines
- [ ] Self-review completed
- [ ] Comments added for complex code
- [ ] Documentation updated
- [ ] Tests added/updated
- [ ] No new linting warnings
```

### Review Process

1. At least one maintainer must review
2. All CI checks must pass
3. No merge conflicts
4. Follows project conventions

## Reporting Issues

### Bug Reports

Include:
- Clear description of the bug
- Steps to reproduce
- Expected behavior
- Actual behavior
- System information (OS, Go version)
- Relevant logs or error messages

### Feature Requests

Include:
- Problem you're trying to solve
- Proposed solution
- Alternative solutions considered
- Additional context

### Use Issue Templates

We provide templates for:
- Bug reports
- Feature requests
- Documentation improvements

## Security Vulnerabilities

**DO NOT** create public issues for security vulnerabilities.

Instead:
1. Email security details to support@agentstation.ai
2. Include steps to reproduce
3. Wait for confirmation before disclosure

See [SECURITY.md](SECURITY.md) for full policy.

## Community

### Getting Help

- **GitHub Discussions**: Ask questions and share ideas
- **Issues**: Report bugs and request features
- **Discord**: Join our community (coming soon)

### Stay Updated

- Watch the repository for updates
- Subscribe to releases
- Follow our blog (coming soon)

## Recognition

Contributors will be:
- Listed in our CONTRIBUTORS file
- Mentioned in release notes
- Given credit in documentation

## License

By contributing to Starport, you agree that your contributions will be licensed under the GNU AGPLv3 License that covers the project. Feel free to contact the maintainers if that's a concern.

## Questions?

Don't hesitate to ask questions! You can:
- Open an issue with your question
- Start a discussion in GitHub Discussions
- Email us at support@agentstation.ai

Thank you for contributing to Starport! 🚀