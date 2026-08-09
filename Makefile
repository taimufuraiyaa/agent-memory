APP := agent-memory
BIN_DIR := bin

.PHONY: help build test integration-test lint clean setup test-verbose test-coverage bench bench-mem bench-cpu fmt vet clean-all install-dev build-dashboard embed-dashboard build-with-dashboard hygiene-clean contracts-check saas-build saas-dev-up saas-dev-down saas-local-up saas-floci-up saas-floci-oidc-up saas-local-down saas-local-profile-test saas-local-alpha-gate saas-local-alpha-gate-test saas-smoke saas-upload-smoke saas-lifecycle-script-test saas-integration-test saas-object-policy-test saas-kubernetes-check saas-release-script-test saas-external-evidence-check

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
	go build -trimpath -o $(BIN_DIR)/$(APP) ./cmd/agent-memory

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
	go build -trimpath -o $(BIN_DIR)/$(APP) ./cmd/agent-memory
	@echo "✓ Build complete with embedded dashboard"

install-dev: build ## Build and install locally for development
	go install ./cmd/agent-memory

test: ## Run all tests
	go test ./...

integration-test: ## Run integration parity, privacy, benchmark, MCP, and dashboard gates
	go test ./internal/application ./internal/api ./internal/cli ./internal/connectors ./internal/hooks ./internal/replay ./internal/storage/sqlite
	python3 -m unittest benchmark.test_benchmark.IntegrationReliabilityFixtureTest
	cd tools/agent-memory/mcp-server && npm test && npm run build
	cd tools/agent-memory/dashboard && npm run build

contracts-check: ## Validate hosted API and event contracts
	go test ./internal/contracts

saas-build: contracts-check ## Build the hosted edge, API, worker, reconciler, and migration images
	docker compose -f deploy/saas/compose.yaml build edge api worker reconciler migrate

saas-dev-up: ## Start the local hosted-service dependencies and workloads
	docker compose -f deploy/saas/compose.yaml up -d --build --wait

saas-dev-down: ## Stop the local hosted-service environment
	docker compose -f deploy/saas/compose.yaml down

saas-local-up: ## Start the complete persistent local product with MinIO
	docker compose -f deploy/saas/compose.yaml up -d --build --wait --remove-orphans
	curl --fail --silent --show-error http://localhost:58081/_edge/health/ready
	@echo "Agent Memory local dashboard: http://localhost:58081/dashboard/"

saas-floci-up: ## Start the local product with Floci providing AWS-compatible S3
	docker compose -f deploy/saas/compose.yaml -f deploy/saas/compose.floci.yaml up -d --build --wait --remove-orphans
	curl --fail --silent --show-error http://localhost:58081/_edge/health/ready
	@echo "Agent Memory Floci dashboard: http://localhost:58081/dashboard/"

saas-floci-oidc-up: ## Start Floci with the development-only managed-identity rehearsal
	docker compose -f deploy/saas/compose.yaml -f deploy/saas/compose.floci.yaml -f deploy/saas/compose.oidc.yaml up -d --build --wait --remove-orphans
	curl --fail --silent --show-error http://localhost:58081/_edge/health/ready
	@echo "Agent Memory Floci + OIDC dashboard: http://localhost:58081/dashboard/"
	@echo "Synthetic OIDC token endpoint (development only): http://localhost:58082/token"

saas-local-down: ## Stop either local product profile while preserving volumes
	docker compose -f deploy/saas/compose.yaml -f deploy/saas/compose.floci.yaml -f deploy/saas/compose.oidc.yaml down

saas-local-profile-test: ## Validate local MinIO and Floci deployment contracts
	scripts/tests/saas-local-profiles_test.sh

saas-local-alpha-gate: ## Produce a content-free evidence package from an isolated Floci alpha run
	scripts/saas-local-alpha-gate.sh

saas-local-alpha-gate-test: ## Validate the local alpha evidence and isolation contracts
	scripts/tests/saas-local-alpha-gate_test.sh
	GOCACHE=/tmp/agent-memory-go-cache go test ./cmd/agent-memory-local-evidence ./internal/saas/localevidence

saas-smoke: ## Verify the local hosted API is live and ready
	curl --fail --silent --show-error http://localhost:58081/_edge/health/live
	curl --fail --silent --show-error http://localhost:58081/_edge/health/ready

saas-upload-smoke: ## Exercise the complete local SaaS lifecycle across every retained format
	scripts/saas-upload-smoke.sh

saas-lifecycle-script-test: ## Validate the full-lifecycle smoke contract without starting services
	scripts/tests/saas-lifecycle-smoke_test.sh

saas-kubernetes-check: ## Validate hosted Kubernetes isolation and immutable workload policy
	scripts/validate-saas-kubernetes.sh

saas-release-script-test: ## Verify migration-before-rollout and automatic rollback behavior
	scripts/tests/saas-kubernetes-release_test.sh

saas-external-evidence-check: ## Verify the signed P0-P12 external-evidence index
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-external-evidence \
		--catalog api/evidence/v1/external-control-catalog.json \
		--index "$(EVIDENCE_INDEX)" \
		--artifacts-root "$(EVIDENCE_ROOT)" \
		--trust "$(EVIDENCE_TRUST)" \
		--approvals-dir "$(EVIDENCE_APPROVALS)"

saas-integration-test: contracts-check saas-object-policy-test ## Run hosted package tests and local smoke checks
	docker compose -f deploy/saas/compose.yaml stop worker reconciler
	status=0; AGENT_MEMORY_TEST_POSTGRES_URL='postgres://agent_memory:local-development@127.0.0.1:55432/agent_memory?sslmode=disable' AGENT_MEMORY_TEST_QUEUE_URL='nats://127.0.0.1:54222' AGENT_MEMORY_TEST_OBJECT_ENDPOINT='http://127.0.0.1:59000' AGENT_MEMORY_TEST_OBJECT_ACCESS_KEY='agent-memory-worker' AGENT_MEMORY_TEST_OBJECT_SECRET_KEY='worker-local-development-only' go test -p 1 -count=1 ./internal/saas/... ./evaluation/parity || status=$$?; docker compose -f deploy/saas/compose.yaml start worker reconciler; exit $$status
	$(MAKE) saas-smoke

saas-object-policy-test: ## Verify service-specific MinIO capabilities
	docker compose -f deploy/saas/compose.yaml --profile test run --rm object-policy-test

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
