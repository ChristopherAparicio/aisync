BINARY_NAME := aisync
BUILD_DIR := bin
VERSION := 0.1.0-dev
GO := go
GOFLAGS := -trimpath
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build test test-verbose test-race test-cover lint fmt vet install clean release-dry

all: lint test build

## Build

build:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/aisync

install:
	$(GO) install $(GOFLAGS) -ldflags "$(LDFLAGS)" ./cmd/aisync

## Test

test:
	$(GO) test ./... -count=1

test-verbose:
	$(GO) test ./... -count=1 -v

test-race:
	$(GO) test ./... -count=1 -race

test-cover:
	$(GO) test ./... -count=1 -coverprofile=coverage.out
	$(GO) tool cover -func=coverage.out
	@echo "HTML report: go tool cover -html=coverage.out"

## Code quality

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .
	goimports -w .

vet:
	$(GO) vet ./...

## Cleanup

clean:
	rm -rf $(BUILD_DIR)
	$(GO) clean -cache

## Release

release-dry:
	goreleaser release --snapshot --clean
