BIN := codebox
PKG := ./...

# Tool versions are pinned here per the supply-chain policy in AGENTS.md.
# Bump deliberately, never to a release younger than 14 days, and run
# `make audit` after any change.
GOLINES       := github.com/segmentio/golines@v0.13.0
GOLANGCI_LINT := github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
GOVULNCHECK   := golang.org/x/vuln/cmd/govulncheck@v1.3.0

.DEFAULT_GOAL := build
.PHONY: deps format lint test audit build clean

deps: ## Download and tidy module dependencies.
	go mod download
	go mod tidy

format: ## Format Go source code to a 120-column width.
	go run $(GOLINES) -m 120 -w .
	gofmt -s -w .

lint: ## Run static analysis.
	go run $(GOLANGCI_LINT) run

test: ## Run unit tests with the race detector and coverage.
	go test -race -cover $(PKG)

audit: ## Audit dependencies for known vulnerabilities.
	go run $(GOVULNCHECK) $(PKG)
	go mod verify

build: ## Build the codebox binary into ./bin.
	mkdir -p bin
	go build -o bin/$(BIN) ./cmd/$(BIN)

clean: ## Remove build artifacts.
	rm -rf bin/$(BIN)

install:
	sudo install -m 0755 bin/codebox /usr/local/bin/codebox
