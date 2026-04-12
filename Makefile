.PHONY: all build test clean lint embedder vault

all: build

build:
	go build -o bin/ghyll ./cmd/ghyll
	go build -o bin/ghyll-vault ./cmd/ghyll-vault

test:
	go test ./...

lint:
	go vet ./...
	golangci-lint run

clean:
	rm -rf bin/

embedder:
	@mkdir -p ~/.ghyll/models
	@echo "Downloading GTE-micro ONNX model..."
	curl -L -o ~/.ghyll/models/gte-micro.onnx \
		"https://huggingface.co/Xenova/gte-micro/resolve/main/model.onnx"
	@echo "Done. Model at ~/.ghyll/models/gte-micro.onnx"

vault: 
	go build -o bin/ghyll-vault ./cmd/ghyll-vault
