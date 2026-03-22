BINARY := bin/agentmux
UNIT_PACKAGES := $(shell go list ./... | grep -v '^agentmux/internal/tmux$$')

.PHONY: build test test-unit test-integration fmt check clean

build:
	mkdir -p bin
	go build -o $(BINARY) ./cmd/agentmux

test: test-unit test-integration

test-unit:
	go test $(UNIT_PACKAGES)

test-integration:
	go test ./internal/tmux -run TestExecute

fmt:
	gofmt -w cmd internal

check:
	test -z "$$(gofmt -l cmd internal)"
	go test ./...

clean:
	rm -rf bin
