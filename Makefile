MIN_GO_VERSION := 1.24
LOCAL_GO ?= $(shell sh ./scripts/select-go.sh "$(CURDIR)/.tools" "$(MIN_GO_VERSION)")
GO ?= $(if $(LOCAL_GO),$(LOCAL_GO),go)
GOCACHE ?= $(CURDIR)/.cache/go-build
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%d)
BINARY := bin/agentmux
UNIT_PACKAGES := $(shell GOCACHE=$(GOCACHE) $(GO) list ./... | grep -v '^agentmux/internal/tmux$$')

.PHONY: build test test-unit test-integration test-managed test-sandbox test-resources test-research fmt check clean

build:
	mkdir -p bin
	GOCACHE=$(GOCACHE) $(GO) build -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)" -o $(BINARY) ./cmd/agentmux

test: test-unit test-integration

test-unit:
	GOCACHE=$(GOCACHE) $(GO) test $(UNIT_PACKAGES)

test-integration:
	GOCACHE=$(GOCACHE) $(GO) test ./internal/tmux -run TestExecute

# Command-only integration checks: no provider credentials or model calls.
test-managed: build
	GOCACHE=$(GOCACHE) AGENTMUX_MANAGED_INTEGRATION=1 AGENTMUX_EXPERIMENT_INTEGRATION=1 $(GO) test -race ./cmd/agentmux ./internal/managed ./internal/managedlaunch ./internal/managedcheck ./internal/experiment

# Requires Linux with working Bubblewrap namespaces; no isolation fallback.
test-sandbox: build
	GOCACHE=$(GOCACHE) AGENTMUX_MANAGED_INTEGRATION=1 AGENTMUX_SANDBOX_INTEGRATION=1 $(GO) test -race ./internal/managed ./internal/managedlaunch ./internal/sandbox
	python3 scripts/smoke-managed.py
	python3 scripts/smoke-managed-team.py

# Requires delegated cgroup v2 controllers and a recent systemd user manager.
# Verifies actual controls, aggregate OOM, and escaped-descendant cleanup.
test-resources:
	GOCACHE=$(GOCACHE) AGENTMUX_SANDBOX_INTEGRATION=1 AGENTMUX_RESOURCE_INTEGRATION=1 $(GO) test -race ./internal/sandbox

# Repeated rounds, scarce credits and real consented teams; no model calls.
test-research: build
	GOCACHE=$(GOCACHE) AGENTMUX_RESEARCH_INTEGRATION=1 AGENTMUX_TEST_BINARY="$(CURDIR)/$(BINARY)" $(GO) test -race ./internal/research

fmt:
	gofmt -w cmd internal

check:
	test -z "$$(gofmt -l cmd internal)"
	GOCACHE=$(GOCACHE) $(GO) test ./...

clean:
	rm -rf agentmux bin .cache
