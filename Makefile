GO ?= go
NODE ?= node

.PHONY: format lint build test-static test-unit test-parity test-integration test-api test-observability test-compose test-race verify

format:
	$(GO) fmt ./...

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

test-api:
	$(GO) test ./internal/gateway/... -count=1

test-observability:
	$(GO) test ./internal/observability/... ./internal/deploytest/... -count=1

test-compose:
	$(GO) test ./internal/smoke/... -tags=compose -run TestComposeSmoke -count=1

test-race:
	$(GO) test -race ./... -count=1

verify: format lint build test-static test-unit test-parity test-integration test-api test-observability test-race
