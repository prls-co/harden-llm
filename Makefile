GO ?= go
GOFMT ?= gofmt
NODE ?= node

.PHONY: format lint build test-static test-unit test-parity test-integration test-integration-race test-api test-observability test-compose test-race test-vulnerability verify

format:
	@unformatted="$$($(GOFMT) -l $$(find . -type f -name '*.go' -not -path './.git/*' -not -path './.codex/*'))"; \
	test -z "$$unformatted" || { echo "Unformatted Go files:"; echo "$$unformatted"; exit 1; }

lint:
	$(GO) vet ./...

build:
	$(GO) build ./...

test-static:
	$(GO) test ./internal/testkit/... -count=1
	$(NODE) scripts/verify-parity-fixtures.mjs

test-unit:
	$(GO) test ./... -count=1

test-parity:
	$(NODE) scripts/verify-parity-fixtures.mjs
	$(GO) test ./... -run 'Parity|Contract|Identity|Replay' -count=1

test-integration:
	$(GO) test ./... -tags=integration -count=1

test-integration-race:
	$(GO) test -race ./... -tags=integration -count=1

test-api:
	$(GO) test ./internal/gateway/... -count=1

test-observability:
	$(GO) test ./internal/runtime/... ./internal/gateway/... ./internal/artifacts/... ./internal/deploytest/... ./internal/eval/... -count=1

test-compose:
	$(GO) test ./internal/smoke/... -tags=compose -run TestComposeSmoke -count=1

test-race:
	$(GO) test -race ./... -count=1

test-vulnerability:
	$(GO) tool govulncheck ./...

verify: format lint build test-static test-unit test-parity test-integration test-integration-race test-api test-observability test-race test-vulnerability
