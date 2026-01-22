.PHONY: all build clean test test-race lint run docker docker-up docker-down help server

BINARY_NAME := kar
BUILD_DIR := bin
GO := go
GOFLAGS := -trimpath
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.gitCommit=$(GIT_COMMIT)

all: build

## build: Build the kar binary
build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/kar98k

## build-all: Build for multiple platforms
build-all:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/kar98k
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/kar98k
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/kar98k
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/kar98k
	GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/kar98k

## server: Build the sample echo server
server:
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/echoserver ./examples/echoserver

## clean: Remove build artifacts
clean:
	rm -rf $(BUILD_DIR)
	$(GO) clean

## test: Run tests
test:
	$(GO) test -v ./...

## test-race: Run tests with race detector
test-race:
	$(GO) test -race -v ./...

## test-cover: Run tests with coverage
test-cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

## lint: Run linter
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed"; exit 1; }
	golangci-lint run ./...

## fmt: Format code
fmt:
	$(GO) fmt ./...
	goimports -w .

## run: Run kar (interactive mode)
run: build
	./$(BUILD_DIR)/$(BINARY_NAME) start

## run-server: Run the sample echo server
run-server: server
	./$(BUILD_DIR)/echoserver

## demo: Run full demo (server + kar)
demo: build server
	@echo "Starting echo server in background..."
	@./$(BUILD_DIR)/echoserver &
	@sleep 1
	@echo "Starting kar..."
	./$(BUILD_DIR)/$(BINARY_NAME) run --config examples/demo.yaml --trigger

## docker: Build Docker image
docker:
	docker build -t kar98k:latest .

## docker-up: Start with docker-compose
docker-up:
	docker-compose up -d

## docker-down: Stop docker-compose
docker-down:
	docker-compose down

## deps: Download dependencies
deps:
	$(GO) mod download
	$(GO) mod tidy

## install: Install kar to GOPATH/bin
install: build
	cp $(BUILD_DIR)/$(BINARY_NAME) $(GOPATH)/bin/$(BINARY_NAME)

## help: Show this help
help:
	@echo "Available targets:"
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':'
