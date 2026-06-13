BINARY_NAME := aisync
BUILD_DIR := bin
VERSION := 0.1.0-dev
GO := go
GOFLAGS := -trimpath
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build test test-verbose test-race test-cover lint fmt vet install clean install-skills uninstall-skills release-dry

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

## Agent Skills

SKILLS_DIR := $(shell pwd)/.opencode/skills
OPENCODE_SKILLS_DIR := $(HOME)/.config/opencode/skills

install-skills:
	@mkdir -p $(OPENCODE_SKILLS_DIR)
	@for skill in aisync-session-finder aisync-stats aisync-analyze; do \
		ln -sfn $(SKILLS_DIR)/$$skill $(OPENCODE_SKILLS_DIR)/$$skill; \
		echo "Installed: $(OPENCODE_SKILLS_DIR)/$$skill -> $(SKILLS_DIR)/$$skill"; \
	done

uninstall-skills:
	@for skill in aisync-session-finder aisync-stats aisync-analyze; do \
		target=$(OPENCODE_SKILLS_DIR)/$$skill; \
		if [ -L "$$target" ]; then \
			rm "$$target"; \
			echo "Removed: $$target"; \
		elif [ -e "$$target" ]; then \
			echo "Skipped (not a symlink): $$target"; \
		fi; \
	done

## Release

release-dry:
	goreleaser release --snapshot --clean
