IMAGE ?= agent-memory-graphrag-adapter:local
RELEASE_IMAGE ?=
RELEASE_COMMIT ?= $(shell git rev-parse HEAD)
PYTHON_IMAGE ?=
UV_IMAGE ?=
WHEELHOUSE := wheelhouse

.PHONY: lock-check wheelhouse image container-test supply-chain clean

lock-check:
	uv lock --check
	uv sync --frozen --offline --no-dev

wheelhouse:
	test -n "$(PYTHON_IMAGE)"
	mkdir -p $(WHEELHOUSE)
	uv --quiet export --frozen --no-dev --no-editable --no-emit-project --format requirements-txt --output-file $(WHEELHOUSE)/requirements.txt
	docker run --rm --user "$$(id -u):$$(id -g)" --volume "$(CURDIR):/work" --workdir /work "$(PYTHON_IMAGE)" \
		python -m pip download --require-hashes --only-binary=:all: --dest $(WHEELHOUSE) --requirement $(WHEELHOUSE)/requirements.txt
	uv build --wheel --out-dir $(WHEELHOUSE)
	cd $(WHEELHOUSE) && shasum -a 256 agent_memory_graphrag_adapter-*.whl > adapter-wheel.sha256

image:
	test -n "$(PYTHON_IMAGE)"
	test -n "$(UV_IMAGE)"
	case "$(PYTHON_IMAGE)" in *@sha256:*) ;; *) exit 1 ;; esac
	case "$(UV_IMAGE)" in *@sha256:*) ;; *) exit 1 ;; esac
	cd ../.. && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o tools/graphrag-adapter/$(WHEELHOUSE)/agent-memory-graph-worker ./cmd/agent-memory-graph-worker
	docker build --network=none --build-arg PYTHON_IMAGE="$(PYTHON_IMAGE)" --build-arg UV_IMAGE="$(UV_IMAGE)" --tag "$(IMAGE)" .

container-test:
	docker run --rm --network=none --read-only --tmpfs /graph-job:rw,noexec,nosuid,size=64m --cap-drop=ALL --security-opt=no-new-privileges --entrypoint /opt/adapter/.venv/bin/agent-memory-graphrag "$(IMAGE)" readiness
	docker inspect "$(IMAGE)" --format '{{json .Config.User}}' | grep -q '65532'
	docker run --rm --entrypoint sh "$(IMAGE)" -c '! command -v pip && ! command -v apt && ! command -v uv'
	docker run --rm --entrypoint sh "$(IMAGE)" -c 'test -x /usr/local/bin/agent-memory-graph-worker'

supply-chain:
	command -v syft
	command -v grype
	command -v cosign
	case "$(RELEASE_IMAGE)" in *@sha256:*) ;; *) echo "RELEASE_IMAGE must be an immutable digest reference" >&2; exit 1 ;; esac
	test -n "$(COSIGN_KEY)"
	test -n "$(COSIGN_PUBLIC_KEY)"
	syft "$(RELEASE_IMAGE)" -o cyclonedx-json=sbom.cdx.json
	grype "$(RELEASE_IMAGE)" --fail-on high --output json > vulnerabilities.json
	python3 scripts/supply_chain_evidence.py licenses --sbom sbom.cdx.json --output licenses.txt
	cosign sign --yes --key "$(COSIGN_KEY)" "$(RELEASE_IMAGE)"
	cosign verify --output json --key "$(COSIGN_PUBLIC_KEY)" "$(RELEASE_IMAGE)" > signature-verification.json
	python3 scripts/supply_chain_evidence.py report \
		--release-commit "$(RELEASE_COMMIT)" \
		--image "$(RELEASE_IMAGE)" \
		--generated-at "$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		--lock uv.lock \
		--requirements requirements.lock \
		--sbom sbom.cdx.json \
		--licenses licenses.txt \
		--license-policy license-policy.json \
		--vulnerabilities vulnerabilities.json \
		--signature-verification signature-verification.json \
		--output supply-chain-report.json
	cosign sign-blob --yes --key "$(COSIGN_KEY)" --bundle supply-chain-report.bundle.json supply-chain-report.json

clean:
	rm -rf $(WHEELHOUSE) sbom.cdx.json licenses.txt vulnerabilities.json signature-verification.json supply-chain-report.json supply-chain-report.bundle.json
