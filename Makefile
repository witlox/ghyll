.PHONY: all build build-bin test test-unit test-acceptance lint fmt clean \
       embedder vault verify-scenarios coverage coverage-check install-tools setup \
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
	if [ $$(echo "$${COVERAGE} < 50" | bc -l) -eq 1 ]; then \
		echo "FAIL: coverage below 50%"; exit 1; \
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

embedder:
	@mkdir -p ~/.ghyll/models
	@echo "Downloading GTE-micro ONNX model..."
	curl -L -o ~/.ghyll/models/gte-micro.onnx \
		"https://huggingface.co/nicholasgasior/gte-micro-onnx/resolve/main/model.onnx"
	@echo "Done. Model at ~/.ghyll/models/gte-micro.onnx"
	@echo ""
	@echo "ONNX Runtime shared library is also required."
	@echo "  macOS:  brew install onnxruntime"
	@echo "  Linux:  See https://github.com/microsoft/onnxruntime/releases"
	@echo "  Set ONNXRUNTIME_LIB_PATH if not in default search path."

docs:
	mdbook build

docs-serve:
	mdbook serve --open
