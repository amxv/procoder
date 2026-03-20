SHELL := /bin/bash

GO ?= go
GOFMT ?= gofmt
BIN_NAME ?= procoder
CMD_PATH ?= ./cmd/$(BIN_NAME)
HELPER_NAME ?= procoder-return
HELPER_GOOS ?= linux
HELPER_GOARCH ?= amd64
HELPER_ASSET ?= $(HELPER_NAME)_$(HELPER_GOOS)_$(HELPER_GOARCH)
HELPER_CMD_PATH ?= ./cmd/$(HELPER_NAME)
DIST_DIR ?= dist
BIN_PATH ?= $(DIST_DIR)/$(BIN_NAME)
HELPER_PATH ?= $(DIST_DIR)/$(HELPER_ASSET)
VERSION ?= $(shell node -p "require('./package.json').version" 2>/dev/null)
LDFLAGS ?= -s -w -X github.com/amxv/procoder/internal/buildinfo.Version=$(if $(VERSION),$(VERSION),dev)

.PHONY: help fmt test vet lint check build build-helper build-all install-local clean release-tag

help:
	@echo "procoder command runner"
	@echo ""
	@echo "Targets:"
	@echo "  make fmt          - format Go files"
	@echo "  make test         - run go test ./..."
	@echo "  make vet          - run go vet ./..."
	@echo "  make lint         - run Node script checks"
	@echo "  make check        - fmt + test + vet + lint"
	@echo "  make build        - build local binary to dist/procoder"
	@echo "  make build-helper - build the linux/amd64 procoder-return helper asset"
	@echo "  make build-all    - build release binaries for 5 target platforms plus helper asset"
	@echo "  make install-local - install CLI and helper to ~/.local/bin"
	@echo "  make clean        - remove dist artifacts"
	@echo "  make release-tag  - create and push git tag (requires VERSION=x.y.z)"

fmt:
	@$(GOFMT) -w $$(find . -type f -name '*.go' -not -path './dist/*')

test:
	@$(GO) test ./...

vet:
	@$(GO) vet ./...

lint:
	@npm run lint

check: fmt test vet lint

build:
	@mkdir -p $(DIST_DIR)
	@$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN_PATH) $(CMD_PATH)

build-helper:
	@mkdir -p $(DIST_DIR)
	@CGO_ENABLED=0 GOOS=$(HELPER_GOOS) GOARCH=$(HELPER_GOARCH) \
		$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(HELPER_PATH) $(HELPER_CMD_PATH)

build-all: build-helper
	@mkdir -p $(DIST_DIR)
	@for target in "darwin amd64" "darwin arm64" "linux amd64" "linux arm64" "windows amd64"; do \
		set -- $$target; \
		GOOS=$$1; GOARCH=$$2; \
		EXT=""; \
		if [ "$$GOOS" = "windows" ]; then EXT=".exe"; fi; \
		echo "Building $(BIN_NAME) for $$GOOS/$$GOARCH"; \
		CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o "$(DIST_DIR)/$(BIN_NAME)_$$GOOS_$$GOARCH$$EXT" $(CMD_PATH); \
	done

install-local: build build-helper
	@mkdir -p $$HOME/.local/bin
	@install -m 755 $(BIN_PATH) $$HOME/.local/bin/$(BIN_NAME)
	@install -m 755 $(HELPER_PATH) $$HOME/.local/bin/$(HELPER_ASSET)
	@echo "Installed $(BIN_NAME) to $$HOME/.local/bin/$(BIN_NAME)"
	@echo "Installed $(HELPER_ASSET) to $$HOME/.local/bin/$(HELPER_ASSET)"

clean:
	@rm -rf $(DIST_DIR)

release-tag:
	@test -n "$(VERSION)" || (echo "Usage: make release-tag VERSION=x.y.z" && exit 1)
	@git tag "v$(VERSION)"
	@git push origin "v$(VERSION)"
