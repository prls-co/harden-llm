GO ?= go
GOFMT ?= gofmt
NODE ?= node
# The canonical runner owns the integration service pool and selects the
# measured package caps; these variables remain available to downstream local
# wrappers but are not an independent scheduler.
INTEGRATION_PACKAGE_PARALLELISM ?= 1
RACE_PACKAGE_PARALLELISM ?= 1

.PHONY: format lint build test-static test-unit test-parity test-integration test-integration-race test-api test-observability test-compose test-race test-vulnerability live-structured-call test-fast test-browser test-release test-live benchmark-test-feedback verify

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
	$(NODE) scripts/run-test-tier.mjs --task go-integration

test-integration-race:
	$(NODE) scripts/run-test-tier.mjs --task go-integration-race

test-api:
	$(GO) test ./internal/gateway/... -count=1

test-observability:
	$(GO) test ./internal/runtime/... ./internal/gateway/... ./internal/artifacts/... ./internal/deploytest/... ./internal/eval/... -count=1

test-compose:
	$(GO) test ./internal/smoke/... -tags=compose -run TestComposeSmoke -count=1

test-race:
	$(GO) test -race -p=$(RACE_PACKAGE_PARALLELISM) ./... -count=1

test-vulnerability:
	$(GO) tool govulncheck ./...

live-structured-call:
	./scripts/harden-structured-call.sh

test-fast:
	$(NODE) scripts/run-test-tier.mjs --task fast

test-browser:
	$(NODE) scripts/run-test-tier.mjs --task browser

test-release:
	$(NODE) scripts/run-test-tier.mjs --task release

test-live:
	$(NODE) scripts/run-test-tier.mjs --task live

benchmark-test-feedback:
	$(NODE) scripts/benchmark-test-feedback.mjs --mode baseline --warm-samples 5 --cold-samples 3 --seeds 104729,130363,155921 --output plans/evidence/harden-llm/test-feedback-latest.json

verify: format lint build test-static test-unit test-parity test-integration test-integration-race test-api test-observability test-race test-vulnerability
