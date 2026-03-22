BINARY_NAME := context-link
CMD_PATH    := ./cmd/context-link
BUILD_DIR   := ./bin
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

.PHONY: all build test lint clean vet coverage

all: lint test build

## build: Compile the binary with version injection
build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 go build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_PATH)

## test: Run all tests
test:
	CGO_ENABLED=1 go test ./... -count=1 -timeout 120s

## coverage: Run tests with coverage report
coverage:
	CGO_ENABLED=1 go test ./... -count=1 -coverprofile=coverage.out -covermode=atomic
	go tool cover -func=coverage.out

## lint: Run golangci-lint
lint:
	golangci-lint run ./...

## vet: Run go vet
vet:
	go vet ./...

## clean: Remove build artifacts
clean:
	rm -rf $(BUILD_DIR) coverage.out

## help: Show this help
help:
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
