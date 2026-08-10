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
GORELEASER_VERSION=2.17.1
GOLANGCI_LINT_VERSION=v2.12.2
AIR_VERSION=v1.67.4
GOIMPORTS_VERSION=v0.48.0
VALKEY_INTEGRATION_PORT ?= 16379
INTEGRATION_COMPOSE=docker compose -f docker-compose.yml -f docker-compose.integration.yml

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
build-run: run ## Alias for build and run

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
test-integration: ## Run Valkey integration tests with Docker Compose
	@set -eu; \
		export COMPOSE_PROJECT_NAME=starport-integration-test-$$$$; \
		export STARPORT_SECURITY_MASTER_KEY=integration-test-master-key-0001; \
		export STARPORT_PROVIDERS_OPENAI_API_KEY=integration-test-provider-key; \
		export STARPORT_VALKEY_PORT=$(VALKEY_INTEGRATION_PORT); \
		trap '$(INTEGRATION_COMPOSE) down --volumes --remove-orphans' EXIT INT TERM; \
		$(INTEGRATION_COMPOSE) up -d --wait valkey; \
		TEST_VALKEY_URL=valkey://localhost:$(VALKEY_INTEGRATION_PORT) $(GO) test -count=1 -v ./internal/storage -run 'Test(Valkey|KVStoreContract)'; \
		TEST_VALKEY_URL=valkey://localhost:$(VALKEY_INTEGRATION_PORT) $(GO) test -count=1 -v ./internal/cache -run TestValkey; \
		TEST_VALKEY_URL=valkey://localhost:$(VALKEY_INTEGRATION_PORT) $(GO) test -count=1 -v ./internal/app -run TestAppWithValkey; \
		TEST_VALKEY_URL=valkey://localhost:$(VALKEY_INTEGRATION_PORT) $(GO) test -count=1 -v \
			./internal/credentials ./internal/identity ./internal/presets ./internal/ratelimit \
			-run RepositoryContract

.PHONY: check
check: format-check lint test verify ## Run all read-only checks

.PHONY: check-race
check-race: format-check lint test-race verify ## Run all read-only checks with race detection

.PHONY: verify
verify: ## Run deterministic architecture and release contract checks
	bash scripts/verify-starmap-ownership.sh
	bash scripts/verify-v1-architecture.sh
	bash scripts/verify-v1-release.sh
	bash scripts/verify-release-workflow.sh
	bash scripts/verify-developer-experience.sh
	bash scripts/verify-doc-links.sh
	bash scripts/test-doc-link-verifier.sh

.PHONY: release-check
release-check: verify ## Check the release configuration and online action provenance
	@command -v goreleaser >/dev/null 2>&1 || (echo "goreleaser $(GORELEASER_VERSION) is required"; exit 1)
	@test "$$(goreleaser --version | awk '/GitVersion:/ {sub(/^v/, "", $$2); print $$2}')" = "$(GORELEASER_VERSION)" || \
		(echo "goreleaser $(GORELEASER_VERSION) is required"; exit 1)
	goreleaser check
	bash scripts/verify-action-pins.sh

.PHONY: release-snapshot
release-snapshot: release-check ## Build and verify the complete local release snapshot
	@command -v syft >/dev/null 2>&1 || (echo "syft is required"; exit 1)
	goreleaser release --snapshot --clean --skip=notarize
	scripts/verify-release-binaries.sh dist
	scripts/verify-release-archives.sh dist
	scripts/verify-homebrew-cask.sh dist/homebrew/Casks/starport.rb
	scripts/audit-homebrew-cask.sh dist/homebrew/Casks/starport.rb

.PHONY: format-check
format-check: ## Check Go formatting without changing files
	@if ! unformatted="$$(gofmt -l $$(find . -type f -name '*.go' -not -path './vendor/*' -not -path './tmp/*'))"; then \
		echo "gofmt failed"; \
		exit 1; \
	fi; \
		if [ -n "$$unformatted" ]; then \
			echo "gofmt is required for:"; \
			echo "$$unformatted"; \
			exit 1; \
		fi
	@if ! unformatted="$$(go run golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION) -l $$(find . -type f -name '*.go' -not -path './vendor/*' -not -path './tmp/*'))"; then \
		echo "goimports failed"; \
		exit 1; \
	fi; \
		if [ -n "$$unformatted" ]; then \
			echo "goimports is required for:"; \
			echo "$$unformatted"; \
			exit 1; \
		fi

.PHONY: format
format: ## Format Go code with the pinned goimports version
	@echo "Formatting code..."
	@go run golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION) -w \
		$$(find . -type f -name '*.go' -not -path './vendor/*' -not -path './tmp/*')
	@echo "Format complete"

.PHONY: fmt
fmt: format ## Alias for format

.PHONY: lint
lint: ## Lint code
	@echo "Linting code..."
	@$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run
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
	@go install github.com/air-verse/air@$(AIR_VERSION)
	@echo "Air installed successfully!"

.PHONY: tools
tools: install-air ## Install all development tools
	@echo "Installing development tools..."
	@go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@go install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)
	@echo "All tools installed!"

##@ Dependencies

.PHONY: deps
deps: ## Install dependencies
	@echo "Downloading dependencies..."
	$(GO) mod download
	@echo "Dependencies downloaded"

.PHONY: tidy
tidy: ## Update Go module metadata
	$(GO) mod tidy

##@ Docker
### Requires Docker with the Compose plugin

.PHONY: dev-docker
dev-docker: ## Start the Compose development environment
	@echo "Starting development environment with Docker Compose..."
	docker compose up -d
	@echo "Development environment started:"
	@echo "  - Starport: http://localhost:8080"
	@echo "  - Valkey: localhost:6379"
	@echo "Use 'make dev-docker-logs' to view logs"

.PHONY: dev-docker-logs
dev-docker-logs: ## View Compose logs
	docker compose logs -f

.PHONY: dev-docker-stop
dev-docker-stop: ## Stop the Compose environment
	@echo "Stopping development environment..."
	docker compose down
	@echo "Development environment stopped"

.PHONY: dev-docker-clean
dev-docker-clean: ## Remove the Compose environment and its volumes
	@echo "Cleaning development environment..."
	docker compose down -v
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
