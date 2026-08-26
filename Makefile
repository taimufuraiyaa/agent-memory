APP := agent-memory
BIN_DIR := bin

.PHONY: help build test integration-test lint clean setup test-verbose test-coverage bench bench-mem bench-cpu fmt vet clean-all install-dev build-dashboard embed-dashboard build-with-dashboard hygiene-clean contracts-check graphrag-adapter-supply-chain graphrag-adapter-container-test saas-build saas-dev-up saas-dev-down saas-local-up saas-floci-up saas-floci-oidc-up saas-local-down saas-local-profile-test saas-local-alpha-gate saas-local-alpha-gate-test saas-smoke saas-upload-smoke saas-lifecycle-script-test saas-integration-test saas-object-policy-test saas-kubernetes-check saas-release-script-test saas-platform-inventory-check saas-platform-plan-check saas-platform-change-check saas-platform-exposure-check saas-platform-preflight saas-platform-rollback-verify saas-platform-probe saas-launch-state-collect saas-operational-safety-check saas-identity-safety-check saas-parity-evidence-check saas-retrieval-risk-check saas-retrieval-load-check saas-alert-evidence-check saas-game-day-evidence-check saas-alpha-evidence-check saas-blocker-evidence-check saas-security-closure-check saas-migration-cohort-check saas-migration-acceptance-check saas-launch-scope-check saas-privacy-review-check saas-external-integration-check saas-recovery-exit-check saas-program-approval-check saas-notice-readiness-check saas-private-beta-approval-check saas-billing-reconciliation-check saas-support-evidence-check saas-beta-slo-check saas-beta-operations-check saas-beta-integrity-check saas-launch-assets-check saas-public-beta-gate-check saas-approval-export-check saas-ga-scorecard-check saas-ga-drills-check saas-ga-approval-export-check saas-mvp-readiness-check saas-capacity-evidence-check saas-staging-journey-collect saas-staging-format-collect saas-object-custody-check saas-isolation-review-check saas-postgres-restore-check saas-retention-inventory-collect saas-backup-expiry-check saas-external-evidence-check

.DEFAULT_GOAL := help

.PHONY: observability-validate

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
	cp $(BIN_DIR)/$(APP) $(BIN_DIR)/am

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
	cp $(BIN_DIR)/$(APP) $(BIN_DIR)/am
	@echo "✓ Build complete with embedded dashboard"

install-dev: build ## Build and install locally for development
	go install ./cmd/agent-memory ./cmd/am

test: ## Run all tests
	go test ./...

integration-test: ## Run integration parity, privacy, benchmark, MCP, and dashboard gates
	go test ./internal/application ./internal/api ./internal/cli ./internal/connectors ./internal/hooks ./internal/replay ./internal/storage/sqlite
	python3 -m unittest benchmark.test_benchmark.IntegrationReliabilityFixtureTest
	cd tools/agent-memory/mcp-server && npm test && npm run build
	cd tools/agent-memory/dashboard && npm run build

contracts-check: ## Validate hosted API and event contracts
	go test ./internal/contracts

graphrag-adapter-supply-chain: ## Generate and verify GraphRAG adapter SBOM, licenses, vulnerabilities, and signature
	$(MAKE) -C tools/graphrag-adapter supply-chain

graphrag-adapter-container-test: ## Verify the frozen non-root GraphRAG adapter image
	$(MAKE) -C tools/graphrag-adapter container-test

observability-validate: ## Validate content-safe GraphRAG metrics, alert routes, and dashboard contracts
	go test ./internal/observability -run 'GraphObservabilityConfiguration|GraphMetrics'

.PHONY: graphrag-evaluate
graphrag-evaluate: ## Run the deterministic GraphRAG quality, latency, grounding, isolation, and cost gate
	go test ./internal/evaluation -run 'GraphRAG' -count=1
	go run ./tools/evaluation/graphrag-report

.PHONY: graphrag-chaos-test graphrag-security-test graphrag-recovery-test graphrag-capacity-test
graphrag-chaos-test: ## Exercise GraphRAG provider, worker, queue, replay, and credential failure paths
	$(MAKE) -C tools/graphrag-certification chaos

graphrag-security-test: ## Exercise GraphRAG validation, isolation, privacy, and static-analysis controls
	$(MAKE) -C tools/graphrag-certification security

graphrag-recovery-test: ## Exercise GraphRAG deletion, canonical-only rebuild, and restore controls
	$(MAKE) -C tools/graphrag-certification recovery

graphrag-capacity-test: ## Exercise GraphRAG large-corpus latency and pre-model backpressure controls
	$(MAKE) -C tools/graphrag-certification capacity

.PHONY: graphrag-upgrade-certify graphrag-production-gate
graphrag-upgrade-certify: ## Fail-closed GraphRAG dependency upgrade, canary, signature, and rollback certification
	sh tools/graphrag-certification/upgrade.sh

graphrag-production-gate: ## Verify internal controls plus signed GraphRAG production certification and approvals
	sh tools/graphrag-certification/production-gate.sh

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

saas-kubernetes-check saas-kubernetes-validate: ## Validate hosted Kubernetes isolation and immutable workload policy
	scripts/validate-saas-kubernetes.sh

saas-release-script-test: ## Verify migration-before-rollout and automatic rollback behavior
	scripts/tests/saas-kubernetes-release_test.sh

saas-platform-inventory-check: ## Validate a content-free self-managed platform inventory
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-platform-inventory \
		--inventory "$(PLATFORM_INVENTORY)"

saas-architecture-evidence-check: ## Normalize inventory-bound P0.2-A architecture review evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-architecture-evidence \
		--inventory "$(PLATFORM_INVENTORY)" \
		--input "$(ARCHITECTURE_EVIDENCE_INPUT)" \
		--receipt "$(ARCHITECTURE_EVIDENCE_RECEIPT)"

saas-platform-plan-check: ## Validate a content-free self-managed infrastructure plan receipt
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-platform-plan \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)"

saas-platform-change-check: ## Validate a content-free infrastructure apply and drift receipt
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-platform-change \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)"

saas-platform-exposure-check: ## Validate a production private-authority exposure receipt
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-platform-exposure \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--exposure "$(PLATFORM_EXPOSURE)"

saas-platform-preflight: ## Collect a content-free Kubernetes platform preflight receipt
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-platform-preflight \
		--inventory "$(PLATFORM_INVENTORY)" \
		--environment "$(PLATFORM_ENVIRONMENT)" \
		--receipt "$(PLATFORM_PREFLIGHT_RECEIPT)"

saas-platform-rollback-verify: ## Verify live staging restoration after automatic rollback
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-platform-rollback \
		--baseline "$(ROLLBACK_BASELINE)" \
		--failed-attempt "$(ROLLBACK_FAILED_ATTEMPT)" \
		--receipt "$(ROLLBACK_RECEIPT)"

saas-platform-probe: ## Collect a content-free staging edge-to-telemetry receipt
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-platform-probe \
		--release "$(STAGING_RELEASE)" \
		--edge-url "$(STAGING_EDGE_URL)" \
		--internal-url "$(STAGING_INTERNAL_URL)" \
		--receipt "$(STAGING_PROBE_RECEIPT)"

saas-launch-state-collect: ## Collect the fail-closed staging launch-policy state
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-launch-state \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--receipt "$(LAUNCH_STATE_RECEIPT)"

saas-operational-safety-check: ## Normalize staging rollback, secret-rotation, and operator-access drills
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-operational-safety \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--baseline "$(ROLLBACK_BASELINE)" \
		--failed-attempt "$(ROLLBACK_FAILED_ATTEMPT)" \
		--rollback "$(ROLLBACK_RECEIPT)" \
		--drills "$(OPERATIONAL_SAFETY_DRILLS)" \
		--receipt "$(OPERATIONAL_SAFETY_RECEIPT)"

saas-identity-safety-check: ## Normalize staging identity-provider outage and credential-revocation drills
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-identity-safety \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--drills "$(IDENTITY_SAFETY_DRILLS)" \
		--receipt "$(IDENTITY_SAFETY_RECEIPT)"

saas-parity-evidence-check: ## Normalize representative staging retrieval-parity evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-parity-evidence \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--input "$(PARITY_EVIDENCE_INPUT)" \
		--receipt "$(PARITY_EVIDENCE_RECEIPT)"

saas-retrieval-risk-check: ## Normalize an independent staging retrieval-risk review
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-retrieval-risk \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--review "$(RETRIEVAL_RISK_REVIEW)" \
		--receipt "$(RETRIEVAL_RISK_RECEIPT)"

saas-retrieval-load-check: ## Normalize deployed staging retrieval load and model-cost evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-retrieval-load \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--input "$(RETRIEVAL_LOAD_INPUT)" \
		--receipt "$(RETRIEVAL_LOAD_RECEIPT)"

saas-alert-evidence-check: ## Normalize installed staging SLO and cost alert-routing evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-alert-evidence \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--input "$(ALERT_EVIDENCE_INPUT)" \
		--receipt "$(ALERT_EVIDENCE_RECEIPT)"

saas-game-day-evidence-check: ## Normalize release-bound P10.3-A operational game-day evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-game-day-evidence \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--input "$(GAME_DAY_EVIDENCE_INPUT)" \
		--receipt "$(GAME_DAY_EVIDENCE_RECEIPT)"

saas-alpha-evidence-check: ## Normalize release-bound P10.1-A internal-alpha lifecycle evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-alpha-evidence \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--journey "$(STAGING_JOURNEY_RECEIPT)" \
		--input "$(ALPHA_EVIDENCE_INPUT)" \
		--receipt "$(ALPHA_EVIDENCE_RECEIPT)"

saas-blocker-evidence-check: ## Normalize the private-beta incident and launch-blocker review
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-blocker-evidence \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--input "$(BLOCKER_EVIDENCE_INPUT)" \
		--receipt "$(BLOCKER_EVIDENCE_RECEIPT)"

saas-security-closure-check: ## Normalize independent high/critical finding closure evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-security-closure \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--input "$(SECURITY_CLOSURE_INPUT)" \
		--receipt "$(SECURITY_CLOSURE_RECEIPT)"

saas-migration-cohort-check: ## Normalize representative internal migration-cohort evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-migration-cohort \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--input "$(MIGRATION_COHORT_INPUT)" \
		--receipt "$(MIGRATION_COHORT_RECEIPT)"

saas-migration-acceptance-check: ## Normalize migration parity and rollback acceptance evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-migration-acceptance \
		--cohort "$(MIGRATION_COHORT_RECEIPT)" \
		--parity "$(PARITY_EVIDENCE_RECEIPT)" \
		--input "$(MIGRATION_ACCEPTANCE_INPUT)" \
		--receipt "$(MIGRATION_ACCEPTANCE_RECEIPT)"

saas-launch-scope-check: ## Normalize launch-scope and legal-position evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-launch-scope \
		--input "$(LAUNCH_SCOPE_INPUT)" \
		--receipt "$(LAUNCH_SCOPE_RECEIPT)"

saas-privacy-review-check: ## Normalize CP7-A Privacy/Counsel UI and receipt review evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-privacy-review \
		--input "$(PRIVACY_REVIEW_INPUT)" \
		--receipt "$(PRIVACY_REVIEW_RECEIPT)"

saas-external-integration-check: ## Normalize external-integration data-purpose review evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-external-integration \
		--inventory "$(PLATFORM_INVENTORY)" \
		--input "$(EXTERNAL_INTEGRATION_INPUT)" \
		--receipt "$(EXTERNAL_INTEGRATION_RECEIPT)"

saas-recovery-exit-check: ## Normalize P0.2-B component recovery and integration exit evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-recovery-exit \
		--inventory "$(PLATFORM_INVENTORY)" \
		--input "$(RECOVERY_EXIT_INPUT)" \
		--receipt "$(RECOVERY_EXIT_RECEIPT)"

saas-program-approval-check: ## Normalize shared CP0-A and CP0-B program approval evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-program-approval \
		--inventory "$(PLATFORM_INVENTORY)" \
		--launch-scope-receipt "$(LAUNCH_SCOPE_RECEIPT)" \
		--integration-receipt "$(EXTERNAL_INTEGRATION_RECEIPT)" \
		--input "$(PROGRAM_APPROVAL_INPUT)" \
		--receipt "$(PROGRAM_APPROVAL_RECEIPT)"

saas-notice-readiness-check: ## Normalize P6.5-A and CP6-A notice legal/staffing evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-notice-readiness \
		--launch-scope-receipt "$(LAUNCH_SCOPE_RECEIPT)" \
		--input "$(NOTICE_READINESS_INPUT)" \
		--receipt "$(NOTICE_READINESS_RECEIPT)"

saas-private-beta-approval-check: ## Normalize CP10-A signed accountable approval export
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-private-beta-approval \
		--security-closure "$(SECURITY_CLOSURE_RECEIPT)" \
		--alert-routing "$(ALERT_EVIDENCE_RECEIPT)" \
		--blocker-review "$(BLOCKER_EVIDENCE_RECEIPT)" \
		--capacity-economics "$(CAPACITY_EVIDENCE_RECEIPT)" \
		--approver-keys "$(APPROVER_KEYS)" \
		--approvals-dir "$(PRIVATE_BETA_APPROVALS_DIR)" \
		--export-manifest "$(PRIVATE_BETA_APPROVAL_MANIFEST)" \
		--input "$(PRIVATE_BETA_APPROVAL_INPUT)" \
		--receipt "$(PRIVATE_BETA_APPROVAL_RECEIPT)"

saas-billing-reconciliation-check: ## Normalize production processor, invoice, settlement, and usage reconciliation
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-billing-reconciliation \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(PRODUCTION_RELEASE)" \
		--input "$(BILLING_RECONCILIATION_INPUT)" \
		--receipt "$(BILLING_RECONCILIATION_RECEIPT)"

saas-support-evidence-check: ## Normalize production support-channel staffing evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-support-evidence \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(PRODUCTION_RELEASE)" \
		--input "$(SUPPORT_EVIDENCE_INPUT)" \
		--receipt "$(SUPPORT_EVIDENCE_RECEIPT)"

saas-beta-slo-check: ## Normalize an elapsed production beta SLO observation
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-beta-slo \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(PRODUCTION_RELEASE)" \
		--input "$(BETA_SLO_INPUT)" \
		--receipt "$(BETA_SLO_RECEIPT)"

saas-beta-operations-check: ## Normalize same-window production beta trust operations
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-beta-operations \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(PRODUCTION_RELEASE)" \
		--beta-slo "$(BETA_SLO_RECEIPT)" \
		--input "$(BETA_OPERATIONS_INPUT)" \
		--receipt "$(BETA_OPERATIONS_RECEIPT)"

saas-beta-integrity-check: ## Normalize same-window production isolation and audit-integrity evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-beta-integrity \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(PRODUCTION_RELEASE)" \
		--beta-slo "$(BETA_SLO_RECEIPT)" \
		--beta-operations "$(BETA_OPERATIONS_RECEIPT)" \
		--input "$(BETA_INTEGRITY_INPUT)" \
		--receipt "$(BETA_INTEGRITY_RECEIPT)"

saas-launch-assets-check: ## Normalize production public launch-asset evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-launch-assets \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(PRODUCTION_RELEASE)" \
		--input "$(LAUNCH_ASSETS_INPUT)" \
		--receipt "$(LAUNCH_ASSETS_RECEIPT)"

saas-public-beta-gate-check: ## Normalize one shared-window production public-beta gate
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-public-beta-gate \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(PRODUCTION_RELEASE)" \
		--billing "$(BILLING_RECONCILIATION_RECEIPT)" \
		--beta-slo "$(BETA_SLO_RECEIPT)" \
		--beta-operations "$(BETA_OPERATIONS_RECEIPT)" \
		--beta-integrity "$(BETA_INTEGRITY_RECEIPT)" \
		--input "$(PUBLIC_BETA_GATE_INPUT)" \
		--receipt "$(PUBLIC_BETA_GATE_RECEIPT)"

saas-approval-export-check: ## Normalize signed current public-beta approval export evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-approval-export \
		--launch-assets "$(LAUNCH_ASSETS_RECEIPT)" \
		--public-beta-gate "$(PUBLIC_BETA_GATE_RECEIPT)" \
		--approver-keys "$(APPROVER_TRUST_BUNDLE)" \
		--approvals-dir "$(PUBLIC_BETA_APPROVALS_DIR)" \
		--export-manifest "$(PUBLIC_BETA_APPROVAL_MANIFEST)" \
		--input "$(PUBLIC_BETA_APPROVAL_INPUT)" \
		--receipt "$(PUBLIC_BETA_APPROVAL_RECEIPT)"

saas-ga-scorecard-check: ## Normalize retention-aware production GA scorecard evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-ga-scorecard \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(PRODUCTION_RELEASE)" \
		--input "$(GA_SCORECARD_INPUT)" \
		--receipt "$(GA_SCORECARD_RECEIPT)"

saas-ga-drills-check: ## Normalize repeated production GA drill evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-ga-drills \
		--ga-scorecard "$(GA_SCORECARD_RECEIPT)" \
		--input "$(GA_DRILLS_INPUT)" \
		--receipt "$(GA_DRILLS_RECEIPT)"

saas-ga-approval-export-check: ## Normalize signed current GA approval export evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-ga-approval-export \
		--ga-scorecard "$(GA_SCORECARD_RECEIPT)" \
		--ga-drills "$(GA_DRILLS_RECEIPT)" \
		--approver-keys "$(APPROVER_TRUST_BUNDLE)" \
		--approvals-dir "$(GA_APPROVALS_DIR)" \
		--export-manifest "$(GA_APPROVAL_MANIFEST)" \
		--input "$(GA_APPROVAL_INPUT)" \
		--receipt "$(GA_APPROVAL_RECEIPT)"

saas-mvp-readiness-check: ## Normalize final eight-gate MVP readiness evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-mvp-readiness \
		--catalog "$(EXTERNAL_EVIDENCE_CATALOG)" \
		--index "$(EXTERNAL_EVIDENCE_INDEX)" \
		--artifacts-root "$(EXTERNAL_EVIDENCE_ARTIFACTS_ROOT)" \
		--trust "$(EXTERNAL_EVIDENCE_TRUST)" \
		--approvals-dir "$(EXTERNAL_EVIDENCE_APPROVALS_DIR)" \
		--input "$(MVP_READINESS_INPUT)" \
		--receipt "$(MVP_READINESS_RECEIPT)"

saas-capacity-evidence-check: ## Normalize private-beta capacity and worst-case economics evidence
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-capacity-evidence \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--retrieval-load "$(RETRIEVAL_LOAD_RECEIPT)" \
		--input "$(CAPACITY_EVIDENCE_INPUT)" \
		--receipt "$(CAPACITY_EVIDENCE_RECEIPT)"

saas-staging-journey-collect: ## Combine content-free human and agent staging journey receipts
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-staging-journey \
		--release "$(STAGING_RELEASE)" \
		--human-journey "$(STAGING_HUMAN_JOURNEY)" \
		--agent-journey "$(STAGING_AGENT_JOURNEY)" \
		--receipt "$(STAGING_JOURNEY_RECEIPT)"

saas-staging-format-collect: ## Validate a content-free four-format staging ingestion receipt
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-staging-format \
		--release "$(STAGING_RELEASE)" \
		--input "$(STAGING_FORMAT_INPUT)" \
		--receipt "$(STAGING_FORMAT_RECEIPT)"

saas-object-custody-check: ## Normalize a deployed staging object-custody review
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-object-custody \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--review "$(OBJECT_CUSTODY_REVIEW)" \
		--receipt "$(OBJECT_CUSTODY_RECEIPT)"

saas-isolation-review-check: ## Normalize an independent staging tenant-isolation review
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-isolation-review \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--release "$(STAGING_RELEASE)" \
		--review "$(ISOLATION_REVIEW)" \
		--receipt "$(ISOLATION_REVIEW_RECEIPT)"

saas-postgres-restore-check: ## Validate a content-free self-managed PostgreSQL restore drill
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-postgres-restore \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--drill "$(POSTGRES_RESTORE_DRILL)" \
		--receipt "$(POSTGRES_RESTORE_RECEIPT)"

saas-retention-inventory-collect: ## Collect the installed self-managed retention policy inventory
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-retention-inventory \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--receipt "$(RETENTION_INVENTORY_RECEIPT)"

saas-backup-expiry-check: ## Validate a production aged-backup expiry drill
	GOCACHE=/tmp/agent-memory-go-cache go run ./cmd/agent-memory-backup-expiry \
		--inventory "$(PLATFORM_INVENTORY)" \
		--plan "$(PLATFORM_PLAN)" \
		--change "$(PLATFORM_CHANGE)" \
		--retention-inventory "$(RETENTION_INVENTORY_RECEIPT)" \
		--drill "$(BACKUP_EXPIRY_DRILL)" \
		--receipt "$(BACKUP_EXPIRY_RECEIPT)"

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
