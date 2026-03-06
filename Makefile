# ABOUTME: Build and test targets for the tracker project.
# ABOUTME: Provides build targets for both tracker and tracker-conformance binaries.

.PHONY: build test clean

GOCACHE ?= $(CURDIR)/.gocache

build:
	mkdir -p bin
	GOCACHE=$(GOCACHE) go build -o bin/tracker ./cmd/tracker
	GOCACHE=$(GOCACHE) go build -o bin/tracker-conformance ./cmd/tracker-conformance

test:
	GOCACHE=$(GOCACHE) go test ./...

clean:
	rm -rf bin/
