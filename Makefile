SHELL := /bin/bash
GO    ?= go
VERSION_BASE ?= v0.9.0-202604.1
VERSION ?= $(shell ./scripts/version.sh "$(VERSION_BASE)")

# --- cross-build matrix ---------------------------------------------------
GO_PKG         ?= ./cmd/pangaeactl
APP_NAME       ?= pangaeactl
PROVIDER_CODEX_IMAGE ?= pangaea/provider-codex:dev
PROVIDER_GEMINI_IMAGE ?= pangaea/provider-gemini:dev
PROVIDER_CLAUDE_IMAGE ?= pangaea/provider-claude:dev
OS_LIST        ?= linux darwin windows
ARCH_LIST      ?= amd64 arm64
BUILD_VARIANTS ?= debug release
OUTPUT_DIR     ?= build

GO_DEBUG_FLAGS   ?= -trimpath -gcflags=all=-N\ -l -ldflags=-X\ main.version=$(VERSION)
GO_RELEASE_FLAGS ?= -trimpath -ldflags=-X\ main.version=$(VERSION)\ -s\ -w\ -extldflags\ -static

# Recursive wildcard: tracks every *.go change so a single touch retriggers
# only the affected build target via the per-output rule below.
rwildcard = $(wildcard $(1)$(2)) $(foreach d,$(wildcard $(1)*),$(call rwildcard,$d/,$(2)))
GO_FILES := $(call rwildcard,,*.go)

artifact_os  = $(if $(filter darwin,$(1)),macos,$(1))
artifact_dir = $(OUTPUT_DIR)/$(call artifact_os,$(1))-$(2)/$(3)
artifact     = $(call artifact_dir,$(1),$(2),$(3))/$(APP_NAME)$(if $(filter windows,$(1)),.exe,)

OS_ARCH_PAIRS      := $(foreach os,$(OS_LIST),$(foreach arch,$(ARCH_LIST),$(os)-$(arch)))
OS_VARIANT_PAIRS   := $(foreach os,$(OS_LIST),$(foreach var,$(BUILD_VARIANTS),$(os)-$(var)))
ARCH_VARIANT_PAIRS := $(foreach arch,$(ARCH_LIST),$(foreach var,$(BUILD_VARIANTS),$(arch)-$(var)))
FULL_KEYS          := $(foreach os,$(OS_LIST),$(foreach arch,$(ARCH_LIST),$(foreach var,$(BUILD_VARIANTS),$(os)-$(arch)-$(var))))
FULL_TARGETS       := $(foreach k,$(FULL_KEYS),$(call artifact,$(word 1,$(subst -, ,$(k))),$(word 2,$(subst -, ,$(k))),$(word 3,$(subst -, ,$(k)))))
ALL_DIRS           := $(sort $(foreach k,$(FULL_KEYS),$(call artifact_dir,$(word 1,$(subst -, ,$(k))),$(word 2,$(subst -, ,$(k))),$(word 3,$(subst -, ,$(k))))))

# Per-output build recipe. release -> CGO_ENABLED=0 + static + stripped;
# debug  -> keeps symbols, leaves CGO_ENABLED to environment.
define BUILD_RULE
$(call artifact,$(1),$(2),$(3)): $(GO_FILES) | $(call artifact_dir,$(1),$(2),$(3))
	@echo "build $(1)/$(2)/$(3) -> $$@"
	@GOOS=$(1) GOARCH=$(2) $(if $(filter release,$(3)),CGO_ENABLED=0 ) \
		$(GO) build $(if $(filter release,$(3)),$(GO_RELEASE_FLAGS),$(GO_DEBUG_FLAGS)) \
		-o $$@ $(GO_PKG)
endef

# Selector helpers: every partial selector expands to the matching full
# artifact list. token{1,2,3} pulls dash-separated parts out of $@.
all_for_os         = $(foreach a,$(ARCH_LIST),$(foreach v,$(BUILD_VARIANTS),$(call artifact,$(1),$(a),$(v))))
all_for_arch       = $(foreach o,$(OS_LIST),$(foreach v,$(BUILD_VARIANTS),$(call artifact,$(o),$(1),$(v))))
all_for_variant    = $(foreach o,$(OS_LIST),$(foreach a,$(ARCH_LIST),$(call artifact,$(o),$(a),$(1))))
all_for_os_arch    = $(foreach v,$(BUILD_VARIANTS),$(call artifact,$(1),$(2),$(v)))
all_for_os_variant = $(foreach a,$(ARCH_LIST),$(call artifact,$(1),$(a),$(2)))
all_for_arch_variant = $(foreach o,$(OS_LIST),$(call artifact,$(o),$(1),$(2)))
token1 = $(word 1,$(subst -, ,$(1)))
token2 = $(word 2,$(subst -, ,$(1)))
token3 = $(word 3,$(subst -, ,$(1)))

.PHONY: all clean help \
	$(OS_LIST) $(ARCH_LIST) $(BUILD_VARIANTS) \
	$(OS_ARCH_PAIRS) $(OS_VARIANT_PAIRS) $(ARCH_VARIANT_PAIRS) $(FULL_KEYS) \
	test race integration lint fmt vet tidy demo docker-provider-codex docker-provider-gemini docker-provider-claude docker-providers

all: $(FULL_TARGETS)

clean:
	rm -rf $(OUTPUT_DIR)
	rm -f $(APP_NAME) coverage.out coverage.html
	rm -rf dist/

$(ALL_DIRS):
	@mkdir -p $@

$(OS_LIST):
	@$(MAKE) $(call all_for_os,$@)

$(ARCH_LIST):
	@$(MAKE) $(call all_for_arch,$@)

$(BUILD_VARIANTS):
	@$(MAKE) $(call all_for_variant,$@)

$(OS_ARCH_PAIRS):
	@$(MAKE) $(call all_for_os_arch,$(call token1,$@),$(call token2,$@))

$(OS_VARIANT_PAIRS):
	@$(MAKE) $(call all_for_os_variant,$(call token1,$@),$(call token2,$@))

$(ARCH_VARIANT_PAIRS):
	@$(MAKE) $(call all_for_arch_variant,$(call token1,$@),$(call token2,$@))

$(FULL_KEYS):
	@$(MAKE) $(call artifact,$(call token1,$@),$(call token2,$@),$(call token3,$@))

$(foreach os,$(OS_LIST),$(foreach arch,$(ARCH_LIST),$(foreach var,$(BUILD_VARIANTS),$(eval $(call BUILD_RULE,$(os),$(arch),$(var))))))

# --- housekeeping ---------------------------------------------------------
test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

integration:
	$(GO) test -tags=integration ./...

lint:
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not installed"; exit 1; }
	golangci-lint run ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

demo:
	docker compose up --build

docker-provider-codex:
	docker build -f providers/codex/Dockerfile -t $(PROVIDER_CODEX_IMAGE) --build-arg VERSION=$(VERSION) .

docker-provider-gemini:
	docker build -f providers/gemini/Dockerfile -t $(PROVIDER_GEMINI_IMAGE) --build-arg VERSION=$(VERSION) .

docker-provider-claude:
	docker build -f providers/claude/Dockerfile -t $(PROVIDER_CLAUDE_IMAGE) --build-arg VERSION=$(VERSION) .

docker-providers: docker-provider-codex docker-provider-gemini docker-provider-claude

help:
	@echo "Build matrix:"
	@echo "  make all                     build every $(OS_LIST) x $(ARCH_LIST) x $(BUILD_VARIANTS)"
	@echo "  make <os>                    build all $(BUILD_VARIANTS) for one OS"
	@echo "  make <arch>                  build all $(BUILD_VARIANTS) for one ARCH"
	@echo "  make <variant>               build all OS/ARCH for one variant"
	@echo "  make <os>-<arch>             build $(BUILD_VARIANTS) for that pair"
	@echo "  make <os>-<variant>          build all ARCH for that pair"
	@echo "  make <arch>-<variant>        build all OS for that pair"
	@echo "  make <os>-<arch>-<variant>   build a single artifact"
	@echo "  make clean                   remove $(OUTPUT_DIR)/, dist/, $(APP_NAME)"
	@echo "Artifacts: $(OUTPUT_DIR)/<os-label>-<arch>/<variant>/$(APP_NAME)[.exe]"
	@echo "  note: darwin builds are written under $(OUTPUT_DIR)/macos-<arch>/..."
	@echo "Version: $(VERSION)"
	@echo
	@echo "Housekeeping: test  race  integration  lint  fmt  vet  tidy  demo"
	@echo "Provider images: docker-provider-codex  docker-provider-gemini  docker-provider-claude  docker-providers"

%:
	@echo "Unknown target '$@'"
	@echo "Run 'make help' for valid patterns"
	@exit 1
