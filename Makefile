# RunWisp build orchestrator. Replaces Nx with GNU make.
#
# Slow lint targets (svelte-check, eslint) are cached via root-level
# .cache/*.stamp files; `make clean` or branch switches invalidate them.
# Parallel by default; override with `make -j1 <target>`.

JOBS := $(shell sysctl -n hw.ncpu 2>/dev/null || nproc 2>/dev/null || echo 4)
E2E_COVDIR := $(CURDIR)/apps/runwisp/.gocoverdir
MAKEFLAGS += --no-print-directory -j$(JOBS)
SHELL := /bin/bash
.DEFAULT_GOAL := build

# Progress banners. `NO_COLOR=1` disables ANSI; pipe-detection keeps CI logs sane.
ifeq ($(NO_COLOR),)
ifeq ($(shell [ -t 1 ] && echo tty),tty)
C_BOLD := \033[1;36m
C_DIM := \033[2;37m
C_OK := \033[1;32m
C_FAIL := \033[1;31m
C_OFF := \033[0m
endif
endif

# $(call step,target description,command...) prints "▶ <desc>" then runs the
# command, timing it and printing "✓ <desc> (<elapsed>)" on success.
define step
@printf '$(C_BOLD)▶ %s$(C_OFF) $(C_DIM)%s$(C_OFF)\n' "$(1)" "$(2)"
@start=$$(date +%s); $(2); rc=$$?; end=$$(date +%s); \
	if [ $$rc -eq 0 ]; then \
		printf '$(C_OK)✓ %s$(C_OFF) $(C_DIM)(%ss)$(C_OFF)\n' "$(1)" $$((end-start)); \
	else \
		exit $$rc; \
	fi
endef

# $(call ci-run,label,command...) runs a command silently, printing one line
# on success/failure.  Captured output is shown only when the command fails.
define ci-run
@out=$$(mktemp); \
printf '  %-24s ' "$(1)"; \
if $(2) >"$$out" 2>&1; then \
	printf '$(C_OK)ok$(C_OFF)\n'; \
	rm -f "$$out"; \
else \
	rc=$$?; \
	printf '$(C_FAIL)FAIL$(C_OFF)\n'; \
	cat "$$out"; \
	rm -f "$$out"; \
	exit $$rc; \
fi
endef

# Source globs -------------------------------------------------------------

GO_SOURCES := $(filter-out %_test.go,$(shell find apps/runwisp/cmd apps/runwisp/internal -name '*.go' -not -path '*/generated/*' 2>/dev/null))
GO_MOD := apps/runwisp/go.mod apps/runwisp/go.sum

UI_APP_SOURCES := $(shell find apps/ui/src apps/ui/static -type f 2>/dev/null)
UI_APP_CONFIGS := apps/ui/svelte.config.js apps/ui/vite.config.ts apps/ui/tsconfig.json apps/ui/package.json apps/ui/eslint.config.js apps/ui/prettier.config.cjs

UI_LIB_SOURCES := $(shell find packages/ui/src -type f 2>/dev/null)
UI_LIB_CONFIGS := packages/ui/svelte.config.js packages/ui/tsconfig.json packages/ui/package.json packages/ui/eslint.config.js packages/ui/prettier.config.cjs

COMMON_SOURCES := $(shell find packages/common/src -type f -not -path '*/generated/*' 2>/dev/null)
COMMON_CONFIGS := packages/common/tsconfig.json packages/common/package.json packages/common/eslint.config.js

ASYNCAPI_SOURCES := $(shell find packages/asyncapi/src -type f -not -path '*/generated/*' 2>/dev/null)
ASYNCAPI_CONFIGS := packages/asyncapi/tsconfig.json packages/asyncapi/package.json packages/asyncapi/eslint.config.js

DOCS_SOURCES := $(shell find apps/docs/src -type f 2>/dev/null) $(shell find apps/docs/scripts -type f 2>/dev/null)

# Generated artefacts ------------------------------------------------------

ASYNCAPI_STAMP := .cache/asyncapi-gen.stamp
OPENAPI_JSON := apps/runwisp/openapi.json
COMMON_API_TS := packages/common/src/generated/api.ts
UI_BUILD_STAMP := apps/ui/build/.stamp
UI_DIST_STAMP := apps/runwisp/internal/ui/dist/.stamp
RUNWISP_BIN := apps/runwisp/runwisp

# Lint cache stamps --------------------------------------------------------

UI_APP_TYPES_STAMP := .cache/apps-ui-types.stamp
UI_APP_LINT_STAMP := .cache/apps-ui-lint.stamp
UI_LIB_TYPES_STAMP := .cache/packages-ui-types.stamp
UI_LIB_LINT_STAMP := .cache/packages-ui-lint.stamp
COMMON_LINT_STAMP := .cache/packages-common-lint.stamp
ASYNCAPI_LINT_STAMP := .cache/packages-asyncapi-lint.stamp
DOCS_LINT_STAMP := .cache/apps-docs-lint.stamp

ALL_LINT_STAMPS := $(UI_APP_TYPES_STAMP) $(UI_APP_LINT_STAMP) \
	$(UI_LIB_TYPES_STAMP) $(UI_LIB_LINT_STAMP) \
	$(COMMON_LINT_STAMP) $(ASYNCAPI_LINT_STAMP) $(DOCS_LINT_STAMP)

# Helper: create stamp directory and touch the stamp file.
define stamp
@mkdir -p $(dir $@) && touch $@
endef

# Codegen ------------------------------------------------------------------

$(ASYNCAPI_STAMP): packages/asyncapi/asyncapi.yaml packages/asyncapi/scripts/generate.js
	$(call step,asyncapi codegen,cd packages/asyncapi && bun run generate)
	$(stamp)

$(OPENAPI_JSON): $(GO_SOURCES) $(GO_MOD) $(ASYNCAPI_STAMP)
	$(call step,openapi.json (runwisp),cd apps/runwisp && ./scripts/generate-openapi.sh)

$(COMMON_API_TS): $(OPENAPI_JSON) packages/common/scripts/generate-api.ts
	$(call step,common API types,cd packages/common && bun run generate:api)

# UI build -> embed in Go binary -------------------------------------------

$(UI_BUILD_STAMP): $(UI_APP_SOURCES) $(UI_APP_CONFIGS) $(UI_LIB_SOURCES) $(COMMON_SOURCES) $(COMMON_API_TS) $(ASYNCAPI_STAMP)
	$(call step,web UI build (apps/ui),cd apps/ui && bun run build)
	$(stamp)

$(UI_DIST_STAMP): $(UI_BUILD_STAMP)
	$(call step,sync UI dist -> internal/ui/dist,cd apps/runwisp && ./scripts/sync-ui-dist.sh)
	$(stamp)

# Go binary ----------------------------------------------------------------

$(RUNWISP_BIN): $(GO_SOURCES) $(GO_MOD) $(ASYNCAPI_STAMP) $(UI_DIST_STAMP)
	$(call step,go build runwisp,cd apps/runwisp && ./scripts/build.sh)

# Lint cache stamps --------------------------------------------------------

$(UI_APP_TYPES_STAMP): $(UI_APP_SOURCES) $(UI_APP_CONFIGS) $(UI_LIB_SOURCES) $(COMMON_SOURCES) $(COMMON_API_TS) $(ASYNCAPI_STAMP)
	$(call step,svelte-check apps/ui,cd apps/ui && bun run check:types)
	$(stamp)

$(UI_APP_LINT_STAMP): $(UI_APP_SOURCES) $(UI_APP_CONFIGS) prettier.base.cjs
	$(call step,prettier+eslint apps/ui,cd apps/ui && bun run check:lint)
	$(stamp)

$(UI_LIB_TYPES_STAMP): $(UI_LIB_SOURCES) $(UI_LIB_CONFIGS) $(COMMON_SOURCES) $(COMMON_API_TS)
	$(call step,svelte-check packages/ui,cd packages/ui && bun run check:types)
	$(stamp)

$(UI_LIB_LINT_STAMP): $(UI_LIB_SOURCES) $(UI_LIB_CONFIGS) prettier.base.cjs
	$(call step,prettier+eslint packages/ui,cd packages/ui && bun run check:lint)
	$(stamp)

$(COMMON_LINT_STAMP): $(COMMON_SOURCES) $(COMMON_CONFIGS) $(COMMON_API_TS)
	$(call step,tsc+eslint packages/common,cd packages/common && bun run check)
	$(stamp)

$(ASYNCAPI_LINT_STAMP): $(ASYNCAPI_SOURCES) $(ASYNCAPI_CONFIGS) $(ASYNCAPI_STAMP)
	$(call step,tsc+eslint packages/asyncapi,cd packages/asyncapi && bun run check)
	$(stamp)

$(DOCS_LINT_STAMP): $(DOCS_SOURCES) apps/docs/package.json $(OPENAPI_JSON) $(UI_LIB_SOURCES)
	$(call step,astro check + lint apps/docs,cd apps/docs && bun run check)
	$(stamp)

# Phony orchestration targets ---------------------------------------------

.PHONY: build build-dev build-all dev generate check check-go format test \
	test-e2e ci web-ui theme docs dev-docs clean help \
	format-runwisp format-ui-app format-docs format-ui \
	test-runwisp test-ui-unit

build: $(RUNWISP_BIN) ## build the runwisp binary (UI + Go embed)

build-dev: $(RUNWISP_BIN) ## alias of build

build-all: $(UI_DIST_STAMP) $(ASYNCAPI_STAMP) ## cross-compile binaries for all platforms
	$(call step,cross-compile binaries,cd apps/runwisp && ./scripts/build-all.sh)

dev: $(RUNWISP_BIN) ## build then run the daemon in foreground
	$(call step,run runwisp daemon,cd apps/runwisp && ./runwisp)

generate: $(ASYNCAPI_STAMP) $(OPENAPI_JSON) $(COMMON_API_TS) ## regenerate asyncapi + openapi + common api

check: $(ALL_LINT_STAMPS) check-go ## type-check + lint everything (cached per package)

check-go: $(ASYNCAPI_STAMP) ## go vet + gofmt
	$(call step,go vet + gofmt,cd apps/runwisp && ./scripts/check-go.sh)

format: format-runwisp format-ui-app format-docs format-ui ## go fmt + prettier across the workspace

format-runwisp:
	$(call step,go fmt apps/runwisp,cd apps/runwisp && bun run format)

format-ui-app:
	$(call step,prettier apps/ui,cd apps/ui && bun run format)

format-docs:
	$(call step,prettier apps/docs,cd apps/docs && bun run format)

format-ui:
	$(call step,prettier packages/ui,cd packages/ui && bun run format)

test: test-runwisp test-ui-unit ## unit tests (go + vitest); see test-e2e for playwright

test-runwisp:
	@rm -rf $(E2E_COVDIR) && mkdir -p $(E2E_COVDIR)
	$(call step,go test apps/runwisp,\
		cd apps/runwisp && RUNWISP_E2E_COVDIR=$(E2E_COVDIR) \
		go test -covermode=atomic -coverpkg=./... -coverprofile=.coverage_unit.out ./...)
	@cd apps/runwisp && \
		go tool covdata textfmt -i $(E2E_COVDIR) -o .coverage_e2e.out 2>/dev/null; \
		cat .coverage_unit.out > .coverage_raw.out; \
		grep -v '^mode:' .coverage_e2e.out >> .coverage_raw.out 2>/dev/null || true; \
		python3 ./scripts/merge-coverage.py; \
		rm -f .coverage_unit.out .coverage_e2e.out .coverage_raw.out
	@rm -rf $(E2E_COVDIR)

test-ui-unit:
	$(call step,vitest apps/ui,cd apps/ui && bun run test:unit)

test-e2e: $(RUNWISP_BIN) ## playwright e2e against the built binary
	$(call step,playwright e2e,cd apps/ui && bun run test)

ci: ## generate -> format -> check -> test -> test-e2e (silent, one line per stage)
	@echo "CI pipeline"
	$(call ci-run,generate,$(MAKE) generate)
	$(call ci-run,format,$(MAKE) format)
	$(call ci-run,check,$(MAKE) check)
	$(call ci-run,test,$(MAKE) test)
	$(call ci-run,test-e2e,$(MAKE) test-e2e)
	@echo "$(C_OK)All stages passed.$(C_OFF)"

web-ui: ## vite dev server for apps/ui
	cd apps/ui && bun run dev

theme: ## storybook for packages/ui
	cd packages/ui && bun run dev

dev-docs: $(OPENAPI_JSON) ## astro dev server for apps/docs
	cd apps/docs && bun run dev

docs: $(OPENAPI_JSON) ## build apps/docs and serve the result
	cd apps/docs && bun run build && bun run preview

clean: ## remove all build outputs, generated code, and caches
	$(call step,clean workspace,rm -rf .cache $(RUNWISP_BIN) $(OPENAPI_JSON) apps/runwisp/dist apps/runwisp/internal/ui/dist apps/runwisp/internal/generated/protocol apps/ui/build apps/ui/.svelte-kit apps/docs/dist apps/docs/.astro apps/docs/public/openapi.json packages/asyncapi/src/generated packages/common/src/generated apps/runwisp/.gocoverdir)

help: ## show this help
	@awk -v on='$(C_BOLD)' -v off='$(C_OFF)' \
		'BEGIN { FS = ":.*## "; printf "\nUsage: make [target]\n\n" } \
		/^[a-zA-Z_-]+:.*## / { printf "  %s%-16s%s %s\n", on, $$1, off, $$2 }' \
		$(MAKEFILE_LIST)
