GO ?= go
GO_PKG ?= ./cmd/proxy
APP_NAME ?= antigravity-compat-proxy
OUTPUT_DIR ?= build
VERSION ?= $(shell cat VERSION)

GO_RELEASE_FLAGS ?= -trimpath -ldflags="-s -w -X main.version=$(VERSION) -extldflags '-static'"

.PHONY: all clean \
	linux-amd64-release linux-arm64-release \
	windows-amd64-release windows-arm64-release \
	darwin-amd64-release darwin-arm64-release

all: linux-amd64-release linux-arm64-release windows-amd64-release windows-arm64-release darwin-amd64-release darwin-arm64-release

clean:
	@rm -rf $(OUTPUT_DIR)

# Directories
$(OUTPUT_DIR)/linux-amd64/release:
	@mkdir -p $@
$(OUTPUT_DIR)/linux-arm64/release:
	@mkdir -p $@
$(OUTPUT_DIR)/windows-amd64/release:
	@mkdir -p $@
$(OUTPUT_DIR)/windows-arm64/release:
	@mkdir -p $@
$(OUTPUT_DIR)/darwin-amd64/release:
	@mkdir -p $@
$(OUTPUT_DIR)/darwin-arm64/release:
	@mkdir -p $@

# Linux
linux-amd64-release: $(OUTPUT_DIR)/linux-amd64/release/$(APP_NAME)
$(OUTPUT_DIR)/linux-amd64/release/$(APP_NAME): $(OUTPUT_DIR)/linux-amd64/release
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GO_RELEASE_FLAGS) -o $@ $(GO_PKG)

linux-arm64-release: $(OUTPUT_DIR)/linux-arm64/release/$(APP_NAME)
$(OUTPUT_DIR)/linux-arm64/release/$(APP_NAME): $(OUTPUT_DIR)/linux-arm64/release
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(GO_RELEASE_FLAGS) -o $@ $(GO_PKG)

# Windows
windows-amd64-release: $(OUTPUT_DIR)/windows-amd64/release/$(APP_NAME).exe
$(OUTPUT_DIR)/windows-amd64/release/$(APP_NAME).exe: $(OUTPUT_DIR)/windows-amd64/release
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(GO_RELEASE_FLAGS) -o $@ $(GO_PKG)

windows-arm64-release: $(OUTPUT_DIR)/windows-arm64/release/$(APP_NAME).exe
$(OUTPUT_DIR)/windows-arm64/release/$(APP_NAME).exe: $(OUTPUT_DIR)/windows-arm64/release
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GO) build $(GO_RELEASE_FLAGS) -o $@ $(GO_PKG)

# macOS (Darwin)
darwin-amd64-release: $(OUTPUT_DIR)/darwin-amd64/release/$(APP_NAME)
$(OUTPUT_DIR)/darwin-amd64/release/$(APP_NAME): $(OUTPUT_DIR)/darwin-amd64/release
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build $(GO_RELEASE_FLAGS) -o $@ $(GO_PKG)

darwin-arm64-release: $(OUTPUT_DIR)/darwin-arm64/release/$(APP_NAME)
$(OUTPUT_DIR)/darwin-arm64/release/$(APP_NAME): $(OUTPUT_DIR)/darwin-arm64/release
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build $(GO_RELEASE_FLAGS) -o $@ $(GO_PKG)

help:
	@echo "make all: build all targets"
	@echo "make clean: remove $(OUTPUT_DIR)"
	@echo "make linux-amd64-release: build for linux amd64"
	@echo "make linux-arm64-release: build for linux arm64"
	@echo "make windows-amd64-release: build for windows amd64"
	@echo "make windows-arm64-release: build for windows arm64"
	@echo "make darwin-amd64-release: build for macos amd64"
	@echo "make darwin-arm64-release: build for macos arm64"
