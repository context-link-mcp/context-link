BINARY_NAME := context-link
CMD_PATH    := ./cmd/context-link
BUILD_DIR   := ./bin

.PHONY: all build test lint clean vet coverage

all: lint test build

## build: Compile the binary
build:
	@mkdir -p $(BUILD_DIR)
	go build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_PATH)

## test: Run all tests
test:
	go test ./... -count=1 -timeout 120s

## coverage: Run tests with coverage report
coverage:
	go test ./... -count=1 -coverprofile=coverage.out -covermode=atomic
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
