# dbsgomysql — development targets.
#
# `make check` is the gate named by AGENTS.md. Nothing is "done" until it
# passes and its output has been read.

MODULE       := github.com/dbsmedya/dbsgomysql
GO           ?= go
GOLANGCI     ?= golangci-lint
COVERPROFILE ?= coverage.out

# The single source of truth for the linter version. `ci.yml` reads it from
# here via `make print-golangci-version`, so there is one place to bump.
# Findings differ between golangci-lint releases; an unpinned local install is
# how "passes on my machine" happens.
GOLANGCI_VERSION := v2.12.2

# The development toolchain, read from go.mod's `toolchain` directive so there
# is exactly one place to bump — `go mod tidy` maintains that line, so it cannot
# drift from what the module itself declares. `ci.yml` reads it back out of here
# via `make print-go-version`, the same way it reads the linter version.
#
# Two directives, two different jobs. `go 1.24.0` is the *consumer* floor and
# the compatibility unit; raising it would break a consumer on 1.24.5 for no
# reason. `toolchain go1.24.13` is the *development platform*, ignored entirely
# when this module is somebody's dependency.
#
# A floor is not a platform: `go 1.24.0` is satisfied by 1.24, 1.25, and 1.26
# alike, so every contributor and every agent can be on a different compiler
# while all of them pass. Vet and lint findings differ across those releases,
# which is how a failure reaches code review as a surprise instead of arriving
# as a red check.
#
# The `toolchain` line alone does not fix that, because it is a minimum too: a
# developer on a newer Go keeps using it. Exporting GOTOOLCHAIN is what forces
# the downgrade, so every `go` invocation below runs exactly this toolchain,
# fetching it on first use and ignoring whatever is on PATH. It also means the
# gate compiles on the declared floor on every run, so the "Go floor 1.24"
# promise in AGENTS.md is tested rather than asserted.
#
# 1.24 is past upstream end-of-life — 1.24.13 is its final patch. That is a
# deliberate trade: this is a library whose floor is its contract, and the floor
# is what the gate must prove. Revisit at v1.0.0 alongside the compatibility
# rules, when raising the floor is on the table anyway.
GO_VERSION := $(shell awk '$$1 == "toolchain" { sub(/^go/, "", $$2); print $$2; exit }' go.mod)

ifeq ($(strip $(GO_VERSION)),)
$(error go.mod has no "toolchain" directive; the pinned development toolchain lives there)
endif

export GOTOOLCHAIN := go$(GO_VERSION)

# `go vet`, `go test`, and `golangci-lint` all exit non-zero on a module that
# contains no packages. Until the first package lands, targets guarded by these
# skip cleanly rather than failing the gate for the wrong reason. The guards
# disappear on their own the moment the packages exist — they are never a way to
# make a real failure quiet.
HAVE_PKGS     := $(GO) list ./... 2>/dev/null | grep -q .
HAVE_E2E_PKGS := $(GO) list -tags=e2e ./tests/e2e/... 2>/dev/null | grep -q .

.DEFAULT_GOAL := help

# ---------------------------------------------------------------------------
# Help
# ---------------------------------------------------------------------------

.PHONY: help
help: ## Show available targets
	@echo "dbsgomysql — make targets"
	@echo ""
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'
	@echo ""

# ---------------------------------------------------------------------------
# The gate
# ---------------------------------------------------------------------------

.PHONY: check
check: tools-check fmt-check vet vet-tags lint lint-tags test tidy-check deps-check build ## Full verification gate (AGENTS.md)
	@echo ""
	@echo "make check: PASS"

# ---------------------------------------------------------------------------
# Toolchain
# ---------------------------------------------------------------------------

.PHONY: print-golangci-version
print-golangci-version: ## Print the pinned golangci-lint version (used by CI)
	@echo "$(GOLANGCI_VERSION)"

.PHONY: print-go-version
print-go-version: ## Print the pinned Go toolchain version (used by CI)
	@echo "$(GO_VERSION)"

.PHONY: tools-check
tools-check: ## Fail if the installed golangci-lint is not the pinned version
	@got="$$($(GO) env GOVERSION 2>/dev/null)"; \
	if [ "$$got" != "go$(GO_VERSION)" ]; then \
		echo "tools-check: FAIL — go $$got in use, go$(GO_VERSION) pinned."; \
		echo "GOTOOLCHAIN is exported by this Makefile, so this means the pinned"; \
		echo "toolchain could not be fetched. Check network access, then:"; \
		echo "  GOTOOLCHAIN=go$(GO_VERSION) go version"; \
		exit 1; \
	fi; \
	echo "tools-check: ok (go$(GO_VERSION))"
	@command -v $(GOLANGCI) >/dev/null 2>&1 || { \
		echo "tools-check: FAIL — $(GOLANGCI) not found. Install the pinned version:"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)"; \
		exit 1; \
	}
	@want="$(GOLANGCI_VERSION)"; want="$${want#v}"; \
	got="$$($(GOLANGCI) version 2>&1 | sed -n 's/.*has version \([0-9][0-9.]*\).*/\1/p')"; \
	if [ "$$got" != "$$want" ]; then \
		echo "tools-check: FAIL — golangci-lint $$got installed, $$want pinned."; \
		echo "Lint findings differ between versions; CI installs the pinned one."; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)"; \
		exit 1; \
	fi; \
	echo "tools-check: ok (golangci-lint $$got)"

# ---------------------------------------------------------------------------
# Formatting & static analysis
# ---------------------------------------------------------------------------

.PHONY: fmt
fmt: ## Format code (gofmt + goimports via golangci-lint)
	$(GOLANGCI) fmt

.PHONY: fmt-check
fmt-check: ## Fail if any file is unformatted
	@out="$$($(GOLANGCI) fmt --diff 2>&1)"; \
	if [ -n "$$out" ]; then \
		echo "$$out"; \
		echo ""; \
		echo "fmt-check: FAIL — run 'make fmt'"; \
		exit 1; \
	fi
	@echo "fmt-check: ok"

.PHONY: vet
vet: ## Run go vet
	@if $(HAVE_PKGS); then \
		$(GO) vet ./...; \
	else \
		echo "vet: skipped (no Go packages yet)"; \
	fi

# Neither `go vet ./...` nor `golangci-lint run` compiles files behind a build
# tag, so an `integration`- or `e2e`-tagged test can stop compiling without the
# gate noticing — until someone runs the database matrix. Both passes cover the
# whole module, not just the directory their target runs: an e2e-tagged file
# outside tests/e2e/ is invisible to every other check, and `make test-e2e` would
# report `skipped` while exiting 0 forever. Each tag is vetted separately rather
# than together, so a helper defined in both layers is not a duplicate symbol.
# `&&`, not `;`: a `;` would let the second vet's exit status mask the first's.
.PHONY: vet-tags
vet-tags: ## Type-check the build-tagged test layers (integration, e2e)
	@if $(HAVE_PKGS); then \
		$(GO) vet -tags=integration ./... && \
		$(GO) vet -tags=e2e ./...; \
	else \
		echo "vet-tags: skipped (no Go packages yet)"; \
	fi

.PHONY: lint
lint: ## Run golangci-lint
	@if $(HAVE_PKGS); then \
		$(GOLANGCI) run; \
	else \
		echo "lint: skipped (no Go packages yet)"; \
	fi

# Like vet, golangci-lint ignores build-tagged files unless the tags are named.
# Keep the passes separate so helpers that may eventually exist in both layers
# do not become duplicate declarations.
.PHONY: lint-tags
lint-tags: ## Lint the build-tagged test layers (integration, e2e)
	@if $(HAVE_PKGS); then \
		$(GOLANGCI) run --build-tags=integration && \
		$(GOLANGCI) run --build-tags=e2e; \
	else \
		echo "lint-tags: skipped (no Go packages yet)"; \
	fi

# ---------------------------------------------------------------------------
# Build & test
# ---------------------------------------------------------------------------

.PHONY: build
build: ## Compile all packages
	$(GO) build ./...

.PHONY: test
test: ## Unit tests (no database required)
	@if $(HAVE_PKGS); then \
		$(GO) test -race -shuffle=on ./...; \
	else \
		echo "test: skipped (no Go packages yet)"; \
	fi

.PHONY: cover
cover: ## Unit tests with coverage report
	@if $(HAVE_PKGS); then \
		$(GO) test -race -coverprofile=$(COVERPROFILE) -covermode=atomic ./... && \
		$(GO) tool cover -func=$(COVERPROFILE) | tail -1 && \
		echo "HTML report: $(GO) tool cover -html=$(COVERPROFILE)"; \
	else \
		echo "cover: skipped (no Go packages yet)"; \
	fi

.PHONY: test-smoke
test-smoke: ## Smoke tests against a single MySQL 8.4 container
	@if $(HAVE_PKGS); then \
		$(GO) test -tags=integration -run 'Smoke' ./...; \
	else \
		echo "test-smoke: skipped (no Go packages yet)"; \
	fi

.PHONY: test-integration
test-integration: ## Integration tests (requires MySQL; see docs/testing.md)
	@if $(HAVE_PKGS); then \
		$(GO) test -tags=integration ./...; \
	else \
		echo "test-integration: skipped (no Go packages yet)"; \
	fi

.PHONY: test-e2e
test-e2e: ## End-to-end scenarios (requires MySQL; see docs/testing.md)
	@if $(HAVE_E2E_PKGS); then \
		$(GO) test -tags=e2e ./tests/e2e/...; \
	else \
		echo "test-e2e: skipped (no packages under ./tests/e2e yet)"; \
	fi

# ---------------------------------------------------------------------------
# Dependency discipline (AGENTS.md)
# ---------------------------------------------------------------------------

.PHONY: deps-check
deps-check: ## Assert public packages depend on stdlib only
	@pkgs="$$($(GO) list ./pkg/... 2>/dev/null)"; \
	if [ -z "$$pkgs" ]; then \
		echo "deps-check: ok (no packages under ./pkg yet)"; \
		exit 0; \
	fi; \
	ext="$$($(GO) list -deps ./pkg/... 2>/dev/null \
		| awk -F/ '$$1 ~ /\./' \
		| grep -v '^$(MODULE)' || true)"; \
	if [ -n "$$ext" ]; then \
		echo "deps-check: FAIL — non-stdlib dependencies reachable from ./pkg/..."; \
		echo "$$ext" | sed 's/^/  /'; \
		echo ""; \
		echo "Library code imports stdlib only (AGENTS.md)."; \
		exit 1; \
	fi; \
	echo "deps-check: ok (stdlib only)"

.PHONY: tidy-check
tidy-check: ## Fail if go.mod/go.sum are untidy (does not write)
	@if ! $(GO) mod tidy -diff; then \
		echo ""; \
		echo "tidy-check: FAIL — run 'make tidy' and commit the result"; \
		exit 1; \
	fi
	@echo "tidy-check: ok"

.PHONY: tidy
tidy: ## Tidy and verify go.mod
	$(GO) mod tidy
	$(GO) mod verify

# ---------------------------------------------------------------------------
# Documentation
# ---------------------------------------------------------------------------

.PHONY: doc
doc: ## Serve GoDoc locally at http://localhost:6060
	@command -v pkgsite >/dev/null 2>&1 || { \
		echo "pkgsite not found. Install with:"; \
		echo "  go install golang.org/x/pkgsite/cmd/pkgsite@latest"; \
		exit 1; \
	}
	pkgsite -http=localhost:6060

# ---------------------------------------------------------------------------
# Housekeeping
# ---------------------------------------------------------------------------

.PHONY: clean
clean: ## Remove build and coverage artifacts
	rm -f $(COVERPROFILE) coverage.html
	$(GO) clean -testcache
