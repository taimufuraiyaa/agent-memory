APP := agent-memory
BIN_DIR := bin

.PHONY: build test lint clean

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(APP) ./cmd/agent-memory

test:
	go test ./...

lint:
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8 run

clean:
	rm -rf $(BIN_DIR)
