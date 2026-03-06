# ABOUTME: Build and test targets for the mammoth-lite project.
# ABOUTME: Provides build targets for both mammoth and conformance binaries.

.PHONY: build test clean

GOCACHE ?= $(CURDIR)/.gocache

build:
	mkdir -p bin
	GOCACHE=$(GOCACHE) go build -o bin/conformance ./cmd/conformance

test:
	GOCACHE=$(GOCACHE) go test ./...

clean:
	rm -rf bin/
