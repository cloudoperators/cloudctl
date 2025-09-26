# Project variables
BIN           ?= cloudctl
PKG           ?= github.com/cloudoperators/cloudctl
CMD_PKG       ?= .
BUILD_DIR     ?= bin

# Versioning (overridable)
VERSION       ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Go options
GO            ?= go
GOFLAGS       ?=
TAGS          ?=
LDFLAGS       ?= -X '$(PKG)/cmd.Version=$(VERSION)' -X '$(PKG)/cmd.GitCommit=$(GIT_COMMIT)' -X '$(PKG)/cmd.BuildDate=$(BUILD_DATE)'
GCFLAGS       ?=
ASMFLAGS      ?=
RACE          ?=

# E2E options
E2E_CLUSTER_NAME ?= cloudctl-e2e
E2E_KUBECONFIG   ?= $(CURDIR)/e2e/e2e-kubeconfig
E2E_PKG          ?= ./e2e
E2E_TAGS         ?= e2e
# Use absolute path so tests can find the binary regardless of working directory
E2E_BIN          ?= $(CURDIR)/$(BUILD_DIR)/$(BIN)

# Extra args
ARGS          ?=

# Derived
BUILD_FLAGS   := $(if $(RACE),-race,) $(GOFLAGS) -tags '$(TAGS)' -ldflags "$(LDFLAGS)" -gcflags '$(GCFLAGS)' -asmflags '$(ASMFLAGS)'

.PHONY: all build install run test cover cover-html fmt vet tidy clean version print-vars \
        e2e-up e2e-down e2e-build e2e-test e2e

all: build

build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BIN) $(CMD_PKG)

install:
	$(GO) install $(BUILD_FLAGS) $(CMD_PKG)

run:
	$(GO) run $(BUILD_FLAGS) $(CMD_PKG) $(ARGS)

test:
	$(GO) test $(GOFLAGS) -tags '$(TAGS)' $(if $(RACE),-race,) ./...

cover:
	$(GO) test $(GOFLAGS) -tags '$(TAGS)' -coverprofile=coverage.out ./...
	@echo "Coverage summary:"
	$(GO) tool cover -func=coverage.out

cover-html: cover
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Open coverage.html in your browser."

fmt:
	$(GO) fmt ./...

fmt-check:
	@echo "Checking formatting with gofmt and gofumpt..."
	@set -e; \
	out1="$$(gofmt -l .)"; \
	out2="$$( $(GO) run mvdan.cc/gofumpt@latest -l .)"; \
	files=""; \
	if [ -n "$$out1" ]; then files="$$files\n$$out1"; fi; \
	if [ -n "$$out2" ]; then files="$$files\n$$out2"; fi; \
	if [ -n "$$files" ]; then \
		echo "The following files need formatting:"; \
		printf "%b\n" "$$files" | sed '/^$$/d' | sort -u; \
		echo "Run: make fmt"; \
		exit 1; \
	fi

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

clean:
	@rm -rf $(BUILD_DIR) coverage.out coverage.html ./bin/cloudctl ./e2e/cloudctl ./e2e/e2e-kubeconfig

version:
	@echo "Version:    $(VERSION)"
	@echo "Git commit: $(GIT_COMMIT)"
	@echo "Build date: $(BUILD_DATE)"

print-vars:
	@echo "BIN=$(BIN)"
	@echo "PKG=$(PKG)"
	@echo "CMD_PKG=$(CMD_PKG)"
	@echo "BUILD_DIR=$(BUILD_DIR)"
	@echo "VERSION=$(VERSION)"
	@echo "GIT_COMMIT=$(GIT_COMMIT)"
	@echo "BUILD_DATE=$(BUILD_DATE)"
	@echo "GOFLAGS=$(GOFLAGS)"
	@echo "TAGS=$(TAGS)"
	@echo "LDFLAGS=$(LDFLAGS)"
	@echo "RACE=$(RACE)"
	@echo "E2E_CLUSTER_NAME=$(E2E_CLUSTER_NAME)"
	@echo "E2E_KUBECONFIG=$(E2E_KUBECONFIG)"
	@echo "E2E_BIN=$(E2E_BIN)"
	@echo "E2E_PKG=$(E2E_PKG)"
	@echo "E2E_TAGS=$(E2E_TAGS)"

# --- E2E ---

e2e-up:
	@./e2e/k3d-up.sh "$(E2E_CLUSTER_NAME)" "$(E2E_KUBECONFIG)"

e2e-down:
	@./e2e/k3d-down.sh "$(E2E_CLUSTER_NAME)"

e2e-build: build
	@echo "Using E2E binary: $(E2E_BIN)"

# Ensure kubeconfig exists before running tests; if not, bring the cluster up and write it.
e2e-test: e2e-build
	@if [ ! -f "$(E2E_KUBECONFIG)" ]; then \
		echo "Kubeconfig $(E2E_KUBECONFIG) not found; creating via k3d-up..."; \
		./e2e/k3d-up.sh "$(E2E_CLUSTER_NAME)" "$(E2E_KUBECONFIG)"; \
	fi
	@echo "Running e2e tests against kubeconfig: $(E2E_KUBECONFIG)"
	E2E_KUBECONFIG="$(E2E_KUBECONFIG)" E2E_BIN="$(E2E_BIN)" $(GO) test -v -tags '$(E2E_TAGS)' $(E2E_PKG)

# Convenience: bring up cluster, run tests, then tear down.
e2e: e2e-up e2e-test e2e-down
