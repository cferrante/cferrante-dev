# Build, test and lint entry points. Run `make` or `make help` to list targets.
.DEFAULT_GOAL := help

# Go commands exit non-zero on a module with no packages, so Go steps are
# skipped until the first package exists.
GO_PKGS := $(shell go list ./... 2>/dev/null)
SH_FILES := $(shell find . -name '*.sh' -not -path './.git/*')
SHELLCHECK ?= shellcheck

.PHONY: help tools css site css-check jstest test corpus-check engine-test bench-engine bench-engine-scale bench-go dev smoke-groq smoke-groq-schema measure-groq-cache smoke-bedrock smoke-titan lint fmt docs-check

ENGINE_BUILD := build/engine
ENGINE_RELEASE := build/engine-release

help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-19s %s\n", $$1, $$2}'

tools: ## Fetch the pinned frontend tools (Tailwind, Deno) into build/tools, checksum-verified
	scripts/fetch-tools.sh

css: tools ## Build web/dist/app.css with the Tailwind standalone CLI
	build/tools/tailwindcss -i web/src/app.css -o web/dist/app.css --minify

site: ## Write web/dist/config.js from SITE_NAME, ASSISTANT_NAME and MESSAGES_FILE (or .env)
	go run ./orchestrator/cmd/siteconfig

css-check: css ## Check the CSS budget (< 15 KB gzipped) and required classes
	scripts/check-css.sh

jstest: tools ## Run the frontend's JS tests under Deno with no permissions
	build/tools/deno test --no-prompt web/js/

test: engine-test css-check jstest corpus-check ## Run all tests
ifneq ($(GO_PKGS),)
	go test -race ./...
else
	@echo "test: no Go packages yet"
endif
	infra/bootstrap/aws/secret_test.sh
	scripts/fetch-tools_test.sh
	scripts/check-css_test.sh

corpus-check: ## Fail if a tracked file would publish invitee-only documents
	go run ./orchestrator/cmd/corpuscheck

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

dev: css site ## Serve the page and /chat locally over Ollama; DEV_FLAGS=-invitee etc.
	@# Built then run directly, not with `go run`: go run exits 1 on Ctrl+C even
	@# when the server shuts down cleanly, so make would report a false error.
	go build -o build/dev/server ./orchestrator/cmd/dev
	build/dev/server $(DEV_FLAGS)

smoke-groq: ## Call the real Groq API once (needs aws sso login; spends free-tier quota)
	LIVE_GROQ=1 go test -count=1 -run 'TestLiveGroq$$' -v ./orchestrator/model/oai/

smoke-groq-schema: ## Check Groq's strict JSON output once (needs aws sso login; free tier)
	LIVE_GROQ=1 go test -count=1 -run TestLiveGroqSchema -v ./orchestrator/model/oai/

measure-groq-cache: ## Repeat a 1,460-token prompt up to 4 times and log Groq's cached tokens (free tier)
	LIVE_GROQ=1 go test -count=1 -run TestLiveGroqCache -v ./orchestrator/model/oai/

smoke-bedrock: ## Call the real Bedrock API once (needs aws sso login; about $0.0001)
	LIVE_BEDROCK=1 go test -count=1 -run TestLiveBedrock -v ./orchestrator/model/bedrock/

smoke-titan: ## Embed three texts with the real Titan v2 (needs aws sso login; far below $0.0001)
	LIVE_TITAN=1 go test -count=1 -run TestLiveTitan -v ./orchestrator/embed/

lint: tools ## Check formatting, run go vet, shellcheck and deno lint
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
ifneq ($(GO_PKGS),)
	go vet ./...
endif
	@# -x follows `source`; SCRIPTDIR resolves it relative to each script.
	$(SHELLCHECK) -x -P SCRIPTDIR $(SH_FILES)
	build/tools/deno lint web/js/
	build/tools/deno fmt --check --line-width 110 web/js/

fmt: ## Format Go code in place
	gofmt -w .

docs-check: ## Check doc anchor links (HEAD must be pushed)
	scripts/check-doc-links.sh
