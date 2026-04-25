SHELL := /bin/bash
BIN   := claude-creds-share
PKG   := github.com/dh-kam/claude-creds-share
GO    ?= go
GOFLAGS ?=

.PHONY: all build test race lint fmt tidy vet clean demo integration

all: fmt vet lint test build

build:
	$(GO) build $(GOFLAGS) -o $(BIN) ./cmd/claude-creds-share

test:
	$(GO) test $(GOFLAGS) ./...

race:
	$(GO) test -race $(GOFLAGS) ./...

integration:
	$(GO) test -tags=integration $(GOFLAGS) ./...

lint:
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not installed"; exit 1; }
	golangci-lint run ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

clean:
	rm -f $(BIN) coverage.out coverage.html
	rm -rf dist/

demo:
	docker compose up --build
