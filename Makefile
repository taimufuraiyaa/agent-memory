APP := agent-memory
BIN_DIR := bin

.PHONY: help build test lint clean setup test-verbose test-coverage bench fmt vet clean-all install-dev

.DEFAULT_GOAL := help

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

setup: ## Install development dependencies and tools
	@echo "Setting up development environment..."
	go mod download
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
	@if command -v npm >/dev/null 2>&1; then \
		echo "Installing dashboard dependencies..."; \
		cd tools/agent-memory/dashboard && npm ci; \
	else \
		echo "⚠️  npm not found - skipping dashboard setup"; \
	fi
	@echo "✓ Development environment ready"

build: ## Build the agent-memory binary
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(APP) ./cmd/agent-memory

install-dev: build ## Build and install locally for development
	go install ./cmd/agent-memory

test: ## Run all tests
	go test ./...

test-verbose: ## Run tests with verbose output
	go test -v -race ./...

test-coverage: ## Generate test coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

bench: ## Run benchmarks
	go test -bench=. -benchmem ./...

fmt: ## Format Go code
	gofmt -s -w .
	go mod tidy

vet: ## Run go vet
	go vet ./...

lint: ## Run linter
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8 run

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)

clean-all: clean ## Remove all build artifacts including coverage reports
	rm -rf coverage.out coverage.html *.prof
	@echo "✓ Cleaned all build artifacts"
