# ABOUTME: Build and test targets for the tracker project.
# ABOUTME: Provides build targets for both tracker and tracker-conformance binaries.

.PHONY: build test clean install

GOCACHE ?= $(CURDIR)/.gocache

build:
	mkdir -p bin
	GOCACHE=$(GOCACHE) go build -o bin/tracker ./cmd/tracker
	GOCACHE=$(GOCACHE) go build -o bin/tracker-conformance ./cmd/tracker-conformance

test:
	GOCACHE=$(GOCACHE) go test ./...

install: build
	cp bin/tracker $(GOPATH)/bin/tracker 2>/dev/null || cp bin/tracker $(HOME)/go/bin/tracker

clean:
	rm -rf bin/
