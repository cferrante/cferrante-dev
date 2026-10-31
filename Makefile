# Build, test and lint entry points. Run `make` or `make help` to list targets.
.DEFAULT_GOAL := help

# Go commands exit non-zero on a module with no packages, so Go steps are
# skipped until the first package exists.
GO_PKGS := $(shell go list ./... 2>/dev/null)
SH_FILES := $(shell find . -name '*.sh' -not -path './.git/*')
SHELLCHECK ?= shellcheck

.PHONY: help test lint fmt docs-check

help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'

test: ## Run all tests
ifneq ($(GO_PKGS),)
	go test -race ./...
else
	@echo "test: no Go packages yet"
endif

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
