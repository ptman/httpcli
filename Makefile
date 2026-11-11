# Development tasks for httpcli. Run `make` to list them.
GO ?= go

BIN ?= httpcli
COVER_PROFILE := coverage.out
COVER_HTML := coverage.html

# -s -w drop the symbol table and DWARF data; use "make build LDFLAGS=" to debug.
LDFLAGS ?= -s -w

GOFUMPT ?= gofumpt
GOLANGCI_LINT ?= golangci-lint
GOVULNCHECK ?= govulncheck

# Versions installed by "make tools"; CI installs the same ones.
GOFUMPT_VERSION ?= v0.12.0
GOLANGCI_LINT_VERSION ?= v2.15.0
GOVULNCHECK_VERSION ?= v1.9.0

.DEFAULT_GOAL := help
.PHONY: help build test coverage clean gofumpt vet golangci-lint govulncheck tools

help: ## show this help
	@echo "httpcli development tasks:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## build ./httpcli (static, stripped)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='$(LDFLAGS)' -o $(BIN) .

test: ## run all tests (offline)
	$(GO) test ./...

coverage: ## run tests and write coverage.out and coverage.html
	$(GO) test -covermode=atomic -coverprofile=$(COVER_PROFILE) ./...
	$(GO) tool cover -func=$(COVER_PROFILE) | tail -n 1
	$(GO) tool cover -html=$(COVER_PROFILE) -o $(COVER_HTML)

clean: ## remove build and coverage output
	rm -f $(BIN) $(COVER_PROFILE) $(COVER_HTML)

gofumpt: ## format the tree with gofumpt
	$(GOFUMPT) -l -w .

vet: ## run go vet
	$(GO) vet ./...

golangci-lint: ## run golangci-lint
	$(GOLANGCI_LINT) run

govulncheck: ## check for known vulnerabilities
	$(GOVULNCHECK) ./...

tools: ## install the pinned development tools
	$(GO) install mvdan.cc/gofumpt@$(GOFUMPT_VERSION)
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
