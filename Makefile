APP := agent-memory
BIN_DIR := bin

.PHONY: help build test lint clean setup test-verbose test-coverage bench bench-mem bench-cpu fmt vet clean-all install-dev build-dashboard embed-dashboard build-with-dashboard hygiene-clean

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

build-dashboard: ## Build dashboard assets for embedding
	@echo "Building dashboard assets..."
	@if ! command -v npm >/dev/null 2>&1; then \
		echo "❌ npm is required to build dashboard"; \
		exit 1; \
	fi
	cd tools/agent-memory/dashboard && npm ci && npm run build
	@echo "✓ Dashboard built successfully"

embed-dashboard: build-dashboard ## Copy dashboard assets for embedding
	@echo "Embedding dashboard assets..."
	mkdir -p internal/api/dashboard/dist
	cp -r tools/agent-memory/dashboard/dist/* internal/api/dashboard/dist/
	@echo "✓ Dashboard assets ready for embedding"

build-with-dashboard: embed-dashboard ## Build agent-memory with embedded dashboard
	@echo "Building agent-memory with embedded dashboard..."
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(APP) ./cmd/agent-memory
	@echo "✓ Build complete with embedded dashboard"

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
	go test -bench=. -benchmem -benchtime=100ms ./...

bench-mem: ## Run benchmarks with memory profiling
	@mkdir -p .profiles
	go test -bench=. -benchmem -memprofile=.profiles/mem.prof ./internal/engine
	@echo "Memory profile saved to .profiles/mem.prof"
	@echo "View with: go tool pprof .profiles/mem.prof"

bench-cpu: ## Run benchmarks with CPU profiling
	@mkdir -p .profiles
	go test -bench=. -benchmem -cpuprofile=.profiles/cpu.prof ./internal/engine
	@echo "CPU profile saved to .profiles/cpu.prof"
	@echo "View with: go tool pprof .profiles/cpu.prof"

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

hygiene-clean: ## Remove scratch/debug files and stray committed binaries (see scripts/repo-hygiene-cleanup.sh)
	bash scripts/repo-hygiene-cleanup.sh
