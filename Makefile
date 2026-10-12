APP := agent-memory
BIN_DIR := bin

.PHONY: help build test integration-test lint clean setup test-verbose test-coverage bench bench-mem bench-cpu fmt vet clean-all install-dev hygiene-clean contracts-check graphrag-adapter-supply-chain graphrag-adapter-container-test observability-validate graphrag-evaluate

.DEFAULT_GOAL := help

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-32s\033[0m %s\n", $$1, $$2}'

setup: ## Install development dependencies and tools
	@echo "Setting up development environment..."
	go mod download
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
	@echo "Development environment ready"

build: ## Build the local agent-memory binary
	mkdir -p $(BIN_DIR)
	go build -trimpath -o $(BIN_DIR)/$(APP) ./cmd/agent-memory
	cp $(BIN_DIR)/$(APP) $(BIN_DIR)/am

install-dev: build ## Build and install local binaries
	go install ./cmd/agent-memory ./cmd/am

test: ## Run all Go tests
	go test ./...

integration-test: ## Run local integration, MCP, and TUI gates
	go test ./internal/application ./internal/api ./internal/cli ./internal/connectors ./internal/hooks ./internal/replay ./internal/storage/sqlite
	python3 -m unittest benchmark.test_benchmark.IntegrationReliabilityFixtureTest
	cd tools/agent-memory/mcp-server && npm test && npm run build
	go test ./internal/tui

contracts-check: ## Validate retained local contracts
	go test ./internal/contracts

graphrag-adapter-supply-chain: ## Generate and verify GraphRAG adapter SBOM, licenses, vulnerabilities, and signature
	$(MAKE) -C tools/graphrag-adapter supply-chain

graphrag-adapter-container-test: ## Verify the frozen non-root GraphRAG adapter image
	$(MAKE) -C tools/graphrag-adapter container-test

observability-validate: ## Validate retained content-safe metrics
	go test ./internal/observability -run 'SkillLifecycleMetrics'

graphrag-evaluate: ## Run the deterministic GraphRAG quality, latency, grounding, isolation, and cost gate
	go test ./internal/evaluation -run 'GraphRAG' -count=1
	go run ./tools/evaluation/graphrag-report

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

bench-cpu: ## Run benchmarks with CPU profiling
	@mkdir -p .profiles
	go test -bench=. -benchmem -cpuprofile=.profiles/cpu.prof ./internal/engine
	@echo "CPU profile saved to .profiles/cpu.prof"

fmt: ## Format Go code and tidy modules
	gofmt -s -w .
	go mod tidy

vet: ## Run go vet
	go vet ./...

lint: ## Run the Go linter
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8 run

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)

clean-all: clean ## Remove build artifacts and coverage reports
	rm -rf coverage.out coverage.html *.prof
	@echo "Cleaned all build artifacts"

hygiene-clean: ## Remove scratch/debug files and stray committed binaries
	bash scripts/repo-hygiene-cleanup.sh
