.PHONY: all build test test-acceptance test-unit lint clean embedder vault verify-scenarios

all: build

build:
	go build -o bin/ghyll ./cmd/ghyll
	go build -o bin/ghyll-vault ./cmd/ghyll-vault

test: test-unit test-acceptance

test-unit:
	go test $(shell go list ./... | grep -v tests/acceptance)

test-acceptance:
	go test -v ./tests/acceptance/ -count=1

lint:
	go vet ./...
	golangci-lint run

clean:
	rm -rf bin/

verify-scenarios:
	go run scripts/verify-scenarios.go

embedder:
	@mkdir -p ~/.ghyll/models
	@echo "Downloading GTE-micro ONNX model..."
	curl -L -o ~/.ghyll/models/gte-micro.onnx \
		"https://huggingface.co/Xenova/gte-micro/resolve/main/model.onnx"
	@echo "Done. Model at ~/.ghyll/models/gte-micro.onnx"

vault:
	go build -o bin/ghyll-vault ./cmd/ghyll-vault
