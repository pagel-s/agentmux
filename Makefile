GO ?= $(if $(wildcard $(CURDIR)/.tools/go1.26.1/bin/go),$(CURDIR)/.tools/go1.26.1/bin/go,go)
GOCACHE ?= $(CURDIR)/.cache/go-build
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BINARY := bin/agentmux
UNIT_PACKAGES := $(shell GOCACHE=$(GOCACHE) $(GO) list ./... | grep -v '^agentmux/internal/tmux$$')

.PHONY: build test test-unit test-integration fmt check clean

build:
	mkdir -p bin
	GOCACHE=$(GOCACHE) $(GO) build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/agentmux

test: test-unit test-integration

test-unit:
	GOCACHE=$(GOCACHE) $(GO) test $(UNIT_PACKAGES)

test-integration:
	GOCACHE=$(GOCACHE) $(GO) test ./internal/tmux -run TestExecute

fmt:
	gofmt -w cmd internal

check:
	test -z "$$(gofmt -l cmd internal)"
	GOCACHE=$(GOCACHE) $(GO) test ./...

clean:
	rm -rf agentmux bin .cache
