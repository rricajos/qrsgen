# qrsgen — Makefile de dev convenience.
#
# Targets están pensados para dev local. CI usa GitHub Actions
# workflows separados (test.yml, release.yml, security.yml).
#
# Variables sobrecargables:
#   GO=$(go) — binario Go a usar. Default `go` del PATH.
#   GOFLAGS — flags adicionales pasados a `go test/build`.

GO ?= go
PKG ?= ./...

.PHONY: help
help: ## Muestra esta ayuda
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Compila el binario qrsgen
	$(GO) build -o bin/qrsgen ./cmd/server

.PHONY: test
test: ## Corre el test suite (sin integration tests)
	$(GO) test $(PKG)

.PHONY: test-race
test-race: ## Test suite con detector de race conditions
	$(GO) test -race $(PKG)

.PHONY: test-integration
test-integration: ## Tests integration (requiere INTEGRATION_PG_DSN)
	@if [ -z "$$INTEGRATION_PG_DSN" ]; then \
		echo "❌ INTEGRATION_PG_DSN no está set"; \
		echo "   export INTEGRATION_PG_DSN=postgres://user:pass@host:port/db?sslmode=disable"; \
		exit 1; \
	fi
	$(GO) test -run "^TestIntegration_" $(PKG)

.PHONY: bench
bench: ## Corre los microbenchmarks de hot paths
	$(GO) test -bench=. -benchmem -run=^$$ ./internal/bridge/...

.PHONY: cover
cover: ## Coverage report agregado
	$(GO) test -cover $(PKG)

.PHONY: cover-html
cover-html: ## Coverage HTML report en /tmp/coverage.html
	$(GO) test -coverprofile=/tmp/coverage.out $(PKG)
	$(GO) tool cover -html=/tmp/coverage.out -o /tmp/coverage.html
	@echo "→ open /tmp/coverage.html"

.PHONY: vet
vet: ## go vet
	$(GO) vet $(PKG)

.PHONY: lint
lint: ## golangci-lint (requiere instalado)
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "❌ golangci-lint no instalado"; \
		echo "   curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b \$$(go env GOPATH)/bin v2.4.0"; \
		exit 1; \
	}
	golangci-lint run --timeout=5m $(PKG)

.PHONY: vuln
vuln: ## govulncheck (requiere instalado)
	@command -v govulncheck >/dev/null 2>&1 || $(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

.PHONY: sec
sec: ## gosec — falsos positivos esperados en goroutines async (ver docs)
	@command -v gosec >/dev/null 2>&1 || $(GO) install github.com/securego/gosec/v2/cmd/gosec@latest
	gosec -severity medium -fmt text ./...

.PHONY: tidy
tidy: ## go mod tidy
	$(GO) mod tidy

.PHONY: check
check: vet test ## vet + test rápido (pre-commit conveniente)

.PHONY: check-full
check-full: vet lint test test-race vuln ## Suite completa local (CI mirror)

.PHONY: docs-serve
docs-serve: ## Sirve la docs site localmente con mkdocs (requiere `pip install mkdocs-material`)
	mkdocs serve

.PHONY: clean
clean: ## Limpia binarios + coverage artifacts
	rm -rf bin/ /tmp/coverage.out /tmp/coverage.html
