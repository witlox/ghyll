.PHONY: all build build-bin test test-unit test-acceptance test-race test-live \
       bench lint fmt clean \
       vault verify-scenarios coverage coverage-check install-tools setup \
       docs docs-serve

VERSION ?= dev

all: lint test build

build:
	go build ./...

build-bin:
	CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(VERSION)" -o bin/ghyll ./cmd/ghyll
	CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(VERSION)" -o bin/ghyll-vault ./cmd/ghyll-vault

test: test-unit test-acceptance

test-unit:
	go test -count=1 $(shell go list ./... | grep -v tests/acceptance)

test-acceptance:
	go test -v ./tests/acceptance/ -count=1

test-race:
	go test -race -count=1 $(shell go list ./... | grep -v tests/acceptance)

# Tier 3 / live-endpoint tests. Opt-in: requires the GHYLL_LIVE_*
# env vars (URL, MODEL, optionally KEY). Without those the tests
# t.Skip; with them set we hit a real OpenAI-compatible endpoint
# and assert streaming SSE + delta callbacks + content render.
test-live:
	go test -tags live -count=1 -timeout 5m ./stream/...

# Tier 3 perf baselines: run all benchmarks at -benchtime=2s,
# print one line per benchmark. Append output to perf/baselines.md
# manually when re-baselining.
bench:
	go test -bench=. -benchmem -run=^$$ -benchtime=2s ./engine/ ./runner/

lint:
	go vet ./...
	golangci-lint run --timeout=5m

fmt:
	gofmt -l -w .
	@which goimports > /dev/null 2>&1 && goimports -l -w . || true

coverage:
	go test -count=1 -coverprofile=coverage.out -coverpkg=./... $(shell go list ./... | grep -v tests/acceptance)
	go tool cover -func=coverage.out | tail -1

coverage-check: coverage
	@COVERAGE=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | tr -d '%'); \
	echo "Coverage: $${COVERAGE}%"; \
	if [ $$(echo "$${COVERAGE} < 78" | bc -l) -eq 1 ]; then \
		echo "FAIL: coverage below 78% (Tier 3 floor; aim 80%)"; exit 1; \
	fi

verify-scenarios:
	go run scripts/verify-scenarios.go

clean:
	rm -rf bin/ coverage.out

install-tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@which lefthook > /dev/null 2>&1 || brew install lefthook
	@which goimports > /dev/null 2>&1 || go install golang.org/x/tools/cmd/goimports@latest

setup: install-tools
	lefthook install

docs:
	mdbook build

docs-serve:
	mdbook serve --open
