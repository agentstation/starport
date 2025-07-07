# Starport Makefile

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
all: build

# Build the binary
.PHONY: build
build:
	@echo "Building $(BINARY_NAME)..."
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

# Run tests
.PHONY: test
test:
	@echo "Running tests..."
	$(GO) test $(GOFLAGS) ./...

# Run tests with coverage
.PHONY: test-coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"
	@$(GO) tool cover -func=coverage.out | grep total | awk '{print "Total coverage: " $$3}'

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning..."
	rm -f $(BUILD_DIR)/$(BINARY_NAME)
	$(GO) clean -testcache
	@echo "Clean complete"

# Format code
.PHONY: fmt
fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...
	@echo "Format complete"

# Lint code
.PHONY: lint
lint:
	@echo "Linting code..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed, running basic go vet..."; \
		$(GO) vet ./...; \
	fi
	@echo "Lint complete"

# Development build (with race detector)
.PHONY: dev
dev:
	@echo "Building development version with race detector..."
	$(GO) build -race $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "Development build complete"

# Release build (optimized, stripped)
.PHONY: release
release:
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

# Install dependencies
.PHONY: deps
deps:
	@echo "Installing dependencies..."
	$(GO) mod download
	$(GO) mod tidy
	@echo "Dependencies installed"

# Run the server
.PHONY: run
run: build
	./$(BINARY_NAME) serve

# Show version
.PHONY: version
version: build
	./$(BINARY_NAME) version

# Help target
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  make build         - Build the binary"
	@echo "  make release       - Build optimized release binary"
	@echo "  make test          - Run tests"
	@echo "  make test-coverage - Run tests with coverage report"
	@echo "  make clean         - Clean build artifacts"
	@echo "  make fmt           - Format code"
	@echo "  make lint          - Lint code"
	@echo "  make dev           - Build with race detector"
	@echo "  make deps          - Install dependencies"
	@echo "  make run           - Build and run the server"
	@echo "  make version       - Show version"
	@echo "  make help          - Show this help message"