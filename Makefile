# Build, test and lint entry points. Run `make` or `make help` to list targets.
.DEFAULT_GOAL := help

# Go commands exit non-zero on a module with no packages, so Go steps are
# skipped until the first package exists.
GO_PKGS := $(shell go list ./... 2>/dev/null)
SH_FILES := $(shell find . -name '*.sh' -not -path './.git/*')
SHELLCHECK ?= shellcheck

.PHONY: help test engine-test bench-engine bench-engine-scale bench-go dev lint fmt docs-check

ENGINE_BUILD := build/engine
ENGINE_RELEASE := build/engine-release

help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-19s %s\n", $$1, $$2}'

test: engine-test ## Run all tests
ifneq ($(GO_PKGS),)
	go test -race ./...
else
	@echo "test: no Go packages yet"
endif
	infra/bootstrap/aws/secret_test.sh

engine-test: ## Build and run the C++ engine tests with ASan and UBSan
	cmake -S engine -B $(ENGINE_BUILD) -DCMAKE_BUILD_TYPE=Debug -DRAG_SANITIZE=ON >/dev/null
	cmake --build $(ENGINE_BUILD) -j
	ctest --test-dir $(ENGINE_BUILD) --output-on-failure

bench-engine: ## Benchmark retrieval (2k x 512) and fail if p99 >= 1 ms
	cmake -S engine -B $(ENGINE_RELEASE) -DCMAKE_BUILD_TYPE=Release >/dev/null
	cmake --build $(ENGINE_RELEASE) -j --target rag_bench
	$(ENGINE_RELEASE)/rag_bench

bench-engine-scale: ## Benchmark retrieval from 1k to 50k chunks (informational)
	cmake -S engine -B $(ENGINE_RELEASE) -DCMAKE_BUILD_TYPE=Release >/dev/null
	cmake --build $(ENGINE_RELEASE) -j --target rag_bench
	$(ENGINE_RELEASE)/rag_bench --scale

bench-go: ## Benchmark the pure-Go vector baseline (informational)
	go run ./engine/baseline/cmd/benchgo

dev: ## Serve /chat locally over Ollama (qwen3:4b, nomic-embed-text); DEV_FLAGS=-invitee etc.
	@# Built then run directly, not with `go run`: go run exits 1 on Ctrl+C even
	@# when the server shuts down cleanly, so make would report a false error.
	go build -o build/dev/server ./orchestrator/cmd/dev
	build/dev/server $(DEV_FLAGS)

lint: ## Check formatting, run go vet and shellcheck
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
ifneq ($(GO_PKGS),)
	go vet ./...
endif
	@# -x follows `source`; SCRIPTDIR resolves it relative to each script.
	$(SHELLCHECK) -x -P SCRIPTDIR $(SH_FILES)

fmt: ## Format Go code in place
	gofmt -w .

docs-check: ## Check doc anchor links (HEAD must be pushed)
	scripts/check-doc-links.sh
