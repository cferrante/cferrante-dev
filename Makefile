# Build, test and lint entry points. Run `make` or `make help` to list targets.
.DEFAULT_GOAL := help

# Go commands exit non-zero on a module with no packages, so Go steps are
# skipped until the first package exists.
GO_PKGS := $(shell go list ./... 2>/dev/null)
SH_FILES := $(shell find . -name '*.sh' -not -path './.git/*')
SHELLCHECK ?= shellcheck

.PHONY: help test engine-test lint fmt docs-check

ENGINE_BUILD := build/engine

help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

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
