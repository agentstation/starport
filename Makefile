# Starport Makefile
MAKEFLAGS += --no-print-directory

# Variables
BINARY_NAME=starport
ifeq ($(OS),Windows_NT)
    BINARY_NAME=starport.exe
endif
GO=go
GOFLAGS=-v
BUILD_DIR=.
MAIN_PATH=./cmd/starport

# Version information
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "v0.1.0-dev")
BUILD_TIME = $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT = $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_BRANCH = $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
GO_VERSION = $(shell go version | awk '{print $$3}')

# Build flags
LDFLAGS = -ldflags "\
	-X main.version=$(VERSION) \
	-X main.buildTime=$(BUILD_TIME) \
	-X main.gitCommit=$(GIT_COMMIT) \
	-X main.gitBranch=$(GIT_BRANCH) \
	-X main.goVersion=$(GO_VERSION)"

# Default target
.PHONY: all
all: help

##@ General

.PHONY: help
help: ## Display this help message
	@awk 'BEGIN {FS = ":.*##"; printf "\n\033[1mUsage:\033[0m\n  make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } \
		/^###/ { printf "  \033[90m%s\033[0m\n", substr($$0, 4) }' $(MAKEFILE_LIST)

##@ Development
### Use 'make dev' for hot-reload development mode

.PHONY: dev
dev: check-air ## Start development server with hot-reloading (recommended)
	@echo "Starting development server with hot reload..."
	@air

.PHONY: run
run: build ## Build and run the server (no hot reload)
	./$(BINARY_NAME) serve

.PHONY: build-run
build-run: ## Build and run in one command
	@$(MAKE) build
	@$(MAKE) run

##@ Build

.PHONY: build
build: ## Build the binary
	@echo "Building $(BINARY_NAME)..."
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

.PHONY: build-race
build-race: ## Build with race detector enabled
	@echo "Building with race detector..."
	$(GO) build -race $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete with race detector"

.PHONY: release
release: ## Build optimized production binary
	@echo "Building release version..."
	$(GO) build -trimpath -ldflags "-s -w \
		-X main.version=$(VERSION) \
		-X main.buildTime=$(BUILD_TIME) \
		-X main.gitCommit=$(GIT_COMMIT) \
		-X main.gitBranch=$(GIT_BRANCH) \
		-X main.goVersion=$(GO_VERSION)" \
		-o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Release build complete"
	@echo "Binary size: $$(du -h $(BUILD_DIR)/$(BINARY_NAME) | cut -f1)"

##@ Testing & Quality
### Run 'make check' for all quality checks

.PHONY: test
test: ## Run all tests
	@echo "Running tests..."
	$(GO) test $(GOFLAGS) ./...

.PHONY: tests
tests: test ## Alias for test

.PHONY: test-race
test-race: ## Run tests with race detector
	@echo "Running tests with race detector..."
	$(GO) test -race $(GOFLAGS) ./...

.PHONY: test-coverage
test-coverage: ## Run tests with coverage report
	@echo "Running tests with coverage..."
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"
	@$(GO) tool cover -func=coverage.out | grep total | awk '{print "Total coverage: " $$3}'

.PHONY: test-integration
test-integration: ## Run integration tests with docker-compose
	@echo "Starting Valkey for integration tests..."
	docker-compose up -d valkey
	@echo "Waiting for Valkey to be ready..."
	@sleep 3
	@echo "Running integration tests..."
	TEST_VALKEY_URL=valkey://localhost:6379 $(GO) test -v ./internal/storage -run TestValkey
	TEST_VALKEY_URL=valkey://localhost:6379 $(GO) test -v ./internal/cache -run TestValkeyIntegration
	TEST_VALKEY_URL=valkey://localhost:6379 $(GO) test -v ./internal/app -run TestAppWithValkey
	@echo "Stopping Valkey..."
	docker-compose stop valkey
	@echo "Integration tests complete"

.PHONY: check
check: format lint test ## Run all checks (format, lint, test)

.PHONY: check-race
check-race: format lint test-race ## Run all checks with race detection

.PHONY: format
format: ## Format code using goimports (or go fmt)
	@echo "Formatting code..."
	@if command -v goimports >/dev/null 2>&1; then \
		echo "Using goimports..."; \
		goimports -w $$(find . -type f -name '*.go' -not -path "./vendor/*" -not -path "./tmp/*"); \
	else \
		echo "Using go fmt..."; \
		$(GO) fmt ./...; \
	fi
	@echo "Format complete"

.PHONY: fmt
fmt: format ## Alias for format

.PHONY: lint
lint: ## Lint code
	@echo "Linting code..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed, running basic go vet..."; \
		$(GO) vet ./...; \
	fi
	@echo "Lint complete"

##@ Tools
### Install development tools with 'make tools'

.PHONY: check-air
check-air:
	@if ! command -v air &> /dev/null; then \
		echo "Error: Air not found."; \
		echo "Install with: make install-air"; \
		exit 1; \
	fi

.PHONY: install-air
install-air: ## Install Air for hot-reloading
	@echo "Installing Air..."
	@go install github.com/cosmtrek/air@latest
	@echo "Air installed successfully!"

.PHONY: tools
tools: install-air ## Install all development tools
	@echo "Installing development tools..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@go install golang.org/x/tools/cmd/goimports@latest
	@echo "Optional: Install gofumpt for stricter formatting:"
	@echo "  go install mvdan.cc/gofumpt@latest"
	@echo "All tools installed!"

##@ Dependencies

.PHONY: deps
deps: ## Install dependencies
	@echo "Installing dependencies..."
	$(GO) mod download
	$(GO) mod tidy
	@echo "Dependencies installed"

##@ Docker
### Requires: Docker and docker-compose installed

.PHONY: dev-docker
dev-docker: ## Start development environment with docker-compose
	@echo "Starting development environment with docker-compose..."
	docker-compose up -d
	@echo "Development environment started:"
	@echo "  - Starport: http://localhost:8080"
	@echo "  - Valkey: localhost:6379"
	@echo "Use 'make dev-docker-logs' to view logs"

.PHONY: dev-docker-logs
dev-docker-logs: ## View docker-compose logs
	docker-compose logs -f

.PHONY: dev-docker-stop
dev-docker-stop: ## Stop docker-compose environment
	@echo "Stopping development environment..."
	docker-compose down
	@echo "Development environment stopped"

.PHONY: dev-docker-clean
dev-docker-clean: ## Clean docker-compose volumes
	@echo "Cleaning development environment..."
	docker-compose down -v
	@echo "Development environment cleaned"

##@ Utilities

.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -f $(BUILD_DIR)/$(BINARY_NAME)
	rm -rf tmp/
	rm -f coverage.out coverage.html
	rm -f build-errors.log
	$(GO) clean -testcache
	@echo "Clean complete"

.PHONY: version
version: build ## Show version information
	./$(BINARY_NAME) version

